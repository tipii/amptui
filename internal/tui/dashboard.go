package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/textutil"
)

// The dashboard is the home screen: a band with what's playing beside the
// listening activity and library numbers, then rows of album and
// playlist cards fed live from the server. Rendering is in dashview.go.

// dashRow is one row of cards.
type dashRow int

const (
	rowJumpBackIn dashRow = iota
	rowRecentlyAdded
	rowMostPlayed
	rowRediscover
	rowUnplayed
	rowPlaylists
	dashRowCount
)

const (
	// dashFetchLimit is how many cards a row loads: browsing further
	// than that is what the library screen is for.
	dashFetchLimit   = 24
	dashFetchTimeout = 15 * time.Second
	// dashHistoryTimeout bounds the history fetch, which pages.
	dashHistoryTimeout = 2 * time.Minute
	// dashHeatWeeks is how far back the activity heatmap (and so the
	// history fetch) goes: a year, when the width allows; a narrower
	// terminal shows fewer weeks.
	dashHeatWeeks = 52
	// dashMostPlayedDays is the window of the Most played row.
	dashMostPlayedDays = 30
	// dashRediscoverAfter is how long an album must have gone unplayed
	// to be suggested again.
	dashRediscoverAfter = 90 * 24 * time.Hour
	// dashPlayedPool is how many of the most-played albums are fetched
	// for Rediscover to pick from.
	dashPlayedPool = 200
)

// dashFeed is one of the album feeds the rows are built from.
type dashFeed int

const (
	feedRecent   dashFeed = iota // played albums, latest first
	feedAdded                    // recently added albums
	feedByPlays                  // played albums, most played first
	feedUnplayed                 // random never-played albums
)

// dashboardOutcome is what the dashboard asks its parent to do after a
// key, since it touches state the parent owns (player, browser).
type dashboardOutcome int

const (
	dashOutcomeNone         dashboardOutcome = iota
	dashOutcomeGoToPlaying                   // enter on the band
	dashOutcomeOpenAlbum                     // enter on an album card
	dashOutcomePlayPlaylist                  // enter on a playlist card
	dashOutcomeEnqueue                       // q on a card
)

// Messages from the background fetches.
type (
	dashAlbumsMsg struct {
		feed   dashFeed
		albums []media.AlbumSummary
		total  int // feedUnplayed: how many albums were never played
		err    error
	}
	dashHistoryMsg struct {
		plays []media.Play
		err   error
	}
	dashPlaylistsMsg struct {
		playlists []media.Playlist
		err       error
	}
)

// dashCard is one card in a row.
type dashCard struct {
	key      string // album or playlist RatingKey: what enter opens
	art      string // artwork key ("" when there's none)
	title    string
	sub      string
	playlist bool
}

// dashRowState is a row's cards and where its cursor is.
type dashRowState struct {
	cards  []dashCard
	loaded bool
	err    error
	cursor int
	offset int // first card in view
}

// dashboardModel owns the dashboard's data and cursor. Playback, the
// library cache and the artwork belong to the parent, which renders the
// band and the cards.
type dashboardModel struct {
	rows [dashRowCount]dashRowState

	// history holds the plays of the last dashHeatWeeks weeks, for the
	// heatmap and Most played; noHistory when the server keeps none.
	history       []media.Play
	historyLoaded bool
	historyErr    error
	noHistory     bool

	// byPlays are the most-played albums: Rediscover's pool, and Most
	// played itself on a server without history.
	byPlays       []media.AlbumSummary
	byPlaysLoaded bool
	byPlaysErr    error

	unplayedTotal int // albums never played; -1 until known

	focus int // 0 is the band; 1+r is row r
	cols  int // cards per row at the current width
	now   func() time.Time
}

func newDashboardModel() dashboardModel {
	return dashboardModel{focus: 1 + int(rowJumpBackIn), cols: 1, unplayedTotal: -1, now: time.Now}
}

// Loading reports whether anything is still waiting on its fetch (a
// failed fetch counts as loaded: it shows its error instead).
func (d dashboardModel) Loading() bool {
	if !d.historyLoaded {
		return true
	}
	for _, r := range d.rows {
		if !r.loaded {
			return true
		}
	}
	return false
}

// Load fans out the dashboard's fetches; results arrive as dash*Msg.
// What's on screen stays until they do, so a refresh doesn't flash
// empty rows.
func (d dashboardModel) Load(client media.Backend, libraryKey string) tea.Cmd {
	if client == nil || libraryKey == "" {
		return nil
	}
	albums := func(feed dashFeed, fetch func(ctx context.Context) ([]media.AlbumSummary, int, error)) tea.Cmd {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), dashFetchTimeout)
			defer cancel()
			a, total, err := fetch(ctx)
			return dashAlbumsMsg{feed: feed, albums: a, total: total, err: err}
		}
	}
	noTotal := func(a []media.AlbumSummary, err error) ([]media.AlbumSummary, int, error) { return a, 0, err }
	since := d.now().AddDate(0, 0, -7*dashHeatWeeks)
	return tea.Batch(
		albums(feedRecent, func(ctx context.Context) ([]media.AlbumSummary, int, error) {
			return noTotal(client.PlayedAlbums(ctx, libraryKey, media.ByLastPlayed, dashFetchLimit))
		}),
		albums(feedAdded, func(ctx context.Context) ([]media.AlbumSummary, int, error) {
			return noTotal(client.RecentlyAddedAlbums(ctx, libraryKey, dashFetchLimit))
		}),
		albums(feedByPlays, func(ctx context.Context) ([]media.AlbumSummary, int, error) {
			return noTotal(client.PlayedAlbums(ctx, libraryKey, media.ByPlays, dashPlayedPool))
		}),
		albums(feedUnplayed, func(ctx context.Context) ([]media.AlbumSummary, int, error) {
			return client.UnplayedAlbums(ctx, libraryKey, dashFetchLimit)
		}),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), dashHistoryTimeout)
			defer cancel()
			plays, err := client.PlayHistory(ctx, libraryKey, since)
			return dashHistoryMsg{plays: plays, err: err}
		},
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), dashFetchTimeout)
			defer cancel()
			ps, err := client.AudioPlaylists(ctx, dashFetchLimit)
			return dashPlaylistsMsg{playlists: ps, err: err}
		},
	)
}

// ApplyAlbums / ApplyHistory / ApplyPlaylists fold a fetch's result in.
func (d *dashboardModel) ApplyAlbums(msg dashAlbumsMsg) {
	switch msg.feed {
	case feedRecent:
		d.setRow(rowJumpBackIn, albumCards(msg.albums, func(a media.AlbumSummary) string { return a.Artist }), msg.err)
	case feedAdded:
		d.setRow(rowRecentlyAdded, albumCards(msg.albums, artistAndYear), msg.err)
	case feedByPlays:
		d.byPlays, d.byPlaysErr, d.byPlaysLoaded = msg.albums, msg.err, true
		d.derive()
	case feedUnplayed:
		if msg.err == nil {
			d.unplayedTotal = msg.total
		}
		d.setRow(rowUnplayed, albumCards(msg.albums, artistAndYear), msg.err)
	}
}

func (d *dashboardModel) ApplyHistory(msg dashHistoryMsg) {
	d.history, d.historyErr, d.historyLoaded = msg.plays, msg.err, true
	d.noHistory = errors.Is(msg.err, media.ErrNoHistory)
	if d.noHistory {
		d.historyErr = nil
	}
	d.derive()
}

func (d *dashboardModel) ApplyPlaylists(msg dashPlaylistsMsg) {
	cards := make([]dashCard, 0, len(msg.playlists))
	for _, p := range msg.playlists {
		sub := countPhrase(p.LeafCount, "track")
		if p.Smart {
			sub = "smart · " + sub
		}
		// Plex's default playlists (e.g. "❤️ Tracks") embed glyphs that
		// lipgloss measures wrong; SafeText drops them so the card
		// borders stay aligned.
		cards = append(cards, dashCard{key: p.RatingKey, art: p.Art, title: textutil.SafeText(p.Title), sub: sub, playlist: true})
	}
	d.setRow(rowPlaylists, cards, msg.err)
}

// derive builds the rows that come from more than one feed: Most played
// (from the history, or the play counts where there's none) and
// Rediscover (well-played albums gone quiet).
func (d *dashboardModel) derive() {
	now := d.now()
	switch {
	case d.historyLoaded && !d.noHistory:
		top := mostPlayedAlbums(d.history, now.AddDate(0, 0, -dashMostPlayedDays), dashFetchLimit)
		d.setRow(rowMostPlayed, albumCards(top, playsAndArtist), d.historyErr)
	case d.historyLoaded && d.byPlaysLoaded:
		top := d.byPlays[:min(dashFetchLimit, len(d.byPlays))]
		d.setRow(rowMostPlayed, albumCards(top, playsAndArtist), d.byPlaysErr)
	}
	if d.byPlaysLoaded {
		quiet := rediscoverAlbums(d.byPlays, now.Add(-dashRediscoverAfter), dashFetchLimit)
		d.setRow(rowRediscover, albumCards(quiet, func(a media.AlbumSummary) string { return a.Artist }), d.byPlaysErr)
	}
}

func (d *dashboardModel) setRow(r dashRow, cards []dashCard, err error) {
	s := &d.rows[r]
	s.cards, s.err, s.loaded = cards, err, true
	s.cursor = min(s.cursor, max(len(cards)-1, 0))
	d.keepInView(r)
	d.fixFocus()
}

// mostPlayedAlbums counts the plays since a time per album, most played
// first (the more recent first on a tie).
func mostPlayedAlbums(plays []media.Play, since time.Time, limit int) []media.AlbumSummary {
	var out []media.AlbumSummary
	at := map[string]int{}
	for _, p := range plays {
		if p.AlbumKey == "" || p.At.Before(since) {
			continue
		}
		i, ok := at[p.AlbumKey]
		if !ok {
			i = len(out)
			at[p.AlbumKey] = i
			out = append(out, media.AlbumSummary{RatingKey: p.AlbumKey, Title: p.Album, Artist: p.Artist})
		}
		out[i].Plays++
		if p.At.After(out[i].LastPlayed) {
			out[i].LastPlayed = p.At
		}
	}
	slices.SortStableFunc(out, func(a, b media.AlbumSummary) int {
		if n := cmp.Compare(b.Plays, a.Plays); n != 0 {
			return n
		}
		return b.LastPlayed.Compare(a.LastPlayed)
	})
	return out[:min(limit, len(out))]
}

// rediscoverAlbums picks, from albums in most-played order, those last
// played before a time.
func rediscoverAlbums(byPlays []media.AlbumSummary, before time.Time, limit int) []media.AlbumSummary {
	var out []media.AlbumSummary
	for _, a := range byPlays {
		if len(out) == limit {
			break
		}
		if !a.LastPlayed.IsZero() && a.LastPlayed.Before(before) {
			out = append(out, a)
		}
	}
	return out
}

func albumCards(albums []media.AlbumSummary, sub func(media.AlbumSummary) string) []dashCard {
	cards := make([]dashCard, 0, len(albums))
	for _, a := range albums {
		cards = append(cards, dashCard{key: a.RatingKey, art: a.RatingKey, title: a.Title, sub: sub(a)})
	}
	return cards
}

func artistAndYear(a media.AlbumSummary) string {
	if a.Year > 0 {
		return fmt.Sprintf("%s · %d", a.Artist, a.Year)
	}
	return a.Artist
}

func playsAndArtist(a media.AlbumSummary) string {
	return countPhrase(a.Plays, "play") + " · " + a.Artist
}

// rowTitle is a row's heading and the note after it.
func (d dashboardModel) rowTitle(r dashRow) (title, note string) {
	switch r {
	case rowJumpBackIn:
		return "Jump back in", ""
	case rowRecentlyAdded:
		return "Recently added", ""
	case rowMostPlayed:
		if d.noHistory {
			return "Most played", "all time"
		}
		return "Most played", fmt.Sprintf("last %d days", dashMostPlayedDays)
	case rowRediscover:
		return "Rediscover", "not played in 3 months"
	case rowUnplayed:
		return "Unplayed picks", "never played · R for new picks"
	case rowPlaylists:
		return "Playlists", ""
	}
	return "", ""
}

// rowVisible: a row that loaded empty is left out (nothing to show in
// Rediscover yet, no playlists…); loading and failed rows show.
func (d dashboardModel) rowVisible(r dashRow) bool {
	s := d.rows[r]
	return !s.loaded || s.err != nil || len(s.cards) > 0
}

// sections are the focusable sections in screen order: the band (0),
// then the visible rows (1+row).
func (d dashboardModel) sections() []int {
	out := []int{0}
	for r := range dashRowCount {
		if d.rowVisible(r) {
			out = append(out, 1+int(r))
		}
	}
	return out
}

// fixFocus moves the focus off a row that turned out empty: to the next
// row down, else up.
func (d *dashboardModel) fixFocus() {
	secs := d.sections()
	if slices.Contains(secs, d.focus) {
		return
	}
	for _, s := range secs {
		if s > d.focus {
			d.focus = s
			return
		}
	}
	d.focus = secs[len(secs)-1]
}

// SetWidth tells the dashboard its width, which sets how many cards a
// row shows.
func (d *dashboardModel) SetWidth(width int) {
	d.cols = gridCols(width - dashIndentW)
	for r := range dashRowCount {
		d.keepInView(r)
	}
}

// keepInView scrolls a row just enough to show its cursor.
func (d *dashboardModel) keepInView(r dashRow) {
	s := &d.rows[r]
	cols := max(d.cols, 1)
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+cols {
		s.offset = s.cursor - cols + 1
	}
	s.offset = max(min(s.offset, len(s.cards)-cols), 0)
}

// focusedRow is the row under focus, if the focus is on one.
func (d dashboardModel) focusedRow() (dashRow, bool) {
	if d.focus <= 0 {
		return 0, false
	}
	return dashRow(d.focus - 1), true
}

// HandleKey moves around the dashboard; what enter / q mean comes back
// as an outcome for the parent.
func (d dashboardModel) HandleKey(msg tea.KeyPressMsg, km KeyMap) (dashboardModel, dashboardOutcome) {
	secs := d.sections()
	at := max(slices.Index(secs, d.focus), 0)
	row, onRow := d.focusedRow()
	switch {
	case key.Matches(msg, km.Up):
		d.focus = secs[max(at-1, 0)]
	case key.Matches(msg, km.Down):
		d.focus = secs[min(at+1, len(secs)-1)]
	case key.Matches(msg, km.Left):
		if onRow && d.rows[row].cursor > 0 {
			d.rows[row].cursor--
			d.keepInView(row)
		}
	case key.Matches(msg, km.Right):
		if onRow && d.rows[row].cursor+1 < len(d.rows[row].cards) {
			d.rows[row].cursor++
			d.keepInView(row)
		}
	case key.Matches(msg, km.Enter):
		if !onRow {
			return d, dashOutcomeGoToPlaying
		}
		if c, ok := d.Selected(); ok {
			if c.playlist {
				return d, dashOutcomePlayPlaylist
			}
			return d, dashOutcomeOpenAlbum
		}
	case key.Matches(msg, km.Enqueue):
		if _, ok := d.Selected(); ok {
			return d, dashOutcomeEnqueue
		}
	}
	return d, dashOutcomeNone
}

// Selected is the card under the cursor of the focused row.
func (d dashboardModel) Selected() (dashCard, bool) {
	r, ok := d.focusedRow()
	if !ok {
		return dashCard{}, false
	}
	s := d.rows[r]
	if s.cursor >= len(s.cards) {
		return dashCard{}, false
	}
	return s.cards[s.cursor], true
}

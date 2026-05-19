package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/theopalhol/amptui/internal/plex"
)

// dashboardSection is one of the three horizontal tiles on the home screen.
type dashboardSection int

const (
	sectionRecentPlays dashboardSection = iota
	sectionRecentlyAdded
	sectionRecentPlaylists
	dashboardSectionCount
)

// dashboard tile sizing — outer width of one card and how many we ever
// load per section (browsing more than this on a dashboard tile isn't
// the point; users should switch to the library screen for depth).
const (
	dashCardOuterW   = 28
	dashCardOuterH   = 5
	dashFetchLimit   = 24
	dashFetchTimeout = 15 * time.Second
)

// dashboardOutcome is what the dashboard sub-model asks its parent to do
// after handling a key.
type dashboardOutcome int

const (
	dashOutcomeNone dashboardOutcome = iota
	dashOutcomePlayTrack    // selected: plex.Track
	dashOutcomeOpenAlbum    // selected: plex.RecentlyAddedAlbum
	dashOutcomeOpenPlaylist // selected: plex.Playlist
)

// Messages from the per-section background fetches.
type (
	dashboardPlaysMsg     struct{ tracks []plex.Track; err error }
	dashboardAddedMsg     struct{ albums []plex.RecentlyAddedAlbum; err error }
	dashboardPlaylistsMsg struct{ playlists []plex.Playlist; err error }
)

// dashboardModel owns the home screen: three horizontal tiles of recent
// data, a section cursor (which tile is focused), and a per-section
// item cursor (which card within the tile is focused). It does not own
// playback or the library cache — those are parent-state and reached
// via outcomes.
type dashboardModel struct {
	plays     []plex.Track
	added     []plex.RecentlyAddedAlbum
	playlists []plex.Playlist

	playsErr     error
	addedErr     error
	playlistsErr error

	loaded  [dashboardSectionCount]bool
	cursors [dashboardSectionCount]int
	section dashboardSection
}

func newDashboardModel() dashboardModel { return dashboardModel{} }

// Load returns a tea.Cmd that fans out the three section fetches in
// parallel; results arrive as dashboard*Msg values.
func (d dashboardModel) Load(client *plex.Client, sectionKey string) tea.Cmd {
	if client == nil || sectionKey == "" {
		return nil
	}
	return tea.Batch(
		fetchRecentPlays(client, sectionKey),
		fetchRecentlyAdded(client, sectionKey),
		fetchRecentPlaylists(client),
	)
}

func fetchRecentPlays(client *plex.Client, sectionKey string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), dashFetchTimeout)
		defer cancel()
		tracks, err := client.RecentlyPlayedTracks(ctx, sectionKey, dashFetchLimit)
		return dashboardPlaysMsg{tracks: tracks, err: err}
	}
}

func fetchRecentlyAdded(client *plex.Client, sectionKey string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), dashFetchTimeout)
		defer cancel()
		albums, err := client.RecentlyAddedAlbums(ctx, sectionKey, dashFetchLimit)
		return dashboardAddedMsg{albums: albums, err: err}
	}
}

func fetchRecentPlaylists(client *plex.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), dashFetchTimeout)
		defer cancel()
		ps, err := client.AudioPlaylists(ctx, dashFetchLimit)
		return dashboardPlaylistsMsg{playlists: ps, err: err}
	}
}

// ApplyPlays / ApplyAdded / ApplyPlaylists fold the async result into
// the model. Kept as methods so the parent can do them on msg receipt
// without poking at internals.
func (d *dashboardModel) ApplyPlays(m dashboardPlaysMsg) {
	d.plays, d.playsErr = m.tracks, m.err
	d.loaded[sectionRecentPlays] = true
	d.clampCursor(sectionRecentPlays)
}

func (d *dashboardModel) ApplyAdded(m dashboardAddedMsg) {
	d.added, d.addedErr = m.albums, m.err
	d.loaded[sectionRecentlyAdded] = true
	d.clampCursor(sectionRecentlyAdded)
}

func (d *dashboardModel) ApplyPlaylists(m dashboardPlaylistsMsg) {
	d.playlists, d.playlistsErr = m.playlists, m.err
	d.loaded[sectionRecentPlaylists] = true
	d.clampCursor(sectionRecentPlaylists)
}

func (d *dashboardModel) clampCursor(s dashboardSection) {
	n := d.sectionLen(s)
	if n == 0 {
		d.cursors[s] = 0
		return
	}
	if d.cursors[s] >= n {
		d.cursors[s] = n - 1
	}
}

func (d dashboardModel) sectionLen(s dashboardSection) int {
	switch s {
	case sectionRecentPlays:
		return len(d.plays)
	case sectionRecentlyAdded:
		return len(d.added)
	case sectionRecentPlaylists:
		return len(d.playlists)
	}
	return 0
}

// HandleKey routes a keypress. Outcomes that need parent state (play,
// drill) come back to the parent; nav within the dashboard is handled
// here.
func (d dashboardModel) HandleKey(msg tea.KeyPressMsg, km KeyMap) (dashboardModel, tea.Cmd, dashboardOutcome) {
	switch {
	case key.Matches(msg, km.Up):
		if d.section > 0 {
			d.section--
		}
		return d, nil, dashOutcomeNone
	case key.Matches(msg, km.Down):
		if d.section < dashboardSectionCount-1 {
			d.section++
		}
		return d, nil, dashOutcomeNone
	case key.Matches(msg, km.Left):
		if d.cursors[d.section] > 0 {
			d.cursors[d.section]--
		}
		return d, nil, dashOutcomeNone
	case key.Matches(msg, km.Right):
		if d.cursors[d.section]+1 < d.sectionLen(d.section) {
			d.cursors[d.section]++
		}
		return d, nil, dashOutcomeNone
	case key.Matches(msg, km.Enter):
		switch d.section {
		case sectionRecentPlays:
			if len(d.plays) > 0 {
				return d, nil, dashOutcomePlayTrack
			}
		case sectionRecentlyAdded:
			if len(d.added) > 0 {
				return d, nil, dashOutcomeOpenAlbum
			}
		case sectionRecentPlaylists:
			if len(d.playlists) > 0 {
				return d, nil, dashOutcomeOpenPlaylist
			}
		}
	}
	return d, nil, dashOutcomeNone
}

// SelectedTrack / SelectedAlbum / SelectedPlaylist return the item under
// the cursor for the section that produced the outcome, or zero values
// if there isn't one.
func (d dashboardModel) SelectedTrack() (plex.Track, bool) {
	if c := d.cursors[sectionRecentPlays]; c < len(d.plays) {
		return d.plays[c], true
	}
	return plex.Track{}, false
}

func (d dashboardModel) SelectedAlbum() (plex.RecentlyAddedAlbum, bool) {
	if c := d.cursors[sectionRecentlyAdded]; c < len(d.added) {
		return d.added[c], true
	}
	return plex.RecentlyAddedAlbum{}, false
}

func (d dashboardModel) SelectedPlaylist() (plex.Playlist, bool) {
	if c := d.cursors[sectionRecentPlaylists]; c < len(d.playlists) {
		return d.playlists[c], true
	}
	return plex.Playlist{}, false
}

// View renders the dashboard body. width is the terminal width, height
// is the rows available for the dashboard body (caller composes header
// / now-playing / footer around this). sp is forwarded for inline
// loading spinners.
func (d dashboardModel) View(width, height int, sp spinner.Model) string {
	tiles := []struct {
		title  string
		body   string
		loaded bool
		err    error
	}{
		{title: "Recently played", body: d.renderPlays(width), loaded: d.loaded[sectionRecentPlays], err: d.playsErr},
		{title: "Recently added", body: d.renderAdded(width), loaded: d.loaded[sectionRecentlyAdded], err: d.addedErr},
		{title: "Recent playlists", body: d.renderPlaylists(width), loaded: d.loaded[sectionRecentPlaylists], err: d.playlistsErr},
	}
	var b strings.Builder
	for i, t := range tiles {
		marker := "  "
		if dashboardSection(i) == d.section {
			marker = npStyle.Render("▶ ")
		}
		b.WriteString(marker + sectionStyle.Render(t.title) + "\n")
		switch {
		case t.err != nil:
			b.WriteString(dashIndent(errStyle.Render("error: " + t.err.Error())))
		case !t.loaded:
			b.WriteString(dashIndent(helpStyle.Render(sp.View() + "loading…")))
		default:
			b.WriteString(t.body)
		}
		if i < len(tiles)-1 {
			b.WriteString("\n\n")
		}
	}
	return lipgloss.NewStyle().Height(height).Render(b.String())
}

func (d dashboardModel) renderPlays(width int) string {
	if len(d.plays) == 0 {
		return dashIndent(helpStyle.Render("(nothing yet)"))
	}
	return d.renderRow(width, sectionRecentPlays, len(d.plays), func(i int) (string, string) {
		t := d.plays[i]
		return t.Title, t.Artist
	})
}

func (d dashboardModel) renderAdded(width int) string {
	if len(d.added) == 0 {
		return dashIndent(helpStyle.Render("(none)"))
	}
	return d.renderRow(width, sectionRecentlyAdded, len(d.added), func(i int) (string, string) {
		a := d.added[i]
		sub := a.Artist
		if a.Year > 0 {
			sub = fmt.Sprintf("%s · %d", a.Artist, a.Year)
		}
		return a.Title, sub
	})
}

func (d dashboardModel) renderPlaylists(width int) string {
	if len(d.playlists) == 0 {
		return dashIndent(helpStyle.Render("(no playlists)"))
	}
	return d.renderRow(width, sectionRecentPlaylists, len(d.playlists), func(i int) (string, string) {
		p := d.playlists[i]
		sub := fmt.Sprintf("%d tracks", p.LeafCount)
		if p.Smart {
			sub = "smart · " + sub
		}
		return p.Title, sub
	})
}

// renderRow lays out a horizontal strip of cards. Only the cards that
// fit in the visible window are rendered; the cursor stays in view by
// shifting the window when it would fall off either edge. Cards are
// composed manually (see renderDashCard) so widths use go-runewidth's
// terminal-cell measurement and adjacent cards stay border-aligned
// even when titles contain ambiguous-width glyphs (♥ ❤ ♡, etc.).
func (d dashboardModel) renderRow(width int, s dashboardSection, n int, item func(i int) (title, sub string)) string {
	avail := width - 4 // 2-char indent each side
	cols := avail / dashCardOuterW
	if cols < 1 {
		cols = 1
	}
	cursor := d.cursors[s]
	start := 0
	if cursor >= cols {
		start = cursor - cols + 1
	}
	end := start + cols
	if end > n {
		end = n
	}
	cells := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		title, sub := item(i)
		focused := i == cursor && d.section == s
		cells = append(cells, renderDashCard(title, sub, dashCardOuterW, dashCardOuterH, focused))
	}
	return dashIndent(joinCardsHorizontally(cells))
}

// dashIndent pads every line of s by the dashboard's gutter width.
// PaddingLeft applies to all rows (where prepending "  " would only
// indent the first line of a multi-line block).
func dashIndent(s string) string {
	return lipgloss.NewStyle().PaddingLeft(2).Render(s)
}

// renderDashCard composes one dashboard card from scratch so width
// math uses terminal-cell measurement throughout (via the helpers in
// widthsafe.go). See that file's header for why we don't just call
// lipgloss.NewStyle().Border(...).Width(N).Render(...).
func renderDashCard(title, sub string, outerW, outerH int, focused bool) string {
	innerW := outerW - 2 // borders
	if innerW < 2 {
		innerW = 2
	}
	interiorH := outerH - 2
	if interiorH < 1 {
		interiorH = 1
	}

	titleLine := centerCells(truncateCells(title, innerW), innerW)
	blank := strings.Repeat(" ", innerW)

	// Interior rows: title and (optional) sub centered vertically.
	rows := make([]string, interiorH)
	for i := range rows {
		rows[i] = blank
	}
	switch {
	case sub == "":
		mid := (interiorH - 1) / 2
		rows[mid] = titleLine
	default:
		subLine := centerCells(truncateCells(sub, innerW), innerW)
		mid := (interiorH - 2) / 2
		rows[mid] = titleLine
		if mid+1 < interiorH {
			rows[mid+1] = helpStyle.Render(subLine)
		}
	}

	edgeColor := theme.Muted
	if focused {
		edgeColor = theme.Accent
	}
	edge := lipgloss.NewStyle().Foreground(edgeColor)
	if focused {
		// Bold the title line on focused cards (mirrors what
		// cardCursorStyle used to do via lipgloss).
		rows[(interiorH-2)/2] = lipgloss.NewStyle().
			Foreground(edgeColor).Bold(true).Render(titleLine)
		if sub == "" {
			rows[(interiorH-1)/2] = lipgloss.NewStyle().
				Foreground(edgeColor).Bold(true).Render(titleLine)
		}
	}

	border := lipgloss.RoundedBorder()
	top := edge.Render(border.TopLeft + strings.Repeat(border.Top, innerW) + border.TopRight)
	bot := edge.Render(border.BottomLeft + strings.Repeat(border.Bottom, innerW) + border.BottomRight)
	left := edge.Render(border.Left)
	right := edge.Render(border.Right)

	lines := make([]string, 0, outerH)
	lines = append(lines, top)
	for _, r := range rows {
		lines = append(lines, left+r+right)
	}
	lines = append(lines, bot)
	return strings.Join(lines, "\n")
}



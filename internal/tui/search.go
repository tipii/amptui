package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
)

// librarySyncTimeout caps the cold-start library fetch when no cache exists.
const librarySyncTimeout = 90 * time.Second

// searchResultLimit caps the number of results returned per query.
const searchResultLimit = 200

// loadOrSyncLibrary returns a Cmd that loads the cache from disk when fresh,
// otherwise syncs from Plex and persists. Result arrives as libraryReadyMsg
// or libraryErrMsg.
func loadOrSyncLibrary(client media.Backend, plexLib media.MusicLibrary) tea.Cmd {
	return func() tea.Msg {
		if l, err := library.Load(plexLib.UUID); err == nil && l.IsFresh(plexLib) {
			return libraryReadyMsg{lib: l}
		}
		return runSync(client, plexLib)
	}
}

// syncLibrary forces a re-sync from Plex, bypassing the on-disk cache.
// Used by the manual refresh key (R).
func syncLibrary(client media.Backend, plexLib media.MusicLibrary) tea.Cmd {
	return func() tea.Msg {
		return runSync(client, plexLib)
	}
}

func runSync(client media.Backend, plexLib media.MusicLibrary) tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), librarySyncTimeout)
	defer cancel()
	l, err := library.Sync(ctx, client, plexLib)
	if err != nil {
		return libraryErrMsg{err: err}
	}
	return libraryReadyMsg{lib: l}
}

// Messages from the background library loader.
type (
	libraryReadyMsg struct{ lib *library.Library }
	libraryErrMsg   struct{ err error }
)

// librariesReadyMsg is the result of an async MusicLibraries fetch — fired
// after the user enters credentials in the settings screen so first-time
// setup can complete without a restart.
type librariesReadyMsg struct {
	libs []media.MusicLibrary
	err  error
}

// fetchLibraries asks the server for its music library sections. Used on
// first-time setup once the user has entered credentials.
func fetchLibraries(client media.Backend) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		libs, err := client.MusicLibraries(ctx)
		return librariesReadyMsg{libs: libs, err: err}
	}
}

// serverNameMsg carries the backend's friendly name for the breadcrumb.
type serverNameMsg struct{ name string }

// fetchServerName resolves the backend's display name in the background.
// Failures are swallowed (the breadcrumb just omits the name).
func fetchServerName(client media.Backend) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		name, err := client.ServerName(ctx)
		if err != nil {
			return serverNameMsg{}
		}
		return serverNameMsg{name: name}
	}
}

// searchFilters lists the kind-filters that Tab cycles through, paired with
// the labels shown in the modal's filter bar.
var (
	searchFilters     = [][]library.Kind{nil, {library.KindArtist}, {library.KindAlbum}, {library.KindTrack}}
	searchFilterNames = []string{"All", "Artists", "Albums", "Songs"}
)

// searchOutcome is what the search sub-model asks its parent to do after
// handling a key. Most keys are sub-model-internal (cursor, filter,
// typing); a few — close, play, enqueue, jump — need parent state
// (queue, browser crumbs).
type searchOutcome int

const (
	searchOutcomeNone searchOutcome = iota
	searchOutcomeClose
	searchOutcomePlayTrack    // SelectedEntry() is a track to play
	searchOutcomeEnqueueTrack // SelectedEntry() is a track to enqueue
	searchOutcomeJumpArtist   // SelectedEntry() is an artist to drill into
	searchOutcomeJumpAlbum    // SelectedEntry() is an album to drill into
)

// searchModel owns the fuzzy-finder modal: the text input, the ranked
// result list, the cursor, and the kind filter. It does not own the
// library cache — that's a parent-level concern passed in at use sites.
type searchModel struct {
	input   textinput.Model
	results []library.Entry
	cursor  int
	filter  int
	open    bool
}

// newSearchModel returns a ready-but-closed sub-model.
func newSearchModel() searchModel {
	si := textinput.New()
	si.Placeholder = "search artists, albums, tracks…"
	si.Prompt = "> "
	return searchModel{input: si}
}

// Open shows the modal, clears prior state, and returns the input's
// focus cmd so the cursor blinks immediately.
func (s searchModel) Open() (searchModel, tea.Cmd) {
	s.open = true
	s.input.Reset()
	s.results = nil
	s.cursor = 0
	return s, s.input.Focus()
}

// Close hides the modal and clears all per-session state.
func (s searchModel) Close() searchModel {
	s.open = false
	s.input.Blur()
	s.input.Reset()
	s.results = nil
	s.cursor = 0
	return s
}

// IsOpen reports whether the modal is currently visible.
func (s searchModel) IsOpen() bool { return s.open }

// SelectedEntry returns the entry under the cursor, or nil if there
// isn't one. Used by parent after a Play/Enqueue/Jump outcome.
func (s searchModel) SelectedEntry() *library.Entry {
	if s.cursor < 0 || s.cursor >= len(s.results) {
		return nil
	}
	e := s.results[s.cursor]
	return &e
}

// HandleKey routes a keypress while the search modal is open. lib is
// the active library snapshot (may be nil while syncing) — passed in
// so re-running the search on a typed character can hit the cache.
func (s searchModel) HandleKey(msg tea.KeyPressMsg, km KeyMap, lib *library.Library) (searchModel, tea.Cmd, searchOutcome) {
	switch {
	case key.Matches(msg, km.Quit):
		return s, tea.Quit, searchOutcomeNone
	case key.Matches(msg, km.InputBack):
		return s, nil, searchOutcomeClose
	case key.Matches(msg, km.CycleFilter):
		s.filter = (s.filter + 1) % len(searchFilters)
		s.runQuery(lib)
		return s, nil, searchOutcomeNone
	case key.Matches(msg, km.InputUp):
		s.moveCursor(-1)
		return s, nil, searchOutcomeNone
	case key.Matches(msg, km.InputDown):
		s.moveCursor(1)
		return s, nil, searchOutcomeNone
	case key.Matches(msg, km.InputEnter):
		e := s.SelectedEntry()
		if e == nil {
			return s, nil, searchOutcomeNone
		}
		switch e.Kind {
		case library.KindTrack:
			return s, nil, searchOutcomePlayTrack
		case library.KindArtist:
			return s, nil, searchOutcomeJumpArtist
		case library.KindAlbum:
			return s, nil, searchOutcomeJumpAlbum
		}
		return s, nil, searchOutcomeNone
	case key.Matches(msg, km.EnqueueFromSearch):
		if e := s.SelectedEntry(); e != nil && e.Kind == library.KindTrack {
			return s, nil, searchOutcomeEnqueueTrack
		}
		return s, nil, searchOutcomeNone
	}
	// Anything else is a text-input keystroke — re-run on change.
	prev := s.input.Value()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	if s.input.Value() != prev {
		s.runQuery(lib)
	}
	return s, cmd, searchOutcomeNone
}

// RunQuery re-runs the active query against lib. Used after the parent
// learns the library is finally ready (so prior keystrokes show results).
func (s *searchModel) RunQuery(lib *library.Library) { s.runQuery(lib) }

func (s *searchModel) runQuery(lib *library.Library) {
	q := s.input.Value()
	if lib == nil || q == "" {
		s.results = nil
		s.cursor = 0
		return
	}
	s.results = lib.Search(q, searchFilters[s.filter], searchResultLimit)
	if s.cursor >= len(s.results) {
		s.cursor = 0
	}
}

func (s *searchModel) moveCursor(delta int) {
	if len(s.results) == 0 {
		return
	}
	c := s.cursor + delta
	if c < 0 {
		c = 0
	}
	if c >= len(s.results) {
		c = len(s.results) - 1
	}
	s.cursor = c
}

// View renders the modal body (without the modal box / border — caller
// wraps with modalFrame). Stats from the parent (library readiness,
// spinner, errors) are passed in so the sub-model stays decoupled from
// library loading state.
func (s searchModel) View(innerWidth, resultsHeight int, lib *library.Library, libErr error, sp spinner.Model) string {
	title := headerStyle.Render("Search") + "   " + s.filterBar()
	input := s.input.View()

	var body string
	switch {
	case lib == nil && libErr != nil:
		body = errStyle.Render("library error: " + libErr.Error())
	case lib == nil:
		body = helpStyle.Render(sp.View() + "syncing library… results will appear here when ready")
	case s.input.Value() == "":
		body = helpStyle.Render("type to search · tab cycles filter")
	case len(s.results) == 0:
		body = helpStyle.Render("no matches")
	default:
		body = s.resultsView(innerWidth, resultsHeight)
	}
	return title + "\n" + input + "\n\n" + body
}

// filterBar renders the [All] Artists Albums Songs tabs with the
// current filter highlighted.
func (s searchModel) filterBar() string {
	parts := make([]string, len(searchFilterNames))
	for i, name := range searchFilterNames {
		if i == s.filter {
			parts[i] = headerStyle.Render("[" + name + "]")
		} else {
			parts[i] = helpStyle.Render(name)
		}
	}
	return strings.Join(parts, " ")
}

// resultsView formats the ranked results, marking the cursor row.
// resultsHeight is the number of rows available for results.
func (s searchModel) resultsView(innerWidth, resultsHeight int) string {
	if resultsHeight < 1 {
		resultsHeight = 1
	}
	start := 0
	if s.cursor >= resultsHeight {
		start = s.cursor - resultsHeight + 1
	}
	end := start + resultsHeight
	if end > len(s.results) {
		end = len(s.results)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		e := s.results[i]
		cursor := "  "
		if i == s.cursor {
			cursor = npStyle.Render("▶ ")
		}
		b.WriteString(cursor + formatSearchEntry(e, innerWidth-2))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// entryToTrack reconstructs a media.Track from a library Entry so it can be
// fed into playTracks / enqueue.
func entryToTrack(e library.Entry) media.Track {
	return media.Track{
		RatingKey:       e.RatingKey,
		Title:           e.Title,
		Album:           e.Album,
		Artist:          e.Artist,
		AlbumRatingKey:  e.AlbumKey,
		ArtistRatingKey: e.ArtistKey,
		Index:           e.Index,
		Year:            e.Year,
		Duration:        e.Duration,
		PartKey:         e.PartKey,
	}
}

// jumpToArtist closes the search modal and points the browser at the
// artist's albums. Builds a Libraries → Artists crumb trail so the
// breadcrumb reads naturally and "back" walks back through the same
// hierarchy a manual drill-down would have produced.
func (m Model) jumpToArtist(artistKey string) (Model, tea.Cmd) {
	m.search = m.search.Close()
	m.crumbs = m.crumbs[:0]
	m.pushLibrariesCrumb()
	m.pushArtistsCrumb(artistKey)
	m.applyItems(levelAlbums, m.albumItems(artistKey))
	m.artistMeta, m.albumMeta = nil, nil
	m.metaLoading = true
	// Rebuild the artist surfaces with kittyIDs tied to this ratingKey,
	// matching the per-key pattern in drillDown so a revisit reuses the
	// terminal's already-cached image at the same ID.
	m.artistHeaderPic = newKeyedPicture("artist-header", artistKey, headerThumbCellsW, headerThumbCellsH)
	m.artistModalPic = newKeyedPicture("artist-modal", artistKey, modalThumbCellsW, modalThumbCellsH)
	return m, tea.Batch(
		fetchArtistMeta(m.client, artistKey),
		fetchArtwork(m.client, artistKey, "artist"),
	)
}

// jumpToAlbum closes the search modal and points the browser at the
// album's tracks, pushing synthetic Artists and Albums crumbs along
// the way so the breadcrumb shows "Music / Artist / Album / Tracks"
// and "back" lands on the artist's album list.
func (m Model) jumpToAlbum(albumKey string) (Model, tea.Cmd) {
	m.search = m.search.Close()
	m.crumbs = m.crumbs[:0]
	m.pushLibrariesCrumb()

	// Look the album up in the cache so we know its parent artist —
	// pushArtists/AlbumsCrumb need the artist key to build the right
	// item lists.
	if m.library != nil {
		var artistKey string
		for _, e := range m.library.Entries {
			if e.Kind == library.KindAlbum && e.RatingKey == albumKey {
				artistKey = e.ArtistKey
				break
			}
		}
		if artistKey != "" {
			m.pushArtistsCrumb(artistKey)
			m.pushAlbumsCrumb(artistKey, albumKey)
		}
	}
	m.applyItems(levelTracks, m.trackItems(albumKey))
	m.albumMeta = nil
	m.metaLoading = true
	m.albumHeaderPic = newKeyedPicture("album-header", albumKey, headerThumbCellsW, headerThumbCellsH)
	m.albumModalPic = newKeyedPicture("album-modal", albumKey, modalThumbCellsW, modalThumbCellsH)
	return m, tea.Batch(
		fetchAlbumMeta(m.client, albumKey),
		fetchArtwork(m.client, albumKey, "album"),
	)
}

// pushArtistsCrumb pushes a synthetic Artists-level crumb whose cursor
// is parked on the artist with the given key. Lets "back" from the
// next level land on a real artist list rather than the picker.
func (m *Model) pushArtistsCrumb(artistKey string) {
	items := m.artistItems()
	idx := 0
	for i, it := range items {
		if ai, ok := it.(artistItem); ok && ai.artist.RatingKey == artistKey {
			idx = i
			break
		}
	}
	title := ""
	if ai, ok := items[idx].(artistItem); ok {
		title = ai.artist.Title
	}
	m.crumbs = append(m.crumbs, crumb{
		level: levelArtists,
		title: title,
		items: items,
		index: idx,
	})
}

// pushAlbumsCrumb pushes a synthetic Albums-level crumb whose cursor
// is parked on the album with the given key.
func (m *Model) pushAlbumsCrumb(artistKey, albumKey string) {
	items := m.albumItems(artistKey)
	idx := 0
	for i, it := range items {
		if ai, ok := it.(albumItem); ok && ai.album.RatingKey == albumKey {
			idx = i
			break
		}
	}
	title := ""
	if ai, ok := items[idx].(albumItem); ok {
		title = ai.album.Title
	}
	m.crumbs = append(m.crumbs, crumb{
		level: levelAlbums,
		title: title,
		items: items,
		index: idx,
	})
}

// pushLibrariesCrumb pushes a synthetic Libraries crumb built from m.libs,
// used by search jumps so esc has somewhere coherent to land. The
// breadcrumb label is the active library's title — the one the cache was
// built against — so the trail reads naturally.
func (m *Model) pushLibrariesCrumb() {
	items := make([]list.Item, len(m.libs))
	for i, l := range m.libs {
		items[i] = libraryItem{lib: l}
	}
	title := "Libraries"
	switch {
	case m.startupLibrary != nil:
		title = m.startupLibrary.Title
	case len(m.libs) > 0:
		title = m.libs[0].Title
	}
	m.crumbs = append(m.crumbs, crumb{
		level: levelLibraries,
		title: title,
		items: items,
		index: 0,
	})
}

package tui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/theopalhol/amptui/internal/library"
	"github.com/theopalhol/amptui/internal/plex"
)

// librarySyncTimeout caps the cold-start library fetch when no cache exists.
const librarySyncTimeout = 90 * time.Second

// searchResultLimit caps the number of results returned per query.
const searchResultLimit = 200

// loadOrSyncLibrary returns a Cmd that loads the cache from disk when fresh,
// otherwise syncs from Plex and persists. Result arrives as libraryReadyMsg
// or libraryErrMsg.
func loadOrSyncLibrary(client *plex.Client, plexLib plex.MusicLibrary) tea.Cmd {
	return func() tea.Msg {
		if l, err := library.Load(plexLib.UUID); err == nil && l.IsFresh(plexLib) {
			return libraryReadyMsg{lib: l}
		}
		return runSync(client, plexLib)
	}
}

// syncLibrary forces a re-sync from Plex, bypassing the on-disk cache.
// Used by the manual refresh key (R).
func syncLibrary(client *plex.Client, plexLib plex.MusicLibrary) tea.Cmd {
	return func() tea.Msg {
		return runSync(client, plexLib)
	}
}

func runSync(client *plex.Client, plexLib plex.MusicLibrary) tea.Msg {
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

// searchFilters lists the kind-filters that Tab cycles through, paired with
// the labels shown in the modal's filter bar.
var (
	searchFilters     = [][]library.Kind{nil, {library.KindArtist}, {library.KindAlbum}, {library.KindTrack}}
	searchFilterNames = []string{"All", "Artists", "Albums", "Songs"}
)

// openSearch shows the search modal and focuses its input.
func (m *Model) openSearch() tea.Cmd {
	m.showSearch = true
	m.searchInput.Reset()
	m.searchResults = nil
	m.searchCursor = 0
	return m.searchInput.Focus()
}

// closeSearch hides the search modal and clears its state.
func (m *Model) closeSearch() {
	m.showSearch = false
	m.searchInput.Blur()
	m.searchInput.Reset()
	m.searchResults = nil
	m.searchCursor = 0
}

// runSearch re-fuzzies the current query and updates the visible results.
func (m *Model) runSearch() {
	q := m.searchInput.Value()
	if m.library == nil || q == "" {
		m.searchResults = nil
		m.searchCursor = 0
		return
	}
	m.searchResults = m.library.Search(q, searchFilters[m.searchFilter], searchResultLimit)
	if m.searchCursor >= len(m.searchResults) {
		m.searchCursor = 0
	}
}

// cycleSearchFilter advances the kind filter (Tab) and re-runs the search.
func (m *Model) cycleSearchFilter() {
	m.searchFilter = (m.searchFilter + 1) % len(searchFilters)
	m.runSearch()
}

// moveSearchCursor clamps the cursor inside the current result list.
func (m *Model) moveSearchCursor(delta int) {
	if len(m.searchResults) == 0 {
		return
	}
	c := m.searchCursor + delta
	if c < 0 {
		c = 0
	}
	if c >= len(m.searchResults) {
		c = len(m.searchResults) - 1
	}
	m.searchCursor = c
}

// activateSearchResult handles enter on the selected result.
//   - track:  play it (replaces queue, like enter on a track in the browser)
//   - artist: jump the browser to that artist's albums
//   - album:  jump the browser to that album's tracks
func (m Model) activateSearchResult() (tea.Model, tea.Cmd) {
	if m.searchCursor < 0 || m.searchCursor >= len(m.searchResults) {
		return m, nil
	}
	e := m.searchResults[m.searchCursor]
	switch e.Kind {
	case library.KindTrack:
		t := entryToTrack(e)
		m.closeSearch()
		return m.playTracks([]plex.Track{t}, 0)
	case library.KindArtist:
		return m.jumpToArtist(e.RatingKey), nil
	case library.KindAlbum:
		return m.jumpToAlbum(e.RatingKey), nil
	}
	return m, nil
}

// enqueueSearchResult appends the highlighted track to the queue. Only
// tracks can be enqueued individually from search — for whole-artist or
// whole-album enqueue use the browser (Q) after jumping there with enter.
func (m *Model) enqueueSearchResult() {
	if m.searchCursor < 0 || m.searchCursor >= len(m.searchResults) {
		return
	}
	e := m.searchResults[m.searchCursor]
	if e.Kind != library.KindTrack {
		return
	}
	m.enqueue(entryToTrack(e))
}

// entryToTrack reconstructs a plex.Track from a library Entry so it can be
// fed into playTracks / enqueue.
func entryToTrack(e library.Entry) plex.Track {
	return plex.Track{
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
// artist's albums. Resets the crumb trail to a single Libraries frame so
// `esc` from the new view returns to the library picker.
func (m Model) jumpToArtist(artistKey string) Model {
	m.closeSearch()
	m.crumbs = m.crumbs[:0]
	m.pushLibrariesCrumb()
	m.applyItems(levelAlbums, m.albumItems(artistKey))
	return m
}

// jumpToAlbum closes the search modal and points the browser at the album's
// tracks. See jumpToArtist for the crumb behavior.
func (m Model) jumpToAlbum(albumKey string) Model {
	m.closeSearch()
	m.crumbs = m.crumbs[:0]
	m.pushLibrariesCrumb()
	m.applyItems(levelTracks, m.trackItems(albumKey))
	return m
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

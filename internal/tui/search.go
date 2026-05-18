package tui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/theopalhol/amptui/internal/index"
	"github.com/theopalhol/amptui/internal/plex"
)

// indexBuildTimeout caps the cold-start library fetch when no cache exists.
const indexBuildTimeout = 90 * time.Second

// searchResultLimit caps the number of results returned per query.
const searchResultLimit = 200

// loadOrBuildIndex returns a Cmd that loads the library index from disk when
// fresh, otherwise rebuilds and persists it. Result arrives as
// indexReadyMsg or indexErrMsg.
func loadOrBuildIndex(client *plex.Client, lib plex.MusicLibrary) tea.Cmd {
	return func() tea.Msg {
		if idx, err := index.Load(lib.UUID); err == nil && idx.IsFresh(lib) {
			return indexReadyMsg{idx: idx}
		}
		ctx, cancel := context.WithTimeout(context.Background(), indexBuildTimeout)
		defer cancel()
		idx, err := index.Build(ctx, client, lib)
		if err != nil {
			return indexErrMsg{err: err}
		}
		_ = idx.Save() // best effort; an unwritable cache shouldn't break search
		return indexReadyMsg{idx: idx}
	}
}

// Messages from the background index loader.
type (
	indexReadyMsg struct{ idx *index.Index }
	indexErrMsg   struct{ err error }
)

// searchFilters lists the kind-filters that Tab cycles through, paired with
// the labels shown in the modal's filter bar.
var (
	searchFilters     = [][]index.Kind{nil, {index.KindArtist}, {index.KindAlbum}, {index.KindTrack}}
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
	if m.index == nil || q == "" {
		m.searchResults = nil
		m.searchCursor = 0
		return
	}
	m.searchResults = m.index.Search(q, searchFilters[m.searchFilter], searchResultLimit)
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
	case index.KindTrack:
		t := entryToTrack(e)
		m.closeSearch()
		return m.playTracks([]plex.Track{t}, 0)
	case index.KindArtist:
		return m.jumpToArtist(e.RatingKey)
	case index.KindAlbum:
		return m.jumpToAlbum(e.RatingKey)
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
	if e.Kind != index.KindTrack {
		return
	}
	m.enqueue(entryToTrack(e))
}

// entryToTrack reconstructs a plex.Track from an indexed entry so it can be
// fed into playTracks / enqueue.
func entryToTrack(e index.Entry) plex.Track {
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
func (m Model) jumpToArtist(ratingKey string) (tea.Model, tea.Cmd) {
	m.closeSearch()
	m.crumbs = m.crumbs[:0]
	m.pushLibrariesCrumb()
	m.loading, m.err = true, nil
	return m, m.fetchAlbums(ratingKey)
}

// jumpToAlbum closes the search modal and points the browser at the album's
// tracks. See jumpToArtist for the crumb behavior.
func (m Model) jumpToAlbum(ratingKey string) (tea.Model, tea.Cmd) {
	m.closeSearch()
	m.crumbs = m.crumbs[:0]
	m.pushLibrariesCrumb()
	m.loading, m.err = true, nil
	return m, m.fetchTracks(ratingKey)
}

// pushLibrariesCrumb pushes a synthetic Libraries crumb built from m.libs,
// used by search jumps so esc has somewhere coherent to land.
func (m *Model) pushLibrariesCrumb() {
	items := make([]list.Item, len(m.libs))
	for i, l := range m.libs {
		items[i] = libraryItem{lib: l}
	}
	m.crumbs = append(m.crumbs, crumb{
		level: levelLibraries,
		title: "Libraries",
		items: items,
		index: 0,
	})
}

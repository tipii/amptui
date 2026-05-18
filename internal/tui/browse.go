package tui

import (
	"errors"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

type level int

const (
	levelLibraries level = iota
	levelArtists
	levelAlbums
	levelTracks
)

// crumb is a saved navigation frame, restored when the user goes back.
type crumb struct {
	level level
	title string
	items []list.Item
	index int
}

// drillDown enters the selected item: opens the next level via library
// lookups (instant — no network), or for a track, starts playback.
func (m Model) drillDown() (tea.Model, tea.Cmd) {
	if m.loading {
		return m, nil
	}
	if m.library == nil {
		// Library isn't ready yet; opening a child level would have nothing
		// to show. Leave the user where they are.
		m.err = errors.New("library still syncing — try again in a moment")
		return m, nil
	}
	sel := m.selectedItem()
	if sel == nil {
		return m, nil
	}

	switch it := sel.(type) {
	case libraryItem:
		m.pushCrumb(it.lib.Title)
		m.applyItems(levelArtists, m.artistItems())
		return m, nil
	case artistItem:
		m.pushCrumb(it.artist.Title)
		m.applyItems(levelAlbums, m.albumItems(it.artist.RatingKey))
		return m, nil
	case albumItem:
		m.pushCrumb(it.album.Title)
		m.applyItems(levelTracks, m.trackItems(it.album.RatingKey))
		return m, nil
	case albumActionItem:
		return m.playTracks(it.tracks, 0)
	case trackItem:
		return m.playTracks(it.tracks, it.pos)
	}
	return m, nil
}

// goBack restores the previous navigation frame.
func (m Model) goBack() (tea.Model, tea.Cmd) {
	if m.loading || len(m.crumbs) == 0 {
		return m, nil
	}
	c := m.crumbs[len(m.crumbs)-1]
	m.crumbs = m.crumbs[:len(m.crumbs)-1]

	m.level = c.level
	m.err = nil
	m.list.SetItems(c.items)
	// Restore the list's heading by level (the crumb's title is the
	// breadcrumb label for the item the user drilled into, not the page).
	m.list.Title = m.titleForLevel(c.level)
	m.list.Select(c.index)
	return m, nil
}

// pushCrumb saves the current frame so goBack can restore it. title is the
// breadcrumb label for this frame — typically the name of the item the user
// is drilling into (so the trail reads "Music / Al Green / …").
func (m *Model) pushCrumb(title string) {
	m.crumbs = append(m.crumbs, crumb{
		level: m.level,
		title: title,
		items: m.list.Items(),
		index: m.list.Index(),
	})
}

// applyItems installs a freshly fetched level into the list. Also resets
// the grid cursor + scroll so the new level starts cleanly at the top.
func (m *Model) applyItems(lvl level, items []list.Item) {
	m.loading = false
	m.err = nil
	m.level = lvl
	m.list.SetItems(items)
	m.list.Select(0)
	m.list.Title = m.titleForLevel(lvl)
	m.list.SetSize(m.width, m.listHeight())
	m.gridCursor = 0
	m.gridScrollTop = 0
}

// selectedItem returns the highlighted item, accounting for grid mode (the
// grid keeps its own cursor independent of the bubbles list).
func (m Model) selectedItem() list.Item {
	if m.gridView && m.supportsGrid() {
		items := m.list.Items()
		if m.gridCursor >= 0 && m.gridCursor < len(items) {
			return items[m.gridCursor]
		}
		return nil
	}
	return m.list.SelectedItem()
}

func (m Model) titleForLevel(lvl level) string {
	switch lvl {
	case levelArtists:
		return "Artists"
	case levelAlbums:
		return "Albums"
	case levelTracks:
		return "Tracks"
	default:
		return "Libraries"
	}
}

// refreshCurrentLevel re-derives the current view's items from the library
// — used after a manual refresh so counts/titles update in place. Cursor
// position is preserved (it's tied to the bubbles list, not items identity).
// Crumbs aren't regenerated, so go-back may show slightly stale data until
// the user navigates forward again.
func (m *Model) refreshCurrentLevel() {
	if m.library == nil {
		return
	}
	switch m.level {
	case levelArtists:
		m.list.SetItems(m.artistItems())
	case levelAlbums:
		if key := m.parentRatingKey(); key != "" {
			m.list.SetItems(m.albumItems(key))
		}
	case levelTracks:
		if key := m.parentRatingKey(); key != "" {
			m.list.SetItems(m.trackItems(key))
		}
	}
}

// parentRatingKey returns the RatingKey of the item the user drilled into
// to reach the current level — read off the top crumb's highlighted item.
func (m Model) parentRatingKey() string {
	if len(m.crumbs) == 0 {
		return ""
	}
	c := m.crumbs[len(m.crumbs)-1]
	if c.index < 0 || c.index >= len(c.items) {
		return ""
	}
	switch it := c.items[c.index].(type) {
	case artistItem:
		return it.artist.RatingKey
	case albumItem:
		return it.album.RatingKey
	}
	return ""
}

// --- library-driven item builders ---

// artistItems builds the list rows for the Artists level from the cache.
func (m Model) artistItems() []list.Item {
	if m.library == nil {
		return nil
	}
	artists := m.library.Artists()
	items := make([]list.Item, len(artists))
	for i, a := range artists {
		items[i] = artistItem{artist: a}
	}
	return items
}

// albumItems builds the list rows for the Albums level of one artist.
func (m Model) albumItems(artistKey string) []list.Item {
	if m.library == nil {
		return nil
	}
	albums := m.library.Albums(artistKey)
	items := make([]list.Item, len(albums))
	for i, a := range albums {
		items[i] = albumItem{album: a}
	}
	return items
}

// trackItems builds the list rows for the Tracks level of one album,
// prepended with the "Play album" action row.
func (m Model) trackItems(albumKey string) []list.Item {
	if m.library == nil {
		return nil
	}
	tracks := m.library.Tracks(albumKey)
	items := make([]list.Item, 0, len(tracks)+1)
	if len(tracks) > 0 {
		items = append(items, albumActionItem{tracks: tracks})
	}
	for i, t := range tracks {
		items = append(items, trackItem{track: t, tracks: tracks, pos: i})
	}
	return items
}

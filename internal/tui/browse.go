package tui

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// fetchTimeout bounds each Plex request fired from the UI.
const fetchTimeout = 15 * time.Second

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

// Messages delivered by async fetch commands.
type (
	artistsMsg []list.Item
	albumsMsg  []list.Item
	tracksMsg  []list.Item
	errMsg     struct{ err error }
)

// drillDown enters the selected item: fetches the next level, or for a track,
// starts playback.
func (m Model) drillDown() (tea.Model, tea.Cmd) {
	if m.loading {
		return m, nil
	}
	sel := m.list.SelectedItem()
	if sel == nil {
		return m, nil
	}

	switch it := sel.(type) {
	case libraryItem:
		m.pushCrumb()
		m.loading, m.err = true, nil
		return m, m.fetchArtists(it.lib.Key)
	case artistItem:
		m.pushCrumb()
		m.loading, m.err = true, nil
		return m, m.fetchAlbums(it.artist.RatingKey)
	case albumItem:
		m.pushCrumb()
		m.loading, m.err = true, nil
		return m, m.fetchTracks(it.album.RatingKey)
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
	m.list.Title = c.title
	m.list.Select(c.index)
	return m, nil
}

// pushCrumb saves the current frame so goBack can restore it.
func (m *Model) pushCrumb() {
	m.crumbs = append(m.crumbs, crumb{
		level: m.level,
		title: m.list.Title,
		items: m.list.Items(),
		index: m.list.Index(),
	})
}

// applyItems installs a freshly fetched level into the list.
func (m *Model) applyItems(lvl level, items []list.Item) {
	m.loading = false
	m.err = nil
	m.level = lvl
	m.list.SetItems(items)
	m.list.Select(0)
	m.list.Title = m.titleForLevel(lvl)
	m.list.SetSize(m.width, m.listHeight())
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

// --- async fetch commands ---

func (m Model) fetchArtists(sectionKey string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		artists, err := client.Artists(ctx, sectionKey)
		if err != nil {
			return errMsg{fmt.Errorf("loading artists: %w", err)}
		}
		items := make([]list.Item, len(artists))
		for i, a := range artists {
			items[i] = artistItem{artist: a}
		}
		return artistsMsg(items)
	}
}

func (m Model) fetchAlbums(artistKey string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		albums, err := client.Albums(ctx, artistKey)
		if err != nil {
			return errMsg{fmt.Errorf("loading albums: %w", err)}
		}
		items := make([]list.Item, len(albums))
		for i, a := range albums {
			items[i] = albumItem{album: a}
		}
		return albumsMsg(items)
	}
}

func (m Model) fetchTracks(albumKey string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		tracks, err := client.Tracks(ctx, albumKey)
		if err != nil {
			return errMsg{fmt.Errorf("loading tracks: %w", err)}
		}
		items := make([]list.Item, 0, len(tracks)+1)
		if len(tracks) > 0 {
			items = append(items, albumActionItem{tracks: tracks})
		}
		for i, t := range tracks {
			items = append(items, trackItem{track: t, tracks: tracks, pos: i})
		}
		return tracksMsg(items)
	}
}

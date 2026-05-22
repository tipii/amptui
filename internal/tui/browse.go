package tui

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/theopalhol/amptui/internal/imgcache"
	"github.com/theopalhol/amptui/internal/media"
)

// metaFetchTimeout caps the per-screen metadata fetch.
const metaFetchTimeout = 10 * time.Second

// artistMetaMsg / albumMetaMsg deliver the result of a per-screen
// metadata fetch fired by drillDown when entering levelAlbums /
// levelTracks.
type (
	artistMetaMsg struct {
		meta *media.ArtistMetadata
		err  error
	}
	albumMetaMsg struct {
		meta *media.AlbumMetadata
		err  error
	}
)

func fetchArtistMeta(client media.Backend, ratingKey string) tea.Cmd {
	if client == nil || ratingKey == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), metaFetchTimeout)
		defer cancel()
		meta, err := client.ArtistMetadata(ctx, ratingKey)
		return artistMetaMsg{meta: meta, err: err}
	}
}

func fetchAlbumMeta(client media.Backend, ratingKey string) tea.Cmd {
	if client == nil || ratingKey == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), metaFetchTimeout)
		defer cancel()
		meta, err := client.AlbumMetadata(ctx, ratingKey)
		return albumMetaMsg{meta: meta, err: err}
	}
}

// thumbReadyMsg delivers a decoded thumbnail image. kind selects
// which Model slot it goes into; decoding happens in the fetch
// goroutine so the UI thread doesn't block on PNG/JPEG decode.
// Rendering happens via picture.Model.SetImage in the parent Update,
// keeping all image-state ownership in picture.Model.
type thumbReadyMsg struct {
	kind string // "artist", "album", or "grid:<ratingKey>"
	img  image.Image
	err  error
}

// gridThumbCellsW / H is the cell footprint of a thumbnail inside one
// grid card. Half-block rendering packs 2 image rows per cell, so a
// (cellsW × cellsH) block looks visually square when cellsW ≈ cellsH×2
// (typical 2:1 monospace cells). Sized to fill the card's inner area
// (cardIdealOuterW-2 wide × cardOuterH-2-1 tall) so the thumb is the
// dominant visual element with one row left at the bottom for the
// title.
const (
	// 12 image cols × (6 cells × 2 rows) = 12 × 12 image pixels =
	// visually square. Fills the card's inner area exactly.
	gridThumbCellsW = cardIdealOuterW - cardBorderCols // 12
	gridThumbCellsH = cardOuterH - cardBorderCols - 1  // 6
)

// fetchArtwork loads the default thumb for a Plex item by ratingKey,
// cache-first, through the direct /library/metadata/<key>/thumb URL
// (no transcoder hop). One synthetic cache key — "grid/<ratingKey>" —
// is shared across every view that wants this item's artwork (grid
// card, list row, artist/album header, info modal), so the second
// caller for a given item hits the on-disk cache instead of Plex.
// kind is "grid:<key>", "artist", or "album" — used by the parent to
// route the decoded image into the right picture.Model slot.
func fetchArtwork(client media.Backend, ratingKey, kind string) tea.Cmd {
	if client == nil || ratingKey == "" {
		return nil
	}
	cacheKey := "grid/" + ratingKey
	return func() tea.Msg {
		data, _ := imgcache.Get(cacheKey, 0, 0)
		if len(data) == 0 {
			ctx, cancel := context.WithTimeout(context.Background(), metaFetchTimeout)
			defer cancel()
			var err error
			data, err = client.FetchImage(ctx, client.ArtworkURL(ratingKey))
			if err != nil {
				return thumbReadyMsg{kind: kind, err: err}
			}
			_ = imgcache.Put(cacheKey, 0, 0, data)
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return thumbReadyMsg{kind: kind, err: err}
		}
		return thumbReadyMsg{kind: kind, img: img}
	}
}

// gridThumbFetches batches per-card fetches for every item in items
// that doesn't already have a picture.Model.
func (m Model) gridThumbFetches(items []list.Item) tea.Cmd {
	if !m.cfg.Images || m.client == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, it := range items {
		var key string
		switch v := it.(type) {
		case artistItem:
			key = v.artist.RatingKey
		case albumItem:
			key = v.album.RatingKey
		}
		if key == "" {
			continue
		}
		if _, ok := m.gridPics[key]; ok {
			continue
		}
		cmds = append(cmds, fetchArtwork(m.client, key, "grid:"+key))
	}
	return tea.Batch(cmds...)
}

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
		items := m.artistItems()
		m.applyItems(levelArtists, items)
		return m, m.visibleArtworkFetches()
	case artistItem:
		m.pushCrumb(it.artist.Title)
		items := m.albumItems(it.artist.RatingKey)
		m.applyItems(levelAlbums, items)
		m.artistMeta, m.albumMeta = nil, nil
		m.metaLoading = true
		// Rebuild the artist surfaces with kittyIDs derived from this
		// artist's ratingKey so a revisit hits the terminal's existing
		// image at the same ID — no inter-run stale-image flicker.
		m.artistHeaderPic = newKeyedPicture("artist-header", it.artist.RatingKey, headerThumbCellsW, headerThumbCellsH)
		m.artistModalPic = newKeyedPicture("artist-modal", it.artist.RatingKey, modalThumbCellsW, modalThumbCellsH)
		// No SetImage(nil) on the old models: picture.Model.SetImage
		// docs call out that synchronously clearing the placeholder
		// grid creates a visible blank + glyph-fallback frame between
		// renders. The freshly-constructed models above have no image
		// yet, so View renders empty until SetImage lands. When the
		// fetch fails (artist without artwork), the thumbReadyMsg
		// error handler clears.
		cmds := []tea.Cmd{
			fetchArtistMeta(m.client, it.artist.RatingKey),
			fetchArtwork(m.client, it.artist.RatingKey, "artist"),
			m.visibleArtworkFetches(),
		}
		return m, tea.Batch(cmds...)
	case albumItem:
		m.pushCrumb(it.album.Title)
		m.applyItems(levelTracks, m.trackItems(it.album.RatingKey))
		m.albumMeta = nil
		m.metaLoading = true
		m.albumHeaderPic = newKeyedPicture("album-header", it.album.RatingKey, headerThumbCellsW, headerThumbCellsH)
		m.albumModalPic = newKeyedPicture("album-modal", it.album.RatingKey, modalThumbCellsW, modalThumbCellsH)
		return m, tea.Batch(
			fetchAlbumMeta(m.client, it.album.RatingKey),
			fetchArtwork(m.client, it.album.RatingKey, "album"),
		)
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
	// listHeight depends on level (image-bearing levels carry a taller
	// chrome). Reflow now that we're back on a different level so the
	// list doesn't keep its previous frame's size.
	m.list.SetSize(m.width, m.listHeight())
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
	if m.currentGridView() {
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

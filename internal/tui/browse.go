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

	"github.com/tipii/amptui/internal/imgcache"
	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
)

// metaFetchTimeout caps the per-screen metadata fetch.
const metaFetchTimeout = 10 * time.Second

// artistMetaMsg / albumMetaMsg deliver the result of a per-screen
// metadata fetch fired by drillDown when entering levelAlbums /
// levelTracks. key is the item fetched: a slow reply for an artist the
// user has already left must not overwrite the one now on screen.
type (
	artistMetaMsg struct {
		key  string
		meta *media.ArtistMetadata
		err  error
	}
	albumMetaMsg struct {
		key  string
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
		return artistMetaMsg{key: ratingKey, meta: meta, err: err}
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
		return albumMetaMsg{key: ratingKey, meta: meta, err: err}
	}
}

// thumbReadyMsg delivers a decoded thumbnail image. kind selects
// which Model slot it goes into; decoding happens in the fetch
// goroutine so the UI thread doesn't block on PNG/JPEG decode.
// Rendering happens via picture.Model.SetImage in the parent Update,
// keeping all image-state ownership in picture.Model.
type thumbReadyMsg struct {
	kind string // "artist", "album", or "grid:<ratingKey>"
	key  string // the item's RatingKey, checked for "artist" / "album"
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
				return thumbReadyMsg{kind: kind, key: ratingKey, err: err}
			}
			_ = imgcache.Put(cacheKey, 0, 0, data)
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			// A truncated or corrupt entry would otherwise fail here on
			// every visit; drop it so the next one refetches.
			_ = imgcache.Delete(cacheKey, 0, 0)
			return thumbReadyMsg{kind: kind, key: ratingKey, err: err}
		}
		return thumbReadyMsg{kind: kind, key: ratingKey, img: img}
	}
}

// maxThumbPics bounds the grid/list thumbnails kept in memory. Each one
// holds a decoded image, and browsing a large library used to keep every
// one ever seen.
const maxThumbPics = 150

// trimThumbs drops off-screen thumbnails once there are more than
// maxThumbPics. They come back from the disk cache when scrolled to.
func (m *Model) trimThumbs() {
	if len(m.gridPics) <= maxThumbPics {
		return
	}
	keep := map[string]bool{}
	for _, it := range m.visibleItems() {
		switch v := it.(type) {
		case artistItem:
			keep[v.artist.RatingKey] = true
		case albumItem:
			keep[v.album.RatingKey] = true
		}
	}
	for key := range m.gridPics {
		if !keep[key] {
			delete(m.gridPics, key)
			delete(m.listPics, key)
			delete(m.artRequested, key)
		}
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
		// artRequested covers fetches still in flight (fast scrolling
		// fired duplicates) and ones that failed (an item without art
		// was re-requested on every keypress).
		if _, ok := m.gridPics[key]; ok || m.artRequested[key] {
			continue
		}
		m.artRequested[key] = true
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
// index is the highlighted row within items (the full, unfiltered list);
// key is the RatingKey (or library key) of the item that was opened from
// it, i.e. the parent of the level now on screen.
type crumb struct {
	level level
	title string
	items []list.Item
	index int
	key   string
}

// drillDown enters the selected item: opens the next level via library
// lookups (instant — no network), or for a track, starts playback.
func (m Model) drillDown() (tea.Model, tea.Cmd) {
	if m.loading {
		return m, nil
	}
	// Picking a library works without a loaded cache — it's how you get
	// one — so it's handled before the "still syncing" guard.
	if it, ok := m.selectedItem().(libraryItem); ok {
		return m.openLibrary(it.lib)
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
	case artistItem:
		m.pushCrumb(it.artist.Title, it.artist.RatingKey)
		m.applyItems(levelAlbums, m.albumItems(it.artist.RatingKey))
		return m, tea.Batch(m.showArtist(it.artist.RatingKey), m.visibleArtworkFetches())
	case albumItem:
		m.pushCrumb(it.album.Title, it.album.RatingKey)
		m.applyItems(levelTracks, m.trackItems(it.album.RatingKey))
		return m, m.showAlbum(it.album.RatingKey)
	case albumActionItem:
		return m.playTracks(it.tracks, 0)
	case trackItem:
		return m.playTracks(it.tracks, it.pos)
	}
	return m, nil
}

// showArtist points the artist header / info modal at key and fetches
// its metadata and artwork. Replies are matched against curArtistKey,
// so one for a previously shown artist is dropped.
func (m *Model) showArtist(key string) tea.Cmd {
	m.curArtistKey = key
	m.artistMeta = nil
	m.metaLoading = true
	// Rebuild the surfaces with kittyIDs derived from this ratingKey so a
	// revisit hits the terminal's existing image at the same ID — no
	// inter-run stale-image flicker. No SetImage(nil) on the old models:
	// picture.Model.SetImage docs call out that synchronously clearing the
	// placeholder grid creates a visible blank + glyph-fallback frame
	// between renders. The fresh models have no image yet, so View
	// renders empty until SetImage lands; when the fetch fails (no
	// artwork), the thumbReadyMsg error handler clears.
	m.artistHeaderPic = newKeyedPicture("artist-header", key, headerThumbCellsW, headerThumbCellsH)
	m.artistModalPic = newKeyedPicture("artist-modal", key, modalThumbCellsW, modalThumbCellsH)
	return tea.Batch(fetchArtistMeta(m.client, key), fetchArtwork(m.client, key, "artist"))
}

// showAlbum is showArtist for the album header / info modal.
func (m *Model) showAlbum(key string) tea.Cmd {
	m.curAlbumKey = key
	m.albumMeta = nil
	m.metaLoading = true
	m.albumHeaderPic = newKeyedPicture("album-header", key, headerThumbCellsW, headerThumbCellsH)
	m.albumModalPic = newKeyedPicture("album-modal", key, modalThumbCellsW, modalThumbCellsH)
	return tea.Batch(fetchAlbumMeta(m.client, key), fetchArtwork(m.client, key, "album"))
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
	// c.items is the unfiltered list and c.index points into it, so the
	// filter has to go; left applied, SetItems would empty the list
	// until a re-filter cmd ran.
	m.list.ResetFilter()
	m.list.SetItems(c.items)
	// Restore the list's heading by level (the crumb's title is the
	// breadcrumb label for the item the user drilled into, not the page).
	m.list.Title = m.listTitleForLevel(c.level)
	m.list.Select(c.index)
	// listHeight depends on level (image-bearing levels carry a taller
	// chrome). Reflow now that we're back on a different level so the
	// list doesn't keep its previous frame's size.
	m.list.SetSize(m.width, m.listHeight())
	m.gridCursor = c.index
	m.gridScrollTop = 0
	m.ensureCursorVisible()
	return m, m.visibleArtworkFetches()
}

// pushCrumb saves the current frame so goBack can restore it. title is the
// breadcrumb label for this frame — typically the name of the item the user
// is drilling into (so the trail reads "Music / Al Green / …") — and key
// is that item's RatingKey.
func (m *Model) pushCrumb(title, key string) {
	m.crumbs = append(m.crumbs, crumb{
		level: m.level,
		title: title,
		items: m.list.Items(),
		index: m.selectedIndex(),
		key:   key,
	})
}

// selectedIndex is the highlighted row's position in the full item list:
// the grid's own cursor in grid mode (the list's index doesn't follow
// it), and the unfiltered index when a list filter is applied.
func (m Model) selectedIndex() int {
	if m.currentGridView() {
		return m.gridCursor
	}
	return m.list.GlobalIndex()
}

// openLibrary drills from the picker into lib. The loaded cache is
// reused when it already belongs to lib; otherwise lib becomes the
// active library and its cache is loaded (or synced), landing on its
// artists when ready — the same path startup takes.
func (m Model) openLibrary(lib media.MusicLibrary) (tea.Model, tea.Cmd) {
	m.pushCrumb(lib.Title, lib.Key)
	if active, ok := m.activeLibrary(); ok && active.Key == lib.Key && m.library != nil {
		m.applyItems(levelArtists, m.artistItems())
		return m, m.visibleArtworkFetches()
	}
	if m.client == nil {
		m.crumbs = m.crumbs[:len(m.crumbs)-1]
		return m, nil
	}
	m.activeLib = &lib
	m.library = nil
	m.libraryErr = nil
	m.loading = true
	m.librarySyncing = true
	return m, tea.Batch(loadOrSyncLibrary(m.client, lib), m.dashboard.Load(m.client, lib.Key))
}

// activeLibrary is the library the cache, dashboard and R act on: the
// one picked in the library picker or configured as default, else the
// first. ok is false when the server reported none.
func (m Model) activeLibrary() (media.MusicLibrary, bool) {
	if m.activeLib != nil {
		return *m.activeLib, true
	}
	if len(m.libs) > 0 {
		return m.libs[0], true
	}
	return media.MusicLibrary{}, false
}

// applyItems installs a freshly fetched level into the list. Also resets
// the grid cursor + scroll so the new level starts cleanly at the top,
// and drops any filter, which was typed against the previous level.
func (m *Model) applyItems(lvl level, items []list.Item) {
	m.loading = false
	m.err = nil
	m.level = lvl
	m.list.ResetFilter()
	m.list.SetItems(items)
	m.list.Select(0)
	m.list.Title = m.listTitleForLevel(lvl)
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

// listTitleForLevel is the heading the browser list shows: the level name
// plus the active ordering ("Albums · year ↑"), so the current sort is
// visible without opening the modal.
func (m Model) listTitleForLevel(lvl level) string {
	return m.titleForLevel(lvl) + m.sortSuffix(lvl)
}

// refreshCurrentLevel re-derives the current view's items from the library
// — used after a manual refresh so counts/titles update in place. Cursor
// position is preserved (it's tied to the bubbles list, not items identity).
// Crumbs aren't regenerated, so go-back may show slightly stale data until
// the user navigates forward again. The returned cmd re-applies an active
// list filter to the new items; it must be run, or a filtered list
// renders empty.
func (m *Model) refreshCurrentLevel() tea.Cmd {
	if m.library == nil {
		return nil
	}
	switch m.level {
	case levelArtists:
		return m.list.SetItems(m.artistItems())
	case levelAlbums:
		if key := m.parentRatingKey(); key != "" {
			return m.list.SetItems(m.albumItems(key))
		}
	case levelTracks:
		if key := m.parentRatingKey(); key != "" {
			return m.list.SetItems(m.trackItems(key))
		}
	}
	return nil
}

// parentRatingKey returns the RatingKey of the item the user drilled into
// to reach the current level, as recorded on the top crumb.
func (m Model) parentRatingKey() string {
	if len(m.crumbs) == 0 {
		return ""
	}
	return m.crumbs[len(m.crumbs)-1].key
}

// --- library-driven item builders ---

// artistItems builds the list rows for the Artists level from the cache,
// in the user's chosen order. Every path that lands on this level —
// drill-down, a search jump, a post-sync refresh — goes through here, so
// ordering is applied in exactly one place per level.
func (m Model) artistItems() []list.Item {
	if m.library == nil {
		return nil
	}
	artists := m.library.Artists()
	library.SortArtists(artists, m.sortArtist)
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
	library.SortAlbums(albums, m.sortAlbum)
	items := make([]list.Item, len(albums))
	for i, a := range albums {
		items[i] = albumItem{album: a}
	}
	return items
}

// trackItems builds the list rows for the Tracks level of one album,
// prepended with the "Play album" action row.
//
// The sort applies to the playback order too: "Play album" and "enter on
// a track" queue the tracks as displayed, so what you see is what plays.
// Enqueueing the album from the Albums level (q) is unaffected — it reads
// the album's own running order, since no track list is on screen there.
func (m Model) trackItems(albumKey string) []list.Item {
	if m.library == nil {
		return nil
	}
	tracks := m.library.Tracks(albumKey)
	library.SortTracks(tracks, m.sortTrack)
	items := make([]list.Item, 0, len(tracks)+1)
	if len(tracks) > 0 {
		items = append(items, albumActionItem{tracks: tracks})
	}
	for i, t := range tracks {
		items = append(items, trackItem{track: t, tracks: tracks, pos: i})
	}
	return items
}

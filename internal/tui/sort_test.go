package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
)

// newAlbumsModel builds a browser sitting on one artist's albums, with the
// Artists crumb behind it so refreshCurrentLevel can resolve the parent.
// Albums are deliberately out of alphabetical order relative to their years
// so year / title / added sorts each produce a distinct leading row.
func newAlbumsModel(t *testing.T, cfg config.Config) Model {
	t.Helper()

	libs := []media.MusicLibrary{{Key: "1", Title: "Music"}}
	m := New(cfg, nil, nil, nil, libs, nil)
	m.screen = screenBrowser
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = updated.(Model)

	day := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	m.library = &library.Library{
		SchemaVersion: library.CacheSchemaVersion,
		Entries: []library.Entry{
			{Kind: library.KindArtist, Title: "Led Zeppelin", RatingKey: "ar1"},
			{Kind: library.KindAlbum, Title: "Houses of the Holy", RatingKey: "al1",
				Artist: "Led Zeppelin", ArtistKey: "ar1", Year: 1973, AddedAt: day},
			{Kind: library.KindAlbum, Title: "Physical Graffiti", RatingKey: "al2",
				Artist: "Led Zeppelin", ArtistKey: "ar1", Year: 1975, AddedAt: day.AddDate(0, 0, 9)},
			{Kind: library.KindAlbum, Title: "Coda", RatingKey: "al3",
				Artist: "Led Zeppelin", ArtistKey: "ar1", Year: 1982, AddedAt: day.AddDate(0, 0, 4)},
		},
		AlbumTrackCount: map[string]int{"al1": 8, "al2": 15, "al3": 8},
		AlbumDuration: map[string]time.Duration{
			"al1": 41 * time.Minute, "al2": 82 * time.Minute, "al3": 33 * time.Minute,
		},
	}
	m.librarySyncing = false
	m.crumbs = []crumb{{
		level: levelArtists,
		title: "Led Zeppelin",
		items: m.artistItems(),
		index: 0,
	}}
	m.applyItems(levelAlbums, m.albumItems("ar1"))
	return m
}

// albumOrder is the album titles as currently listed, top to bottom.
func albumOrder(m Model) []string {
	var out []string
	for _, it := range m.list.Items() {
		if ai, ok := it.(albumItem); ok {
			out = append(out, ai.album.Title)
		}
	}
	return out
}

func press(m Model, msg tea.KeyPressMsg) Model {
	upd, _ := m.Update(msg)
	return upd.(Model)
}

var (
	keyO     = tea.KeyPressMsg{Code: 'o', Text: "o"}
	keyJ     = tea.KeyPressMsg{Code: 'j', Text: "j"}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

func TestSortModalOpensOnActiveField(t *testing.T) {
	m := newAlbumsModel(t, config.Config{ServerURL: "https://x", PlexToken: "t"})
	m = press(m, keyO)

	if !m.showSort {
		t.Fatal("o should open the sort modal")
	}
	// Default album sort is year, the first field offered.
	if m.sortCursor != 0 {
		t.Errorf("cursor should start on the active field (year), got index %d", m.sortCursor)
	}

	out := m.View().Content
	for _, want := range []string{"Sort · Albums", "Year", "Title", "Date added", "Duration", "↑ asc"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the sort modal", want)
		}
	}
	t.Log("\n" + out)
}

// TestSortModalOffersOnlyLevelFields guards against showing an ordering a
// level can't answer — "Album count" is artists-only.
func TestSortModalOffersOnlyLevelFields(t *testing.T) {
	m := newAlbumsModel(t, config.Config{ServerURL: "https://x", PlexToken: "t"})
	m = press(m, keyO)
	if out := m.View().Content; strings.Contains(out, "Album count") {
		t.Error("the Albums level should not offer the artists-only Album count sort")
	}
}

func TestSortApplyChangesOrder(t *testing.T) {
	m := newAlbumsModel(t, config.Config{ServerURL: "https://x", PlexToken: "t"})
	if got := albumOrder(m)[0]; got != "Houses of the Holy" {
		t.Fatalf("default (year asc) should lead with the 1973 album, got %q", got)
	}

	// o, down to "Date added", enter.
	m = press(m, keyO)
	m = press(m, keyJ)
	m = press(m, keyJ)
	m = press(m, keyEnter)

	if m.showSort {
		t.Error("applying a sort should close the modal")
	}
	if got := albumOrder(m)[0]; got != "Physical Graffiti" {
		t.Errorf("date added should default to descending (newest first), got %q", got)
	}
	if m.sortAlbum.Field != library.SortAdded || m.sortAlbum.Dir != library.Desc {
		t.Errorf("sortAlbum = %+v, want added:desc", m.sortAlbum)
	}
	// The heading advertises the active order.
	if !strings.Contains(m.list.Title, "date added ↓") {
		t.Errorf("list title should show the sort, got %q", m.list.Title)
	}
}

// TestSortReselectingActiveFieldFlipsDirection covers the "o enter"
// shortcut for reversing the current order.
func TestSortReselectingActiveFieldFlipsDirection(t *testing.T) {
	m := newAlbumsModel(t, config.Config{ServerURL: "https://x", PlexToken: "t"})

	m = press(m, keyO)
	m = press(m, keyEnter) // enter on the already-active "Year"

	if m.sortAlbum.Dir != library.Desc {
		t.Fatalf("re-picking the active field should flip direction, got %+v", m.sortAlbum)
	}
	if got := albumOrder(m)[0]; got != "Coda" {
		t.Errorf("year desc should lead with the 1982 album, got %q", got)
	}

	m = press(m, keyO)
	m = press(m, keyEnter)
	if m.sortAlbum.Dir != library.Asc {
		t.Errorf("a second flip should return to ascending, got %+v", m.sortAlbum)
	}
}

// TestSortReturnsCursorToTop is the UX guarantee: re-sorting shows you the
// front of the new order, rather than leaving the cursor parked wherever
// the previously-selected row ended up. (Observed live: keeping the place
// made "sort by most albums" open on a 2-album artist and read as broken.)
func TestSortReturnsCursorToTop(t *testing.T) {
	m := newAlbumsModel(t, config.Config{ServerURL: "https://x", PlexToken: "t"})
	// Park the cursor at the bottom of the year-ascending list.
	m.list.Select(2)
	m.gridCursor = 2
	m.gridScrollTop = 1

	m = press(m, keyO)
	m = press(m, keyJ) // Title
	m = press(m, keyEnter)

	if m.list.Index() != 0 {
		t.Errorf("cursor should return to the top after a re-sort, at %d", m.list.Index())
	}
	if m.gridCursor != 0 || m.gridScrollTop != 0 {
		t.Errorf("grid cursor/scroll should reset, got cursor=%d scroll=%d", m.gridCursor, m.gridScrollTop)
	}
	sel, ok := m.selectedItem().(albumItem)
	if !ok || sel.album.Title != "Coda" {
		t.Errorf("top row under title:asc should be Coda, got %#v", m.selectedItem())
	}
}

// TestSortPersistsToConfig covers the Plex-like "remembers your order"
// behaviour — the spec is written back to the config the app saves.
func TestSortPersistsToConfig(t *testing.T) {
	m := newAlbumsModel(t, config.Config{ServerURL: "https://x", PlexToken: "t"})
	m = press(m, keyO)
	m = press(m, keyJ) // Title
	m = press(m, keyEnter)

	if got, want := m.cfg.SortAlbum, "title:asc"; got != want {
		t.Errorf("cfg.SortAlbum = %q, want %q", got, want)
	}

	// And a saved config seeds the next launch.
	next := newAlbumsModel(t, config.Config{
		ServerURL: "https://x", PlexToken: "t", SortAlbum: "title:desc",
	})
	if next.sortAlbum.Field != library.SortTitle || next.sortAlbum.Dir != library.Desc {
		t.Errorf("config sort not restored: %+v", next.sortAlbum)
	}
	if got := albumOrder(next)[0]; got != "Physical Graffiti" {
		t.Errorf("restored title:desc should lead with %q, got %q", "Physical Graffiti", got)
	}
}

// TestSortModalEscCloses verifies esc abandons the picker without touching
// the current order.
func TestSortModalEscCloses(t *testing.T) {
	m := newAlbumsModel(t, config.Config{ServerURL: "https://x", PlexToken: "t"})
	before := albumOrder(m)

	m = press(m, keyO)
	m = press(m, keyJ)
	m = press(m, keyEsc)

	if m.showSort {
		t.Error("esc should close the sort modal")
	}
	after := albumOrder(m)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("esc must not re-order: %v -> %v", before, after)
		}
	}
}

// TestSortNoOpOnLibrariesLevel covers the level that has nothing to sort:
// `o` must not open an empty modal.
func TestSortNoOpOnLibrariesLevel(t *testing.T) {
	libs := []media.MusicLibrary{{Key: "1", Title: "Music"}, {Key: "2", Title: "Soundtracks"}}
	m := New(config.Config{ServerURL: "https://x", PlexToken: "t"}, nil, nil, nil, libs, nil)
	m.screen = screenBrowser
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = updated.(Model)

	m = press(m, keyO)
	if m.showSort {
		t.Error("the Libraries level has no sort options; o should no-op")
	}
}

// TestSortAppliesToTracksLevel exercises the third sortable level, whose
// rows are built alongside the "Play album" action row.
func TestSortAppliesToTracksLevel(t *testing.T) {
	m := newAlbumsModel(t, config.Config{ServerURL: "https://x", PlexToken: "t"})
	m.library.Entries = append(m.library.Entries,
		library.Entry{Kind: library.KindTrack, Title: "The Song Remains the Same", RatingKey: "t1",
			Album: "Houses of the Holy", AlbumKey: "al1", Artist: "Led Zeppelin", ArtistKey: "ar1",
			Index: 1, Duration: 5 * time.Minute},
		library.Entry{Kind: library.KindTrack, Title: "The Rain Song", RatingKey: "t2",
			Album: "Houses of the Holy", AlbumKey: "al1", Artist: "Led Zeppelin", ArtistKey: "ar1",
			Index: 2, Duration: 7 * time.Minute},
	)
	m.crumbs = append(m.crumbs, crumb{
		level: levelAlbums,
		title: "Houses of the Holy",
		items: m.albumItems("ar1"),
		index: 0, // al1
	})
	m.applyItems(levelTracks, m.trackItems("al1"))

	// Track number ascending by default: the action row, then track 1.
	if _, ok := m.list.Items()[0].(albumActionItem); !ok {
		t.Fatalf("expected the Play album action row first, got %#v", m.list.Items()[0])
	}
	if ti, ok := m.list.Items()[1].(trackItem); !ok || ti.track.Index != 1 {
		t.Fatalf("expected track 1 first, got %#v", m.list.Items()[1])
	}

	// o, down twice to "Duration", enter → longest first (desc is not the
	// default for duration, so ascending: 5m then 7m).
	m = press(m, keyO)
	m = press(m, keyJ)
	m = press(m, keyJ)
	m = press(m, keyEnter)

	if m.sortTrack.Field != library.SortDuration {
		t.Fatalf("sortTrack = %+v, want duration", m.sortTrack)
	}
	if _, ok := m.list.Items()[0].(albumActionItem); !ok {
		t.Error("the Play album action row must stay pinned at the top after a re-sort")
	}
	ti, ok := m.list.Items()[1].(trackItem)
	if !ok || ti.track.Title != "The Song Remains the Same" {
		t.Errorf("duration ascending should lead with the 5m track, got %#v", m.list.Items()[1])
	}
}

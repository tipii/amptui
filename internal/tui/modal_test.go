package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/theopalhol/amptui/internal/config"
	"github.com/theopalhol/amptui/internal/library"
	"github.com/theopalhol/amptui/internal/plex"
)

// TestMain redirects $XDG_CONFIG_HOME and $XDG_CACHE_HOME to per-run temp
// directories so any test that ends up calling config.Save() or
// library.Save() can NEVER clobber the user's real config or cache file.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "amptui-test-*")
	if err != nil {
		panic(err)
	}
	_ = os.MkdirAll(filepath.Join(tmp, "config"), 0o755)
	_ = os.MkdirAll(filepath.Join(tmp, "cache"), 0o755)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	_ = os.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "cache"))
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// newQueueModel builds a realistic model: a sized browser list in the
// background and the queue modal open on top.
func newQueueModel(t *testing.T) Model {
	t.Helper()

	libs := []plex.MusicLibrary{
		{Key: "1", Title: "Music"},
		{Key: "2", Title: "Soundtracks"},
		{Key: "3", Title: "Podcasts"},
	}
	m := New(config.Config{ServerURL: "https://x", Token: "t"}, nil, nil, libs, nil)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = updated.(Model)

	m.queue = []plex.Track{
		{Title: "I'm a Ram", Artist: "Al Green", Album: "Gets Next to You", Duration: 3 * time.Minute},
		{Title: "Tired of Being Alone", Artist: "Al Green", Album: "Gets Next to You", Duration: 162 * time.Second},
		{Title: "Driving Wheel", Artist: "Al Green", Album: "Gets Next to You", Duration: 200 * time.Second},
	}
	m.queueIdx = 1
	m.nowPlaying = &plex.Track{Title: "Tired of Being Alone", Artist: "Al Green"}
	m.openQueue()
	return m
}

func TestQueueModalRenders(t *testing.T) {
	m := newQueueModel(t)
	out := m.View().Content

	if !strings.Contains(out, "╭") || !strings.Contains(out, "╰") {
		t.Errorf("expected a rounded border in the modal view")
	}
	if !strings.Contains(out, "Queue · 3 track(s)") {
		t.Errorf("expected the modal title in the view")
	}
	// The browser list should still be visible behind the modal.
	if !strings.Contains(out, "Soundtracks") {
		t.Errorf("expected the background browser list to show through the overlay")
	}
	// Visual check: run `go test -run TestQueueModalRenders -v` to eyeball it.
	t.Log("\n" + out)
}

func TestQueueModalEmpty(t *testing.T) {
	m := newQueueModel(t)
	m.queue = nil
	m.nowPlaying = nil
	m.rebuildQueueList()

	out := m.View().Content
	if !strings.Contains(out, "queue is empty") {
		t.Errorf("expected empty-queue hint in the view")
	}
}

func TestHelpModalRenders(t *testing.T) {
	m := newQueueModel(t)
	m.showQueue = false
	m.showHelp = true

	out := m.View().Content
	if !strings.Contains(out, "Keybindings") {
		t.Errorf("expected the help modal title in the view")
	}
	if !strings.Contains(out, "Soundtracks") {
		t.Errorf("expected the background list to show through the overlay")
	}
	t.Log("\n" + out)
}

// TestMoveQueueItem covers reordering the currently-playing track.
func TestMoveQueueItem(t *testing.T) {
	m := newQueueModel(t)
	// queue = [I'm a Ram, Tired of Being Alone, Driving Wheel]; idx 1 plays.
	m.queueList.Select(1)
	m.moveQueueItem(1)

	if got := m.queue[2].Title; got != "Tired of Being Alone" {
		t.Errorf("moved track should be at idx 2, got %q", got)
	}
	if m.queueIdx != 2 {
		t.Errorf("queueIdx must follow the playing track, got %d", m.queueIdx)
	}
}

// TestSearchModalRenders verifies the search modal composites with a
// non-empty result list, including the kind filter bar and the cursor row.
func TestSearchModalRenders(t *testing.T) {
	m := newQueueModel(t)
	m.showQueue = false

	// Inject a small in-memory library, open the modal, and seed a query.
	m.library = &library.Library{Entries: []library.Entry{
		{Kind: library.KindArtist, Title: "Al Green", RatingKey: "ar1"},
		{Kind: library.KindAlbum, Title: "Gets Next to You", RatingKey: "al1", Artist: "Al Green"},
		{Kind: library.KindTrack, Title: "I'm a Ram", RatingKey: "t1", Album: "Gets Next to You", Artist: "Al Green"},
	}}
	m.librarySyncing = false
	_ = m.openSearch()
	m.searchInput.SetValue("al green")
	m.runSearch()
	m.showSearch = true

	out := m.View().Content
	if !strings.Contains(out, "Search") {
		t.Errorf("expected search modal title in the view")
	}
	if !strings.Contains(out, "[All]") {
		t.Errorf("expected filter bar with [All] highlighted")
	}
	if !strings.Contains(out, "Al Green") {
		t.Errorf("expected the Al Green artist row in the results")
	}
	if !strings.Contains(out, "Soundtracks") {
		t.Errorf("expected the background browser list to show through")
	}
	t.Log("\n" + out)
}

// TestSettingsScreenRenders verifies the settings page shows server info
// (URL, masked token, default library) and library cache stats.
func TestSettingsScreenRenders(t *testing.T) {
	libs := []plex.MusicLibrary{{Key: "1", Title: "Music", UUID: "uuid-test"}}
	cfg := config.Config{
		ServerURL:      "https://plex.example.dev",
		Token:          "abcdef1234567890wxyz",
		DefaultLibrary: "Music",
	}
	m := New(cfg, nil, nil, libs, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	m.library = &library.Library{
		SchemaVersion: library.CacheSchemaVersion,
		SectionUUID:   "uuid-test",
		SyncedAt:      time.Now().Add(-3 * time.Minute),
		Entries: []library.Entry{
			{Kind: library.KindArtist, Title: "Al Green", RatingKey: "ar1"},
			{Kind: library.KindAlbum, Title: "Gets Next to You", RatingKey: "al1"},
			{Kind: library.KindTrack, Title: "I'm a Ram", RatingKey: "t1"},
		},
	}
	m.librarySyncing = false
	m.screen = screenSettings

	out := m.View().Content
	for _, want := range []string{
		"Settings /",
		"https://plex.example.dev",
		"Token",                  // password field label (value masked by huh)
		"Music",                  // default library value
		"1 artists",              // entry breakdown in cache stats
		"Default view (Artists)", // huh select label
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in settings view", want)
		}
	}
	t.Log("\n" + out)
}

// TestSettingsSelectEdit drives j/k inside an open Select to confirm the
// per-field edit-mode navigation actually toggles the bound value.
func TestSettingsSelectEdit(t *testing.T) {
	libs := []plex.MusicLibrary{{Key: "1", Title: "Music"}}
	cfg := config.Config{
		ServerURL:         "https://x",
		Token:             "abcd",
		DefaultViewArtist: "list",
	}
	m := New(cfg, nil, nil, libs, nil)
	// Bootstrap: window size + flush all fields' Init cmds via
	// forwardToAllSettingsFields (which Update does for non-key msgs).
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)

	// Run each field's Init cmd through Update so updateFieldMsg fires.
	for _, f := range m.settingsFields {
		if c := f.Init(); c != nil {
			if msg := c(); msg != nil {
				upd, _ := m.Update(msg)
				m = upd.(Model)
			}
		}
	}

	// Enter settings, move cursor to the "Default view (Artists)" field
	// (index 3 in our slice), press enter to edit.
	m.screen = screenSettings
	m.settingsCursor = 3
	upd, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = upd.(Model)
	if !m.settingsEditing {
		t.Fatal("expected to be in edit mode after enter")
	}

	// Press 'j' to move to the next option ("grid").
	upd, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = upd.(Model)

	// Press enter to commit + exit edit mode.
	upd, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = upd.(Model)
	if m.settingsEditing {
		t.Errorf("expected edit mode to exit after enter")
	}
	if got := m.cfg.DefaultViewArtist; got != "grid" {
		t.Errorf("DefaultViewArtist should be 'grid', got %q", got)
	}
	if !m.gridArtists {
		t.Errorf("gridArtists should be true after committing 'grid'")
	}
}

// TestSelectStandaloneResponds probes huh.Select directly to confirm it
// reacts to j/k navigation when used as a standalone field (no Form).
func TestSelectStandaloneResponds(t *testing.T) {
	var v string = "list"
	field := huh.NewSelect[string]().
		Options(huh.NewOption("list", "list"), huh.NewOption("grid", "grid")).
		Value(&v).WithKeyMap(huh.NewDefaultKeyMap())
	sel := field.(*huh.Select[string])
	// Init + flush
	if cmd := sel.Init(); cmd != nil {
		if msg := cmd(); msg != nil {
			updated, _ := sel.Update(msg)
			if f, ok := updated.(*huh.Select[string]); ok {
				sel = f
			}
		}
	}
	_ = sel.Focus()
	t.Logf("before j: v=%q", v)
	upd, _ := sel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if f, ok := upd.(*huh.Select[string]); ok {
		sel = f
	}
	t.Logf("after j:  v=%q", v)
	// Try pressing enter to commit
	upd, _ = sel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if f, ok := upd.(*huh.Select[string]); ok {
		sel = f
	}
	_ = sel.Blur()
	t.Logf("after enter+blur: v=%q", v)
}

// TestStatusBarSyncingIndicator verifies the right-aligned syncing
// indicator appears in the footer while the library loader is running.
func TestStatusBarSyncingIndicator(t *testing.T) {
	m := newQueueModel(t)
	m.showQueue = false
	m.librarySyncing = true

	out := m.View().Content
	if !strings.Contains(out, "syncing library") {
		t.Errorf("expected 'syncing library' indicator in the footer")
	}
	t.Log("\n" + out)
}

// TestArtistGridRenders verifies that toggling grid view at the Artists
// level produces a multi-column layout and highlights the cursor cell.
func TestArtistGridRenders(t *testing.T) {
	libs := []plex.MusicLibrary{{Key: "1", Title: "Music"}}
	m := New(config.Config{ServerURL: "https://x", Token: "t"}, nil, nil, libs, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = updated.(Model)

	// Populate the artists level as if a sync had completed (with counts so
	// we exercise the two-line card render).
	items := []list.Item{
		artistItem{artist: library.Artist{RatingKey: "ar1", Title: "Al Green", AlbumCount: 5, TrackCount: 72}},
		artistItem{artist: library.Artist{RatingKey: "ar2", Title: "Led Zeppelin", AlbumCount: 9, TrackCount: 81}},
		artistItem{artist: library.Artist{RatingKey: "ar3", Title: "Pink Floyd", AlbumCount: 14, TrackCount: 165}},
		artistItem{artist: library.Artist{RatingKey: "ar4", Title: "Radiohead", AlbumCount: 9, TrackCount: 102}},
		artistItem{artist: library.Artist{RatingKey: "ar5", Title: "The Beatles", AlbumCount: 13, TrackCount: 213}},
		artistItem{artist: library.Artist{RatingKey: "ar6", Title: "Mac DeMarco", AlbumCount: 4, TrackCount: 55}},
		artistItem{artist: library.Artist{RatingKey: "ar7", Title: "Arctic Monkeys", AlbumCount: 7, TrackCount: 85}},
	}
	m.applyItems(levelArtists, items)
	m.toggleGrid()
	if !m.currentGridView() {
		t.Fatal("toggleGrid did not enable grid view")
	}

	out := m.View().Content
	// 110 / 25 (cellWidth + gap) = 4 columns. Expect at least two artists on
	// the same line — assert by checking they share a line.
	lines := strings.Split(out, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "Al Green") && strings.Contains(line, "Led Zeppelin") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Al Green and Led Zeppelin to share a row in grid view")
	}
	t.Log("\n" + out)
}

// TestDeleteQueueItemBeforePlaying covers deleting a non-playing track that
// sits before the playing one — queueIdx must decrement.
func TestDeleteQueueItemBeforePlaying(t *testing.T) {
	m := newQueueModel(t)
	m.queueList.Select(0) // cursor on "I'm a Ram" (not playing)
	m.deleteQueueItem()

	if len(m.queue) != 2 {
		t.Fatalf("expected 2 tracks left, got %d", len(m.queue))
	}
	if got := m.queue[0].Title; got != "Tired of Being Alone" {
		t.Errorf("expected playing track to shift to idx 0, got %q", got)
	}
	if m.queueIdx != 0 {
		t.Errorf("queueIdx should now be 0, got %d", m.queueIdx)
	}
}

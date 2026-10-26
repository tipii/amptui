package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
)

var (
	keyRight = tea.KeyPressMsg{Code: tea.KeyRight}
	keySlash = tea.KeyPressMsg{Code: '/', Text: "/"}
)

// pressFiltering is press for keys that drive the list filter: the
// bubbles list computes matches in a cmd, so this runs the returned cmds
// (briefly — ticks would block) and feeds any filter results back in.
func pressFiltering(m Model, msg tea.KeyPressMsg) Model {
	upd, cmd := m.Update(msg)
	m = upd.(Model)
	for _, fm := range filterMsgs(cmd) {
		upd, _ = m.Update(fm)
		m = upd.(Model)
	}
	return m
}

func filterMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		switch msg := msg.(type) {
		case tea.BatchMsg:
			var out []tea.Msg
			for _, c := range msg {
				out = append(out, filterMsgs(c)...)
			}
			return out
		case list.FilterMatchesMsg:
			return []tea.Msg{msg}
		}
	case <-time.After(50 * time.Millisecond):
	}
	return nil
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		m = pressFiltering(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

// newArtistsModel is a browser on the Artists level of a three-artist
// library, one album each, sitting behind a Libraries crumb.
func newArtistsModel(t *testing.T, cfg config.Config) Model {
	t.Helper()
	libs := []media.MusicLibrary{{Key: "1", Title: "Music"}}
	m := New(cfg, nil, nil, nil, libs, nil)
	m.screen = screenBrowser
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = upd.(Model)
	m.library = &library.Library{
		SchemaVersion: library.CacheSchemaVersion,
		Entries: []library.Entry{
			{Kind: library.KindArtist, Title: "Abba", RatingKey: "ar1"},
			{Kind: library.KindArtist, Title: "Beck", RatingKey: "ar2"},
			{Kind: library.KindArtist, Title: "Cream", RatingKey: "ar3"},
			{Kind: library.KindAlbum, Title: "Arrival", RatingKey: "al1", ArtistKey: "ar1"},
			{Kind: library.KindAlbum, Title: "Odelay", RatingKey: "al2", ArtistKey: "ar2"},
			{Kind: library.KindAlbum, Title: "Disraeli Gears", RatingKey: "al3", ArtistKey: "ar3"},
		},
	}
	m.librarySyncing = false
	m.pushCrumb("Music", "1")
	m.applyItems(levelArtists, m.artistItems())
	return m
}

// In grid mode the list's own index never moves, so the crumb used to
// record row 0 and every re-sort or sync reloaded the first artist.
func TestGridDrillRemembersArtist(t *testing.T) {
	m := newArtistsModel(t, config.Config{DefaultViewArtist: "grid"})
	m = press(press(m, keyRight), keyRight) // grid cursor → Cream
	m = press(m, keyEnter)
	if got := albumOrder(m); len(got) != 1 || got[0] != "Disraeli Gears" {
		t.Fatalf("albums after opening Cream = %v", got)
	}

	m.resortCurrentLevel()
	if got := albumOrder(m); len(got) != 1 || got[0] != "Disraeli Gears" {
		t.Errorf("albums after re-sort = %v, want Cream's", got)
	}

	m = press(m, keyEsc)
	if m.gridCursor != 2 {
		t.Errorf("grid cursor after back = %d, want 2 (Cream)", m.gridCursor)
	}
}

// Drilling in and back out with a filter applied used to leave both
// levels empty.
func TestFilteredDrillAndBack(t *testing.T) {
	m := newArtistsModel(t, config.Config{})
	m = typeText(press(m, keySlash), "beck")
	m = pressFiltering(m, keyEnter) // apply filter
	if n := len(m.list.VisibleItems()); n != 1 {
		t.Fatalf("filter shows %d artists, want 1", n)
	}
	m = press(m, keyEnter) // open Beck
	if vis := m.list.VisibleItems(); len(vis) != 1 || vis[0].(albumItem).album.Title != "Odelay" {
		t.Fatalf("albums on screen after opening filtered Beck = %v", vis)
	}

	m = press(m, keyEsc)
	if n := len(m.list.VisibleItems()); n != 3 {
		t.Errorf("artists visible after back = %d, want all 3", n)
	}
	if ai, ok := m.list.SelectedItem().(artistItem); !ok || ai.artist.Title != "Beck" {
		t.Errorf("selection after back = %v, want Beck", m.list.SelectedItem())
	}

	// A filter applied on the child level must not carry back up either.
	m = press(m, keyEnter) // open Beck again
	m = typeText(press(m, keySlash), "ode")
	m = pressFiltering(m, keyEnter)
	m = press(m, keyEsc)
	if n := len(m.list.VisibleItems()); n != 3 {
		t.Errorf("artists visible after backing out of a filtered album list = %d, want 3", n)
	}
}

// Picking a second library loads that library instead of reopening the
// cache of the first, and a late result for the old one is dropped.
func TestPickingLibrarySwitchesCache(t *testing.T) {
	libs := []media.MusicLibrary{{Key: "1", Title: "Music"}, {Key: "2", Title: "Audiobooks"}}
	m := New(config.Config{}, toneBackend{}, nil, nil, libs, nil)
	m.screen = screenBrowser
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = upd.(Model)
	m.library = &library.Library{Entries: []library.Entry{{Kind: library.KindArtist, Title: "Abba", RatingKey: "ar1"}}}
	m.librarySyncing = false

	m.list.Select(1)
	upd, _ = m.drillDown()
	m = upd.(Model)
	if active, _ := m.activeLibrary(); active.Key != "2" || m.library != nil || !m.loading {
		t.Fatalf("after picking lib 2: active=%q library=%v loading=%v", active.Key, m.library, m.loading)
	}

	stale := &library.Library{Entries: []library.Entry{{Kind: library.KindArtist, Title: "Abba", RatingKey: "ar1"}}}
	upd, _ = m.Update(libraryReadyMsg{key: "1", lib: stale})
	m = upd.(Model)
	if m.library != nil {
		t.Fatal("stale result for library 1 was applied")
	}

	fresh := &library.Library{Entries: []library.Entry{{Kind: library.KindArtist, Title: "Narrator", RatingKey: "n1"}}}
	upd, _ = m.Update(libraryReadyMsg{key: "2", lib: fresh})
	m = upd.(Model)
	if m.level != levelArtists || m.loading {
		t.Fatalf("level=%v loading=%v; want artists, not loading", m.level, m.loading)
	}
	if ai, ok := m.list.Items()[0].(artistItem); !ok || ai.artist.Title != "Narrator" {
		t.Errorf("first artist = %v, want Narrator", m.list.Items()[0])
	}
}

// The settings page is taller than a typical terminal. It must scroll
// rather than push the now-playing block and footer off the bottom, and
// the cache stats below the last field must still be reachable.
func TestSettingsScrollsToKeepChromeVisible(t *testing.T) {
	cfg := config.Config{ServerURL: "https://x", PlexToken: "t"}
	m := New(cfg, nil, nil, nil, []media.MusicLibrary{{Key: "1", Title: "Music"}}, nil)
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = upd.(Model)
	m.screen = screenSettings
	m.nowPlaying = &media.Track{Artist: "Nirvana", Title: "Serve the Servants"}

	check := func(when string) string {
		t.Helper()
		out := m.View().Content
		lines := strings.Split(out, "\n")
		if len(lines) != 30 {
			t.Errorf("%s: view is %d rows, want 30", when, len(lines))
		}
		if !strings.Contains(strings.Join(lines[len(lines)-3:], "\n"), "Serve the Servants") {
			t.Errorf("%s: now-playing not in the bottom rows:\n%s", when, out)
		}
		return out
	}
	check("top of page")
	var out string
	for range len(m.settings.fields) {
		m = press(m, keyJ)
		out = check("scrolled")
	}
	if !strings.Contains(out, "Library cache") {
		t.Errorf("cache stats not reachable at the last field:\n%s", out)
	}
}

// A slow metadata or artwork reply for an artist the user already left
// must not land on the one now on screen.
func TestStaleArtistRepliesDropped(t *testing.T) {
	m := newArtistsModel(t, config.Config{})
	m = press(m, keyEnter) // Abba
	m = press(m, keyEsc)
	m = press(press(m, keyJ), keyEnter) // Beck
	if m.curArtistKey != "ar2" {
		t.Fatalf("curArtistKey = %q, want ar2", m.curArtistKey)
	}

	upd, _ := m.Update(artistMetaMsg{key: "ar1", meta: &media.ArtistMetadata{Title: "Abba"}})
	m = upd.(Model)
	if m.artistMeta != nil {
		t.Errorf("Abba's metadata replaced Beck's: %+v", m.artistMeta)
	}
	upd, _ = m.Update(artistMetaMsg{key: "ar2", meta: &media.ArtistMetadata{Title: "Beck"}})
	m = upd.(Model)
	if m.artistMeta == nil || m.artistMeta.Title != "Beck" {
		t.Errorf("Beck's own metadata not applied: %+v", m.artistMeta)
	}
}

// Jumping to an album from search sets up its artist too (so back shows
// the right artist header), and leaves the dashboard if that's where
// the search was opened.
func TestJumpToAlbumFromDashboard(t *testing.T) {
	m := newArtistsModel(t, config.Config{})
	m = press(m, keyEnter) // browse Abba first
	m.screen = screenDashboard

	m, _ = m.jumpToAlbum("al2") // Odelay, by Beck
	if m.screen != screenBrowser {
		t.Errorf("screen = %v, want browser", m.screen)
	}
	if m.curArtistKey != "ar2" || m.curAlbumKey != "al2" {
		t.Errorf("headers point at artist %q / album %q, want ar2 / al2", m.curArtistKey, m.curAlbumKey)
	}
}

// Esc while editing a settings field puts the old value back (it used to
// save whatever had been typed), and nothing is committed.
func TestSettingsEscCancelsEdit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // in case anything saves
	cfg := config.Config{ServerURL: "https://keep.example", PlexToken: "t"}
	m := New(cfg, nil, nil, nil, []media.MusicLibrary{{Key: "1", Title: "Music"}}, nil)
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = upd.(Model)
	m.screen = screenSettings

	m = press(m, keyJ)     // Backend → Server URL
	m = press(m, keyEnter) // edit
	m = typeText(m, "/typo")
	if m.settings.values.ServerURL != "https://keep.example/typo" {
		t.Fatalf("typing didn't reach the bound value: %q", m.settings.values.ServerURL)
	}
	m = press(m, keyEsc)
	if got := m.settings.values.ServerURL; got != "https://keep.example" {
		t.Errorf("after esc ServerURL = %q, want the original", got)
	}
	if m.cfg.ServerURL != "https://keep.example" {
		t.Errorf("esc committed %q to the config", m.cfg.ServerURL)
	}
	if m.settings.editing {
		t.Error("still editing after esc")
	}
	if out := m.View().Content; strings.Contains(out, "/typo") {
		t.Error("rebuilt field still shows the abandoned text")
	}
}

// Closing settings returns to the screen it was opened from.
func TestSettingsReturnsToDashboard(t *testing.T) {
	cfg := config.Config{ServerURL: "https://x", PlexToken: "t", Home: "dashboard"}
	m := New(cfg, nil, nil, nil, []media.MusicLibrary{{Key: "1", Title: "Music"}}, nil)
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = upd.(Model)
	m = press(m, tea.KeyPressMsg{Code: ',', Text: ","})
	if m.screen != screenSettings {
		t.Fatalf("screen = %v, want settings", m.screen)
	}
	m = press(m, keyEsc)
	if m.screen != screenDashboard {
		t.Errorf("screen after closing settings = %v, want dashboard", m.screen)
	}
}

// A finished download's summary leaves the footer after a while, so it
// no longer hides the sync spinner and library errors for good.
func TestDownloadStatusExpires(t *testing.T) {
	m := Model{width: 100, librarySyncing: true}
	m.setDownloadStatus("downloaded 12")
	if !strings.Contains(m.footerLine("help"), "downloaded 12") {
		t.Fatal("fresh status not shown")
	}
	m.downloadStatusAt = time.Now().Add(-downloadStatusLinger - time.Second)
	if got := m.footerLine("help"); strings.Contains(got, "downloaded 12") || !strings.Contains(got, "syncing library") {
		t.Errorf("expired status still shown / sync hidden: %q", got)
	}
	m.downloadJobs = []*downloadJob{{state: downloadRunning}}
	if !strings.Contains(m.footerLine("help"), "downloaded 12") {
		t.Error("status hidden while a download is running")
	}
}

// The spinner's tick chain stops once nothing is loading (it used to
// redraw the screen ~10×/s forever) and restarts when something is.
func TestSpinnerOnlyTicksWhileLoading(t *testing.T) {
	m := newArtistsModel(t, config.Config{})
	m.loading, m.librarySyncing, m.metaLoading = false, false, false

	upd, cmd := m.Update(m.spinner.Tick())
	m = upd.(Model)
	if cmd != nil || m.spinning {
		t.Fatalf("idle tick re-armed (cmd=%v spinning=%v)", cmd != nil, m.spinning)
	}

	m = press(m, keyEnter) // opening an artist starts its metadata fetch
	if !m.metaLoading || !m.spinning {
		t.Errorf("spinner not restarted for a load (metaLoading=%v spinning=%v)", m.metaLoading, m.spinning)
	}
}

// Transport keys work from the queue modal and the settings screen, and
// never fire while typing a filter.
func TestPlaybackKeysEverywhere(t *testing.T) {
	m := newQueueTestModel(t, map[string]string{"a": "5", "b": "5"})
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = upd.(Model)
	model, _ := m.playTracks([]media.Track{{RatingKey: "a"}, {RatingKey: "b"}}, 0)
	m = model.(Model)
	m = syncUntil(t, m, "a to play", func(m Model) bool { return m.player.State().Position > 0 })

	keyN := tea.KeyPressMsg{Code: 'n', Text: "n"}
	m.openQueue()
	m = press(m, keyN)
	m = syncUntil(t, m, "next from the queue modal", func(m Model) bool { return playingKey(m) == "b" })

	m.showQueue = false
	m.screen = screenSettings
	m = press(m, tea.KeyPressMsg{Code: 'p', Text: "p"})
	m = syncUntil(t, m, "prev from settings", func(m Model) bool { return playingKey(m) == "a" })

	m.screen = screenBrowser
	m.list.SetItems([]list.Item{artistItem{artist: library.Artist{Title: "Nina", RatingKey: "ar1"}}})
	m = typeText(press(m, keySlash), "n")
	if m.list.FilterState() != list.Filtering {
		t.Fatal("filter didn't open")
	}
	time.Sleep(300 * time.Millisecond)
	if m = m.syncFromPlayer(); playingKey(m) != "a" {
		t.Errorf("typing n into a filter skipped to %q", playingKey(m))
	}
}

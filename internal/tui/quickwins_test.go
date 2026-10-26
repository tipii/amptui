package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
)

var (
	keyV = tea.KeyPressMsg{Code: 'v', Text: "v"}
	keyG = tea.KeyPressMsg{Code: 'g', Text: "g"}
	keyQ = tea.KeyPressMsg{Code: 'q', Text: "q"}
	keyP = tea.KeyPressMsg{Code: 'p', Text: "p"}
)

// twoAlbumLibrary: one artist with two albums of two tracks each.
func twoAlbumLibrary() *library.Library {
	return &library.Library{
		SchemaVersion: library.CacheSchemaVersion,
		Entries: []library.Entry{
			{Kind: library.KindArtist, Title: "Beck", RatingKey: "ar1"},
			{Kind: library.KindAlbum, Title: "Odelay", RatingKey: "al1", ArtistKey: "ar1", Year: 1996},
			{Kind: library.KindAlbum, Title: "Sea Change", RatingKey: "al2", ArtistKey: "ar1", Year: 2002},
			{Kind: library.KindTrack, Title: "Devils Haircut", RatingKey: "t1", AlbumKey: "al1", ArtistKey: "ar1", Index: 1},
			{Kind: library.KindTrack, Title: "Hotwax", RatingKey: "t2", AlbumKey: "al1", ArtistKey: "ar1", Index: 2},
			{Kind: library.KindTrack, Title: "The Golden Age", RatingKey: "t3", AlbumKey: "al2", ArtistKey: "ar1", Index: 1},
			{Kind: library.KindTrack, Title: "Paper Tiger", RatingKey: "t4", AlbumKey: "al2", ArtistKey: "ar1", Index: 2},
		},
	}
}

func TestToggleViewFlipsAndRemembers(t *testing.T) {
	m := newArtistsModel(t, config.Config{})
	if m.currentGridView() {
		t.Fatal("artists start in list view")
	}
	m = press(m, keyV)
	if !m.currentGridView() || m.cfg.DefaultViewArtist != "grid" {
		t.Errorf("after v: grid=%v cfg=%q", m.currentGridView(), m.cfg.DefaultViewArtist)
	}
	m = press(m, keyV)
	if m.currentGridView() || m.cfg.DefaultViewArtist != "list" {
		t.Errorf("after v v: grid=%v cfg=%q", m.currentGridView(), m.cfg.DefaultViewArtist)
	}
}

func TestQueueWholeArtistAndGoToPlaying(t *testing.T) {
	m := newQueueTestModel(t, map[string]string{"t1": "5", "t2": "5", "t3": "5", "t4": "5"})
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = upd.(Model)
	m.screen = screenBrowser // an empty test config opens on settings
	m.library = twoAlbumLibrary()
	m.librarySyncing = false
	m.pushCrumb("Music", "1")
	m.applyItems(levelArtists, m.artistItems())

	m = press(m, keyQ) // q on the artist row
	if len(m.queue) != 4 || m.queue[0].RatingKey != "t1" || m.queue[2].RatingKey != "t3" {
		t.Fatalf("queue = %v, want both albums in order", keys(m.queue))
	}
	m = syncUntil(t, m, "t1 to play", func(m Model) bool { return m.player.State().Position > 0 })

	m.screen = screenDashboard
	m = press(m, keyG)
	if m.screen != screenBrowser || m.level != levelTracks || m.curAlbumKey != "al1" {
		t.Fatalf("g: screen=%v level=%v album=%q", m.screen, m.level, m.curAlbumKey)
	}
	if ti, ok := m.list.SelectedItem().(trackItem); !ok || ti.track.RatingKey != "t1" {
		t.Errorf("g highlighted %v, want the playing track", m.list.SelectedItem())
	}
}

// p restarts a track more than a few seconds in, rather than skipping
// back a whole track.
func TestPrevRestartsTrackPastThreshold(t *testing.T) {
	m := newQueueTestModel(t, map[string]string{"a": "30", "b": "30"})
	model, _ := m.playTracks([]media.Track{{RatingKey: "a"}, {RatingKey: "b"}}, 1)
	m = model.(Model)
	m = syncUntil(t, m, "b to play", func(m Model) bool { return m.player.State().Position > 0 })
	m.seekTo(restartThreshold + 2e9)
	m = syncUntil(t, m, "seek", func(m Model) bool { return m.player.State().Position > restartThreshold })

	m = press(m, keyP)
	m = syncUntil(t, m, "restart", func(m Model) bool { return m.player.State().Position < restartThreshold })
	if playingKey(m) != "b" {
		t.Errorf("p moved to %q, want a restart of b", playingKey(m))
	}
	m = press(m, keyP) // now near the start: goes back
	m = syncUntil(t, m, "previous track", func(m Model) bool { return playingKey(m) == "a" })
}

// Playing a dashboard track carries on through its album.
func TestAlbumContext(t *testing.T) {
	m := newArtistsModel(t, config.Config{})
	m.library = twoAlbumLibrary()
	tracks, start := m.albumContext(media.Track{RatingKey: "t2", AlbumRatingKey: "al1"})
	if len(tracks) != 2 || start != 1 {
		t.Errorf("albumContext = %v at %d, want Odelay at 1", keys(tracks), start)
	}
	tracks, start = m.albumContext(media.Track{RatingKey: "x", AlbumRatingKey: "nope"})
	if len(tracks) != 1 || start != 0 {
		t.Errorf("unknown album: %v at %d, want just the track", keys(tracks), start)
	}
}

func keys(ts []media.Track) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.RatingKey
	}
	return out
}

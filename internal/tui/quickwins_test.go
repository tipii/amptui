package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/player"
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

// Shuffle reorders what's after the playing track without interrupting
// it, mpv follows the new order, and turning it off restores the order.
func TestShuffleToggleKeepsPlayingAndRestores(t *testing.T) {
	secs := map[string]string{}
	var tracks []media.Track
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		secs[k] = "30"
		tracks = append(tracks, media.Track{RatingKey: k})
	}
	m := newQueueTestModel(t, secs)
	model, _ := m.playTracks(tracks, 2)
	m = model.(Model)
	m = syncUntil(t, m, "c to play", func(m Model) bool { return m.player.State().Position > 0 })

	m.toggleShuffle()
	if playingKey(m) != "c" || m.queue[m.queueIdx].RatingKey != "c" {
		t.Fatalf("playing %q at %d after shuffle", playingKey(m), m.queueIdx)
	}
	m = syncUntil(t, m, "mpv to follow", func(m Model) bool { return m.player.State().PlaylistPos == m.queueIdx })
	if st := m.player.State(); st.Idle || st.Position == 0 {
		t.Errorf("shuffle interrupted playback: %+v", st)
	}
	want := m.queue[m.queueIdx+1].RatingKey
	m.playNext()
	m = syncUntil(t, m, "next in shuffled order", func(m Model) bool { return playingKey(m) == want })

	m.toggleShuffle()
	if got := keys(m.queue); strings.Join(got, "") != "abcdefgh" {
		t.Errorf("unshuffled order = %v", got)
	}
	m = syncUntil(t, m, "mpv at the restored index", func(m Model) bool {
		return m.player.State().PlaylistPos == m.queueIdx && m.queue[m.queueIdx].RatingKey == want
	})
}

func TestRepeatCycles(t *testing.T) {
	m := newArtistsModel(t, config.Config{})
	for _, want := range []player.Repeat{player.RepeatQueue, player.RepeatTrack, player.RepeatOff} {
		m = press(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
		if m.repeat != want {
			t.Errorf("repeat = %v, want %v", m.repeat, want)
		}
	}
}

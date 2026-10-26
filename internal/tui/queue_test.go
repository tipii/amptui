package tui

import (
	"os/exec"
	"testing"
	"time"

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/player"
)

// toneBackend streams every track as a generated tone, so queue logic
// can run against a real (silent) mpv. Only StreamURL is implemented;
// anything else panics on the nil embedded interface.
type toneBackend struct {
	media.Backend
	secs map[string]string // RatingKey → tone duration
}

func (b toneBackend) StreamURL(t media.Track) string {
	d, ok := b.secs[t.RatingKey]
	if !ok {
		return "" // unplayable
	}
	return "av://lavfi:sine=duration=" + d
}

func newQueueTestModel(t *testing.T, secs map[string]string) Model {
	t.Helper()
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv not on PATH")
	}
	p, err := player.New("--ao=null")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return New(config.Config{}, toneBackend{secs: secs}, p, nil, nil, nil)
}

// syncUntil runs syncFromPlayer the way the tick / change loop does
// until cond holds.
func syncUntil(t *testing.T, m Model, what string, cond func(Model) bool) Model {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m = m.syncFromPlayer()
		if cond(m) {
			return m
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (queue=%d idx=%d playing=%v)", what, len(m.queue), m.queueIdx, m.nowPlaying)
	return m
}

func playingKey(m Model) string {
	if m.nowPlaying == nil {
		return ""
	}
	return m.nowPlaying.RatingKey
}

// After a queue plays out, the next enqueue must start at index 0 in
// both m.queue and mpv — mpv keeps finished entries around when idle.
func TestEnqueueAfterQueueFinished(t *testing.T) {
	m := newQueueTestModel(t, map[string]string{"a": "0.2", "b": "0.2", "c": "5", "d": "5"})
	model, _ := m.playTracks([]media.Track{{RatingKey: "a"}, {RatingKey: "b"}}, 0)
	m = model.(Model)
	m = syncUntil(t, m, "queue to finish", func(m Model) bool { return len(m.queue) == 0 && m.nowPlaying == nil })

	m.enqueue(media.Track{RatingKey: "c"}, media.Track{RatingKey: "d"})
	m = syncUntil(t, m, "c to play", func(m Model) bool {
		return m.player.State().Position > 0 && playingKey(m) == "c"
	})
	if st := m.player.State(); st.PlaylistPos != 0 || m.queueIdx != 0 {
		t.Errorf("mpv at %d, UI at %d; want both 0", st.PlaylistPos, m.queueIdx)
	}
}

// Tracks without a stream URL never enter m.queue, so indices still
// line up with mpv's playlist.
func TestUnplayableTracksSkipped(t *testing.T) {
	m := newQueueTestModel(t, map[string]string{"a": "5", "c": "5"})
	model, _ := m.playTracks([]media.Track{{RatingKey: "a"}, {RatingKey: "x"}, {RatingKey: "c"}}, 2)
	m = model.(Model)
	if len(m.queue) != 2 || m.queueIdx != 1 || playingKey(m) != "c" {
		t.Fatalf("queue=%d idx=%d playing=%q; want 2, 1, c", len(m.queue), m.queueIdx, playingKey(m))
	}
	m = syncUntil(t, m, "c to play", func(m Model) bool { return m.player.State().Position > 0 })
	if pos := m.player.State().PlaylistPos; pos != 1 {
		t.Errorf("mpv at %d, want 1", pos)
	}
}

// Deleting the playing track moves now-playing onto its successor even
// though the index doesn't change.
func TestDeletePlayingTrack(t *testing.T) {
	m := newQueueTestModel(t, map[string]string{"a": "5", "b": "5"})
	model, _ := m.playTracks([]media.Track{{RatingKey: "a"}, {RatingKey: "b"}}, 0)
	m = model.(Model)
	m = syncUntil(t, m, "a to play", func(m Model) bool { return m.player.State().Position > 0 })
	m.openQueue()
	m.deleteQueueItem()
	if playingKey(m) != "b" {
		t.Errorf("now playing %q after deleting a, want b", playingKey(m))
	}
	m = syncUntil(t, m, "b to play", func(m Model) bool { return m.player.State().Position > 0 && m.player.State().PlaylistPos == 0 })
	if playingKey(m) != "b" {
		t.Errorf("now playing %q, want b", playingKey(m))
	}
}

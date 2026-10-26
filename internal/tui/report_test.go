package tui

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/media"
)

// recordingBackend is toneBackend plus a log of playback reports.
type recordingBackend struct {
	toneBackend
	mu     *sync.Mutex
	events *[]string
}

func (b recordingBackend) ReportPlayback(_ context.Context, r media.PlaybackReport) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := r.Track.RatingKey + ":" + string(r.State)
	if r.Started {
		e += "+start"
	}
	*b.events = append(*b.events, e)
	return nil
}

func (b recordingBackend) MarkPlayed(_ context.Context, t media.Track) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	*b.events = append(*b.events, t.RatingKey+":played")
	return nil
}

// runCmd executes cmd and any batch inside it, synchronously.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmd(c)
		}
	}
}

func newReportingModel(t *testing.T, cfg config.Config) (Model, func() []string) {
	t.Helper()
	m := newQueueTestModel(t, nil)
	var mu sync.Mutex
	var events []string
	m.client = recordingBackend{
		toneBackend: toneBackend{secs: map[string]string{"a": "1.5", "b": "30"}},
		mu:          &mu, events: &events,
	}
	m.cfg = cfg
	return m, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), events...)
	}
}

// reportUntil drives syncFromPlayer + reportPlayback, as the tick does,
// until cond holds for the recorded events.
func reportUntil(t *testing.T, m Model, events func() []string, what string, cond func([]string) bool) Model {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		m = m.syncFromPlayer()
		runCmd(m.reportPlayback(time.Now()))
		if cond(events()) {
			return m
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; events %v", what, events())
	return m
}

func contains(events []string, want string) bool {
	for _, e := range events {
		if e == want {
			return true
		}
	}
	return false
}

// A play is reported started, counted past 90%, and stopped when the
// next track takes over; pausing is reported too.
func TestPlaybackIsReported(t *testing.T) {
	m, events := newReportingModel(t, config.Config{})
	// a is a 1.5s tone the library thinks is 1s long, so it crosses 90%.
	model, _ := m.playTracks([]media.Track{{RatingKey: "a", Duration: time.Second}, {RatingKey: "b", Duration: 30 * time.Second}}, 0)
	m = model.(Model)

	m = reportUntil(t, m, events, "a started", func(e []string) bool { return contains(e, "a:playing+start") })
	m = reportUntil(t, m, events, "a counted", func(e []string) bool { return contains(e, "a:played") })
	m = reportUntil(t, m, events, "a stopped, b started", func(e []string) bool {
		return contains(e, "a:stopped") && contains(e, "b:playing+start")
	})

	_ = m.player.SetPaused(true)
	m = reportUntil(t, m, events, "b paused", func(e []string) bool { return contains(e, "b:paused") })
	if n := len(events()); contains(events()[:n-1], "b:played") {
		t.Error("b counted as played before reaching 90%")
	}
}

func TestPlaybackReportingCanBeTurnedOff(t *testing.T) {
	off := false
	m, events := newReportingModel(t, config.Config{ReportPlayback: &off})
	model, _ := m.playTracks([]media.Track{{RatingKey: "b", Duration: 30 * time.Second}}, 0)
	m = model.(Model)
	m = syncUntil(t, m, "b to play", func(m Model) bool { return m.player.State().Position > 0 })
	runCmd(m.reportPlayback(time.Now()))
	if e := events(); len(e) != 0 {
		t.Errorf("reports sent with reporting off: %v", e)
	}
}

// A session restored paused isn't announced as playing until it plays.
func TestPausedRestoreIsNotReported(t *testing.T) {
	m, events := newReportingModel(t, config.Config{})
	_, urls := m.playable([]media.Track{{RatingKey: "b"}})
	if err := m.player.LoadListPaused(urls, 0); err != nil {
		t.Fatal(err)
	}
	m.setQueue([]media.Track{{RatingKey: "b", Duration: 30 * time.Second}})
	m.nowPlaying = &m.queue[0]
	time.Sleep(200 * time.Millisecond)
	m = m.syncFromPlayer()
	runCmd(m.reportPlayback(time.Now()))
	if e := events(); len(e) != 0 {
		t.Errorf("paused restore reported: %v", e)
	}
	_ = m.player.SetPaused(false)
	reportUntil(t, m, events, "reported once playing", func(e []string) bool { return contains(e, "b:playing+start") })
}

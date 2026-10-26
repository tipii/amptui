package player

import (
	"encoding/json"
	"os/exec"
	"testing"
	"time"
)

// These tests drive a real mpv with a null audio output and lavfi
// generated tones, so they need mpv on PATH but no media or sound.

func tone(d string) string { return "av://lavfi:sine=duration=" + d }

func newTestPlayer(t *testing.T) *Player {
	t.Helper()
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv not on PATH")
	}
	p, err := New("--ao=null")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func(State) bool, p *Player) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := p.State(); cond(st) {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; state %+v", what, p.State())
	return State{}
}

func TestPlaylistExhaustion(t *testing.T) {
	p := newTestPlayer(t)
	if err := p.LoadList([]string{tone("0.2"), tone("0.2")}, 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second entry", func(s State) bool { return s.PlaylistPos == 1 }, p)
	waitFor(t, "exhaustion", func(s State) bool { return s.Exhausted }, p)
}

// A reload passes through mpv's stop, and must never read as the
// playlist having run out — the UI would wipe the new queue.
func TestReloadIsNotExhaustion(t *testing.T) {
	p := newTestPlayer(t)
	if err := p.LoadList([]string{tone("5")}, 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "playback", func(s State) bool { return s.Position > 0 }, p)
	for range 5 {
		if err := p.LoadList([]string{tone("5"), tone("5")}, 1); err != nil {
			t.Fatal(err)
		}
		for range 10 {
			if st := p.State(); st.Exhausted {
				t.Fatalf("reload reported as exhausted: %+v", st)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFor(t, "requested entry", func(s State) bool { return s.PlaylistPos == 1 && !s.Idle }, p)
}

func TestFailedEntryIsReported(t *testing.T) {
	p := newTestPlayer(t)
	if err := p.LoadList([]string{"/nonexistent/amptui-test.flac"}, 0); err != nil {
		t.Fatal(err)
	}
	st := waitFor(t, "error", func(s State) bool { return s.ErrorSeq == 1 }, p)
	if st.LastError == "" {
		t.Error("LastError empty")
	}
	waitFor(t, "exhaustion after error", func(s State) bool { return s.Exhausted }, p)
}

func TestRestartAfterCrash(t *testing.T) {
	p := newTestPlayer(t)
	p.writeMu.Lock()
	proc := p.cmd.Process
	p.writeMu.Unlock()
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Changes():
	case <-time.After(5 * time.Second):
	}
	waitFor(t, "dead", func(s State) bool { return s.Dead }, p)
	if err := p.TogglePause(); err != ErrDead {
		t.Errorf("command on dead player = %v, want ErrDead", err)
	}

	if err := p.Append(tone("5")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "playback after restart", func(s State) bool { return !s.Dead && s.PlaylistPos == 0 && s.Position > 0 }, p)
}

func TestNullProperties(t *testing.T) {
	p := &Player{changes: make(chan struct{}, 1)}
	p.applyPropertyChange("playlist-pos", json.RawMessage("3"))
	p.applyPropertyChange("playlist-pos", json.RawMessage("null"))
	if got := p.State().PlaylistPos; got != -1 {
		t.Errorf("playlist-pos null → %d, want -1", got)
	}
	p.applyPropertyChange("time-pos", json.RawMessage("12.5"))
	p.applyPropertyChange("time-pos", json.RawMessage("null"))
	if got := p.State().Position; got != 0 {
		t.Errorf("time-pos null → %v, want 0", got)
	}
}

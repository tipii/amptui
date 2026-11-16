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

func TestVolumeAndMute(t *testing.T) {
	p := newTestPlayer(t)
	waitFor(t, "initial volume", func(s State) bool { return s.Volume == 100 }, p)
	if err := p.AddVolume(-15); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "volume 85", func(s State) bool { return s.Volume == 85 }, p)
	if err := p.SetMuted(true); err != nil {
		t.Fatal(err)
	}
	st := waitFor(t, "muted", func(s State) bool { return s.Muted }, p)
	if st.Volume != 85 {
		t.Errorf("mute changed the volume to %v", st.Volume)
	}
	// Turning it up while muted unmutes.
	if err := p.AddVolume(5); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "unmuted at 90", func(s State) bool { return !s.Muted && s.Volume == 90 }, p)
	if err := p.SetVolume(250); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "clamped to 100", func(s State) bool { return s.Volume == 100 }, p)
}

func TestRepeatQueueLoops(t *testing.T) {
	p := newTestPlayer(t)
	if err := p.SetRepeat(RepeatQueue); err != nil {
		t.Fatal(err)
	}
	if err := p.LoadList([]string{tone("0.2"), tone("0.2")}, 1); err != nil {
		t.Fatal(err)
	}
	// Short tones end before time-pos is ever sampled, so follow the
	// index: 1 → wraps to 0 → back to 1 means it's looping.
	waitFor(t, "wrap to first", func(s State) bool { return s.PlaylistPos == 0 }, p)
	waitFor(t, "around again", func(s State) bool { return s.PlaylistPos == 1 }, p)
	if p.State().Exhausted {
		t.Error("a looping playlist reported exhaustion")
	}
}

// Rearrange keeps the playing entry playing and puts the rest around it.
func TestRearrangeAroundCurrent(t *testing.T) {
	p := newTestPlayer(t)
	if err := p.LoadList([]string{tone("5"), tone("5"), tone("5")}, 1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "entry 1 playing", func(s State) bool { return s.PlaylistPos == 1 && s.Position > 0 }, p)
	if err := p.Rearrange([]string{tone("5"), tone("5")}, []string{tone("5")}); err != nil {
		t.Fatal(err)
	}
	st := waitFor(t, "current moved to index 2", func(s State) bool { return s.PlaylistPos == 2 }, p)
	if st.Position == 0 || st.Idle {
		t.Errorf("playback interrupted: %+v", st)
	}
}

// Mid-Rearrange playlist-pos reports (the playing entry at index 0 after
// the clear) are ignored until mpv answers the settling query; reports
// after that apply as usual — including a "next" pressed right away,
// which a time-based pin swallowed.
func TestRearrangePinsPosition(t *testing.T) {
	p := &Player{changes: make(chan struct{}, 1)}
	_ = p.Rearrange([]string{"a", "b"}, []string{"d"}) // no mpv: commands fail, the pin is set first
	if got := p.State().PlaylistPos; got != 2 {
		t.Fatalf("PlaylistPos right after Rearrange = %d, want 2", got)
	}
	p.applyPropertyChange("playlist-pos", json.RawMessage("0"))
	if got := p.State().PlaylistPos; got != 2 {
		t.Errorf("transient 0 was applied: PlaylistPos = %d", got)
	}

	p.settleReq = 7                       // as querySettled would have
	p.applyReply(6, json.RawMessage("0")) // some other command's reply
	p.applyPropertyChange("playlist-pos", json.RawMessage("1"))
	if got := p.State().PlaylistPos; got != 2 {
		t.Errorf("unrelated reply ended the settling: PlaylistPos = %d", got)
	}
	p.applyReply(7, json.RawMessage("2"))
	p.applyPropertyChange("playlist-pos", json.RawMessage("3")) // next, right away
	if got := p.State().PlaylistPos; got != 3 {
		t.Errorf("after settling, PlaylistPos = %d, want 3", got)
	}
}

// Against mpv: polling as fast as possible, the position never shows the
// transient index while the playlist is rearranged around entry 2.
func TestRearrangeNeverReportsTransientPosition(t *testing.T) {
	p := newTestPlayer(t)
	urls := []string{tone("30"), tone("30"), tone("30"), tone("30")}
	if err := p.LoadList(urls, 2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "entry 2 playing", func(s State) bool { return s.PlaylistPos == 2 && s.Position > 0 }, p)
	for range 5 {
		if err := p.Rearrange(urls[:2], urls[3:]); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(300 * time.Millisecond)
		for time.Now().Before(deadline) {
			if pos := p.State().PlaylistPos; pos != 2 {
				t.Fatalf("PlaylistPos reported %d mid-rearrange", pos)
			}
		}
	}
}

// loadedPlayer is a Player as loadList leaves it after asking mpv to
// start index 2 of a fresh list, its loadfile answered with entry id 3.
func loadedPlayer() *Player {
	p := &Player{changes: make(chan struct{}, 1), loadIndex: 2, loadReq: 7}
	p.state.PlaylistPos = 2
	p.applyReply(7, json.RawMessage(`{"playlist_entry_id":3}`))
	return p
}

// Starting up, mpv runs commands before it sets its playlist up, which
// points it back at the first entry: a slow start played entry 0 of a
// list loaded at index 2. The player moves it to the right entry and
// drops the wrong one's reports meanwhile, so neither the UI nor a
// restored position ever sees it.
func TestLoadCorrectsWrongStart(t *testing.T) {
	p := loadedPlayer()
	if p.loadEntryID != 3 {
		t.Fatalf("entry id from the loadfile answer = %d", p.loadEntryID)
	}
	if fix := p.applyStartFile(1); fix != 2 {
		t.Fatalf("mpv started entry id 1 (index 0): correction = %d, want 2", fix)
	}
	p.applyPropertyChange("playlist-pos", json.RawMessage("0"))
	p.applyPropertyChange("duration", json.RawMessage("30"))
	p.applyPropertyChange("time-pos", json.RawMessage("0.5"))
	p.applyEndFile("stop", "")
	if st := p.State(); st.PlaylistPos != 2 || st.Duration != 0 || st.Position != 0 {
		t.Errorf("the wrong entry's reports were applied: %+v", st)
	}
	if fix := p.applyStartFile(3); fix != -1 {
		t.Errorf("the right entry starting asked for another correction: %d", fix)
	}
	p.applyPropertyChange("duration", json.RawMessage("30"))
	if p.State().Duration != 30*time.Second {
		t.Error("reports after the right entry started are dropped")
	}
}

// The right entry starting is left alone, as are later files starting
// (gapless advance) and anything after a command moved the playhead.
func TestLoadRightStartIsLeftAlone(t *testing.T) {
	p := loadedPlayer()
	if fix := p.applyStartFile(3); fix != -1 {
		t.Errorf("right entry: correction %d", fix)
	}
	if fix := p.applyStartFile(4); fix != -1 {
		t.Errorf("next entry after the checked one: correction %d", fix)
	}

	p = loadedPlayer()
	_ = p.Next() // no mpv: the command fails, but the check is dropped first
	if fix := p.applyStartFile(4); fix != -1 {
		t.Errorf("a file starting after next was corrected back: %d", fix)
	}

	// Before mpv answered the loadfile, a starting file is from before the
	// load; it doesn't use up the check.
	p = &Player{changes: make(chan struct{}, 1), loadIndex: 2, loadReq: 7}
	if fix := p.applyStartFile(9); fix != -1 {
		t.Errorf("a file from before the load: correction %d", fix)
	}
	p.applyReply(7, json.RawMessage(`{"playlist_entry_id":3}`))
	if fix := p.applyStartFile(1); fix != 2 {
		t.Errorf("wrong entry after the answer: correction %d, want 2", fix)
	}
}

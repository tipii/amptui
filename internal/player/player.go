// Package player drives an mpv subprocess over its JSON IPC socket.
//
// mpv is launched once in --idle mode; tracks are played by sending it a
// loadfile command. A background goroutine reads mpv's IPC stream and keeps
// the last-known playback state (position, duration, pause) so callers can
// poll State() cheaply — no need to plumb events into the UI loop.
//
// If mpv dies (crash, OOM, pkill), State().Dead turns true and Changes()
// fires; the next LoadList or Append starts a fresh mpv transparently.
package player

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ErrDead is returned by commands sent while mpv isn't running.
var ErrDead = errors.New("mpv is not running")

// State is a snapshot of mpv's playback status.
type State struct {
	Paused   bool
	Position time.Duration
	Duration time.Duration
	// Idle is true when mpv has no file loaded (e.g. playback finished).
	Idle bool
	// PlaylistPos is the index of the entry mpv is currently playing
	// within its internal playlist, or -1 when nothing is loaded. With
	// queue delegation this is the source of truth for "which track" —
	// mpv owns advancement, prefetch, and gapless transitions.
	PlaylistPos int
	// Exhausted is true once mpv went idle because the last entry
	// played out (or failed). Idle alone can't tell that apart from the
	// transient idle a stop-and-reload passes through.
	Exhausted bool
	// Buffering is true while playback is stalled waiting for the
	// network cache to refill (mpv's paused-for-cache). Distinct from
	// Paused, which is a user-initiated pause.
	Buffering bool
	// CacheTime is the absolute timestamp the demuxer cache holds data
	// up to (mpv's demuxer-cache-time) — i.e. how far ahead of the
	// start the track has buffered. Compare against Duration for a
	// "buffered" fraction on the progress bar.
	CacheTime time.Duration
	// LastError describes the most recent entry mpv failed to play
	// (401, 404, unsupported codec — mpv just skips it); ErrorSeq bumps
	// on each one so a caller can show every failure exactly once.
	LastError string
	ErrorSeq  int
	// Volume is mpv's volume, 0–100 (mpv allows up to 130 with
	// softvol; amptui caps at 100). Muted is independent of it.
	Volume float64
	Muted  bool
	// Dead is true once mpv exited or its socket broke.
	Dead bool
}

// Player owns the mpv subprocess and its IPC connection.
type Player struct {
	// writeMu guards the process fields below (swapped on restart) and
	// serializes IPC writes.
	writeMu sync.Mutex
	cmd     *exec.Cmd
	exited  chan struct{} // closed once cmd has been reaped
	sockDir string
	conn    net.Conn
	reqID   int
	dead    bool
	closed  bool

	stateMu sync.RWMutex
	state   State
	// lastEnd is the reason of the most recent end-file event, read
	// when idle-active flips to true to decide whether it's Exhausted.
	lastEnd string

	extraArgs []string // reused on restart

	// changes carries a coalesced "something other than the playhead
	// moved" signal (pause, track, idle, duration, buffering, errors)
	// so the UI can react immediately instead of waiting for its tick.
	changes chan struct{}
}

// New launches mpv in idle mode and connects to its IPC socket.
// extraArgs are appended to mpv's command line (tests pass --ao=null).
// Call Close to shut it down.
func New(extraArgs ...string) (*Player, error) {
	if _, err := exec.LookPath("mpv"); err != nil {
		return nil, fmt.Errorf("mpv not found on PATH: %w", err)
	}
	p := &Player{changes: make(chan struct{}, 1), extraArgs: extraArgs}
	if err := p.start(); err != nil {
		return nil, err
	}
	return p, nil
}

// start spawns mpv, connects, and registers the observed properties.
// Used by New and to restart after mpv died.
func (p *Player) start() error {
	// The socket lives in a fresh 0700 directory with a random name.
	// A fixed path in shared /tmp could be pre-created by another local
	// user, who would then receive every loadfile URL — and those carry
	// the server token.
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	sockDir, err := os.MkdirTemp(base, "amptui-mpv-")
	if err != nil {
		return fmt.Errorf("creating mpv socket dir: %w", err)
	}
	socketPath := filepath.Join(sockDir, "mpv.sock")

	args := []string{
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--no-config",
		// Let mpv own the queue: prefetch opens the demuxer and starts
		// buffering the next playlist entry before the current ends,
		// and gapless-audio removes the silent gap at track boundaries.
		"--prefetch-playlist=yes",
		"--gapless-audio=yes",
		"--input-ipc-server=" + socketPath,
	}
	cmd := exec.Command("mpv", append(args, p.extraArgs...)...)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(sockDir)
		return fmt.Errorf("starting mpv: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()

	conn, err := dialWithRetry(socketPath, exited, 5*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		<-exited
		_ = os.RemoveAll(sockDir)
		return fmt.Errorf("connecting to mpv IPC: %w", err)
	}

	p.writeMu.Lock()
	p.cmd, p.exited, p.sockDir, p.conn, p.dead = cmd, exited, sockDir, conn, false
	p.writeMu.Unlock()

	p.stateMu.Lock()
	vol, muted := p.state.Volume, p.state.Muted
	restoreVol := p.state.Dead // a restart after a crash keeps the old level
	p.state = State{PlaylistPos: -1, ErrorSeq: p.state.ErrorSeq, LastError: p.state.LastError, Volume: 100}
	p.lastEnd = ""
	p.stateMu.Unlock()

	go p.readLoop(conn)
	if restoreVol {
		_ = p.command("set_property", "volume", vol)
		_ = p.command("set_property", "mute", muted)
	}

	// Ask mpv to push updates for the properties we surface in State().
	for i, prop := range []string{
		"time-pos", "duration", "pause", "idle-active",
		"playlist-pos", "paused-for-cache", "demuxer-cache-time",
		"volume", "mute",
	} {
		_ = p.command("observe_property", i+1, prop)
	}
	return nil
}

// ensureRunning restarts mpv if it died. Called by the commands that
// begin playback, so a crash costs the current queue but not the
// session.
func (p *Player) ensureRunning() error {
	p.writeMu.Lock()
	dead, closed := p.dead, p.closed
	p.writeMu.Unlock()
	if closed {
		return ErrDead
	}
	if !dead {
		return nil
	}
	p.reap()
	return p.start()
}

// dialWithRetry waits for mpv to create its socket, then connects. It
// gives up early if mpv exits first (e.g. it rejected a flag).
func dialWithRetry(path string, exited <-chan struct{}, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.Dial("unix", path)
		if err == nil {
			return conn, nil
		}
		select {
		case <-exited:
			return nil, errors.New("mpv exited during startup")
		default:
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// LoadList replaces mpv's playlist with urls and starts playback at
// index start. mpv then owns advancement, prefetch, and gapless
// transitions between entries; callers observe State().PlaylistPos to
// learn which entry is playing. Always unpauses so a fresh selection
// plays rather than silently inheriting a prior paused state.
func (p *Player) LoadList(urls []string, start int) error {
	if len(urls) == 0 {
		return p.Stop()
	}
	if err := p.ensureRunning(); err != nil {
		return err
	}
	if start < 0 || start >= len(urls) {
		start = 0
	}
	// stop empties the playlist and idles mpv; appending then builds the
	// new list without auto-playing, so setting playlist-pos lands on
	// exactly the entry we want with no index-0 glitch. The stop's
	// end-file carries reason "stop", so it never reads as Exhausted.
	if err := p.command("stop"); err != nil {
		return err
	}
	for _, u := range urls {
		if err := p.command("loadfile", u, "append"); err != nil {
			return err
		}
	}
	if err := p.command("set_property", "pause", false); err != nil {
		return err
	}
	if err := p.command("set_property", "playlist-pos", start); err != nil {
		return err
	}
	p.stateMu.Lock()
	p.state.PlaylistPos = start
	p.state.Position, p.state.Duration, p.state.CacheTime = 0, 0, 0
	p.state.Paused, p.state.Exhausted = false, false
	p.stateMu.Unlock()
	return nil
}

// Append adds url to the end of mpv's playlist. If mpv is idle (empty
// queue), playback starts at the appended entry — mpv decides, so a
// stale local idle flag can't make it the wrong call.
func (p *Player) Append(url string) error {
	if err := p.ensureRunning(); err != nil {
		return err
	}
	return p.command("loadfile", url, "append-play")
}

// PlayIndex jumps playback to playlist entry i.
func (p *Player) PlayIndex(i int) error {
	return p.command("set_property", "playlist-pos", i)
}

// RemoveIndex drops playlist entry i. mpv handles the case where i is
// the currently-playing entry (advances to the next).
func (p *Player) RemoveIndex(i int) error {
	return p.command("playlist-remove", i)
}

// MoveIndex moves playlist entry from to index to. mpv's playlist-move
// inserts the entry at index1 *before* index2, so to land an item at a
// strictly higher index we target to+1.
func (p *Player) MoveIndex(from, to int) error {
	dst := to
	if to > from {
		dst = to + 1
	}
	return p.command("playlist-move", from, dst)
}

// Next / Prev step through the playlist.
func (p *Player) Next() error { return p.command("playlist-next") }
func (p *Player) Prev() error { return p.command("playlist-prev") }

// SetPaused pauses or resumes playback.
func (p *Player) SetPaused(paused bool) error {
	return p.command("set_property", "pause", paused)
}

// TogglePause flips the paused state.
func (p *Player) TogglePause() error {
	return p.command("cycle", "pause")
}

// Stop clears the playlist and stops playback.
func (p *Player) Stop() error {
	p.stateMu.Lock()
	p.state = State{PlaylistPos: -1, Idle: true, ErrorSeq: p.state.ErrorSeq, LastError: p.state.LastError,
		Volume: p.state.Volume, Muted: p.state.Muted}
	p.stateMu.Unlock()
	return p.command("stop")
}

// Seek moves the playback position by delta (negative seeks backward).
func (p *Player) Seek(delta time.Duration) error {
	return p.command("seek", delta.Seconds(), "relative")
}

// SetVolume sets the volume, clamped to 0–100.
func (p *Player) SetVolume(v float64) error {
	return p.command("set_property", "volume", min(max(v, 0), 100))
}

// AddVolume changes the volume by delta, clamped to 0–100, unmuting —
// turning it up while muted should be audible.
func (p *Player) AddVolume(delta float64) error {
	if err := p.SetMuted(false); err != nil {
		return err
	}
	return p.SetVolume(p.State().Volume + delta)
}

// SetMuted mutes or unmutes without touching the volume level.
func (p *Player) SetMuted(muted bool) error {
	return p.command("set_property", "mute", muted)
}

// SeekTo moves the playback position to pos within the current track.
func (p *Player) SeekTo(pos time.Duration) error {
	return p.command("seek", pos.Seconds(), "absolute")
}

// State returns the last-known playback snapshot.
func (p *Player) State() State {
	p.stateMu.RLock()
	defer p.stateMu.RUnlock()
	return p.state
}

// Changes fires (coalesced, never blocking mpv's reader) whenever a
// State field other than Position / CacheTime changes.
func (p *Player) Changes() <-chan struct{} { return p.changes }

// Close stops mpv and removes its socket.
func (p *Player) Close() error {
	p.writeMu.Lock()
	p.closed = true
	if p.conn != nil && !p.dead {
		_ = p.writeLocked("quit")
	}
	p.writeMu.Unlock()
	p.reap()
	return nil
}

// reap closes the connection and makes sure the current mpv process is
// gone: it gets a moment to act on "quit", then is killed.
func (p *Player) reap() {
	p.writeMu.Lock()
	cmd, exited, conn, dir := p.cmd, p.exited, p.conn, p.sockDir
	p.dead = true
	p.writeMu.Unlock()
	if cmd == nil {
		return
	}
	select {
	case <-exited:
	case <-time.After(500 * time.Millisecond):
		_ = cmd.Process.Kill()
		<-exited
	}
	_ = conn.Close()
	_ = os.RemoveAll(dir)
}

// command sends a single IPC command. Replies are consumed (and
// ignored) by readLoop; failures that matter surface as end-file
// events instead.
func (p *Player) command(args ...any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return p.writeLocked(args...)
}

func (p *Player) writeLocked(args ...any) error {
	if p.dead || p.conn == nil {
		return ErrDead
	}
	p.reqID++
	payload := map[string]any{"command": args, "request_id": p.reqID}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	// A hung (SIGSTOPped) mpv stops draining the socket; don't let that
	// wedge the UI goroutine forever.
	_ = p.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = p.conn.Write(b)
	return err
}

// notify wakes Changes() listeners without ever blocking.
func (p *Player) notify() {
	select {
	case p.changes <- struct{}{}:
	default:
	}
}

// readLoop consumes mpv's IPC stream, updating state on property changes.
// When the stream ends — mpv exited or the socket broke — it marks the
// player dead, unless that conn was already replaced or closed on
// purpose.
func (p *Player) readLoop(conn net.Conn) {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		var msg struct {
			Event     string          `json:"event"`
			Name      string          `json:"name"`
			Data      json.RawMessage `json:"data"`
			Reason    string          `json:"reason"`
			FileError string          `json:"file_error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		switch msg.Event {
		case "property-change":
			p.applyPropertyChange(msg.Name, msg.Data)
		case "end-file":
			p.applyEndFile(msg.Reason, msg.FileError)
		case "start-file":
			p.stateMu.Lock()
			p.state.Exhausted = false
			p.lastEnd = ""
			p.stateMu.Unlock()
		}
	}

	p.writeMu.Lock()
	current := p.conn == conn && !p.closed
	if current {
		p.dead = true
	}
	p.writeMu.Unlock()
	if !current {
		return
	}
	p.stateMu.Lock()
	p.state = State{PlaylistPos: -1, Idle: true, Dead: true, ErrorSeq: p.state.ErrorSeq, LastError: p.state.LastError,
		Volume: p.state.Volume, Muted: p.state.Muted}
	p.stateMu.Unlock()
	p.notify()
}

func (p *Player) applyEndFile(reason, fileError string) {
	p.stateMu.Lock()
	p.lastEnd = reason
	if reason == "error" {
		if fileError == "" {
			fileError = "playback failed"
		}
		p.state.LastError = fileError
		p.state.ErrorSeq++
	}
	p.stateMu.Unlock()
	if reason == "error" {
		p.notify()
	}
}

func (p *Player) applyPropertyChange(name string, data json.RawMessage) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()

	// time-pos and demuxer-cache-time stream many times a second; only
	// the discrete properties are worth waking the UI for.
	if name != "time-pos" && name != "demuxer-cache-time" {
		p.notify()
	}

	// mpv reports unavailable properties as null. Unmarshalling null
	// into a plain value is a silent no-op, so each case decodes into a
	// pointer and treats nil as "not available".
	switch name {
	case "time-pos":
		if secs := decode[float64](data); secs != nil {
			p.state.Position = seconds(*secs)
		} else {
			p.state.Position = 0
		}
	case "duration":
		if secs := decode[float64](data); secs != nil {
			p.state.Duration = seconds(*secs)
		} else {
			p.state.Duration = 0
		}
	case "pause":
		if v := decode[bool](data); v != nil {
			p.state.Paused = *v
		}
	case "idle-active":
		if v := decode[bool](data); v != nil {
			p.state.Idle = *v
			// Only a natural end counts: LoadList's stop (reason "stop")
			// also passes through idle on its way to the new list.
			p.state.Exhausted = *v && (p.lastEnd == "eof" || p.lastEnd == "error")
		}
	case "playlist-pos":
		if v := decode[int](data); v != nil {
			p.state.PlaylistPos = *v
		} else {
			p.state.PlaylistPos = -1
		}
	case "paused-for-cache":
		if v := decode[bool](data); v != nil {
			p.state.Buffering = *v
		}
	case "demuxer-cache-time":
		if secs := decode[float64](data); secs != nil {
			p.state.CacheTime = seconds(*secs)
		} else {
			p.state.CacheTime = 0
		}
	case "volume":
		if v := decode[float64](data); v != nil {
			p.state.Volume = *v
		}
	case "mute":
		if v := decode[bool](data); v != nil {
			p.state.Muted = *v
		}
	}
}

// decode unmarshals data into a *T, returning nil for null, missing,
// or malformed values.
func decode[T any](data json.RawMessage) *T {
	var v *T
	if len(data) == 0 || json.Unmarshal(data, &v) != nil {
		return nil
	}
	return v
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// Package player drives an mpv subprocess over its JSON IPC socket.
//
// mpv is launched once in --idle mode; tracks are played by sending it a
// loadfile command. A background goroutine reads mpv's IPC stream and keeps
// the last-known playback state (position, duration, pause) so callers can
// poll State() cheaply — no need to plumb events into the UI loop.
package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// State is a snapshot of mpv's playback status.
type State struct {
	// Loaded is true once a file is playing or paused.
	Loaded   bool
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
	// Buffering is true while playback is stalled waiting for the
	// network cache to refill (mpv's paused-for-cache). Distinct from
	// Paused, which is a user-initiated pause.
	Buffering bool
	// CacheTime is the absolute timestamp the demuxer cache holds data
	// up to (mpv's demuxer-cache-time) — i.e. how far ahead of the
	// start the track has buffered. Compare against Duration for a
	// "buffered" fraction on the progress bar.
	CacheTime time.Duration
}

// Player owns the mpv subprocess and its IPC connection.
type Player struct {
	cmd        *exec.Cmd
	socketPath string
	conn       net.Conn

	writeMu sync.Mutex
	reqID   int

	stateMu sync.RWMutex
	state   State
}

// New launches mpv in idle mode and connects to its IPC socket.
// Call Close to shut it down.
func New() (*Player, error) {
	if _, err := exec.LookPath("mpv"); err != nil {
		return nil, fmt.Errorf("mpv not found on PATH: %w", err)
	}

	socketPath := filepath.Join(os.TempDir(),
		fmt.Sprintf("amptui-mpv-%d.sock", os.Getpid()))
	_ = os.Remove(socketPath) // stale socket from a crashed prior run

	cmd := exec.Command("mpv",
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--no-config",
		// Let mpv own the queue: prefetch opens the demuxer and starts
		// buffering the next playlist entry before the current ends,
		// and gapless-audio removes the silent gap at track boundaries.
		"--prefetch-playlist=yes",
		"--gapless-audio=yes",
		"--input-ipc-server="+socketPath,
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting mpv: %w", err)
	}

	conn, err := dialWithRetry(socketPath, 5*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("connecting to mpv IPC: %w", err)
	}

	p := &Player{cmd: cmd, socketPath: socketPath, conn: conn}
	p.state.PlaylistPos = -1
	go p.readLoop()

	// Ask mpv to push updates for the properties we surface in State().
	p.observe(1, "time-pos")
	p.observe(2, "duration")
	p.observe(3, "pause")
	p.observe(4, "idle-active")
	p.observe(5, "playlist-pos")
	p.observe(6, "paused-for-cache")
	p.observe(7, "demuxer-cache-time")
	return p, nil
}

// dialWithRetry waits for mpv to create its socket, then connects.
func dialWithRetry(path string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.Dial("unix", path)
		if err == nil {
			return conn, nil
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
	if start < 0 || start >= len(urls) {
		start = 0
	}
	// stop empties the playlist and idles mpv; appending then builds the
	// new list without auto-playing, so setting playlist-pos lands on
	// exactly the entry we want with no index-0 glitch.
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
	p.state = State{PlaylistPos: start}
	p.stateMu.Unlock()
	return nil
}

// Append adds url to the end of mpv's playlist. If mpv is idle (empty
// queue), playback starts at the appended entry.
func (p *Player) Append(url string) error {
	idle := p.State().Idle || p.State().PlaylistPos < 0
	mode := "append"
	if idle {
		mode = "append-play"
	}
	return p.command("loadfile", url, mode)
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

// TogglePause flips the paused state.
func (p *Player) TogglePause() error {
	return p.command("cycle", "pause")
}

// Stop clears the playlist and stops playback.
func (p *Player) Stop() error {
	p.stateMu.Lock()
	p.state = State{PlaylistPos: -1}
	p.stateMu.Unlock()
	return p.command("stop")
}

// Seek moves the playback position by delta (negative seeks backward).
func (p *Player) Seek(delta time.Duration) error {
	return p.command("seek", delta.Seconds(), "relative")
}

// State returns the last-known playback snapshot.
func (p *Player) State() State {
	p.stateMu.RLock()
	defer p.stateMu.RUnlock()
	return p.state
}

// Close stops mpv and removes its socket.
func (p *Player) Close() error {
	if p.conn != nil {
		_ = p.command("quit")
		_ = p.conn.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
	_ = os.Remove(p.socketPath)
	return nil
}

// command sends a single IPC command. The response is consumed by readLoop;
// we don't block on it since the commands here are fire-and-forget.
func (p *Player) command(args ...any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	p.reqID++
	payload := map[string]any{"command": args, "request_id": p.reqID}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = p.conn.Write(b)
	return err
}

// observe registers a property for change notifications from mpv.
func (p *Player) observe(id int, property string) {
	_ = p.command("observe_property", id, property)
}

// readLoop consumes mpv's IPC stream, updating state on property changes.
func (p *Player) readLoop() {
	scanner := bufio.NewScanner(p.conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		var msg struct {
			Event string          `json:"event"`
			Name  string          `json:"name"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Event != "property-change" {
			continue
		}
		p.applyPropertyChange(msg.Name, msg.Data)
	}
}

func (p *Player) applyPropertyChange(name string, data json.RawMessage) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()

	switch name {
	case "time-pos":
		var secs float64
		if json.Unmarshal(data, &secs) == nil {
			p.state.Position = time.Duration(secs * float64(time.Second))
			p.state.Loaded = true
		}
	case "duration":
		var secs float64
		if json.Unmarshal(data, &secs) == nil {
			p.state.Duration = time.Duration(secs * float64(time.Second))
		}
	case "pause":
		var paused bool
		if json.Unmarshal(data, &paused) == nil {
			p.state.Paused = paused
		}
	case "idle-active":
		var idle bool
		if json.Unmarshal(data, &idle) == nil {
			p.state.Idle = idle
		}
	case "playlist-pos":
		// mpv sends null / -1 when nothing is loaded.
		var pos int
		if json.Unmarshal(data, &pos) == nil {
			p.state.PlaylistPos = pos
		} else {
			p.state.PlaylistPos = -1
		}
	case "paused-for-cache":
		var buffering bool
		if json.Unmarshal(data, &buffering) == nil {
			p.state.Buffering = buffering
		}
	case "demuxer-cache-time":
		var secs float64
		if json.Unmarshal(data, &secs) == nil {
			p.state.CacheTime = time.Duration(secs * float64(time.Second))
		}
	}
}

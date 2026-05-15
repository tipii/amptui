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
		fmt.Sprintf("plexamp-tui-mpv-%d.sock", os.Getpid()))
	_ = os.Remove(socketPath) // stale socket from a crashed prior run

	cmd := exec.Command("mpv",
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--no-config",
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
	go p.readLoop()

	// Ask mpv to push updates for the properties we surface in State().
	p.observe(1, "time-pos")
	p.observe(2, "duration")
	p.observe(3, "pause")
	p.observe(4, "core-idle")
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

// Load starts playback of url, replacing whatever is currently playing.
func (p *Player) Load(url string) error {
	return p.command("loadfile", url, "replace")
}

// TogglePause flips the paused state.
func (p *Player) TogglePause() error {
	return p.command("cycle", "pause")
}

// Stop clears the playlist and stops playback.
func (p *Player) Stop() error {
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
	case "core-idle":
		var idle bool
		if json.Unmarshal(data, &idle) == nil {
			p.state.Idle = idle
		}
	}
}

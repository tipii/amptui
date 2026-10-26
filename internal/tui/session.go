package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/player"
)

// The playback session — queue, position, play modes, volume — is saved
// on quit (and periodically while playing, so a crash loses little) and
// restored, paused, on the next launch. main calls SaveSession and
// RestoreSession explicitly; nothing here runs implicitly from New, so
// tests never pick up a real session file.

// sessionVersion is bumped when the file's shape changes incompatibly;
// an older file is ignored rather than misread.
const sessionVersion = 1

// sessionSaveInterval is how often a playing session is checkpointed.
const sessionSaveInterval = 30 * time.Second

type session struct {
	Version int `json:"version"`
	// Server is the backend + URL the queue's tracks belong to. A
	// session from another server is ignored: its IDs mean nothing here.
	Server   string        `json:"server"`
	Queue    []media.Track `json:"queue,omitempty"`
	QueueSeq []int         `json:"queue_seq,omitempty"`
	Index    int           `json:"index"`
	Position time.Duration `json:"position"`
	Shuffle  bool          `json:"shuffle,omitempty"`
	Repeat   player.Repeat `json:"repeat,omitempty"`
	Volume   float64       `json:"volume"`
	Muted    bool          `json:"muted,omitempty"`
}

// sessionPath is $XDG_STATE_HOME/amptui/session.json, falling back to
// ~/.local/state — state, not config: it changes constantly and losing
// it is harmless.
func sessionPath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "amptui", "session.json"), nil
}

func (m Model) serverID() string { return m.cfg.Backend + "|" + m.cfg.ServerURL }

// SaveSession writes the current queue and play state. Best effort: a
// failure only means the next launch starts empty.
func (m Model) SaveSession() error {
	if m.player == nil || m.client == nil {
		return nil
	}
	st := m.player.State()
	s := session{
		Version: sessionVersion,
		Server:  m.serverID(),
		Shuffle: m.shuffle,
		Repeat:  m.repeat,
		Volume:  st.Volume,
		Muted:   st.Muted,
	}
	if m.nowPlaying != nil && len(m.queue) > 0 {
		m.ensureSeq()
		s.Queue, s.QueueSeq, s.Index = m.queue, m.queueSeq, m.queueIdx
		s.Position = st.Position
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	if err := errors.Join(werr, f.Close()); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), path)
}

// RestoreSession loads the saved session: volume and play modes always,
// and the queue — paused, at the saved track — when it belongs to this
// server. The saved position is applied once mpv has the file open (a
// seek before that is dropped), via pendingSeek in syncFromPlayer.
func (m Model) RestoreSession() Model {
	if m.player == nil || m.client == nil {
		return m
	}
	path, err := sessionPath()
	if err != nil {
		return m
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	var s session
	if json.Unmarshal(b, &s) != nil || s.Version != sessionVersion {
		return m
	}
	_ = m.player.SetVolume(s.Volume)
	_ = m.player.SetMuted(s.Muted)
	m.setRepeat(s.Repeat)
	m.shuffle = s.Shuffle

	if s.Server != m.serverID() || len(s.Queue) == 0 || s.Index < 0 || s.Index >= len(s.Queue) {
		return m
	}
	queue, urls := m.playable(s.Queue)
	if len(queue) != len(s.Queue) {
		return m // the backend can't stream some of it any more; start fresh
	}
	if m.player.LoadListPaused(urls, s.Index) != nil {
		return m
	}
	m.queue, m.queueSeq, m.queueIdx = queue, s.QueueSeq, s.Index
	for _, n := range m.queueSeq {
		m.seqCounter = max(m.seqCounter, n)
	}
	m.ensureSeq()
	t := m.queue[m.queueIdx]
	m.nowPlaying = &t
	m.pendingSeek = s.Position
	return m
}

// maybeCheckpoint saves the session every sessionSaveInterval while
// something is playing, so a crash or kill doesn't lose the queue.
func (m *Model) maybeCheckpoint(now time.Time) {
	if m.nowPlaying == nil || now.Sub(m.lastCheckpoint) < sessionSaveInterval {
		return
	}
	m.lastCheckpoint = now
	_ = m.SaveSession()
}

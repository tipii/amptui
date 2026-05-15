package tui

import (
	"errors"
	"fmt"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/theopalhol/amptui/internal/plex"
)

// playTracks sets the playback queue to tracks and starts at index start
// (playing from there to the end of the queue).
func (m Model) playTracks(tracks []plex.Track, start int) (tea.Model, tea.Cmd) {
	if m.player == nil {
		m.err = errors.New("playback unavailable: mpv is not running")
		return m, nil
	}
	if start < 0 || start >= len(tracks) {
		return m, nil
	}
	m.queue = tracks
	m.queueIdx = start
	m.loadCurrent()
	return m, nil
}

// loadCurrent loads queue[queueIdx] into the player and updates nowPlaying.
// On failure it sets m.err and leaves nowPlaying unchanged. No-ops if the
// player or client haven't been wired (e.g. in tests).
func (m *Model) loadCurrent() {
	if m.player == nil || m.client == nil {
		return
	}
	t := m.queue[m.queueIdx]
	url := m.client.StreamURL(t)
	if url == "" {
		m.err = errors.New("track has no playable media")
		return
	}
	if err := m.player.Load(url); err != nil {
		m.err = fmt.Errorf("playback: %w", err)
		return
	}
	track := t
	m.nowPlaying = &track
	m.err = nil
}

// enqueue appends tracks to the playback queue. If nothing is currently
// playing, playback starts from the first appended track.
func (m *Model) enqueue(tracks ...plex.Track) {
	if len(tracks) == 0 {
		return
	}
	if m.player == nil {
		m.err = errors.New("playback unavailable: mpv is not running")
		return
	}
	wasEmpty := len(m.queue) == 0
	m.queue = append(m.queue, tracks...)
	if wasEmpty {
		m.queueIdx = 0
		m.loadCurrent()
	}
}

// enqueueSelectedTrack adds the highlighted track to the queue. It no-ops
// unless a track row is highlighted.
func (m Model) enqueueSelectedTrack() Model {
	if it, ok := m.list.SelectedItem().(trackItem); ok {
		m.enqueue(it.track)
	}
	return m
}

// enqueueSelectedAlbum adds every track of the current album to the queue.
// It works whether a track row or the "Play album" row is highlighted.
func (m Model) enqueueSelectedAlbum() Model {
	switch it := m.list.SelectedItem().(type) {
	case trackItem:
		m.enqueue(it.tracks...)
	case albumActionItem:
		m.enqueue(it.tracks...)
	}
	return m
}

// openQueue shows the queue modal, rebuilt from the current queue.
func (m *Model) openQueue() {
	m.showQueue = true
	m.rebuildQueueList()
	m.queueList.Select(m.queueIdx)
}

// rebuildQueueList repopulates the modal's list from m.queue.
func (m *Model) rebuildQueueList() {
	items := make([]list.Item, len(m.queue))
	playing := m.nowPlaying != nil
	for i, t := range m.queue {
		items[i] = queueItem{track: t, current: playing && i == m.queueIdx}
	}
	m.queueList.SetItems(items)
}

// playNext skips to the next track in the queue, if any.
func (m *Model) playNext() {
	if m.player == nil || m.queueIdx+1 >= len(m.queue) {
		return
	}
	m.queueIdx++
	m.loadCurrent()
}

// playPrev jumps to the previous track in the queue, if any.
func (m *Model) playPrev() {
	if m.player == nil || m.queueIdx <= 0 || len(m.queue) == 0 {
		return
	}
	m.queueIdx--
	m.loadCurrent()
}

// moveQueueItem shifts the track at the queue-list cursor by delta positions
// (+1 down, -1 up) within the queue. queueIdx is kept pointing at the
// currently-playing track regardless of the move.
func (m *Model) moveQueueItem(delta int) {
	i := m.queueList.Index()
	j := i + delta
	if i < 0 || i >= len(m.queue) || j < 0 || j >= len(m.queue) {
		return
	}
	m.queue[i], m.queue[j] = m.queue[j], m.queue[i]
	switch {
	case m.queueIdx == i:
		m.queueIdx = j
	case m.queueIdx == j:
		m.queueIdx = i
	}
	m.rebuildQueueList()
	m.queueList.Select(j)
}

// deleteQueueItem removes the track at the cursor from the queue. If the
// removed track was the one playing, playback advances to the next track,
// or stops if the queue is now empty.
func (m *Model) deleteQueueItem() {
	i := m.queueList.Index()
	if i < 0 || i >= len(m.queue) {
		return
	}
	playingRemoved := i == m.queueIdx
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
	if i < m.queueIdx {
		m.queueIdx--
	}
	switch {
	case playingRemoved && len(m.queue) == 0:
		m.nowPlaying = nil
		m.queueIdx = 0
		if m.player != nil {
			_ = m.player.Stop()
		}
	case playingRemoved && i >= len(m.queue):
		// Removed the last track (which was playing).
		m.nowPlaying = nil
		m.queue = nil
		m.queueIdx = 0
		if m.player != nil {
			_ = m.player.Stop()
		}
	case playingRemoved:
		m.queueIdx = i
		m.loadCurrent()
	}
	m.rebuildQueueList()
	if i >= len(m.queue) && len(m.queue) > 0 {
		m.queueList.Select(len(m.queue) - 1)
	}
}

// playQueueItem jumps playback to the track at the queue-list cursor.
func (m *Model) playQueueItem() {
	i := m.queueList.Index()
	if i < 0 || i >= len(m.queue) || m.player == nil {
		return
	}
	m.queueIdx = i
	m.loadCurrent()
	m.rebuildQueueList()
}

// advanceIfFinished checks whether the current track has ended and, if so,
// plays the next queued track or clears the now-playing state.
func (m Model) advanceIfFinished() Model {
	if m.player == nil || m.nowPlaying == nil {
		return m
	}
	if !m.player.State().Idle {
		return m
	}
	if m.queueIdx+1 < len(m.queue) {
		m.queueIdx++
		m.loadCurrent()
		return m
	}
	// Queue exhausted: clear the now-playing line.
	m.nowPlaying = nil
	m.queue = nil
	m.queueIdx = 0
	return m
}

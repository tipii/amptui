package tui

import (
	"errors"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/tipii/amptui/internal/media"
)

// Queue playback is delegated to mpv: m.queue mirrors mpv's internal
// playlist (same order), and mpv owns advancement, prefetch, and
// gapless transitions. m.queueIdx is kept in sync with mpv's
// playlist-pos by syncFromPlayer on each tick — every queue mutation
// applies the same change to both m.queue and the mpv playlist so the
// indices stay aligned.

// streamURLs maps the current queue to playable URLs, 1:1 so the slice
// index matches mpv's playlist index.
func (m Model) streamURLs() []string {
	urls := make([]string, len(m.queue))
	if m.client == nil {
		return urls
	}
	for i, t := range m.queue {
		urls[i] = m.client.StreamURL(t)
	}
	return urls
}

// playTracks sets the playback queue to tracks and starts at index start
// (mpv plays from there through the rest of the queue).
func (m Model) playTracks(tracks []media.Track, start int) (tea.Model, tea.Cmd) {
	if m.player == nil {
		m.err = errors.New("playback unavailable: mpv is not running")
		return m, nil
	}
	if start < 0 || start >= len(tracks) {
		return m, nil
	}
	m.queue = tracks
	m.queueIdx = start
	m.loadQueue(start)
	return m, nil
}

// loadQueue hands the whole queue to mpv, starting at index start, and
// sets nowPlaying optimistically. No-ops without a player/client.
func (m *Model) loadQueue(start int) {
	if m.player == nil || m.client == nil {
		return
	}
	if err := m.player.LoadList(m.streamURLs(), start); err != nil {
		m.err = err
		return
	}
	t := m.queue[start]
	m.nowPlaying = &t
	m.err = nil
}

// enqueue appends tracks to the playback queue (and to mpv's playlist).
// If nothing is playing, mpv starts at the first appended track.
func (m *Model) enqueue(tracks ...media.Track) {
	if len(tracks) == 0 {
		return
	}
	if m.player == nil {
		m.err = errors.New("playback unavailable: mpv is not running")
		return
	}
	wasEmpty := len(m.queue) == 0
	m.queue = append(m.queue, tracks...)
	if m.client != nil {
		for _, t := range tracks {
			if u := m.client.StreamURL(t); u != "" {
				_ = m.player.Append(u)
			}
		}
	}
	if wasEmpty {
		m.queueIdx = 0
		t := m.queue[0]
		m.nowPlaying = &t
	}
}

// enqueueSelected adds whatever's under the cursor to the queue:
// a single track on a track row, every track of an album on an album row
// (looked up via the library cache) or on the "▶ Play album" action.
// No-op for any other row type. Mirrors how `d` resolves selection so
// `q` and `d` always agree on what's selected.
func (m Model) enqueueSelected() Model {
	switch it := m.list.SelectedItem().(type) {
	case trackItem:
		m.enqueue(it.track)
	case albumActionItem:
		m.enqueue(it.tracks...)
	case albumItem:
		if m.library != nil {
			m.enqueue(m.library.Tracks(it.album.RatingKey)...)
		}
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

// playNext / playPrev delegate to mpv; syncFromPlayer reflects the new
// position back into queueIdx / nowPlaying on the next tick.
func (m *Model) playNext() {
	if m.player != nil {
		_ = m.player.Next()
	}
}

func (m *Model) playPrev() {
	if m.player != nil {
		_ = m.player.Prev()
	}
}

// moveQueueItem shifts the track at the queue-list cursor by delta
// positions (+1 down, -1 up), applying the same swap to mpv's playlist.
func (m *Model) moveQueueItem(delta int) {
	i := m.queueList.Index()
	j := i + delta
	if i < 0 || i >= len(m.queue) || j < 0 || j >= len(m.queue) {
		return
	}
	m.queue[i], m.queue[j] = m.queue[j], m.queue[i]
	if m.player != nil {
		_ = m.player.MoveIndex(i, j)
	}
	// Keep queueIdx pointing at the playing track across the swap;
	// syncFromPlayer reconciles against mpv shortly regardless.
	switch {
	case m.queueIdx == i:
		m.queueIdx = j
	case m.queueIdx == j:
		m.queueIdx = i
	}
	m.rebuildQueueList()
	m.queueList.Select(j)
}

// deleteQueueItem removes the track at the cursor from the queue and
// mpv's playlist. mpv advances on its own if the playing entry was the
// one removed; syncFromPlayer updates nowPlaying afterward.
func (m *Model) deleteQueueItem() {
	i := m.queueList.Index()
	if i < 0 || i >= len(m.queue) {
		return
	}
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
	if m.player != nil {
		_ = m.player.RemoveIndex(i)
	}
	if i < m.queueIdx {
		m.queueIdx--
	}
	if len(m.queue) == 0 {
		m.nowPlaying = nil
		m.queueIdx = 0
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
	_ = m.player.PlayIndex(i)
	m.queueIdx = i
	t := m.queue[i]
	m.nowPlaying = &t
	m.rebuildQueueList()
}

// syncFromPlayer reconciles queueIdx / nowPlaying with mpv's current
// playlist position. mpv owns advancement (auto-advance, prefetch,
// gapless), so this is how the UI learns a track changed. When the
// playlist is exhausted (mpv idle, playlist-pos < 0) the now-playing
// state is cleared.
func (m Model) syncFromPlayer() Model {
	if m.player == nil || len(m.queue) == 0 {
		return m
	}
	st := m.player.State()
	switch {
	case st.PlaylistPos >= 0 && st.PlaylistPos < len(m.queue):
		if st.PlaylistPos != m.queueIdx || m.nowPlaying == nil {
			m.queueIdx = st.PlaylistPos
			t := m.queue[m.queueIdx]
			m.nowPlaying = &t
		}
	case st.Idle && st.PlaylistPos < 0 && m.nowPlaying != nil:
		// Playlist exhausted.
		m.nowPlaying = nil
		m.queue = nil
		m.queueIdx = 0
	}
	return m
}

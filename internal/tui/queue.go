package tui

import (
	"errors"
	"fmt"
	"time"

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

// playable pairs each track with its stream URL, dropping any the
// backend can't stream (no media part, no client). Everything that
// reaches m.queue goes through here: a track in m.queue without a
// matching mpv entry would shift every later index by one.
func (m Model) playable(tracks []media.Track) ([]media.Track, []string) {
	if m.client == nil {
		return nil, nil
	}
	out := make([]media.Track, 0, len(tracks))
	urls := make([]string, 0, len(tracks))
	for _, t := range tracks {
		if u := m.client.StreamURL(t); u != "" {
			out = append(out, t)
			urls = append(urls, u)
		}
	}
	return out, urls
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
	// Re-find the chosen track once unplayable ones are dropped. If it
	// is unplayable itself, start from whatever follows it.
	before, _ := m.playable(tracks[:start])
	queue, urls := m.playable(tracks)
	start = len(before)
	if start >= len(queue) {
		m.err = errors.New("nothing playable: the server has no stream for that track")
		return m, nil
	}
	if err := m.player.LoadList(urls, start); err != nil {
		m.err = err
		return m, nil
	}
	m.queue = queue
	m.queueIdx = start
	t := m.queue[start]
	m.nowPlaying = &t
	m.err = nil
	return m, nil
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
	tracks, urls := m.playable(tracks)
	wasEmpty := len(m.queue) == 0
	for i, u := range urls {
		if err := m.player.Append(u); err != nil {
			m.err = err
			return
		}
		m.queue = append(m.queue, tracks[i])
	}
	if wasEmpty && len(m.queue) > 0 {
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
	switch it := m.selectedItem().(type) {
	case trackItem:
		m.enqueue(it.track)
	case albumActionItem:
		m.enqueue(it.tracks...)
	case albumItem:
		if m.library != nil {
			m.enqueue(m.library.Tracks(it.album.RatingKey)...)
		}
	case artistItem:
		m.enqueue(m.artistTracks(it.artist.RatingKey)...)
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

// restartThreshold is how far into a track "previous" restarts it
// instead of going back a track — what every music player does.
const restartThreshold = 3 * time.Second

func (m *Model) playPrev() {
	if m.player == nil {
		return
	}
	if m.player.State().Position > restartThreshold || m.queueIdx == 0 {
		m.seekTo(0)
		return
	}
	_ = m.player.Prev()
}

// albumContext returns t's album (in album order) and t's position in
// it, so playing one track carries on through the rest of the album.
// Falls back to just t when the album isn't in the library cache.
func (m Model) albumContext(t media.Track) ([]media.Track, int) {
	if m.library != nil && t.AlbumRatingKey != "" {
		tracks := m.library.Tracks(t.AlbumRatingKey)
		for i, at := range tracks {
			if at.RatingKey == t.RatingKey {
				return tracks, i
			}
		}
	}
	return []media.Track{t}, 0
}

// artistTracks is every track by an artist, album by album in the
// current album ordering, for q / d on an artist row.
func (m Model) artistTracks(artistKey string) []media.Track {
	if m.library == nil {
		return nil
	}
	var out []media.Track
	for _, it := range m.albumItems(artistKey) {
		if ai, ok := it.(albumItem); ok {
			out = append(out, m.library.Tracks(ai.album.RatingKey)...)
		}
	}
	return out
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
// one removed — onto the entry that now sits at the same index, which
// syncFromPlayer can't see as a change, so nowPlaying is moved here.
func (m *Model) deleteQueueItem() {
	i := m.queueList.Index()
	if i < 0 || i >= len(m.queue) {
		return
	}
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
	if m.player != nil {
		_ = m.player.RemoveIndex(i)
	}
	switch {
	case len(m.queue) == 0:
		m.nowPlaying = nil
		m.queueIdx = 0
	case i < m.queueIdx:
		m.queueIdx--
	case i == m.queueIdx && m.nowPlaying != nil:
		if i < len(m.queue) {
			t := m.queue[i]
			m.nowPlaying = &t
		} else {
			// Removed the last, playing entry: mpv stops. The earlier
			// entries stay in both lists, so indices still line up.
			m.nowPlaying = nil
			m.queueIdx = len(m.queue) - 1
		}
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
// gapless), so this is how the UI learns a track changed. It also
// handles the two ways playback ends on its own: the last entry played
// out (clear both queues, so the next enqueue starts at index 0 in
// each) or mpv died (its playlist died with it).
func (m Model) syncFromPlayer() Model {
	if m.player == nil {
		return m
	}
	st := m.player.State()
	if st.ErrorSeq != m.playerErrSeq {
		m.playerErrSeq = st.ErrorSeq
		m.err = fmt.Errorf("couldn't play a track: %s", st.LastError)
	}
	switch {
	case st.Dead:
		if m.queue != nil || m.nowPlaying != nil {
			m.err = errors.New("mpv exited unexpectedly; it will restart with the next track you play")
		}
		m.nowPlaying = nil
		m.queue = nil
		m.queueIdx = 0
	case len(m.queue) == 0:
	case st.PlaylistPos >= 0 && st.PlaylistPos < len(m.queue):
		if st.PlaylistPos != m.queueIdx || m.nowPlaying == nil {
			m.queueIdx = st.PlaylistPos
			t := m.queue[m.queueIdx]
			m.nowPlaying = &t
		}
	case st.Exhausted:
		// mpv keeps finished entries in its playlist when it idles;
		// drop them too or the next append-play lands at index N while
		// m.queue puts it at 0.
		_ = m.player.Stop()
		m.nowPlaying = nil
		m.queue = nil
		m.queueIdx = 0
	}
	return m
}

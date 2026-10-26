package tui

import (
	"context"
	"net/url"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tipii/amptui/internal/imgcache"
	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/mpris"
	"github.com/tipii/amptui/internal/player"
)

// OS media controls. The MPRIS server (internal/mpris) only relays: bus
// requests arrive here as mediaCommandMsg and are applied exactly like
// the matching keys, and publishMedia pushes the now-playing state back
// out. publishMedia runs on every tick and on every player change
// signal, so a pause from either side shows up in the desktop's widgets
// straight away rather than on the next second.

// mediaCommandMsg is one request from a media key / playerctl / widget.
type mediaCommandMsg mpris.Command

// playerChangedMsg means mpv's pause / track / idle state moved.
type playerChangedMsg struct{}

// mediaArtMsg reports that the cover for albumKey is now in imgcache
// (or failed to fetch — publishMedia just finds no file either way).
type mediaArtMsg struct{ albumKey string }

// WithMediaControls attaches an MPRIS server. s may be nil (no session
// bus, non-Linux), which leaves media controls off.
func (m Model) WithMediaControls(s *mpris.Server) Model {
	m.mpris = s
	return m
}

// waitMediaCommand blocks for the next bus request. Re-armed after
// every delivery, like a subscription.
func waitMediaCommand(s *mpris.Server) tea.Cmd {
	if s == nil {
		return nil
	}
	return func() tea.Msg { return mediaCommandMsg(<-s.Commands()) }
}

// waitPlayerChange blocks for the next mpv state change.
func waitPlayerChange(p *player.Player) tea.Cmd {
	if p == nil {
		return nil
	}
	return func() tea.Msg {
		<-p.Changes()
		return playerChangedMsg{}
	}
}

// applyMediaCommand performs a bus request against the player.
func (m Model) applyMediaCommand(c mpris.Command) (tea.Model, tea.Cmd) {
	switch c.Kind {
	case mpris.Quit:
		return m, tea.Quit
	case mpris.SetVolume:
		// Works with nothing playing too, unlike the transport commands.
		if m.player != nil {
			_ = m.player.SetMuted(false)
			_ = m.player.SetVolume(c.Volume * 100)
		}
		return m, waitMediaCommand(m.mpris)
	case mpris.SetShuffle:
		if c.Shuffle != m.shuffle {
			m.toggleShuffle()
		}
		pub := m.publishMedia()
		return m, tea.Batch(waitMediaCommand(m.mpris), pub)
	case mpris.SetLoop:
		m.setRepeat(map[mpris.LoopStatus]player.Repeat{
			mpris.LoopNone: player.RepeatOff, mpris.LoopPlaylist: player.RepeatQueue, mpris.LoopTrack: player.RepeatTrack,
		}[c.Loop])
		pub := m.publishMedia()
		return m, tea.Batch(waitMediaCommand(m.mpris), pub)
	}
	if m.player == nil || m.nowPlaying == nil {
		return m, waitMediaCommand(m.mpris)
	}
	switch c.Kind {
	case mpris.Play:
		_ = m.player.SetPaused(false)
	case mpris.Pause:
		_ = m.player.SetPaused(true)
	case mpris.PlayPause:
		_ = m.player.TogglePause()
	case mpris.Stop:
		// A real stop would clear mpv's playlist, i.e. throw away the
		// queue on a stray keypress. Pause-and-rewind keeps it.
		_ = m.player.SetPaused(true)
		m.seekTo(0)
	case mpris.Next:
		m.playNext()
	case mpris.Previous:
		m.playPrev()
	case mpris.Seek:
		st := m.player.State()
		target := st.Position + c.Offset
		if st.Duration > 0 && target >= st.Duration {
			// The spec: seeking past the end behaves like Next.
			m.playNext()
			break
		}
		m.seekTo(max(target, 0))
	case mpris.SetPosition:
		if d := m.player.State().Duration; d > 0 && c.Position > d {
			break // out of range: the spec says do nothing
		}
		m.seekTo(c.Position)
	}
	return m, waitMediaCommand(m.mpris)
}

// seekBy moves the playhead by delta (the < / > keys).
func (m *Model) seekBy(delta time.Duration) {
	if m.player == nil {
		return
	}
	st := m.player.State()
	target := max(st.Position+delta, 0)
	if st.Duration > 0 {
		target = min(target, st.Duration)
	}
	_ = m.player.Seek(delta)
	if m.mpris != nil {
		m.mpris.Seeked(target)
	}
}

// seekTo moves the playhead to an absolute position and tells MPRIS
// clients the position jumped.
func (m *Model) seekTo(pos time.Duration) {
	_ = m.player.SeekTo(pos)
	if m.mpris != nil {
		m.mpris.Seeked(pos)
	}
}

// publishMedia pushes the now-playing state to MPRIS. The returned cmd
// fetches the album cover into imgcache the first time a track from a
// new album plays, so widgets get a local file:// URL — handing them
// the server URL would leak the auth token to whatever caches it.
func (m *Model) publishMedia() tea.Cmd {
	if m.mpris == nil {
		return nil
	}
	st := mpris.Status{State: mpris.Stopped, Volume: 1, Shuffle: m.shuffle,
		Loop: [...]mpris.LoopStatus{mpris.LoopNone, mpris.LoopPlaylist, mpris.LoopTrack}[m.repeat]}
	if m.player != nil {
		if ps := m.player.State(); ps.Muted {
			st.Volume = 0
		} else {
			st.Volume = ps.Volume / 100
		}
	}
	var cmd tea.Cmd
	if t := m.nowPlaying; t != nil && m.player != nil {
		ps := m.player.State()
		st.State = mpris.Playing
		if ps.Paused {
			st.State = mpris.Paused
		}
		// Prefer the library's duration, as the now-playing bar does:
		// mpv's lags a track change by however long the stream takes
		// to open, and would briefly publish the previous track's.
		length := t.Duration
		if length == 0 {
			length = ps.Duration
		}
		st.Track = &mpris.Track{
			ID:          t.RatingKey,
			Title:       t.Title,
			Artist:      t.Artist,
			Album:       t.Album,
			TrackNumber: t.Index,
			Length:      length,
		}
		st.CanPrev = m.queueIdx > 0
		st.CanNext = m.queueIdx < len(m.queue)-1

		// mediaArtURL remembers the hit, so this runs on every tick
		// without a stat each time.
		if m.mediaArtURLKey == t.AlbumRatingKey && m.mediaArtURL != "" {
			st.Track.ArtURL = m.mediaArtURL
		} else if art, ok := cachedCoverURL(t.AlbumRatingKey); ok {
			m.mediaArtURLKey, m.mediaArtURL = t.AlbumRatingKey, art
			st.Track.ArtURL = art
		} else if t.AlbumRatingKey != m.mediaArtKey {
			m.mediaArtKey = t.AlbumRatingKey
			cmd = fetchCoverToCache(m.client, t.AlbumRatingKey)
		}
	}
	m.mpris.Update(st)
	return cmd
}

// cachedCoverURL returns a file:// URL for the album's cover if
// fetchArtwork / fetchCoverToCache has already stored it.
func cachedCoverURL(albumKey string) (string, bool) {
	if albumKey == "" {
		return "", false
	}
	p, err := imgcache.Path("grid/"+albumKey, 0, 0)
	if err != nil {
		return "", false
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
		return "", false
	}
	return (&url.URL{Scheme: "file", Path: p}).String(), true
}

// fetchCoverToCache stores an album's cover under the same cache key
// fetchArtwork uses, without decoding it.
func fetchCoverToCache(client media.Backend, albumKey string) tea.Cmd {
	if client == nil || albumKey == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), metaFetchTimeout)
		defer cancel()
		if data, err := client.FetchImage(ctx, client.ArtworkURL(albumKey)); err == nil && len(data) > 0 {
			_ = imgcache.Put("grid/"+albumKey, 0, 0, data)
		}
		return mediaArtMsg{albumKey: albumKey}
	}
}

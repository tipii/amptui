package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tipii/amptui/internal/media"
)

// Playback reporting tells the server what's playing (its Now Playing /
// sessions view) and counts plays, so the dashboard's "Recent plays"
// includes what's played in amptui. It runs off the same reconcile
// points as MPRIS — the tick and player change events — comparing what
// was last reported with what's playing now.

const (
	// reportInterval is how often progress is reported while playing.
	reportInterval = 10 * time.Second
	// playedFraction is how far into a track a play counts as played.
	playedFraction = 0.9
	// reportTimeout bounds one report request.
	reportTimeout = 10 * time.Second
)

// playReport is the play being reported: which track, the last state
// and position sent / seen, and whether it's been counted yet.
type playReport struct {
	track     media.Track
	paused    bool
	pos       time.Duration
	sentAt    time.Time
	scrobbled bool
}

// reportPlayback compares the playing state with what was last reported
// and returns the report requests to send, if any.
func (m *Model) reportPlayback(now time.Time) tea.Cmd {
	if m.client == nil || m.player == nil || !m.cfg.ReportsPlayback() {
		return nil
	}
	st := m.player.State()
	cur := m.nowPlaying
	var cmds []tea.Cmd

	// The previous play ended: the track changed, playback stopped, or
	// the same track started over (repeat one, or queued twice in a row).
	if r := m.report; r != nil {
		restarted := cur != nil && cur.RatingKey == r.track.RatingKey && st.Position+5*time.Second < r.pos
		if cur == nil || cur.RatingKey != r.track.RatingKey || restarted {
			cmds = append(cmds, m.sendReport(r.track, media.PlaybackStopped, r.pos, false))
			m.report = nil
		}
	}
	if cur == nil {
		return tea.Batch(cmds...)
	}

	if m.report == nil {
		// A play starts being reported once it's actually playing: a
		// session restored paused at launch isn't "now playing" yet.
		if st.Paused {
			return tea.Batch(cmds...)
		}
		m.report = &playReport{track: *cur, paused: st.Paused, pos: st.Position, sentAt: now}
		return tea.Batch(append(cmds, m.sendReport(*cur, stateOf(st.Paused), st.Position, true))...)
	}

	r := m.report
	r.pos = st.Position
	switch {
	case st.Paused != r.paused:
		r.paused, r.sentAt = st.Paused, now
		cmds = append(cmds, m.sendReport(r.track, stateOf(st.Paused), st.Position, false))
	case !st.Paused && now.Sub(r.sentAt) >= reportInterval:
		r.sentAt = now
		cmds = append(cmds, m.sendReport(r.track, media.PlaybackPlaying, st.Position, false))
	}
	if d := r.track.Duration; !r.scrobbled && d > 0 && float64(st.Position) >= playedFraction*float64(d) {
		r.scrobbled = true
		cmds = append(cmds, m.markPlayed(r.track))
	}
	return tea.Batch(cmds...)
}

func stateOf(paused bool) media.PlaybackState {
	if paused {
		return media.PlaybackPaused
	}
	return media.PlaybackPlaying
}

// sendReport sends one report in the background. Failures are dropped:
// reporting is a courtesy to the server, never worth an error line.
func (m Model) sendReport(t media.Track, state media.PlaybackState, pos time.Duration, started bool) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
		defer cancel()
		_ = client.ReportPlayback(ctx, media.PlaybackReport{
			Track: t, State: state, Position: pos, Duration: t.Duration, Started: started,
		})
		return nil
	}
}

func (m Model) markPlayed(t media.Track) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
		defer cancel()
		_ = client.MarkPlayed(ctx, t)
		return nil
	}
}

// StopReport ends the reported play synchronously, for quitting: the
// server would otherwise show amptui playing until its session timed out.
func (m Model) StopReport() {
	if m.report == nil || m.client == nil || !m.cfg.ReportsPlayback() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pos := m.report.pos
	if m.player != nil {
		pos = m.player.State().Position
	}
	_ = m.client.ReportPlayback(ctx, media.PlaybackReport{
		Track: m.report.track, State: media.PlaybackStopped, Position: pos, Duration: m.report.track.Duration,
	})
}

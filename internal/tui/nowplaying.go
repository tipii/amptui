package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// nowPlayingLine renders a two-row block: the current track + elapsed
// time on row 1, and a track-position bar on row 2 (blank when nothing
// is playing). Two rows are always returned so the surrounding layout
// stays stable across track changes.
func (m Model) nowPlayingLine() string {
	if m.nowPlaying == nil {
		return helpStyle.Render("— nothing playing —") + "\n"
	}
	t := m.nowPlaying

	var status, clock string
	var playPct, bufPct float64
	if m.player != nil {
		s := m.player.State()
		clock = fmt.Sprintf("  %s / %s", fmtDur(s.Position), fmtDur(t.Duration))
		if s.Paused {
			status = " [paused]"
		}
		if t.Duration > 0 {
			playPct = clampFraction(float64(s.Position) / float64(t.Duration))
			bufPct = clampFraction(float64(s.CacheTime) / float64(t.Duration))
		}
	}
	line := npStyle.Render(fmt.Sprintf("♪ %s — %s%s%s",
		t.Artist, t.Title, clock, status))
	bar := ""
	if t.Duration > 0 {
		bar = m.progressBar(playPct, bufPct)
	}
	return line + "\n" + bar
}

// progressBar renders the original bubbles position bar, then recolors
// the buffered-ahead empty cells (between the playhead and the cache
// frontier) to a faint accent — distinct from the played fill and the
// grey not-yet-buffered tail, while preserving the bar's original look.
func (m Model) progressBar(playPct, bufPct float64) string {
	if bufPct < playPct {
		bufPct = playPct
	}
	bar := m.progress.ViewAs(playPct)
	width := m.progress.Width()
	played := cellsFor(playPct, width)
	buffered := cellsFor(bufPct, width)
	if buffered <= played {
		return bar
	}
	prefix := ansi.Truncate(bar, played, "")
	tail := ansi.Cut(bar, buffered, width)
	mid := lipgloss.NewStyle().Foreground(theme.Accent).Faint(true).
		Render(strings.Repeat("░", buffered-played))
	return prefix + mid + tail
}

// cellsFor converts a 0..1 fraction to a cell count, rounding to the
// nearest cell and snapping to the full width once the fraction is
// within a cell of complete — otherwise integer truncation leaves a
// permanent grey sliver at the right edge because mpv's time/cache
// values approach but never exactly equal the duration.
func cellsFor(pct float64, width int) int {
	n := int(pct*float64(width) + 0.5)
	if n > width {
		n = width
	}
	if pct >= 1 || width-n <= 1 && pct > 0.98 {
		n = width
	}
	return n
}

func clampFraction(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

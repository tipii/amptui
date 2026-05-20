// Package textutil holds small, dependency-free string helpers shared
// across the TUI: width-safe sanitizing, rune-aware truncation,
// paragraph reflow, and human-readable byte/duration formatting. None
// of these touch the Bubble Tea Model or lipgloss styles, so they live
// outside internal/tui as plain utilities.
package textutil

import (
	"fmt"
	"strings"
	"time"
)

// SafeText strips codepoints whose terminal cell width lipgloss
// measures incorrectly — most often emoji and the heart-suit glyphs
// Plex puts in default playlist names ("❤️ Tracks"). Without this,
// lipgloss undercounts the visual width, the right border of a styled
// block lands one column off, and rows containing the affected text
// fall out of alignment.
//
// See:
//   - https://github.com/charmbracelet/lipgloss/issues/562
//   - https://github.com/charmbracelet/lipgloss/pull/563
//
// Once that PR merges this becomes unnecessary. Pure ASCII / Latin /
// CJK text is untouched.
func SafeText(s string) string {
	if s == "" {
		return s
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 0xFE00 && r <= 0xFE0F: // emoji / text variation selectors
			continue
		case r == 0x200D: // zero-width joiner (used in emoji sequences)
			continue
		case r >= 0x2660 && r <= 0x2667: // card suits ♠ ♡ ♢ ♣ ♤ ♥ ♦ ♧
			continue
		case r == 0x2764: // ❤ heavy black heart
			continue
		case r >= 0x1F300 && r <= 0x1FAFF: // misc symbols + pictographs (most emoji)
			continue
		}
		out = append(out, r)
	}
	return strings.TrimSpace(string(out))
}

// Truncate cuts s to at most n runes, appending an ellipsis when it
// had to drop content.
func Truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}

// PadRight right-pads s with spaces to at least n bytes wide. Intended
// for plain-ASCII labels where byte length equals display width.
func PadRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// ReflowParagraphs preserves paragraph structure while collapsing any
// in-paragraph whitespace. Plex bios mark paragraph breaks with a
// single \r\n (not \n\n) and use no in-paragraph soft wraps; we split
// on the normalized newline, reflow each paragraph's internal
// whitespace, and rejoin with a blank line so the consumer can show
// the paragraphs visually separated.
func ReflowParagraphs(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	paras := strings.Split(s, "\n")
	out := make([]string, 0, len(paras))
	for _, p := range paras {
		if cleaned := strings.Join(strings.Fields(p), " "); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return strings.Join(out, "\n\n")
}

// HumanDuration formats d as a coarse, single-unit "ago"-style string
// (45s, 12m, 3h, 2d).
func HumanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// HumanBytes formats a byte count as B / KB / MB / GB.
func HumanBytes(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(k*k*k))
	}
}

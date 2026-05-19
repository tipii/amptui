// Width-safe primitives that work around a known upstream bug in
// lipgloss where ansi.StringWidth undercounts Unicode codepoints
// (emoji, ambiguous-width glyphs like ♥ / ❤ / ♡), causing card
// borders to drift out of alignment when joined horizontally.
//
// See:
//   - https://github.com/charmbracelet/lipgloss/issues/562
//   - https://github.com/charmbracelet/lipgloss/pull/563
//
// Once that PR merges, delete this file and replace its call sites
// with lipgloss equivalents (lipgloss.JoinHorizontal, Width(N).Align,
// etc.). All the workarounds live here so the rollback is one diff.

package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// centerCells pads s with spaces so that runewidth.StringWidth(result)
// equals width — i.e. centered by terminal cells, not by rune count.
// If s is already at or beyond width, it is returned unchanged.
func centerCells(s string, width int) string {
	w := runewidth.StringWidth(s)
	if w >= width {
		return s
	}
	left := (width - w) / 2
	right := width - w - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

// truncateCells trims s by terminal-cell width (not rune count) and
// appends an ellipsis when it had to drop content.
func truncateCells(s string, max int) string {
	if runewidth.StringWidth(s) <= max {
		return s
	}
	return runewidth.Truncate(s, max, "…")
}

// joinCardsHorizontally concatenates pre-rendered multi-line blocks
// side-by-side, line-by-line, without measuring anything. Use this
// instead of lipgloss.JoinHorizontal when the blocks may contain
// glyphs that lipgloss measures differently from the terminal — its
// internal alignment would otherwise insert a phantom space between
// blocks. Each input block is expected to already have lines of
// matching visual width (renderDashCard guarantees this via
// centerCells / truncateCells).
func joinCardsHorizontally(cards []string) string {
	if len(cards) == 0 {
		return ""
	}
	split := make([][]string, len(cards))
	maxRows := 0
	for i, c := range cards {
		split[i] = strings.Split(c, "\n")
		if len(split[i]) > maxRows {
			maxRows = len(split[i])
		}
	}
	lines := make([]string, maxRows)
	for r := 0; r < maxRows; r++ {
		var b strings.Builder
		for _, rows := range split {
			if r < len(rows) {
				b.WriteString(rows[r])
			}
		}
		lines[r] = b.String()
	}
	return strings.Join(lines, "\n")
}

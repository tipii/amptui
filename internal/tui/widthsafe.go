// Display-time sanitizer that strips codepoints whose terminal cell
// width lipgloss measures incorrectly — most often emoji and the
// heart-suit glyphs Plex puts in default playlist names ("❤️ Tracks").
// Without this, lipgloss undercounts the visual width, the right
// border of a styled block lands one column off, and rows containing
// the affected text fall out of alignment with the rest of the screen.
//
// See:
//   - https://github.com/charmbracelet/lipgloss/issues/562
//   - https://github.com/charmbracelet/lipgloss/pull/563
//
// Once that PR merges, delete this file and remove the safeText() call
// sites — lipgloss will measure correctly on its own.

package tui

import "strings"

// safeText returns s with any codepoints known to confuse lipgloss's
// width measurement removed, then trimmed of resulting whitespace.
// Apply it to any user-facing string sourced from Plex before handing
// it to a styled block. Pure ASCII / Latin / CJK text is untouched.
func safeText(s string) string {
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

package tui

import (
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/tipii/amptui/internal/config"
)

// The help line never runs past the terminal's edge. bubbles' help kept
// the item that overflows when there was no room left for its "…", so
// at some widths the footer wrapped onto a second row.
func TestHelpLineFitsWidth(t *testing.T) {
	m := newArtistsModel(t, config.Config{})
	views := map[string]helpView{
		"browser":   m.keymap.browserHelp(),
		"dashboard": m.keymap.dashboardHelp(),
	}
	for name, h := range views {
		for w := 10; w <= 140; w++ {
			m.width = w
			m.helpModel.SetWidth(w)
			if got := lipgloss.Width(m.fitHelp(h)); got > w {
				t.Errorf("%s help at width %d is %d wide", name, w, got)
			}
		}
	}
}

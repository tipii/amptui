package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/tipii/amptui/internal/config"
)

// The help modal's body must fit the modal: it used to lay its columns
// out for the whole terminal and ran off the right edge at 80 columns.
func TestHelpBodyFitsWidth(t *testing.T) {
	m := newArtistsModel(t, config.Config{})
	for _, w := range []int{36, 52, 76, 120} {
		body := m.helpBodyContent(w)
		for i, line := range strings.Split(body, "\n") {
			if lw := lipgloss.Width(line); lw > w {
				t.Errorf("width %d: line %d is %d wide: %q", w, i, lw, ansi.Strip(line))
			}
		}
		for _, want := range []string{"shuffle", "repeat", "go to playing", "queue track"} {
			if !strings.Contains(ansi.Strip(body), want) {
				t.Errorf("width %d: %q missing from help", w, want)
			}
		}
	}
}

// The dashboard scrolls by whole sections and says what's cut off.
func TestScrollToSection(t *testing.T) {
	// Three 5-line sections separated by a blank line.
	var lines []string
	starts := []int{0, 6, 12}
	for s := 0; s < 3; s++ {
		for i := 0; i < 5; i++ {
			lines = append(lines, string(rune('A'+s))+string(rune('0'+i)))
		}
		if s < 2 {
			lines = append(lines, "")
		}
	}
	content := strings.Join(lines, "\n")

	if got := scrollToSection(content, 40, starts, 2, 16); got != content {
		t.Error("content that fits was changed")
	}
	view := strings.Split(scrollToSection(content, 12, starts, 2, 16), "\n")
	if view[0] != "B0" {
		t.Errorf("window starts at %q, want a section start (B0)", view[0])
	}
	if last := ansi.Strip(view[len(view)-1]); !strings.Contains(last, "↑ more above") {
		t.Errorf("hint = %q", last)
	}
	view = strings.Split(scrollToSection(content, 12, starts, 0, 4), "\n")
	if view[0] != "A0" || !strings.Contains(ansi.Strip(view[len(view)-1]), "↓ more below") {
		t.Errorf("top section: first %q, last %q", view[0], ansi.Strip(view[len(view)-1]))
	}
}

func TestGridPosition(t *testing.T) {
	for _, c := range []struct {
		from, to, total int
		want            string
	}{
		{0, 3, 3, ""},
		{0, 3, 287, "1–3 of 287 ↓"},
		{3, 6, 287, "4–6 of 287 ↑ ↓"},
		{285, 287, 287, "286–287 of 287 ↑"},
	} {
		if got := strings.TrimSpace(ansi.Strip(gridPosition(c.from, c.to, c.total))); got != c.want {
			t.Errorf("gridPosition(%d,%d,%d) = %q, want %q", c.from, c.to, c.total, got, c.want)
		}
	}
}

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	// Room for A and B whole: both show, C (which wouldn't fit) doesn't.
	view = strings.Split(scrollToSection(content, 13, starts, 0, 4), "\n")
	if !strings.Contains(strings.Join(view, "|"), "B4") || strings.Contains(strings.Join(view, "|"), "C0") {
		t.Errorf("A selected, room for A+B: %q", view)
	}
	// Room for section A and part of B: B is left out whole, not cut.
	view = strings.Split(scrollToSection(content, 9, starts, 0, 4), "\n")
	for _, l := range view {
		if strings.HasPrefix(l, "B") {
			t.Errorf("section B partly shown: %q", view)
			break
		}
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

// A grid level shows as a list while a row of cards can't fit, and the
// highlighted item survives the switch either way.
func TestGridFallsBackToListWhenTooShort(t *testing.T) {
	m := newArtistsModel(t, config.Config{DefaultViewArtist: "grid"})
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = upd.(Model)
	if !m.currentGridView() {
		t.Fatal("grid should show at 100×40")
	}
	m = press(press(m, keyRight), keyRight) // grid cursor → Cream

	upd, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	m = upd.(Model)
	if m.currentGridView() || !m.gridPreferred() {
		t.Fatalf("at 18 rows: shown=%v preferred=%v; want list shown, grid still preferred", m.currentGridView(), m.gridPreferred())
	}
	if ai, ok := m.list.SelectedItem().(artistItem); !ok || ai.artist.Title != "Cream" {
		t.Errorf("list selection after shrinking = %v, want Cream", m.list.SelectedItem())
	}
	if strings.Contains(m.View().Content, "╭") {
		t.Error("cards drawn while showing the list (they used to be cropped)")
	}

	sel := m.list.Index()
	upd, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = upd.(Model)
	if !m.currentGridView() || m.gridCursor != sel {
		t.Errorf("after growing: grid=%v cursor=%d, want grid at %d", m.currentGridView(), m.gridCursor, sel)
	}
}

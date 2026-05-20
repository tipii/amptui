package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/theopalhol/amptui/internal/library"
	"github.com/theopalhol/amptui/internal/textutil"
)

// modalSize returns the outer dimensions of a modal box — roughly 70% of
// the terminal in each axis, with a small floor so very small terminals
// still produce something usable.
func (m Model) modalSize() (w, h int) {
	w = m.width * 7 / 10
	if w < 20 {
		w = 20
	}
	h = m.height * 7 / 10
	if h < 8 {
		h = 8
	}
	return w, h
}

// modalFrame applies the rounded-border modal style with both width and
// height pinned to the current modalSize. The explicit Height keeps every
// modal at the same outer size regardless of how much content sits inside
// — without it, short content (e.g. "no matches" in the search modal)
// makes the box shrink, which feels jittery as the user types.
//
// In lipgloss v2 Style.Width(N) sets the OUTER width including border +
// padding, so Width(w) lands the modal at exactly modalSize. The callers
// that set sub-widget widths to mw-4 (queueList, helpViewport,
// infoViewport) then match the modal's content area (mw - border(2) -
// padding(2) = mw-4) exactly — without this, the modal re-wrapped each
// inner line, splitting paragraphs into orphan single-word rows.
func (m Model) modalFrame(content string) string {
	w, h := m.modalSize()
	return modalStyle.Width(w).Height(h).Render(content)
}

// queueModalBox renders the bordered queue box. Positioning is handled by
// the compositor in overlayBox.
func (m Model) queueModalBox() string {
	title := headerStyle.Render(fmt.Sprintf("Queue · %d track(s)", len(m.queue)))
	body := m.queueList.View()
	if len(m.queue) == 0 {
		body = helpStyle.Render("queue is empty — press q / Q to add tracks")
	}
	return m.modalFrame(title + "\n" + body)
}

// searchModalBox wraps the sub-model's body in the shared modal frame.
// The width / results-height arithmetic stays here because it depends on
// the parent's modalSize layout.
func (m Model) searchModalBox() string {
	w, mh := m.modalSize()
	// Inner height available for results: outer-h minus border(2), title
	// row(1), input row(1), spacer(1).
	resultsH := mh - 5
	body := m.search.View(w-4, resultsH, m.library, m.libraryErr, m.spinner)
	return m.modalFrame(body)
}

func formatSearchEntry(e library.Entry, maxWidth int) string {
	kind := helpStyle.Render(textutil.PadRight(e.Kind.String(), 6))
	var rest string
	switch e.Kind {
	case library.KindArtist:
		rest = e.Title
	case library.KindAlbum:
		rest = e.Title + helpStyle.Render(" · "+e.Artist)
	case library.KindTrack:
		rest = e.Title + helpStyle.Render(" · "+e.Album+" · "+e.Artist)
	}
	line := kind + " " + rest
	if lipgloss.Width(line) > maxWidth {
		// Truncation is approximate — lipgloss-aware truncation is
		// available via ansi, but a naive rune cut is good enough for the
		// modal's needs.
		runes := []rune(line)
		if len(runes) > maxWidth {
			line = string(runes[:maxWidth])
		}
	}
	return line
}

// helpModalBox renders the keybindings reference as a bordered modal box
// with a scrollable body (helpViewport).
func (m Model) helpModalBox() string {
	title := headerStyle.Render("Keybindings")
	return m.modalFrame(title + "\n" + m.helpViewport.View())
}

// helpBodyContent renders the help-modal body from KeyMap.helpModalSections,
// so it stays in sync with the actual bindings. A help.Model in ShowAll
// mode formats each section's binding rows; we prepend a faint title above.
func (m Model) helpBodyContent() string {
	h := m.helpModel
	h.ShowAll = true
	var b strings.Builder
	for i, s := range m.keymap.helpModalSections() {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(helpStyle.Render(s.title))
		b.WriteString("\n")
		b.WriteString(h.View(helpView{full: s.bindings}))
	}
	return b.String()
}

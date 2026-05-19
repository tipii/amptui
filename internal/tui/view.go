package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/theopalhol/amptui/internal/library"
)

func (m Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	if m.width == 0 {
		v.SetContent("loading…")
		return v
	}

	var background string
	switch m.screen {
	case screenSettings:
		background = m.settingsScreen()
	case screenDashboard:
		background = m.dashboardScreen()
	default:
		background = m.browserView()
	}
	switch {
	case m.showHelp:
		v.SetContent(m.overlayBox(background, m.helpModalBox()))
	case m.search.IsOpen():
		v.SetContent(m.overlayBox(background, m.searchModalBox()))
	case m.showQueue:
		v.SetContent(m.overlayBox(background, m.queueModalBox()))
	default:
		v.SetContent(background)
	}
	return v
}

// browserView renders the full screen: header, browser list, now-playing
// line, and footer. Modals are composited on top of this.
func (m Model) browserView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("amptui"))
	if crumbs := m.crumbLine(); crumbs != "" {
		b.WriteString("  " + crumbStyle.Render(crumbs))
	}
	b.WriteString("\n\n")

	if m.currentGridView() {
		b.WriteString(m.gridBodyView())
	} else {
		b.WriteString(m.list.View())
	}
	b.WriteString("\n")

	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")

	var footerLeft string
	switch {
	case m.loading:
		footerLeft = m.spinner.View() + "loading…"
	case m.err != nil:
		footerLeft = errStyle.Render("error: " + m.err.Error())
	default:
		// Auto-render from the active KeyMap context so the help line
		// stays in sync with bindings without hand-maintained strings.
		footerLeft = m.helpModel.View(m.currentHelp())
	}
	b.WriteString(m.footerLine(footerLeft))
	return b.String()
}

// dashboardScreen composes the dashboard sub-model's body with the
// shared chrome (header + now-playing line + footer). All three tiles
// render their loading / error / data states inside the sub-model;
// the parent just wraps it.
func (m Model) dashboardScreen() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("amptui"))
	b.WriteString("  " + crumbStyle.Render("Dashboard"))
	b.WriteString("\n\n")
	b.WriteString(m.dashboard.View(m.width, m.listHeight()-2, m.spinner))
	b.WriteString("\n")
	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")
	b.WriteString(m.footerLine(m.helpModel.View(m.currentHelp())))
	return b.String()
}

// settingsScreen composes the settings sub-model's body with the shared
// chrome (now-playing line + footer). The sub-model itself doesn't own
// those — they're parent-level concerns shared with the browser view.
func (m Model) settingsScreen() string {
	stats := cacheStatsBody(m.library, m.librarySyncing, m.libraryErr, m.spinner)
	body := m.settings.View(m.listHeight(), stats)

	var b strings.Builder
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")
	b.WriteString(m.footerLine(m.helpModel.View(m.currentHelp())))
	return b.String()
}

// footerLine assembles the bottom row, right-aligning a non-blocking
// syncing indicator when the background library loader is running.
func (m Model) footerLine(left string) string {
	right := ""
	switch {
	case m.librarySyncing:
		right = helpStyle.Render(m.spinner.View() + "syncing library")
	case m.libraryErr != nil:
		right = errStyle.Render("library error: " + m.libraryErr.Error())
	}
	if right == "" {
		return left
	}
	// Pad the left side to fill the row minus the right's width, so
	// right ends up flush against the terminal edge regardless of left's
	// length. Floors at width 1 if there's not enough room for both.
	padTo := m.width - lipgloss.Width(right)
	if padTo < lipgloss.Width(left)+1 {
		padTo = lipgloss.Width(left) + 1
	}
	leftPadded := lipgloss.NewStyle().Width(padTo).Render(left)
	return leftPadded + right
}

// overlayBox composites box, centered, on top of background. The background
// stays visible around (and behind, where unobscured) the modal.
func (m Model) overlayBox(background, box string) string {
	x := (m.width - lipgloss.Width(box)) / 2
	y := (m.height - lipgloss.Height(box)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	bg := lipgloss.NewLayer(background)
	fg := lipgloss.NewLayer(box).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(bg, fg).Render()
}

// nowPlayingLine renders the current track plus elapsed/total time.
func (m Model) nowPlayingLine() string {
	if m.nowPlaying == nil {
		return helpStyle.Render("— nothing playing —")
	}
	t := m.nowPlaying

	var status, clock string
	if m.player != nil {
		s := m.player.State()
		clock = fmt.Sprintf("  %s / %s", fmtDur(s.Position), fmtDur(t.Duration))
		if s.Paused {
			status = " [paused]"
		}
	}
	return npStyle.Render(fmt.Sprintf("♪ %s — %s%s%s",
		t.Artist, t.Title, clock, status))
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

func (m Model) crumbLine() string {
	if len(m.crumbs) == 0 {
		return ""
	}
	parts := make([]string, len(m.crumbs))
	for i, c := range m.crumbs {
		parts[i] = c.title
	}
	return strings.Join(parts, " / ") + " /"
}

// listHeight is the height in rows of the body region (browser/grid). The
// view above and below it consumes: header (1), blank spacer (1),
// now-playing line (1), and footer (1) — 4 rows total — so the body fills
// everything in between.
func (m Model) listHeight() int {
	h := m.height - 4
	if h < 1 {
		return 1
	}
	return h
}

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
	kind := helpStyle.Render(padRight(e.Kind.String(), 6))
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

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// helpModalBox renders the keybindings reference as a bordered modal box
// with a scrollable body (helpViewport).
func (m Model) helpModalBox() string {
	title := headerStyle.Render("Keybindings")
	return m.modalFrame(title + "\n" + m.helpViewport.View())
}

// modalFrame applies the rounded-border modal style with both width and
// height pinned to the current modalSize. The explicit Height keeps every
// modal at the same outer size regardless of how much content sits inside
// — without it, short content (e.g. "no matches" in the search modal)
// makes the box shrink, which feels jittery as the user types.
func (m Model) modalFrame(content string) string {
	w, h := m.modalSize()
	return modalStyle.Width(w - 2).Height(h - 2).Render(content)
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

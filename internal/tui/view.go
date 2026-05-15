package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	if m.width == 0 {
		v.SetContent("loading…")
		return v
	}

	background := m.browserView()
	switch {
	case m.showHelp:
		v.SetContent(m.overlayBox(background, m.helpModalBox()))
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

	b.WriteString(m.list.View())
	b.WriteString("\n")

	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")

	switch {
	case m.showHelp:
		b.WriteString(helpStyle.Render("? / esc close"))
	case m.showQueue:
		b.WriteString(helpStyle.Render(
			"j/k move · J/K reorder · d delete · enter play · o/esc close"))
	case m.loading:
		b.WriteString(m.spinner.View() + "loading…")
	case m.err != nil:
		b.WriteString(errStyle.Render("error: " + m.err.Error()))
	default:
		b.WriteString(helpStyle.Render(
			"? keys · enter open · esc back · space pause · n/p skip · o queue · ctrl+q quit"))
	}
	return b.String()
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

// listHeight reserves rows for the header (2), spacer (1), now-playing (1),
// and footer (1).
func (m Model) listHeight() int {
	h := m.height - 5
	if h < 1 {
		return 1
	}
	return h
}

// modalSize returns the outer width and height of a modal box, clamped to
// fit inside the content region.
func (m Model) modalSize() (w, h int) {
	w = m.width - 8
	if w > 60 {
		w = 60
	}
	if w < 16 {
		w = 16
	}
	h = m.listHeight() - 2
	if h < 5 {
		h = 5
	}
	return w, h
}

// queueModalBox renders the bordered queue box. Positioning is handled by
// the compositor in overlayBox.
func (m Model) queueModalBox() string {
	w, _ := m.modalSize()

	title := headerStyle.Render(fmt.Sprintf("Queue · %d track(s)", len(m.queue)))
	body := m.queueList.View()
	if len(m.queue) == 0 {
		body = helpStyle.Render("queue is empty — press q / Q to add tracks")
	}

	// Width(w-2): the style's width is the content box inside the border.
	return modalStyle.Width(w - 2).Render(title + "\n" + body)
}

// helpModalBox renders the keybindings reference as a bordered modal box.
func (m Model) helpModalBox() string {
	w, _ := m.modalSize()

	title := headerStyle.Render("Keybindings")
	lines := []string{
		helpStyle.Render("Browse"),
		"  enter / → / l    open · play track",
		"  esc / ← / h      go back",
		"  j / k / ↑ / ↓    move selection",
		"  /                filter list",
		"",
		helpStyle.Render("Playback"),
		"  space            pause / resume",
		"  n / p            next / previous in queue",
		"  , / .            seek −10s / +10s",
		"",
		helpStyle.Render("Queue"),
		"  q / Q            add track / album to queue",
		"  o                open / close queue modal",
		"  in modal: J/K reorder · d delete · enter play",
		"",
		helpStyle.Render("App"),
		"  ?                this help",
		"  ctrl+c / ctrl+q  quit",
	}
	body := strings.Join(lines, "\n")
	return modalStyle.Width(w - 2).Render(title + "\n\n" + body)
}

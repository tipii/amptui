package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// View is the Bubble Tea render entry point. It picks the active screen,
// then composites any open modal on top. Rendering is split across
// several files by concern:
//
//   - view.go        screen composition + shared layout (this file)
//   - nowplaying.go  the now-playing block and position bar
//   - info.go        artist/album metadata header + the `i` modal
//   - modals.go      the shared modal frame + queue/search/help bodies
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
	case m.showInfo:
		v.SetContent(m.overlayBox(background, m.infoModalBox()))
	case m.search.IsOpen():
		v.SetContent(m.overlayBox(background, m.searchModalBox()))
	case m.showQueue:
		v.SetContent(m.overlayBox(background, m.queueModalBox()))
	case m.showDownloads:
		v.SetContent(m.overlayBox(background, m.downloadsModalBox()))
	case m.showSort:
		v.SetContent(m.overlayBox(background, m.sortModalBox()))
	default:
		v.SetContent(background)
	}
	return v
}

// browserView renders the full screen: header, browser list, now-playing
// line, and footer. Modals are composited on top of this.
func (m Model) browserView() string {
	var b strings.Builder
	title := headerStyle.Render("amptui")
	if crumbs := m.crumbLine(); crumbs != "" {
		title += "  " + crumbStyle.Render(crumbs)
	}
	// Chrome layout:
	//   row 1:   title + breadcrumb (always full-width — image must not
	//            sit on the same row).
	//   row 2:   blank spacer.
	//   row 3+:  on artist / album screens with Images on, the hero
	//            thumb (N rows tall) docked to the left of the info
	//            summary. On other screens this collapses to the one-
	//            line summary directly below the spacer.
	b.WriteString(title)
	b.WriteString("\n")
	if thumb := m.headerThumb(); thumb != "" {
		b.WriteString("\n")
		// Right column gets whatever's left after the thumb + a 2-col
		// gutter, minus a small safety margin so wrapped lines don't
		// touch the terminal edge.
		rightWidth := m.width - headerThumbCellsW - 4
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, thumb, "  ", m.infoHeaderBlock(rightWidth)))
	} else {
		b.WriteString(m.infoHeaderLine())
	}
	b.WriteString("\n")

	if m.currentGridView() {
		b.WriteString(m.gridBodyView())
	} else {
		b.WriteString(m.list.View())
	}
	top := fitRows(b.String(), m.height-bottomChromeRows)
	b.Reset()
	b.WriteString(top)
	b.WriteString("\n")

	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")

	b.WriteString(m.footerLine(m.footerLeft()))
	return b.String()
}

// footerLeft is the footer's main text on every screen: loading, the
// last error, or the key help for the current context. Errors used to
// show only on the browser, so e.g. "playback unavailable" from a
// dashboard enter, or a track mpv couldn't play, vanished silently.
func (m Model) footerLeft() string {
	switch {
	case m.loading && m.screen == screenBrowser:
		return m.spinner.View() + "loading…"
	case m.err != nil:
		return errStyle.Render("error: " + m.err.Error())
	default:
		// Auto-render from the active KeyMap context so the help line
		// stays in sync with bindings without hand-maintained strings.
		return m.helpModel.View(m.currentHelp())
	}
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
	// Dashboard renders its title + spacer outside the sub-model body,
	// matching the browser layout, so the body itself gets exactly
	// listHeight rows.
	b.WriteString(fitRows(m.dashboard.View(m.width, m.bodyHeight(), m.spinner), m.bodyHeight()))
	b.WriteString("\n")
	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")
	b.WriteString(m.footerLine(m.footerLeft()))
	return b.String()
}

// settingsScreen composes the settings sub-model's body with the shared
// chrome (now-playing line + footer). The sub-model itself doesn't own
// those — they're parent-level concerns shared with the browser view.
func (m Model) settingsScreen() string {
	stats := cacheStatsBody(m.library, m.diskStats, m.librarySyncing, m.libraryErr, m.spinner, m.player != nil, m.playerErr)
	// Unlike the browser / dashboard, the settings sub-model renders
	// its own "amptui Settings /" header INSIDE the padded body. So
	// pass it the full screen height minus only the bottom chrome, not
	// bodyHeight (which assumes the title chrome lives outside the body).
	bodyHeight := max(m.height-bottomChromeRows, 1)
	body := fitRows(m.settings.View(bodyHeight, stats), bodyHeight)

	var b strings.Builder
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")
	b.WriteString(m.footerLine(m.footerLeft()))
	return b.String()
}

// footerGap is the minimum blank space kept between the help text and a
// right-aligned status when the row has to be trimmed to fit.
const footerGap = 2

// footerLine assembles the bottom row, right-aligning a non-blocking
// syncing indicator when the background library loader is running.
func (m Model) footerLine(left string) string {
	right := ""
	switch {
	case m.downloadStatusVisible():
		if m.downloadErr {
			right = errStyle.Render(m.downloadStatus)
		} else {
			right = helpStyle.Render(m.downloadStatus)
		}
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
	// length.
	//
	// When there isn't room for both, the help line is what gives way:
	// it's the static half of the row, while the right side is a live
	// download rate or a sync error. Note the trim has to happen before
	// the pad — a lipgloss Width narrower than its content *wraps*
	// rather than truncating, and the wrapped remainder would carry the
	// status onto a second line the footer never renders.
	padTo := m.width - lipgloss.Width(right)
	if padTo < 1 {
		return ansi.Truncate(right, m.width, "")
	}
	if lipgloss.Width(left) > padTo {
		// Cut short of the boundary so the trimmed help doesn't butt
		// straight up against the status text.
		left = ansi.Truncate(left, max(padTo-footerGap, 0), "")
	}
	return lipgloss.NewStyle().Width(padTo).Render(left) + right
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

func (m Model) crumbLine() string {
	parts := make([]string, 0, len(m.crumbs)+1)
	if m.serverName != "" {
		parts = append(parts, m.serverName)
	}
	for _, c := range m.crumbs {
		parts = append(parts, c.title)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " / ") + " /"
}

// bottomChromeRows is what every screen keeps below its body: the
// now-playing block (track line + progress bar) and the footer.
const bottomChromeRows = 3

// bodyHeight is the height in rows of a screen's body region: the
// terminal minus header (1), blank spacer (1), and the bottom chrome.
func (m Model) bodyHeight() int {
	return max(m.height-2-bottomChromeRows, 1)
}

// listHeight is the height in rows of the browser list / grid. On artist
// and album levels with a hero thumb, the chrome above it grows by the
// thumb block's height (title + spacer stay, the image sits below them).
//
// It deliberately doesn't look at m.screen: the list is also resized
// while the dashboard or settings is showing (window resize, toggling
// artwork), and sizing it for those screens left it a thumb-height too
// tall on return, pushing the now-playing block off the bottom.
func (m Model) listHeight() int {
	h := m.bodyHeight()
	if m.headerThumb() != "" {
		h -= headerThumbCellsH
	}
	return max(h, 1)
}

// fitRows pads or cuts s to exactly h lines. Each screen passes the part
// above the now-playing block through it, so a body that outgrows its
// region can never push playback status and the footer off-screen.
func fitRows(s string, h int) string {
	return lipgloss.NewStyle().Height(h).MaxHeight(h).Render(s)
}

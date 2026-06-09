package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	// Dashboard renders its title + spacer outside the sub-model body,
	// matching the browser layout, so the body itself gets exactly
	// listHeight rows.
	b.WriteString(m.dashboard.View(m.width, m.listHeight(), m.spinner))
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
	stats := cacheStatsBody(m.library, m.librarySyncing, m.libraryErr, m.spinner, m.player != nil, m.playerErr)
	// Unlike the browser / dashboard, the settings sub-model renders
	// its own "amptui Settings /" header INSIDE the padded body. So
	// pass it the full screen height minus only the now-playing line
	// + footer (3 rows), not listHeight (which assumes the title
	// chrome lives outside the body).
	bodyHeight := m.height - 3
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	body := m.settings.View(bodyHeight, stats)

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
	case m.downloadStatus != "":
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

// listHeight is the height in rows of the body region (browser/grid).
// Baseline chrome above and below it: header (1), blank spacer (1),
// now-playing block (2: track line + progress bar), and footer (1) —
// 5 rows total. On browser screens that show a hero thumb (artist /
// album), the chrome grows by exactly the thumb-block's height; the
// title + spacer rows are preserved, the image block is rendered
// underneath them.
func (m Model) listHeight() int {
	h := m.height - 5
	if m.screen == screenBrowser && m.headerThumb() != "" {
		h -= headerThumbCellsH
	}
	if h < 1 {
		return 1
	}
	return h
}

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/NimbleMarkets/ntcharts/v2/picture"

	"github.com/theopalhol/amptui/internal/library"
	"github.com/theopalhol/amptui/internal/plex"
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
	// Two-row chrome: title + info-summary line. When inline artwork
	// is on and we have a thumb for the current screen, render it as
	// a sidecar block sharing those two rows.
	chrome := title + "\n" + m.infoHeaderLine()
	if thumb := m.headerThumb(); thumb != "" {
		chrome = lipgloss.JoinHorizontal(lipgloss.Top, thumb, "  ", chrome)
	}
	b.WriteString(chrome)
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

// nowPlayingLine renders a two-row block: the current track + elapsed
// time on row 1, and a track-position bar on row 2 (blank when nothing
// is playing). Two rows are always returned so the surrounding layout
// stays stable across track changes.
func (m Model) nowPlayingLine() string {
	if m.nowPlaying == nil {
		return helpStyle.Render("— nothing playing —") + "\n"
	}
	t := m.nowPlaying

	var status, clock string
	var pct float64
	if m.player != nil {
		s := m.player.State()
		clock = fmt.Sprintf("  %s / %s", fmtDur(s.Position), fmtDur(t.Duration))
		if s.Paused {
			status = " [paused]"
		}
		if t.Duration > 0 {
			pct = float64(s.Position) / float64(t.Duration)
			if pct < 0 {
				pct = 0
			} else if pct > 1 {
				pct = 1
			}
		}
	}
	line := npStyle.Render(fmt.Sprintf("♪ %s — %s%s%s",
		t.Artist, t.Title, clock, status))
	bar := ""
	if t.Duration > 0 {
		bar = m.progress.ViewAs(pct)
	}
	return line + "\n" + bar
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// headerThumbCellsW / H is the cell footprint of the small thumbnail
// shown next to the breadcrumbs on artist / album screens — sized to
// the two-row chrome height.
const (
	headerThumbCellsW = 4
	headerThumbCellsH = 2
)

// headerThumb returns the rendered small thumbnail to dock next to
// the title row, or "" when artwork is off / unavailable / not on
// a screen that has one.
func (m Model) headerThumb() string {
	if !m.cfg.Images {
		return ""
	}
	var p *picture.Model
	switch m.level {
	case levelAlbums:
		p = &m.artistHeaderPic
	case levelTracks:
		p = &m.albumHeaderPic
	}
	if p == nil {
		return ""
	}
	return p.View().Content
}

// infoHeaderLine renders the one-line summary shown under the
// breadcrumbs on screens that have rich metadata (artist's albums or
// album's tracks). Returns the empty string on other levels so the
// chrome height stays constant.
func (m Model) infoHeaderLine() string {
	switch m.level {
	case levelAlbums:
		if m.metaLoading && m.artistMeta == nil {
			return helpStyle.Render("  " + m.spinner.View() + "loading artist info…")
		}
		if a := m.artistMeta; a != nil {
			return "  " + helpStyle.Render(artistHeaderSummary(a))
		}
	case levelTracks:
		if m.metaLoading && m.albumMeta == nil {
			return helpStyle.Render("  " + m.spinner.View() + "loading album info…")
		}
		if a := m.albumMeta; a != nil {
			return "  " + helpStyle.Render(albumHeaderSummary(a))
		}
	}
	return ""
}

func artistHeaderSummary(a *plex.ArtistMetadata) string {
	parts := make([]string, 0, 3)
	if len(a.Countries) > 0 {
		parts = append(parts, a.Countries[0])
	}
	tags := append([]string{}, a.Genres...)
	tags = append(tags, a.Moods...)
	if n := 3; len(tags) > n {
		tags = tags[:n]
	}
	if len(tags) > 0 {
		parts = append(parts, strings.Join(tags, ", "))
	}
	return strings.Join(parts, " · ")
}

func albumHeaderSummary(a *plex.AlbumMetadata) string {
	parts := make([]string, 0, 4)
	if a.Artist != "" {
		parts = append(parts, a.Artist)
	}
	if a.Year > 0 {
		parts = append(parts, fmt.Sprintf("%d", a.Year))
	}
	if a.Studio != "" {
		parts = append(parts, a.Studio)
	}
	tags := append([]string{}, a.Genres...)
	tags = append(tags, a.Moods...)
	if n := 2; len(tags) > n {
		tags = tags[:n]
	}
	if len(tags) > 0 {
		parts = append(parts, strings.Join(tags, ", "))
	}
	return strings.Join(parts, " · ")
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

// listHeight is the height in rows of the body region (browser/grid).
// The view above and below it consumes: header (1), blank spacer (1),
// now-playing block (2: track line + progress bar), and footer (1) —
// 5 rows total. We always reserve the bar row so the body doesn't
// resize between songs that do/don't have a duration.
func (m Model) listHeight() int {
	h := m.height - 5
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

// infoModalBox wraps the per-level metadata body (set via SetContent
// when the modal opens) in the shared modal frame.
func (m Model) infoModalBox() string {
	var heading string
	switch m.level {
	case levelAlbums:
		if a := m.artistMeta; a != nil {
			heading = headerStyle.Render(a.Title)
		} else {
			heading = headerStyle.Render("Artist")
		}
	case levelTracks:
		if a := m.albumMeta; a != nil {
			heading = headerStyle.Render(a.Title)
			if a.Artist != "" {
				heading += helpStyle.Render("  " + a.Artist)
			}
		} else {
			heading = headerStyle.Render("Album")
		}
	default:
		heading = headerStyle.Render("Info")
	}
	return m.modalFrame(heading + "\n" + m.infoViewport.View())
}

// infoModalContent assembles the modal body for whichever level the
// user is on. Returns "" if there's nothing to show — caller uses that
// as a "don't open the modal" signal. When artwork is on and we have
// the bytes, the rendered image is prepended so the bio appears below
// the thumbnail.
func (m Model) infoModalContent() string {
	var (
		meta string
		pic  *picture.Model
	)
	switch m.level {
	case levelAlbums:
		if a := m.artistMeta; a != nil {
			meta = formatArtistInfo(a)
			pic = &m.artistModalPic
		}
	case levelTracks:
		if a := m.albumMeta; a != nil {
			meta = formatAlbumInfo(a)
			pic = &m.albumModalPic
		}
	}
	if meta == "" {
		return ""
	}
	if m.cfg.Images && pic != nil {
		if img := pic.View().Content; img != "" {
			return img + "\n\n" + meta
		}
	}
	return meta
}

// modalThumb* are the cell footprint of the artwork shown above the
// bio in the info modal. Cells are ~2:1 tall:wide so we double the
// width relative to height to land near square.
const (
	modalThumbCellsW = 24
	modalThumbCellsH = 12
)

func formatArtistInfo(a *plex.ArtistMetadata) string {
	var b strings.Builder
	if a.Summary != "" {
		b.WriteString(reflowParagraphs(a.Summary))
		b.WriteString("\n\n")
	}
	writeTags(&b, "Genres", a.Genres)
	writeTags(&b, "Moods", a.Moods)
	writeTags(&b, "Styles", a.Styles)
	writeTags(&b, "Country", a.Countries)
	writeTags(&b, "Similar", a.Similar)
	return strings.TrimRight(b.String(), "\n")
}

func formatAlbumInfo(a *plex.AlbumMetadata) string {
	var b strings.Builder
	if a.Summary != "" {
		b.WriteString(reflowParagraphs(a.Summary))
		b.WriteString("\n\n")
	}
	if a.Year > 0 {
		b.WriteString(helpStyle.Render("Year: "))
		b.WriteString(fmt.Sprintf("%d\n", a.Year))
	}
	if a.Studio != "" {
		b.WriteString(helpStyle.Render("Studio: "))
		b.WriteString(a.Studio + "\n")
	}
	if a.Artist != "" {
		b.WriteString(helpStyle.Render("Artist: "))
		b.WriteString(a.Artist + "\n")
	}
	writeTags(&b, "Genres", a.Genres)
	writeTags(&b, "Moods", a.Moods)
	writeTags(&b, "Styles", a.Styles)
	return strings.TrimRight(b.String(), "\n")
}

// reflowParagraphs preserves paragraph structure while collapsing any
// in-paragraph whitespace. Plex bios mark paragraph breaks with a
// single \r\n (not \n\n) and use no in-paragraph soft wraps; we split
// on the normalized newline, reflow each paragraph's internal
// whitespace, and rejoin with a blank line so the modal shows the
// paragraphs visually separated.
func reflowParagraphs(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	paras := strings.Split(s, "\n")
	out := make([]string, 0, len(paras))
	for _, p := range paras {
		if cleaned := strings.Join(strings.Fields(p), " "); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return strings.Join(out, "\n\n")
}

func writeTags(b *strings.Builder, label string, tags []string) {
	if len(tags) == 0 {
		return
	}
	b.WriteString(helpStyle.Render(label + ": "))
	b.WriteString(strings.Join(tags, ", "))
	b.WriteString("\n")
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

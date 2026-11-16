package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/textutil"
)

// dashIndentW is the dashboard's left gutter: headings start at column
// 0 with their focus marker, what's under them at this column.
const dashIndentW = 2

// dashBandGap separates the band's now-playing and stats columns.
const dashBandGap = 4

// dashboardBody renders the dashboard into width × height: the band and
// the rows one under another, scrolled by whole sections so the focused
// one is in view.
func (m Model) dashboardBody(width, height int) string {
	d := m.dashboard
	var b strings.Builder
	var starts []int
	active, activeBottom := 0, 0
	for i, sec := range d.sections() {
		if i > 0 {
			b.WriteString("\n\n")
		}
		starts = append(starts, strings.Count(b.String(), "\n"))
		focused := sec == d.focus
		if sec == 0 {
			b.WriteString(m.dashBand(width, focused))
		} else {
			b.WriteString(m.dashRowBlock(dashRow(sec-1), width, focused))
		}
		if focused {
			active, activeBottom = i, strings.Count(b.String(), "\n")
		}
	}
	return scrollToSection(b.String(), height, starts, active, activeBottom)
}

// scrollToSection fits content into height rows when it's taller: the
// window starts at a section's first line (never mid-card), as high as
// it can while still showing the selected section through its last
// line, bottom. Below that it fills the height, the last section cut off
// where the space ends, as the library grid does, rather than leaving
// blank rows; the last row says what's out of view.
func scrollToSection(content string, height int, starts []int, active, bottom int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= height || height < 2 {
		return content
	}
	avail := height - 1 // the hint line
	offset := starts[active]
	for _, st := range starts[:active+1] {
		if bottom-st < avail {
			offset = st
			break
		}
	}
	end := min(offset+avail, len(lines))
	view := append([]string(nil), lines[offset:end]...)
	above, below := offset > 0, end < len(lines)
	hint := "↓ more below"
	switch {
	case above && below:
		hint = "↑↓ more"
	case above:
		hint = "↑ more above"
	}
	return strings.Join(append(view, helpStyle.Render("  "+hint)), "\n")
}

// dashHeading is a section's heading: the focus marker, the title and a
// dim note.
func dashHeading(title, note string, focused bool) string {
	marker := "  "
	if focused {
		marker = npStyle.Render("▶ ")
	}
	h := marker + sectionStyle.Render(title)
	if note != "" {
		h += helpStyle.Render(" · " + note)
	}
	return h
}

// dashBand is the top of the dashboard: what's playing (cover, track,
// what's next) beside the listening heatmap and the library's numbers.
// Narrow terminals stack the two.
func (m Model) dashBand(width int, focused bool) string {
	inner := max(width-dashIndentW, 1)
	coverW := 0
	if m.cfg.Images && m.height >= heroMinHeight && m.nowPlaying != nil {
		coverW = gridThumbCellsW + 2
	}
	// The text column takes what the heatmap leaves of the width, up to
	// 40 columns; past that, the heatmap goes back further.
	textW := min(max(inner-coverW-dashBandGap-heatmapWidth(26), 24), 40)
	statsW := inner - coverW - textW - dashBandGap
	stacked := statsW < heatmapWidth(8)
	if stacked {
		textW, statsW = max(inner-coverW, 1), inner
	}

	rows := gridThumbCellsH
	if coverW == 0 {
		rows = 8
	}
	now := m.nowPlayingPanel(textW, rows)
	if coverW > 0 {
		now = lipgloss.JoinHorizontal(lipgloss.Top, m.nowPlayingCover(), "  ", now)
	}
	statsTitle, statsBody := m.dashStats(statsW)

	nowHead := dashHeading("Now playing", "", focused)
	if stacked {
		return nowHead + "\n" + dashIndent(now) + "\n\n" +
			"  " + statsTitle + "\n" + dashIndent(statsBody)
	}
	leftW := dashIndentW + coverW + textW + dashBandGap
	head := nowHead + strings.Repeat(" ", max(leftW-lipgloss.Width(nowHead), 1)) + statsTitle
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(coverW+textW+dashBandGap).Render(now), statsBody)
	return head + "\n" + dashIndent(body)
}

// nowPlayingCover is the playing album's artwork, or a placeholder in
// its footprint.
func (m Model) nowPlayingCover() string {
	if t := m.nowPlaying; t != nil {
		if pic := m.gridPics[t.AlbumRatingKey]; pic != nil {
			if thumb := m.thumbText(t.AlbumRatingKey, pic, gridThumbCellsW, gridThumbCellsH); thumb != "" {
				return thumb
			}
		}
	}
	return artPlaceholder(gridThumbCellsW, gridThumbCellsH)
}

// nowPlayingPanel is the band's text about the playing track — title,
// artist, album, whether it's playing and where, then what's next in
// the queue — in at most rows lines of width w.
func (m Model) nowPlayingPanel(w, rows int) string {
	t := m.nowPlaying
	if t == nil {
		return strings.Join([]string{
			helpStyle.Render("Nothing playing"),
			"",
			helpStyle.Render(textutil.Truncate("enter opens an album,", w)),
			helpStyle.Render(textutil.Truncate("q adds it to the queue", w)),
		}, "\n")
	}
	album := t.Album
	if t.Year > 0 {
		album = fmt.Sprintf("%s · %d", t.Album, t.Year)
	}
	state := npStyle.Render("▶ playing")
	clock := fmtDur(t.Duration)
	if m.player != nil {
		s := m.player.State()
		if s.Paused {
			state = helpStyle.Render("⏸ paused")
		}
		clock = fmtDur(s.Position) + " / " + fmtDur(t.Duration)
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(textutil.Truncate(t.Title, w)),
		textutil.Truncate(t.Artist, w),
		helpStyle.Render(textutil.Truncate(album, w)),
		"",
		state + "  " + clock,
	}
	if next := m.upNext(rows - len(lines) - 2); len(next) > 0 {
		lines = append(lines, "", helpStyle.Render("Up next"))
		for _, n := range next {
			line := textutil.Truncate(n.Title, w)
			if room := w - lipgloss.Width(line) - 3; room > 0 && n.Artist != "" {
				line += helpStyle.Render(" · " + textutil.Truncate(n.Artist, room))
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines[:min(len(lines), rows)], "\n")
}

// upNext is up to n tracks queued after the playing one.
func (m Model) upNext(n int) []media.Track {
	if n <= 0 || m.queueIdx+1 >= len(m.queue) {
		return nil
	}
	rest := m.queue[m.queueIdx+1:]
	return rest[:min(n, len(rest))]
}

// dashStats is the band's stats column: its title, and the heatmap (if
// the server keeps a history) over the library's numbers.
func (m Model) dashStats(w int) (title, body string) {
	d := m.dashboard
	var lines []string
	if d.noHistory {
		title = sectionStyle.Render("Library")
	} else {
		weeks := min(dashHeatWeeks, (w+1)/2-2)
		title = sectionStyle.Render("Listening") + helpStyle.Render(fmt.Sprintf(" · last %d weeks", weeks))
		switch {
		case !d.historyLoaded:
			lines = append(lines, helpStyle.Render(m.spinner.View()+"loading…"))
		case d.historyErr != nil:
			lines = append(lines, errStyle.Render(textutil.Truncate("error: "+d.historyErr.Error(), w)))
		default:
			lines = append(lines, heatmapLines(d.history, d.now(), weeks)...)
		}
		lines = append(lines, "")
	}
	switch {
	case m.library != nil:
		for _, l := range packItems(librarySummary(m.library, d.unplayedTotal), w) {
			lines = append(lines, helpStyle.Render(l))
		}
	case m.librarySyncing:
		lines = append(lines, helpStyle.Render(m.spinner.View()+"loading the library…"))
	}
	return title, strings.Join(lines, "\n")
}

// librarySummary is the library's numbers as short phrases: artists,
// albums, tracks, how long it all plays, and (once known) how many
// albums were never played.
func librarySummary(l *library.Library, unplayed int) []string {
	tracks := 0
	for _, n := range l.AlbumTrackCount {
		tracks += n
	}
	var length time.Duration
	for _, d := range l.AlbumDuration {
		length += d
	}
	items := []string{
		countPhrase(len(l.ArtistAlbumCount), "artist"),
		countPhrase(len(l.AlbumTrackCount), "album"),
		countPhrase(tracks, "track"),
	}
	switch {
	case length >= 48*time.Hour:
		items = append(items, fmt.Sprintf("%d days of music", int(length.Round(24*time.Hour)/(24*time.Hour))))
	case length >= time.Hour:
		items = append(items, fmt.Sprintf("%d hours of music", int(length.Round(time.Hour)/time.Hour)))
	}
	if unplayed >= 0 {
		// Capped: Jellyfin counts album folders without tracks, which
		// the library (built from tracks) doesn't have.
		items = append(items, groupDigits(min(unplayed, len(l.AlbumTrackCount)))+" never played")
	}
	return items
}

// countPhrase is "1 album" / "1,111 albums".
func countPhrase(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return groupDigits(n) + " " + noun + "s"
}

// groupDigits writes n with thousands separators: 11204 → "11,204".
func groupDigits(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return "-" + groupDigits(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// packItems joins phrases with " · " into lines at most w wide, never
// splitting one.
func packItems(items []string, w int) []string {
	var lines []string
	cur := ""
	for _, it := range items {
		switch {
		case cur == "":
			cur = it
		case lipgloss.Width(cur+" · "+it) <= w:
			cur += " · " + it
		default:
			lines = append(lines, cur)
			cur = it
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// heatCell is one day's square in each shade, none to most plays.
var heatCells = func() (cells [len(heatShades)]string) {
	for i, c := range heatShades {
		cells[i] = lipgloss.NewStyle().Foreground(c).Render("■")
	}
	return cells
}()

// heatmapWidth is the heatmap's width for a number of weeks: the day
// labels, then a square and a gap per week.
func heatmapWidth(weeks int) int { return 4 + 2*weeks - 1 }

// heatmapLines draws plays per day over the last weeks weeks, like a
// contributions graph: a column per week (the last one this week, up to
// today), a row per weekday from Monday, month names on top and a
// legend under (if it fits). A day's shade is its plays' quartile among the days
// with any, so one marathon doesn't wash the rest out.
func heatmapLines(plays []media.Play, now time.Time, weeks int) []string {
	loc := now.Location()
	dayKey := func(t time.Time) int {
		y, mo, d := t.In(loc).Date()
		return y*10000 + int(mo)*100 + d
	}
	y, mo, dd := now.In(loc).Date()
	today := time.Date(y, mo, dd, 0, 0, 0, 0, loc)
	first := today.AddDate(0, 0, -(int(today.Weekday())+6)%7-7*(weeks-1)) // a Monday
	firstKey, todayKey := dayKey(first), dayKey(today)
	counts := map[int]int{}
	for _, p := range plays {
		if k := dayKey(p.At); k >= firstKey && k <= todayKey {
			counts[k]++
		}
	}
	level := heatLeveler(counts)

	labels := [7]string{"Mon", "", "Wed", "", "Fri", "", ""}
	lines := []string{monthLabels(first, weeks)}
	for r := range 7 {
		var b strings.Builder
		b.WriteString(helpStyle.Render(fmt.Sprintf("%-3s", labels[r])))
		for c := range weeks {
			b.WriteByte(' ')
			day := first.AddDate(0, 0, 7*c+r)
			if day.After(today) {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(heatCells[level(counts[dayKey(day)])])
		}
		lines = append(lines, b.String())
	}
	// The legend, when the heatmap's wide enough for it.
	legend := "    " + helpStyle.Render("less ") + strings.Join(heatCells[:], " ") + helpStyle.Render(" more")
	if lipgloss.Width(legend) <= heatmapWidth(weeks) {
		lines = append(lines, legend)
	}
	return lines
}

// monthLabels is the heatmap's top line: each month's name over the
// first week that starts in it.
func monthLabels(first time.Time, weeks int) string {
	line := []rune(strings.Repeat(" ", heatmapWidth(weeks)))
	free := 0
	for c := range weeks {
		mon := first.AddDate(0, 0, 7*c)
		switch {
		case c > 0 && mon.Month() == mon.AddDate(0, 0, -7).Month():
			continue // not a new month
		case c == 0 && mon.AddDate(0, 0, 14).Month() != mon.Month():
			continue // the next month's name would crowd it out
		}
		pos := 4 + 2*c
		if pos < free || pos+3 > len(line) {
			continue
		}
		copy(line[pos:], []rune(mon.Format("Jan")))
		free = pos + 4
	}
	return helpStyle.Render(strings.TrimRight(string(line), " "))
}

// heatLeveler maps a day's plays to a shade: 0 for none, then 1–4 by
// quartile of the days that have plays.
func heatLeveler(counts map[int]int) func(int) int {
	var nz []int
	for _, n := range counts {
		if n > 0 {
			nz = append(nz, n)
		}
	}
	slices.Sort(nz)
	q := func(k int) int {
		if len(nz) == 0 {
			return 0
		}
		return nz[(len(nz)-1)*k/4]
	}
	q1, q2, q3 := q(1), q(2), q(3)
	return func(n int) int {
		switch {
		case n <= 0:
			return 0
		case n <= q1:
			return 1
		case n <= q2:
			return 2
		case n <= q3:
			return 3
		}
		return 4
	}
}

// dashRowBlock is one row: its heading (with where the row is scrolled
// to, when it doesn't all fit) over its cards.
func (m Model) dashRowBlock(r dashRow, width int, focused bool) string {
	d := m.dashboard
	s := d.rows[r]
	title, note := d.rowTitle(r)
	head := dashHeading(title, note, focused)
	switch {
	case s.err != nil:
		return head + "\n" + dashIndent(errStyle.Render(textutil.Truncate("error: "+s.err.Error(), width-dashIndentW)))
	case !s.loaded:
		return head + "\n" + dashIndent(helpStyle.Render(m.spinner.View()+"loading…"))
	}
	cols, outerW := gridLayout(width - dashIndentW)
	end := min(s.offset+cols, len(s.cards))
	if pos := dashPosition(s.offset, end, len(s.cards)); pos != "" {
		head += "  " + pos
	}
	var cells []string
	for i := s.offset; i < end; i++ {
		c := s.cards[i]
		title := c.title
		if !c.playlist && c.key != "" && m.playing != nil && c.key == m.playing.album {
			title = "♪ " + title
		}
		if i > s.offset {
			cells = append(cells, strings.Repeat(" ", cardGutter))
		}
		cells = append(cells, m.dashCardView(c.art, title, c.sub, focused && i == s.cursor, outerW))
	}
	return head + "\n" + dashIndent(lipgloss.JoinHorizontal(lipgloss.Top, cells...))
}

// dashCardView is a card: the library grid's, or without artwork a
// short one, since a grid-height card with two lines in it is mostly
// empty.
func (m Model) dashCardView(art, title, sub string, selected bool, outerW int) string {
	if m.cfg.Images {
		return m.artCard(art, title, sub, selected, outerW)
	}
	innerW := outerW - cardBorderCols
	style := cardStyle
	if selected {
		style = cardCursorStyle
	}
	return style.Width(outerW).Height(4).Render(
		textutil.Truncate(title, innerW) + "\n" + helpStyle.Render(textutil.Truncate(sub, innerW)))
}

// dashPosition is "1–4 of 24 →" for a row that doesn't fit, with arrows
// toward the cards out of view; "" when it all fits.
func dashPosition(from, to, total int) string {
	if from == 0 && to >= total {
		return ""
	}
	s := fmt.Sprintf("%d–%d of %d", from+1, to, total)
	if from > 0 {
		s += " ←"
	}
	if to < total {
		s += " →"
	}
	return helpStyle.Render(s)
}

// dashIndent pads every line of s by the dashboard's gutter.
func dashIndent(s string) string {
	return lipgloss.NewStyle().PaddingLeft(dashIndentW).Render(s)
}

// dashArtKeys are the artwork the dashboard shows: what's playing, and
// each row's cards in view plus ahead more, so scrolling a row finds
// the next card's ready.
func (m Model) dashArtKeys(ahead int) []string {
	var keys []string
	if t := m.nowPlaying; t != nil && t.AlbumRatingKey != "" {
		keys = append(keys, t.AlbumRatingKey)
	}
	d := m.dashboard
	for _, s := range d.rows {
		end := min(s.offset+d.cols+ahead, len(s.cards))
		for _, c := range s.cards[min(s.offset, end):end] {
			if c.art != "" {
				keys = append(keys, c.art)
			}
		}
	}
	return keys
}

// dashArtFetches requests the dashboard's artwork not loaded or asked
// for yet.
func (m Model) dashArtFetches() tea.Cmd {
	if !m.cfg.Images || m.client == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, key := range m.dashArtKeys(1) {
		if _, ok := m.gridPics[key]; ok || m.artRequested[key] {
			continue
		}
		m.artRequested[key] = true
		cmds = append(cmds, fetchArtwork(m.client, key, "grid:"+key))
	}
	return tea.Batch(cmds...)
}

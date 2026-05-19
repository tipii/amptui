package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Card geometry. lipgloss v2's Style.Width / Style.Height set the OUTER
// dimensions (border-inclusive), so these values are the total cell size.
// No gap between cards — adjacent borders touch and form a divider, which
// lets the row fill the terminal exactly.
// Card geometry chosen so that a square thumbnail rendered with
// half-block ANSI (2 image rows per cell) fills the inner area
// exactly. With cellW = 2 × cellH visually, a card with inner cells
// W×H = 2H × H displays a square image that takes the full inner
// area minus one row reserved for the title. outerW = inner+2, outerH
// = inner+2+1 (title).
const (
	cardOuterH      = 13 // 10 thumb rows + 1 title + 2 border
	cardIdealOuterW = 22 // 20 thumb cols + 2 border (20 cols × 20 image rows = square)
	cardMinOuterW   = 18
	cardBorderCols  = 2
)

// gridLayout picks the column count and a per-card OUTER width so that
// cols * outerW == terminal width exactly. All cards in a row are the
// same size; adjacent cards share visual borders.
func gridLayout(width int) (cols, outerW int) {
	cols = width / cardIdealOuterW
	if cols < 1 {
		cols = 1
	}
	outerW = width / cols
	if outerW < cardMinOuterW {
		cols = width / cardMinOuterW
		if cols < 1 {
			cols = 1
		}
		outerW = width / cols
		if outerW < cardMinOuterW {
			outerW = cardMinOuterW
		}
	}
	return
}

// gridCols returns just the column count for cursor-movement math.
func gridCols(width int) int {
	cols, _ := gridLayout(width)
	return cols
}

// supportsGrid reports whether the current browser level can render as a
// grid. Currently artists and albums; tracks and the library picker stay
// as lists.
func (m Model) supportsGrid() bool {
	return m.level == levelArtists || m.level == levelAlbums
}

// currentGridView reports whether the current level is presently in grid
// mode (each level has its own flag, primed from config at startup).
func (m Model) currentGridView() bool {
	switch m.level {
	case levelArtists:
		return m.gridArtists
	case levelAlbums:
		return m.gridAlbums
	}
	return false
}

// toggleGrid flips the current level's mode and keeps the same item
// highlighted across the switch.
func (m *Model) toggleGrid() {
	if !m.supportsGrid() {
		return
	}
	on := m.currentGridView()
	if on {
		// grid -> list
		m.list.Select(m.gridCursor)
	} else {
		// list -> grid
		m.gridCursor = m.list.Index()
	}
	m.setCurrentGridView(!on)
	if !on {
		m.ensureCursorVisible()
	}
}

func (m *Model) setCurrentGridView(v bool) {
	switch m.level {
	case levelArtists:
		m.gridArtists = v
	case levelAlbums:
		m.gridAlbums = v
	}
}

// moveGridCursor shifts the gridCursor by (drow, dcol), clamped to the
// items count. dcol moves within a row; drow moves by `cols` items. The
// viewport (gridScrollTop) only moves when the cursor would otherwise
// leave the visible area — so j/k inside the viewport never jumps the
// view, but pushing past the edges scrolls one row at a time.
func (m *Model) moveGridCursor(drow, dcol int) {
	items := m.list.Items()
	if len(items) == 0 {
		return
	}
	cols := gridCols(m.width)
	c := m.gridCursor + dcol + drow*cols
	if c < 0 {
		c = 0
	}
	if c >= len(items) {
		c = len(items) - 1
	}
	m.gridCursor = c
	m.ensureCursorVisible()
}

// gridVisibleRows is how many card rows fit in the body region.
func (m Model) gridVisibleRows() int {
	availRows := m.listHeight() - 1
	if availRows < cardOuterH {
		availRows = cardOuterH
	}
	n := availRows / cardOuterH
	if n < 1 {
		n = 1
	}
	return n
}

// ensureCursorVisible adjusts gridScrollTop the minimum amount required to
// keep gridCursor inside the visible window.
func (m *Model) ensureCursorVisible() {
	cols := gridCols(m.width)
	if cols < 1 {
		return
	}
	visible := m.gridVisibleRows()
	cursorRow := m.gridCursor / cols
	if cursorRow < m.gridScrollTop {
		m.gridScrollTop = cursorRow
		return
	}
	if cursorRow >= m.gridScrollTop+visible {
		m.gridScrollTop = cursorRow - visible + 1
	}
}

// gridBodyView renders the artist grid as rows of bordered, centered-text
// cards. Scrolls vertically to keep the cursor row visible.
func (m Model) gridBodyView() string {
	items := m.list.Items()
	cols, outerW := gridLayout(m.width)
	visibleCardRows := m.gridVisibleRows()

	totalRows := (len(items) + cols - 1) / cols
	scrollTop := m.gridScrollTop
	if scrollTop > totalRows-visibleCardRows {
		scrollTop = totalRows - visibleCardRows
	}
	if scrollTop < 0 {
		scrollTop = 0
	}
	scrollEnd := scrollTop + visibleCardRows
	if scrollEnd > totalRows {
		scrollEnd = totalRows
	}

	normal := cardStyle.Width(outerW)
	cursor := cardCursorStyle.Width(outerW)
	innerW := outerW - cardBorderCols

	var b strings.Builder
	b.WriteString(headerStyle.Render(m.list.Title))
	b.WriteByte('\n')

	for row := scrollTop; row < scrollEnd; row++ {
		cells := make([]string, 0, cols)
		for col := 0; col < cols; col++ {
			idx := row*cols + col
			if idx >= len(items) {
				break
			}
			title, sub := gridCellTexts(items[idx])
			title = truncate(title, innerW)
			content := ""
			if thumb := m.gridCardThumb(items[idx]); thumb != "" {
				content = thumb + "\n"
			}
			content += title
			if sub != "" {
				content += "\n" + helpStyle.Render(truncate(sub, innerW))
			}
			style := normal
			if idx == m.gridCursor {
				style = cursor
			}
			cells = append(cells, style.Render(content))
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, cells...))
		if row < scrollEnd-1 {
			b.WriteByte('\n')
		}
	}

	// Pad the body to exactly listHeight rows so the now-playing line and
	// status bar stay pinned to the bottom of the terminal even when the
	// grid's content doesn't fill the available space.
	return lipgloss.NewStyle().Height(m.listHeight()).Render(b.String())
}

// gridCardThumb returns the rendered thumbnail block for one grid
// card, or "" when artwork is off / not yet fetched / not applicable.
// picture.Model caches its own glyph render so this is a cheap lookup.
func (m Model) gridCardThumb(it interface{ FilterValue() string }) string {
	if !m.cfg.Images {
		return ""
	}
	var key string
	switch v := it.(type) {
	case artistItem:
		key = v.artist.RatingKey
	case albumItem:
		key = v.album.RatingKey
	}
	pic, ok := m.gridPics[key]
	if !ok || pic == nil {
		return ""
	}
	return pic.View().Content
}

// gridCellTexts returns the (title, subtitle) shown inside a grid card.
// Subtitle is dimmed at render time; empty subtitle = single-line card.
func gridCellTexts(item interface{ FilterValue() string }) (title, sub string) {
	switch it := item.(type) {
	case artistItem:
		title = it.artist.Title
		switch {
		case it.artist.AlbumCount == 1:
			sub = "1 album"
		case it.artist.AlbumCount > 1:
			sub = fmt.Sprintf("%d albums", it.artist.AlbumCount)
		}
		return
	case albumItem:
		title = it.album.Title
		var parts []string
		if it.album.Year > 0 {
			parts = append(parts, fmt.Sprintf("%d", it.album.Year))
		}
		if it.album.TrackCount > 0 {
			parts = append(parts, fmt.Sprintf("%d tracks", it.album.TrackCount))
		}
		sub = strings.Join(parts, " · ")
		return
	}
	title = item.FilterValue()
	return
}

// truncate cuts s to at most n runes, appending an ellipsis when it had to
// drop content.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}

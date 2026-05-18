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
const (
	cardOuterH      = 5  // total card height in rows (border + content)
	cardIdealOuterW = 20 // target outer width when picking cols
	cardMinOuterW   = 14 // floor: room for "Pink Floyd"-length names
	cardBorderCols  = 2  // border takes 1 col each side
)

var (
	// cardStyle / cardCursorStyle are templates; the per-card outer width
	// is set at render time from gridLayout's result.
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Height(cardOuterH).
			AlignHorizontal(lipgloss.Center).
			AlignVertical(lipgloss.Center)

	cardCursorStyle = cardStyle.
			BorderForeground(lipgloss.Color("213")).
			Foreground(lipgloss.Color("213")).
			Bold(true)
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

// supportsGrid reports whether the current browser level renders as a grid
// when m.gridView is on. Currently artists and albums; tracks and the
// library picker stay as lists.
func (m Model) supportsGrid() bool {
	return m.level == levelArtists || m.level == levelAlbums
}

// toggleGrid flips between list and grid view, syncing the cursor in both
// directions so the same item stays highlighted across the switch.
func (m *Model) toggleGrid() {
	if m.gridView {
		m.list.Select(m.gridCursor)
		m.gridView = false
	} else {
		m.gridCursor = m.list.Index()
		m.gridView = true
	}
}

// moveGridCursor shifts the gridCursor by (drow, dcol), clamped to the
// items count. dcol moves within a row; drow moves by `cols` items.
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
}

// gridBodyView renders the artist grid as rows of bordered, centered-text
// cards. Scrolls vertically to keep the cursor row visible.
func (m Model) gridBodyView() string {
	items := m.list.Items()
	cols, outerW := gridLayout(m.width)
	availRows := m.listHeight() - 1 // one row for the level title
	if availRows < cardOuterH {
		availRows = cardOuterH
	}
	visibleCardRows := availRows / cardOuterH
	if visibleCardRows < 1 {
		visibleCardRows = 1
	}

	totalRows := (len(items) + cols - 1) / cols
	cursorRow := m.gridCursor / cols
	scrollTop := 0
	if cursorRow >= visibleCardRows {
		scrollTop = cursorRow - visibleCardRows + 1
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
			content := title
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
	out := b.String()
	rendered := lipgloss.Height(out)
	target := m.listHeight()
	if rendered < target {
		out += strings.Repeat("\n", target-rendered)
	}
	return out
}

// gridCellTexts returns the (title, subtitle) shown inside a grid card.
// Subtitle is dimmed at render time; empty subtitle = single-line card.
func gridCellTexts(item interface{ FilterValue() string }) (title, sub string) {
	switch it := item.(type) {
	case artistItem:
		title = it.artist.Title
		switch {
		case it.artist.AlbumCount > 0:
			sub = fmt.Sprintf("%d albums · %d tracks", it.artist.AlbumCount, it.artist.TrackCount)
		case it.artist.TrackCount > 0:
			sub = fmt.Sprintf("%d tracks", it.artist.TrackCount)
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

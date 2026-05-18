package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Grid layout constants. cellWidth includes the right gap; tweak these to
// rebalance density vs readability.
const (
	gridCellWidth  = 24
	gridCellHeight = 1
	gridColGap     = 1
)

var (
	gridCellStyle = lipgloss.NewStyle().Padding(0, 1)
	gridCursorStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("62"))
)

// gridCols returns how many cells fit across the given width.
func gridCols(width int) int {
	step := gridCellWidth + gridColGap
	cols := (width + gridColGap) / step
	if cols < 1 {
		cols = 1
	}
	return cols
}

// toggleGrid flips between list and grid view, syncing the cursor in both
// directions so the same item stays highlighted across the switch.
func (m *Model) toggleGrid() {
	if m.gridView {
		// grid -> list
		m.list.Select(m.gridCursor)
		m.gridView = false
	} else {
		// list -> grid
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

// gridBodyView renders the grid for the current list items, highlighting
// gridCursor and scrolling to keep it visible inside listHeight() rows.
func (m Model) gridBodyView() string {
	items := m.list.Items()
	cols := gridCols(m.width)
	cellW := gridCellWidth - 2 // minus horizontal padding from gridCellStyle
	visibleRows := m.listHeight() - 1 // reserve one row for the level title
	if visibleRows < 1 {
		visibleRows = 1
	}

	totalRows := (len(items) + cols - 1) / cols
	cursorRow := 0
	if m.gridCursor >= 0 {
		cursorRow = m.gridCursor / cols
	}

	// scrollTop keeps the cursor visible.
	scrollTop := 0
	if cursorRow >= visibleRows {
		scrollTop = cursorRow - visibleRows + 1
	}
	scrollEnd := scrollTop + visibleRows
	if scrollEnd > totalRows {
		scrollEnd = totalRows
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render(m.list.Title))
	b.WriteByte('\n')

	for row := scrollTop; row < scrollEnd; row++ {
		for col := 0; col < cols; col++ {
			idx := row*cols + col
			if idx >= len(items) {
				break
			}
			text := truncate(items[idx].(artistItem).artist.Title, cellW)
			cell := text
			if idx == m.gridCursor {
				cell = gridCursorStyle.Width(cellW).Render(text)
			} else {
				cell = gridCellStyle.Width(cellW).Render(text)
			}
			b.WriteString(cell)
			if col < cols-1 && idx+1 < len(items) {
				b.WriteString(strings.Repeat(" ", gridColGap))
			}
		}
		if row < scrollEnd-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// truncate cuts s to at most n runes, appending an ellipsis when it had to
// drop content. Pure ASCII length count is fine here since artist names that
// reach the truncation length almost always contain only single-cell runes.
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

package tui

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// thumbDelegate renders bubbles/list rows with a small album/artist
// thumbnail docked to the left of the default title + description.
// Auto-toggles: when the thumb map is empty (Images off, or no fetch
// has resolved yet) Render delegates straight to the default and
// reserves no thumb column. As soon as any thumb arrives, every row
// gets the column (blank when that row has no thumb) so widths stay
// aligned.
type thumbDelegate struct {
	inner  list.DefaultDelegate
	thumbs map[string]*picture.Model
}

// listThumbCellsW / H is the cell footprint of the thumbnail rendered
// on each list row. Two rows matches the default delegate's height
// (title + description); 4 cols at 2:1 cell aspect yields a 4×4 px
// visually-square thumbnail.
const (
	listThumbCellsW = 4
	listThumbCellsH = 2
)

func newThumbDelegate(thumbs map[string]*picture.Model) thumbDelegate {
	return thumbDelegate{
		inner:  list.NewDefaultDelegate(),
		thumbs: thumbs,
	}
}

func (d thumbDelegate) Height() int  { return d.inner.Height() }
func (d thumbDelegate) Spacing() int { return d.inner.Spacing() }
func (d thumbDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd {
	return d.inner.Update(msg, m)
}

func (d thumbDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	if len(d.thumbs) == 0 {
		d.inner.Render(w, m, index, item)
		return
	}
	var key string
	switch v := item.(type) {
	case albumItem:
		key = v.album.RatingKey
	case artistItem:
		key = v.artist.RatingKey
	}
	var thumb string
	if pic, ok := d.thumbs[key]; ok && pic != nil {
		thumb = pic.View().Content
	}
	if thumb == "" {
		thumb = blankCells(listThumbCellsW, listThumbCellsH)
	}

	// Render the default row into a buffer so we can stack the
	// thumb beside it via JoinHorizontal.
	var inner strings.Builder
	d.inner.Render(&inner, m, index, item)

	fmt.Fprint(w, lipgloss.JoinHorizontal(lipgloss.Top, thumb, " ", inner.String()))
}

// blankCells returns a w×h cell block of spaces. Used to reserve the
// thumb column on rows without (yet) a thumbnail.
func blankCells(w, h int) string {
	row := strings.Repeat(" ", w)
	lines := make([]string, h)
	for i := range lines {
		lines[i] = row
	}
	return strings.Join(lines, "\n")
}

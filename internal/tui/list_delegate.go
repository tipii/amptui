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
	inner   list.DefaultDelegate
	thumbs  map[string]*picture.Model
	chafa   map[string]string
	playing *playingKeys
}

// listThumbCellsW / H is the cell footprint of the thumbnail rendered
// on each list row. Two rows matches the default delegate's height
// (title + description); 4 cols at 2:1 cell aspect yields a 4×4 px
// visually-square thumbnail.
const (
	listThumbCellsW = 4
	listThumbCellsH = 2
)

// chafa holds the Model's chafa renders (see chafa.go), shared by
// reference like thumbs; it stays empty when chafa isn't in use.
func newThumbDelegate(thumbs map[string]*picture.Model, chafa map[string]string, playing *playingKeys) thumbDelegate {
	return thumbDelegate{
		inner:   list.NewDefaultDelegate(),
		thumbs:  thumbs,
		chafa:   chafa,
		playing: playing,
	}
}

func (d thumbDelegate) Height() int  { return d.inner.Height() }
func (d thumbDelegate) Spacing() int { return d.inner.Spacing() }
func (d thumbDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd {
	return d.inner.Update(msg, m)
}

func (d thumbDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	if title, ok := d.playing.playingTitle(item); ok {
		if di, isDefault := item.(list.DefaultItem); isDefault {
			item = retitled{di, title}
		}
	}
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
	if key == "" {
		// Tracks, "Play album", libraries: never have artwork, so no
		// (blank) thumb column either.
		d.inner.Render(w, m, index, item)
		return
	}
	var thumb string
	if out := d.chafa[chafaID(key, listThumbCellsW, listThumbCellsH)]; out != "" {
		thumb = out
	} else if pic, ok := d.thumbs[key]; ok && pic != nil {
		thumb = pic.View().Content
	}
	if thumb == "" {
		thumb = blankCells(listThumbCellsW, listThumbCellsH)
	}

	// The selection bar goes in front of the thumb, at column 0 like on
	// rows without one; the default delegate would draw it as the text's
	// left border, between thumb and title. So the inner row gets no
	// border, and the bar is drawn here.
	inner := d.inner
	st := &inner.Styles
	bar := st.SelectedTitle.GetBorderLeftForeground()
	st.SelectedTitle = st.SelectedTitle.BorderLeft(false).PaddingLeft(1)
	st.SelectedDesc = st.SelectedDesc.BorderLeft(false).PaddingLeft(1)
	st.NormalTitle = st.NormalTitle.PaddingLeft(1)
	st.NormalDesc = st.NormalDesc.PaddingLeft(1)
	st.DimmedTitle = st.DimmedTitle.PaddingLeft(1)
	st.DimmedDesc = st.DimmedDesc.PaddingLeft(1)
	var row strings.Builder
	inner.Render(&row, m, index, item)

	marker := blankCells(1, listThumbCellsH)
	if index == m.Index() && m.FilterState() != list.Filtering {
		marker = strings.TrimSuffix(strings.Repeat(lipgloss.NewStyle().Foreground(bar).Render("│")+"\n", listThumbCellsH), "\n")
	}
	fmt.Fprint(w, lipgloss.JoinHorizontal(lipgloss.Top, marker, " ", thumb, row.String()))
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

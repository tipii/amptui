package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/NimbleMarkets/ntcharts/v2/picture"

	"github.com/tipii/amptui/internal/chafa"
	"github.com/tipii/amptui/internal/imgcache"
)

// chafa rendering: on terminals without the Kitty graphics protocol, when
// the chafa CLI is installed, artwork is drawn by chafa instead of the
// built-in half-block renderer. Each (image, size) is rendered once, in
// a background cmd started when the artwork arrives, and kept in
// m.chafaOut; View only ever reads that map, so no subprocess runs on the
// render path. Until a render lands (or if chafa fails on an image), the
// half-block picture shows as before.

// chafaRenderTimeout bounds one chafa run.
const chafaRenderTimeout = 5 * time.Second

// chafaReadyMsg delivers one chafa render; out is "" when it failed.
type chafaReadyMsg struct {
	id, out string
}

// chafaID keys a render by item and cell size.
func chafaID(key string, cols, rows int) string {
	return fmt.Sprintf("%s|%dx%d", key, cols, rows)
}

// chafaRenders returns cmds rendering key's cached artwork at each size
// not rendered yet. A no-op unless chafa is in use.
func (m Model) chafaRenders(key string, sizes ...[2]int) tea.Cmd {
	if !m.useChafa || key == "" {
		return nil
	}
	path, err := imgcache.Path("grid/"+key, 0, 0)
	if err != nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, sz := range sizes {
		id := chafaID(key, sz[0], sz[1])
		if _, done := m.chafaOut[id]; done {
			continue
		}
		m.chafaOut[id] = "" // requested; a failure stays "" and isn't retried
		cols, rows := sz[0], sz[1]
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), chafaRenderTimeout)
			defer cancel()
			out, _ := chafa.Render(ctx, path, cols, rows)
			return chafaReadyMsg{id: id, out: out}
		})
	}
	return tea.Batch(cmds...)
}

// thumbText is what to draw for key's artwork at a size: the chafa render
// when there is one, else the picture's own (Kitty or half-block) view.
func (m Model) thumbText(key string, p *picture.Model, cols, rows int) string {
	if m.useChafa {
		if out := m.chafaOut[chafaID(key, cols, rows)]; out != "" {
			return out
		}
	}
	if p == nil {
		return ""
	}
	return p.View().Content
}

// Sizes rendered per surface pair: hero artwork appears in the header
// and the info modal, grid items as a card and a list-row thumb.
var (
	heroSizes = [][2]int{{headerThumbCellsW, headerThumbCellsH}, {modalThumbCellsW, modalThumbCellsH}}
	itemSizes = [][2]int{{gridThumbCellsW, gridThumbCellsH}, {listThumbCellsW, listThumbCellsH}}
)

// forgetChafa drops key's renders (when its thumbnails are evicted).
func (m *Model) forgetChafa(key string) {
	for _, sz := range append(heroSizes, itemSizes...) {
		delete(m.chafaOut, chafaID(key, sz[0], sz[1]))
	}
}

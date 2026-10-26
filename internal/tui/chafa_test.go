package tui

import (
	"image"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ntcharts/v2/picture"

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/imgcache"
)

// With chafa in use, arriving artwork queues chafa renders, and once a
// render lands it replaces the half-block picture in grid cards and list
// rows; until then the half-block one shows.
func TestChafaRendersReplaceHalfBlocks(t *testing.T) {
	m := newArtistsModel(t, config.Config{Images: true, DefaultViewArtist: "grid"})
	m.useChafa = true
	// The render cmd needs the cached file to exist.
	if err := imgcache.Put("grid/ar1", 0, 0, []byte("png")); err != nil {
		t.Fatal(err)
	}

	upd, cmd := m.Update(thumbReadyMsg{kind: "grid:ar1", key: "ar1", img: image.NewRGBA(image.Rect(0, 0, 8, 8))})
	m = upd.(Model)
	if cmd == nil {
		t.Fatal("no cmds after artwork arrived")
	}
	cardID := chafaID("ar1", gridThumbCellsW, gridThumbCellsH)
	rowID := chafaID("ar1", listThumbCellsW, listThumbCellsH)
	if _, ok := m.chafaOut[cardID]; !ok {
		t.Fatal("card render not requested")
	}
	halfBlock := m.gridCardThumb(artistItem{artist: m.library.Artists()[0]})
	if halfBlock == "" {
		t.Fatal("no fallback picture before the chafa render lands")
	}

	card := strings.TrimRight(strings.Repeat(strings.Repeat("C", gridThumbCellsW)+"\n", gridThumbCellsH), "\n")
	upd, _ = m.Update(chafaReadyMsg{id: cardID, out: card})
	m = upd.(Model)
	upd, _ = m.Update(chafaReadyMsg{id: rowID, out: "RRRR\nRRRR"})
	m = upd.(Model)
	if got := m.gridCardThumb(artistItem{artist: m.library.Artists()[0]}); got != card {
		t.Errorf("card shows %q, want the chafa render", got)
	}

	m = press(m, keyV) // to list view: rows use the list-size render
	if !strings.Contains(m.View().Content, "RRRR") {
		t.Error("list row doesn't show the chafa render")
	}
}

// Kitty terminals never use chafa, whatever is installed.
func TestChafaOnlyWithoutKitty(t *testing.T) {
	m := newArtistsModel(t, config.Config{})
	if m.picMode == picture.PictureKitty && m.useChafa {
		t.Error("chafa enabled on a Kitty terminal")
	}
	m.useChafa = false
	if cmd := m.chafaRenders("ar1", itemSizes...); cmd != nil {
		t.Error("renders requested with chafa off")
	}
}

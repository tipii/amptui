package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ntcharts/v2/picture"

	"github.com/tipii/amptui/internal/config"
)

// forceKitty sets ntcharts' process-wide Kitty capability for one test.
func forceKitty(t *testing.T, c picture.KittyCapability) {
	t.Helper()
	picture.ForceKittyCapability(c)
	t.Cleanup(func() { picture.ForceKittyCapability(picture.KittyCapabilityUnknown) })
}

// Pictures made before the terminal confirmed Kitty support (the probe
// answers a few ms after start, after the first artwork) must switch to
// Kitty when it does. They used to stay half-block for the session:
// picture.Model refuses Kitty until confirmed and never retries.
func TestPicturesSwitchToKittyWhenConfirmed(t *testing.T) {
	forceKitty(t, picture.KittyCapabilityUnknown)
	m := newArtistsModel(t, config.Config{Images: true})
	grid := newSizedPicture(gridThumbCellsW, gridThumbCellsH)
	m.gridPics["ar1"] = grid
	if m.picMode != picture.PictureGlyph || grid.Mode() != picture.PictureGlyph {
		t.Fatal("pictures should start as half-block")
	}

	forceKitty(t, picture.KittyCapabilitySupported)
	upd, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // any message
	m = upd.(Model)
	if m.picMode != picture.PictureKitty {
		t.Fatal("picMode didn't switch to Kitty")
	}
	for name, p := range map[string]*picture.Model{"grid": grid, "artist header": m.artistHeaderPic, "album modal": m.albumModalPic} {
		if p.Mode() != picture.PictureKitty {
			t.Errorf("%s picture still half-block", name)
		}
	}
}

func TestPicturesStayHalfBlockWithoutKitty(t *testing.T) {
	forceKitty(t, picture.KittyCapabilityUnsupported)
	m := newArtistsModel(t, config.Config{Images: true})
	upd, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = upd.(Model)
	if m.picMode != picture.PictureGlyph || m.artistHeaderPic.Mode() != picture.PictureGlyph {
		t.Error("switched to Kitty on a terminal that doesn't support it")
	}
}

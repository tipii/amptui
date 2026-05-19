package imgcache

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
	"image/png"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	xdraw "golang.org/x/image/draw"
)

// Protocol identifies how the terminal renders inline images.
type Protocol int

const (
	// ProtocolNone means the terminal has no native image protocol;
	// callers should fall back to ANSI half-block rendering.
	ProtocolNone Protocol = iota
	// ProtocolKitty is the Kitty graphics protocol, also supported
	// by ghostty, wezterm, konsole (recent), and others.
	ProtocolKitty
)

// Detect returns the best image protocol the current terminal claims
// to support, inferred from environment variables.
//
// For now we always return ProtocolNone (24-bit half-block ANSI) — the
// Kitty graphics protocol works pixel-perfect on its own but doesn't
// compose cleanly inside lipgloss-styled cards (APC sequences fight
// the styling's cursor moves, and Kitty keeps images live across
// redraws, causing ghosting). Half-block renders correctly in every
// modern terminal including ghostty, kitty, wezterm, alacritty, etc.
// Re-enable the Kitty path once we have a clean integration story.
func Detect() Protocol {
	_ = os.Getenv // keep import; detection heuristics live here when re-enabled
	_ = strings.Contains
	return ProtocolNone
}

// Render returns a string the caller can print/insert into a lipgloss
// view to display data at (cellsW × cellsH) terminal cells using p.
// Always returns a string of the correct visual size (padded with
// blank lines if decoding fails) so layout stays stable.
func Render(p Protocol, data []byte, cellsW, cellsH int) string {
	if len(data) == 0 || cellsW < 1 || cellsH < 1 {
		return blank(cellsW, cellsH)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return blank(cellsW, cellsH)
	}
	switch p {
	case ProtocolKitty:
		return renderKitty(img, cellsW, cellsH)
	default:
		return renderHalfBlock(img, cellsW, cellsH)
	}
}

// blank returns a cellsW × cellsH spacer so the surrounding layout
// stays the same even when an image isn't available.
func blank(cellsW, cellsH int) string {
	if cellsW < 1 || cellsH < 1 {
		return ""
	}
	row := strings.Repeat(" ", cellsW)
	return strings.Repeat(row+"\n", cellsH-1) + row
}

// renderKitty emits the Kitty graphics protocol escape sequence to
// draw the image inline. We re-encode as PNG (Kitty accepts PNG with
// f=100) and base64 the bytes. The s/v parameters give Kitty the
// image's pixel dimensions; the terminal scales to fit the cells
// implied by its current font metrics.
func renderKitty(img image.Image, cellsW, cellsH int) string {
	// Scale to a reasonable pixel size — terminals typically render
	// at ~10-20 px per cell width and ~20-40 px per cell height. We
	// aim at roughly that target so we don't ship a 2000px image.
	pxW := cellsW * 14
	pxH := cellsH * 28
	scaled := resize(img, pxW, pxH)

	var buf bytes.Buffer
	if err := png.Encode(&buf, scaled); err != nil {
		return blank(cellsW, cellsH)
	}
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	// Kitty's chunked protocol: emit 4096-byte chunks with m=1
	// (more) and a final m=0 chunk. Required for images that don't
	// fit in a single sequence.
	const chunk = 4096
	var out strings.Builder
	for i := 0; i < len(b64); i += chunk {
		end := i + chunk
		if end > len(b64) {
			end = len(b64)
		}
		more := 1
		if end == len(b64) {
			more = 0
		}
		// First chunk carries the format/action/dimensions; later
		// chunks only carry m=N and payload.
		if i == 0 {
			fmt.Fprintf(&out, "\x1b_Gf=100,a=T,m=%d;%s\x1b\\", more, b64[i:end])
		} else {
			fmt.Fprintf(&out, "\x1b_Gm=%d;%s\x1b\\", more, b64[i:end])
		}
	}
	// Reserve the cell footprint with a blank block — kitty draws
	// the image as an overlay, so without the spacer the surrounding
	// view collapses around where the image "should" be.
	return out.String() + blank(cellsW, cellsH)
}

// renderHalfBlock draws img using Unicode upper-half-block characters
// where each cell encodes two image rows (foreground = upper, background
// = lower). Works in any 24-bit-color ANSI terminal — no protocol needed.
func renderHalfBlock(img image.Image, cellsW, cellsH int) string {
	// Two image rows per terminal cell.
	pxW := cellsW
	pxH := cellsH * 2
	scaled := resize(img, pxW, pxH)
	b := scaled.Bounds()

	var out strings.Builder
	for y := 0; y < pxH; y += 2 {
		for x := 0; x < pxW; x++ {
			topR, topG, topB, _ := scaled.At(b.Min.X+x, b.Min.Y+y).RGBA()
			botR, botG, botB, _ := scaled.At(b.Min.X+x, b.Min.Y+y+1).RGBA()
			top := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", topR>>8, topG>>8, topB>>8))
			bot := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", botR>>8, botG>>8, botB>>8))
			out.WriteString(lipgloss.NewStyle().Foreground(top).Background(bot).Render("▀"))
		}
		if y+2 < pxH {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// resize scales src to fit a (w × h) box, preserving aspect ratio and
// centering the result on a transparent background. Uses Catmull-Rom
// because for thumbnails the extra quality is cheap.
func resize(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sb := src.Bounds()

	srcAR := float64(sb.Dx()) / float64(sb.Dy())
	dstAR := float64(w) / float64(h)

	var fitW, fitH int
	if srcAR > dstAR {
		fitW = w
		fitH = int(float64(w) / srcAR)
	} else {
		fitH = h
		fitW = int(float64(h) * srcAR)
	}
	offX := (w - fitW) / 2
	offY := (h - fitH) / 2

	xdraw.CatmullRom.Scale(dst, image.Rect(offX, offY, offX+fitW, offY+fitH), src, sb, xdraw.Over, nil)
	return dst
}

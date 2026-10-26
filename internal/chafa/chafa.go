// Package chafa renders images to terminal text with the chafa CLI
// (https://hpjansson.org/chafa/), when it's installed. amptui uses it for
// artwork on terminals without the Kitty graphics protocol (foot,
// Alacritty, xterm, …), where it looks much better than the built-in
// half-block fallback.
//
// Output is chafa's "symbols" mode — ordinary colored text, one cell per
// character — never sixel: sixel images don't occupy cells, so they'd
// break the list and grid layout, which measures everything in cells.
package chafa

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

var (
	lookOnce sync.Once
	binPath  string
)

// Available reports whether chafa is on PATH (looked up once).
func Available() bool {
	lookOnce.Do(func() { binPath, _ = exec.LookPath("chafa") })
	return binPath != ""
}

// Render draws the image file at path into exactly cols × rows cells:
// chafa fits it inside the box keeping its aspect ratio, and the result
// is centered and padded with spaces so every line is cols wide and there
// are rows lines, which is what the fixed-size layout around it expects.
func Render(ctx context.Context, path string, cols, rows int) (string, error) {
	if !Available() {
		return "", fmt.Errorf("chafa not found on PATH")
	}
	colors := "256"
	if ct := os.Getenv("COLORTERM"); ct == "truecolor" || ct == "24bit" {
		colors = "full"
	}
	cmd := exec.CommandContext(ctx, binPath,
		"--format", "symbols",
		"--size", fmt.Sprintf("%dx%d", cols, rows),
		"--colors", colors,
		"--animate", "off",
		// No cursor hiding / terminal queries: the output is embedded in
		// a frame, not written to the terminal directly.
		"--polite", "on",
		path,
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("chafa: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return fitBox(out.String(), cols, rows), nil
}

// fitBox centers text in a cols × rows box of cells, truncating anything
// that overflows it.
func fitBox(s string, cols, rows int) string {
	s = strings.ReplaceAll(s, "\r", "")
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > rows {
		lines = lines[:rows]
	}
	blank := strings.Repeat(" ", cols)
	out := make([]string, 0, rows)
	top := (rows - len(lines)) / 2
	for range top {
		out = append(out, blank)
	}
	for _, l := range lines {
		l = ansi.Truncate(l, cols, "")
		w := ansi.StringWidth(l)
		left := (cols - w) / 2
		// Reset before the padding so a color left open by the image
		// doesn't bleed into it.
		out = append(out, strings.Repeat(" ", left)+l+"\x1b[0m"+strings.Repeat(" ", cols-w-left))
	}
	for len(out) < rows {
		out = append(out, blank)
	}
	return strings.Join(out, "\n")
}

package imgcache

import (
	"os"
	"strings"
)

// Protocol identifies how the terminal renders inline images. Used by
// the TUI layer to pick a picture.Model mode at startup. The actual
// pixel-to-cell rendering is owned by ntcharts/picture; this package
// only holds the on-disk byte cache and the terminal detection.
type Protocol int

const (
	// ProtocolNone means the terminal has no native image protocol;
	// callers should fall back to half-block ANSI rendering.
	ProtocolNone Protocol = iota
	// ProtocolKitty is the Kitty graphics protocol, also supported
	// by ghostty, wezterm, konsole (recent), and others.
	ProtocolKitty
)

// Detect returns the best image protocol the current terminal claims
// to support, inferred from environment variables. Best-effort: we
// don't query the terminal interactively, so users in unusual setups
// can still toggle the "Inline artwork" setting on/off manually.
func Detect() Protocol {
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return ProtocolKitty
	}
	if os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return ProtocolKitty
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "ghostty", "WezTerm":
		return ProtocolKitty
	}
	if term := os.Getenv("TERM"); strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") {
		return ProtocolKitty
	}
	return ProtocolNone
}

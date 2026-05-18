package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/theopalhol/amptui/internal/config"
	"github.com/theopalhol/amptui/internal/library"
)

// settingsView renders the dedicated settings screen — read-only display of
// the loaded config and library cache state. Same chrome as the browser
// (header, body, now-playing, footer).
func (m Model) settingsView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("amptui"))
	b.WriteString("  " + crumbStyle.Render("Settings /"))
	b.WriteString("\n\n")

	body := m.settingsBody()
	// Pad the body to listHeight so the now-playing + footer stay pinned.
	if h := lipgloss.Height(body); h < m.listHeight() {
		body += strings.Repeat("\n", m.listHeight()-h)
	}
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")
	b.WriteString(m.footerLine(helpStyle.Render("esc back · R resync · ctrl+q quit")))
	return b.String()
}

func (m Model) settingsBody() string {
	var b strings.Builder

	// --- Server section ---
	b.WriteString(sectionStyle.Render("Server"))
	b.WriteString("\n")
	b.WriteString(settingRow("URL", m.cfg.ServerURL))
	b.WriteString(settingRow("Token", maskToken(m.cfg.Token)))
	if m.cfg.DefaultLibrary != "" {
		b.WriteString(settingRow("Default library", m.cfg.DefaultLibrary))
	} else {
		b.WriteString(settingRow("Default library", helpStyle.Render("(not set — picker shown on startup)")))
	}
	cfgPath, _ := config.Path()
	b.WriteString(settingRow("Config file", cfgPath))
	b.WriteString("\n")

	// --- Library cache section ---
	b.WriteString(sectionStyle.Render("Library cache"))
	b.WriteString("\n")
	if m.library == nil {
		b.WriteString(settingRow("Status", helpStyle.Render("syncing…" )))
	} else {
		path, _ := library.CachePath(m.library.SectionUUID)
		b.WriteString(settingRow("Path", path))
		b.WriteString(settingRow("Last synced", formatSyncedAt(m.library.SyncedAt)))
		b.WriteString(settingRow("Entries", entryBreakdown(m.library)))
		b.WriteString(settingRow("File size", cacheSize(path)))
		b.WriteString(settingRow("Schema", fmt.Sprintf("v%d", m.library.SchemaVersion)))
	}
	if m.librarySyncing {
		b.WriteString(settingRow("", helpStyle.Render(m.spinner.View()+"re-syncing from Plex…")))
	} else if m.libraryErr != nil {
		b.WriteString(settingRow("", errStyle.Render("error: "+m.libraryErr.Error())))
	}

	return b.String()
}

var sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))

// settingRow formats one "key: value" line for the settings page.
func settingRow(key, value string) string {
	const keyWidth = 18
	padded := key + strings.Repeat(" ", max(0, keyWidth-len(key)))
	if key == "" {
		padded = strings.Repeat(" ", keyWidth)
	}
	return "  " + helpStyle.Render(padded) + value + "\n"
}

// maskToken shows the last 4 chars only; if the token is too short, just
// stars. Plex X-Plex-Token is typically 20 chars.
func maskToken(t string) string {
	if len(t) == 0 {
		return helpStyle.Render("(unset)")
	}
	if len(t) <= 4 {
		return strings.Repeat("•", len(t))
	}
	return strings.Repeat("•", len(t)-4) + t[len(t)-4:]
}

func formatSyncedAt(t time.Time) string {
	if t.IsZero() {
		return helpStyle.Render("(unknown)")
	}
	d := time.Since(t).Round(time.Second)
	return t.Format("2006-01-02 15:04:05") + helpStyle.Render(fmt.Sprintf("  (%s ago)", humanDuration(d)))
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// entryBreakdown summarises Entries by kind.
func entryBreakdown(l *library.Library) string {
	counts := map[library.Kind]int{}
	for _, e := range l.Entries {
		counts[e.Kind]++
	}
	total := len(l.Entries)
	return fmt.Sprintf("%d total · %d artists · %d albums · %d tracks",
		total, counts[library.KindArtist], counts[library.KindAlbum], counts[library.KindTrack])
}

// cacheSize stats the cache file and returns a human-readable size.
func cacheSize(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return helpStyle.Render("(no file)")
	}
	return humanBytes(info.Size())
}

func humanBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	if n < k*k {
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	}
	if n < k*k*k {
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(k*k*k))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

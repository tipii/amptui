package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/theopalhol/amptui/internal/config"
	"github.com/theopalhol/amptui/internal/library"
)

// settingsValues holds the strings bound to the huh form fields. Stored on
// the Model so the pointers remain stable across renders.
type settingsValues struct {
	ServerURL      string
	Token          string
	DefaultLibrary string
	ViewArtist     string
	ViewAlbum      string
}

func newSettingsValues(cfg config.Config) settingsValues {
	return settingsValues{
		ServerURL:      cfg.ServerURL,
		Token:          cfg.Token,
		DefaultLibrary: cfg.DefaultLibrary,
		ViewArtist:     normalizeView(cfg.DefaultViewArtist),
		ViewAlbum:      normalizeView(cfg.DefaultViewAlbum),
	}
}

// buildSettingsForm wires a huh form against the bound values. Submit
// returns the user to the browser (the caller saves first).
func buildSettingsForm(v *settingsValues) *huh.Form {
	viewOpts := []huh.Option[string]{
		huh.NewOption("list", "list"),
		huh.NewOption("grid", "grid"),
	}
	f := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Server URL").Value(&v.ServerURL),
			huh.NewInput().Title("Token").EchoMode(huh.EchoModePassword).Value(&v.Token),
			huh.NewInput().
				Title("Default library").
				Description("Section name or key. Leave empty to show the picker on startup.").
				Value(&v.DefaultLibrary),
			huh.NewSelect[string]().
				Title("Default view (Artists)").
				Options(viewOpts...).
				Value(&v.ViewArtist),
			huh.NewSelect[string]().
				Title("Default view (Albums)").
				Options(viewOpts...).
				Value(&v.ViewAlbum),
		),
	).WithShowHelp(false)
	return f
}

// normalizeView coerces a stored view setting to a known option, defaulting
// to "list" so a missing or invalid value displays sensibly.
func normalizeView(v string) string {
	if v == "grid" {
		return "grid"
	}
	return "list"
}

// handleSettingsKey routes keys when the settings screen is active. Most
// keys pass through to the huh form; we intercept ctrl+c/ctrl+q (quit), `,`
// (close), and R (resync) at the screen level.
func (m Model) handleSettingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "ctrl+q":
		return m, tea.Quit
	case ",":
		// Close settings without committing the form.
		m.cancelSettings()
		return m, nil
	case "R":
		if m.librarySyncing || len(m.libs) == 0 {
			return m, nil
		}
		active := m.libs[0]
		if m.startupLibrary != nil {
			active = *m.startupLibrary
		}
		m.librarySyncing = true
		m.libraryErr = nil
		return m, syncLibrary(m.client, active)
	}

	// Forward to huh form.
	updated, cmd := m.settingsForm.Update(msg)
	if f, ok := updated.(*huh.Form); ok {
		m.settingsForm = f
	}

	// React to form state transitions.
	switch m.settingsForm.State {
	case huh.StateCompleted:
		m.applySettings()
		if err := m.cfg.Save(); err != nil {
			m.settingsErr = err
		} else {
			m.settingsErr = nil
			m.settingsSavedAt = time.Now()
		}
		m.settingsForm = buildSettingsForm(&m.settingsValues)
		m.screen = screenBrowser
	case huh.StateAborted:
		m.cancelSettings()
	}
	return m, cmd
}

// applySettings copies the bound form values back into m.cfg and updates
// runtime state that depends on them (grid view per level).
func (m *Model) applySettings() {
	m.cfg.ServerURL = m.settingsValues.ServerURL
	m.cfg.Token = m.settingsValues.Token
	m.cfg.DefaultLibrary = m.settingsValues.DefaultLibrary
	m.cfg.DefaultViewArtist = m.settingsValues.ViewArtist
	m.cfg.DefaultViewAlbum = m.settingsValues.ViewAlbum
	m.gridArtists = m.cfg.DefaultViewArtist == "grid"
	m.gridAlbums = m.cfg.DefaultViewAlbum == "grid"
}

// cancelSettings discards in-flight edits and returns to the browser.
func (m *Model) cancelSettings() {
	m.settingsValues = newSettingsValues(m.cfg)
	m.settingsForm = buildSettingsForm(&m.settingsValues)
	m.screen = screenBrowser
}

// settingsView renders the dedicated settings screen — the huh form on top,
// read-only library cache stats below.
func (m Model) settingsView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("amptui"))
	b.WriteString("  " + crumbStyle.Render("Settings /"))
	b.WriteString("\n\n")

	body := m.settingsForm.View()
	body += "\n" + m.cacheStatsBody()
	// Status flash
	switch {
	case m.settingsErr != nil:
		body += "\n" + errStyle.Render("save error: "+m.settingsErr.Error())
	case time.Since(m.settingsSavedAt) < 2*time.Second:
		body += "\n" + npStyle.Render("saved ✓")
	}
	// Pad to keep status bar pinned to the bottom.
	if h := lipgloss.Height(body); h < m.listHeight() {
		body += strings.Repeat("\n", m.listHeight()-h)
	}
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")
	b.WriteString(m.footerLine(helpStyle.Render(
		"tab/enter navigate · enter on submit saves · esc cancel · , close · R resync · ctrl+q quit")))
	return b.String()
}

// cacheStatsBody renders the read-only Library cache section: cache path,
// last synced timestamp, entry counts, file size, schema version.
func (m Model) cacheStatsBody() string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Library cache"))
	b.WriteString("\n")
	if m.library == nil {
		b.WriteString(settingRow("Status", helpStyle.Render("syncing…")))
		return b.String()
	}
	path, _ := library.CachePath(m.library.SectionUUID)
	b.WriteString(settingRow("Path", path))
	b.WriteString(settingRow("Last synced", formatSyncedAt(m.library.SyncedAt)))
	b.WriteString(settingRow("Entries", entryBreakdown(m.library)))
	b.WriteString(settingRow("File size", cacheSize(path)))
	b.WriteString(settingRow("Schema", fmt.Sprintf("v%d", m.library.SchemaVersion)))
	if m.librarySyncing {
		b.WriteString(settingRow("", helpStyle.Render(m.spinner.View()+"re-syncing from Plex…")))
	} else if m.libraryErr != nil {
		b.WriteString(settingRow("", errStyle.Render("error: "+m.libraryErr.Error())))
	}
	cfgPath, _ := config.Path()
	b.WriteString("\n")
	b.WriteString(settingRow("Config file", cfgPath))
	return b.String()
}

var sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))

// settingRow formats one read-only "key: value" line.
func settingRow(key, value string) string {
	const keyWidth = 24
	padded := key + strings.Repeat(" ", max(0, keyWidth-len(key)))
	if key == "" {
		padded = strings.Repeat(" ", keyWidth)
	}
	return "  " + helpStyle.Render(padded) + value + "\n"
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

func entryBreakdown(l *library.Library) string {
	counts := map[library.Kind]int{}
	for _, e := range l.Entries {
		counts[e.Kind]++
	}
	total := len(l.Entries)
	return fmt.Sprintf("%d total · %d artists · %d albums · %d tracks",
		total, counts[library.KindArtist], counts[library.KindAlbum], counts[library.KindTrack])
}

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

// forwardToForm calls form.Update(msg) and re-asserts the returned model
// back to a *huh.Form. Returns the form's cmd alongside; ok is false if
// the assertion fails.
func forwardToForm(form *huh.Form, msg tea.Msg) (*huh.Form, tea.Cmd, bool) {
	updated, cmd := form.Update(msg)
	if f, ok := updated.(*huh.Form); ok {
		return f, cmd, true
	}
	return nil, nil, false
}

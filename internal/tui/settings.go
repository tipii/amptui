package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/theopalhol/amptui/internal/config"
	"github.com/theopalhol/amptui/internal/library"
)

// settingsValues holds the strings bound to each huh field. Stored on the
// Model so the pointers remain stable across renders.
type settingsValues struct {
	ServerURL      string
	Token          string
	DefaultLibrary string
	ViewArtist     string
	ViewAlbum      string
}

// newSettingsValues returns a heap-allocated settingsValues. The fields
// bind to this struct's address via huh.Input.Value(&v.X); using a pointer
// keeps the address stable across Model copies (Bubble Tea passes Model by
// value through Update).
func newSettingsValues(cfg config.Config) *settingsValues {
	return &settingsValues{
		ServerURL:      cfg.ServerURL,
		Token:          cfg.Token,
		DefaultLibrary: cfg.DefaultLibrary,
		ViewArtist:     normalizeView(cfg.DefaultViewArtist),
		ViewAlbum:      normalizeView(cfg.DefaultViewAlbum),
	}
}

// buildSettingsFields wires up one huh Field per editable setting. The
// fields are used as standalone widgets — there is no wrapping form, so we
// must apply huh's default keymap manually (Form/Group normally do this
// when fields are members of a form; without it, key.Matches finds nothing
// in the field's zero-valued keymap and navigation silently does nothing).
func buildSettingsFields(v *settingsValues) []huh.Field {
	viewOpts := []huh.Option[string]{
		huh.NewOption("list", "list"),
		huh.NewOption("grid", "grid"),
	}
	fields := []huh.Field{
		huh.NewInput().Title("Server URL").Value(&v.ServerURL),
		huh.NewInput().Title("Token").EchoMode(huh.EchoModePassword).Value(&v.Token),
		huh.NewInput().
			Title("Default library").
			Description("Section name or key. Leave empty to show the picker on startup.").
			Value(&v.DefaultLibrary),
		huh.NewSelect[string]().Title("Default view (Artists)").Height(3).Options(viewOpts...).Value(&v.ViewArtist),
		huh.NewSelect[string]().Title("Default view (Albums)").Height(3).Options(viewOpts...).Value(&v.ViewAlbum),
	}
	km := huh.NewDefaultKeyMap()
	for i, f := range fields {
		fields[i] = f.WithKeyMap(km)
	}
	return fields
}

// normalizeView coerces a stored view setting to a known option, defaulting
// to "list" so a missing or invalid value displays sensibly.
func normalizeView(v string) string {
	if v == "grid" {
		return "grid"
	}
	return "list"
}

// handleSettingsKey routes keys while the settings screen is active.
// Two modes:
//   - navigation (default): j/k between fields, enter focuses one to edit,
//     esc/, closes the screen, R re-syncs the library cache.
//   - editing: keys go to the focused field. enter commits + saves and
//     exits edit mode; esc also commits (we don't support cancel for v1).
func (m Model) handleSettingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.settingsEditing {
		return m.handleSettingsEditKey(msg)
	}
	k := m.keymap
	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Settings), key.Matches(msg, k.Back):
		m.screen = screenBrowser
		return m, nil
	case key.Matches(msg, k.Up):
		if m.settingsCursor > 0 {
			m.settingsCursor--
		}
		return m, nil
	case key.Matches(msg, k.Down):
		if m.settingsCursor < len(m.settingsFields)-1 {
			m.settingsCursor++
		}
		return m, nil
	case key.Matches(msg, k.Enter):
		if m.settingsCursor < 0 || m.settingsCursor >= len(m.settingsFields) {
			return m, nil
		}
		m.settingsEditing = true
		return m, m.settingsFields[m.settingsCursor].Focus()
	case key.Matches(msg, k.Refresh):
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
	return m, nil
}

// handleSettingsEditKey processes a keystroke while a field is focused.
// Most keys forward to the huh field. enter/esc commit (the bound value is
// already up-to-date) and exit edit mode.
func (m Model) handleSettingsEditKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keymap.Quit) {
		return m, tea.Quit
	}

	field := m.settingsFields[m.settingsCursor]
	updated, cmd := field.Update(msg)
	if f, ok := updated.(huh.Field); ok {
		m.settingsFields[m.settingsCursor] = f
	}

	if key.Matches(msg, m.keymap.Enter) || key.Matches(msg, m.keymap.Back) {
		blurCmd := m.settingsFields[m.settingsCursor].Blur()
		m.settingsEditing = false
		m.applyAndSaveSettings()
		return m, tea.Batch(cmd, blurCmd)
	}
	return m, cmd
}

// applyAndSaveSettings copies bound values into m.cfg, applies runtime
// effects (grid view per level), and persists to config.toml.
func (m *Model) applyAndSaveSettings() {
	if m.settingsValues == nil {
		return
	}
	m.cfg.ServerURL = m.settingsValues.ServerURL
	m.cfg.Token = m.settingsValues.Token
	m.cfg.DefaultLibrary = m.settingsValues.DefaultLibrary
	m.cfg.DefaultViewArtist = m.settingsValues.ViewArtist
	m.cfg.DefaultViewAlbum = m.settingsValues.ViewAlbum
	m.gridArtists = m.cfg.DefaultViewArtist == "grid"
	m.gridAlbums = m.cfg.DefaultViewAlbum == "grid"
	if err := m.cfg.Save(); err != nil {
		m.settingsErr = err
		return
	}
	m.settingsErr = nil
	m.settingsSavedAt = time.Now()
}

// forwardToAllSettingsFields fans non-key messages (window size, internal
// huh init/focus cmds) to every field so each can advance its internal
// state. Returns a batched cmd of anything the fields produce.
func (m *Model) forwardToAllSettingsFields(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for i, f := range m.settingsFields {
		updated, c := f.Update(msg)
		if fld, ok := updated.(huh.Field); ok {
			m.settingsFields[i] = fld
		}
		if c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

// settingsView renders the settings screen: each editable field with a
// cursor marker on the current row, followed by read-only library stats.
func (m Model) settingsView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("amptui"))
	b.WriteString("  " + crumbStyle.Render("Settings /"))
	b.WriteString("\n\n")

	// Surface config-file parse errors so the user sees why their on-disk
	// values aren't taking effect (Load is lenient and would otherwise
	// silently drop everything from a malformed file).
	if w := config.LastLoadWarning; w != nil {
		b.WriteString(errStyle.Render("config file error: "+w.Err.Error()) + "\n")
		b.WriteString(helpStyle.Render("  fix the file or correct values below; save here overwrites it") + "\n\n")
	}

	b.WriteString(sectionStyle.Render("Server"))
	b.WriteString("\n")
	for i, f := range m.settingsFields {
		b.WriteString(m.renderSettingsField(i, f))
	}
	// Status flash for save success / error.
	switch {
	case m.settingsErr != nil:
		b.WriteString("\n" + errStyle.Render("save error: "+m.settingsErr.Error()))
	case time.Since(m.settingsSavedAt) < 2*time.Second:
		b.WriteString("\n" + npStyle.Render("saved ✓"))
	}

	b.WriteString("\n\n")
	b.WriteString(m.cacheStatsBody())

	body := b.String()
	if h := lipgloss.Height(body); h < m.listHeight() {
		body += strings.Repeat("\n", m.listHeight()-h)
	}

	var out strings.Builder
	out.WriteString(body)
	out.WriteString("\n")
	out.WriteString(m.nowPlayingLine())
	out.WriteString("\n")

	out.WriteString(m.footerLine(m.helpModel.View(m.currentHelp())))
	return out.String()
}

// renderSettingsField shows one row: cursor marker on the left, then the
// field's own View(). Indents each line of multi-line field output.
func (m Model) renderSettingsField(i int, f huh.Field) string {
	marker := "  "
	switch {
	case i == m.settingsCursor && m.settingsEditing:
		marker = npStyle.Render("✎ ")
	case i == m.settingsCursor:
		marker = npStyle.Render("▶ ")
	}
	view := f.View()
	lines := strings.Split(view, "\n")
	for j, line := range lines {
		if j == 0 {
			lines[j] = marker + line
		} else {
			lines[j] = "  " + line
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// cacheStatsBody renders the read-only Library cache section.
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

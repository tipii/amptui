package tui

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/theopalhol/amptui/internal/config"
	"github.com/theopalhol/amptui/internal/imgcache"
	"github.com/theopalhol/amptui/internal/library"
)

// settingsValues holds the strings bound to each huh field. Stored on the
// settings sub-model so the pointers remain stable across renders.
type settingsValues struct {
	ServerURL      string
	Token          string
	DefaultLibrary string
	ViewArtist     string
	ViewAlbum      string
	Home           string
	Images         bool
}

// settingsOutcome is what the settings sub-model asks its parent to do
// after handling a key. Most keys are sub-model-internal (move cursor,
// edit, etc.); a few — close, refresh, commit — need parent state.
type settingsOutcome int

const (
	settingsOutcomeNone settingsOutcome = iota
	settingsOutcomeClose
	settingsOutcomeRefresh
	settingsOutcomeCommit
	settingsOutcomePurgeImgs
)

// settingsModel owns the settings screen: editable fields, cursor, edit
// state, save flash. It depends on the parent only for the active KeyMap
// (passed in to Update) and the read-only library snapshot (passed in to
// View for cache stats). Cross-cutting actions — close, refresh, commit
// — are surfaced as settingsOutcome values that the parent acts on.
type settingsModel struct {
	fields  []huh.Field
	values  *settingsValues
	cursor  int
	editing bool
	savedAt time.Time
	err     error
}

// newSettingsModel builds the sub-model from an initial config snapshot.
// The bound values are seeded from cfg; subsequent edits are read back
// out via values() when the parent commits.
func newSettingsModel(cfg config.Config) settingsModel {
	v := &settingsValues{
		ServerURL:      cfg.ServerURL,
		Token:          cfg.Token,
		DefaultLibrary: cfg.DefaultLibrary,
		ViewArtist:     normalizeView(cfg.DefaultViewArtist),
		ViewAlbum:      normalizeView(cfg.DefaultViewAlbum),
		Home:           normalizeHome(cfg.Home),
		Images:         cfg.Images,
	}
	return settingsModel{
		fields: buildSettingsFields(v),
		values: v,
	}
}

// Init forwards each field's Init cmd in a single batch so huh's
// internal focus / cursor-blink machinery starts up correctly.
func (s settingsModel) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(s.fields))
	for _, f := range s.fields {
		cmds = append(cmds, f.Init())
	}
	return tea.Batch(cmds...)
}

// HandleKey routes a keypress while the settings screen is active.
// km is the parent's KeyMap (shared, not owned). The returned outcome
// tells the parent whether to close the screen, kick off a library
// refresh, or apply the committed values.
func (s settingsModel) HandleKey(msg tea.KeyPressMsg, km KeyMap) (settingsModel, tea.Cmd, settingsOutcome) {
	if s.editing {
		return s.handleEditKey(msg, km)
	}
	switch {
	case key.Matches(msg, km.Quit):
		return s, tea.Quit, settingsOutcomeNone
	case key.Matches(msg, km.Settings), key.Matches(msg, km.Back):
		return s, nil, settingsOutcomeClose
	case key.Matches(msg, km.Up):
		if s.cursor > 0 {
			s.cursor--
		}
		return s, nil, settingsOutcomeNone
	case key.Matches(msg, km.Down):
		if s.cursor < len(s.fields)-1 {
			s.cursor++
		}
		return s, nil, settingsOutcomeNone
	case key.Matches(msg, km.Enter):
		if s.cursor < 0 || s.cursor >= len(s.fields) {
			return s, nil, settingsOutcomeNone
		}
		s.editing = true
		return s, s.fields[s.cursor].Focus(), settingsOutcomeNone
	case key.Matches(msg, km.Refresh):
		return s, nil, settingsOutcomeRefresh
	case key.Matches(msg, km.PurgeImgs):
		return s, nil, settingsOutcomePurgeImgs
	}
	return s, nil, settingsOutcomeNone
}

// handleEditKey processes a keystroke while a field is focused. Most
// keys forward to the huh field; InputEnter / InputBack commit (the
// bound value is already up-to-date) and exit edit mode. Commit is
// refused while the field's own validation is failing.
func (s settingsModel) handleEditKey(msg tea.KeyPressMsg, km KeyMap) (settingsModel, tea.Cmd, settingsOutcome) {
	if key.Matches(msg, km.Quit) {
		return s, tea.Quit, settingsOutcomeNone
	}

	updated, cmd := s.fields[s.cursor].Update(msg)
	if f, ok := updated.(huh.Field); ok {
		s.fields[s.cursor] = f
	}

	// Input* bindings (arrows / enter / esc only) so vim-letter aliases
	// on Enter/Back don't trigger commit when the user types "l" or "h"
	// into the field they're editing.
	if key.Matches(msg, km.InputEnter) || key.Matches(msg, km.InputBack) {
		if s.fields[s.cursor].Error() != nil {
			return s, cmd, settingsOutcomeNone
		}
		blurCmd := s.fields[s.cursor].Blur()
		s.editing = false
		return s, tea.Batch(cmd, blurCmd), settingsOutcomeCommit
	}
	return s, cmd, settingsOutcomeNone
}

// ForwardMsg fans a non-key message (window resize, huh init/focus
// cmds) to every field so each can advance its internal state. Safe to
// call from any context — fields ignore msgs they don't care about.
func (s settingsModel) ForwardMsg(msg tea.Msg) (settingsModel, tea.Cmd) {
	var cmds []tea.Cmd
	for i, f := range s.fields {
		updated, c := f.Update(msg)
		if fld, ok := updated.(huh.Field); ok {
			s.fields[i] = fld
		}
		if c != nil {
			cmds = append(cmds, c)
		}
	}
	return s, tea.Batch(cmds...)
}

// MarkSaved flips on the "saved ✓" flash; the parent calls this after
// a successful config.Save. err records a save failure for display.
func (s *settingsModel) MarkSaved(err error) {
	s.err = err
	if err == nil {
		s.savedAt = time.Now()
	}
}

// Values returns the current bound values so the parent can copy them
// into its own config on commit.
func (s settingsModel) Values() settingsValues { return *s.values }

// IsEditing reports whether a field currently has focus.
func (s settingsModel) IsEditing() bool { return s.editing }

// View renders the settings body. The caller composes header / now-playing
// / footer around this. spin and stats are pulled from parent state since
// the sub-model doesn't own the spinner or library snapshot.
func (s settingsModel) View(bodyHeight int, statsBody string) string {
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
	for i, f := range s.fields {
		b.WriteString(s.renderField(i, f))
	}
	switch {
	case s.err != nil:
		b.WriteString("\n" + errStyle.Render("save error: "+s.err.Error()))
	case time.Since(s.savedAt) < 2*time.Second:
		b.WriteString("\n" + npStyle.Render("saved ✓"))
	}

	b.WriteString("\n\n")
	b.WriteString(statsBody)

	// Pad up to bodyHeight so the parent's footer stays pinned to the
	// bottom of the terminal regardless of how much content fits.
	return lipgloss.NewStyle().Height(bodyHeight).Render(b.String())
}

// renderField shows one settings row: cursor / edit marker on the left,
// then the huh field's own View() with each subsequent line indented to
// align under the first.
func (s settingsModel) renderField(i int, f huh.Field) string {
	marker := "  "
	switch {
	case i == s.cursor && s.editing:
		marker = npStyle.Render("✎ ")
	case i == s.cursor:
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

// buildSettingsFields wires up one huh Field per editable setting. The
// fields are used as standalone widgets — there is no wrapping form, so
// huh's default keymap must be applied manually (Form/Group normally do
// this; without it, key.Matches finds nothing in the field's zero-valued
// keymap and navigation silently does nothing).
func buildSettingsFields(v *settingsValues) []huh.Field {
	viewOpts := []huh.Option[string]{
		huh.NewOption("list", "list"),
		huh.NewOption("grid", "grid"),
	}
	fields := []huh.Field{
		huh.NewInput().Title("Server URL").Value(&v.ServerURL).Validate(validateServerURL),
		huh.NewInput().Title("Token").EchoMode(huh.EchoModePassword).Value(&v.Token).Validate(validateToken),
		huh.NewInput().
			Title("Default library").
			Description("Section name or key. Leave empty to show the picker on startup.").
			Value(&v.DefaultLibrary),
		huh.NewSelect[string]().Title("Default view (Artists)").Height(3).Options(viewOpts...).Value(&v.ViewArtist),
		huh.NewSelect[string]().Title("Default view (Albums)").Height(3).Options(viewOpts...).Value(&v.ViewAlbum),
		huh.NewSelect[string]().Title("Home screen").Height(3).Options(
			huh.NewOption("dashboard", "dashboard"),
			huh.NewOption("library", "library"),
		).Value(&v.Home),
		huh.NewSelect[bool]().Title("Inline artwork").Height(3).Options(
			huh.NewOption("off", false),
			huh.NewOption("on", true),
		).Value(&v.Images),
	}
	km := huh.NewDefaultKeyMap()
	for i, f := range fields {
		fields[i] = f.WithKeyMap(km)
	}
	return fields
}

// validateServerURL allows the empty string (config can be partially set
// while the user is figuring things out) but rejects anything that isn't
// a valid http(s) URL — those values would fail at connect time anyway.
func validateServerURL(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("not a valid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}

// validateToken is intentionally light — Plex tokens are opaque strings
// with no fixed format, so we only reject whitespace.
func validateToken(s string) error {
	if s == "" {
		return nil
	}
	if strings.ContainsAny(s, " \t\n") {
		return fmt.Errorf("token must not contain whitespace")
	}
	return nil
}

// normalizeView coerces a stored view setting to a known option, defaulting
// to "list" so a missing or invalid value displays sensibly.
func normalizeView(v string) string {
	if v == "grid" {
		return "grid"
	}
	return "list"
}

// normalizeHome coerces a stored home setting to a known option,
// defaulting to "library" for empty / unknown values.
func normalizeHome(v string) string {
	if v == "dashboard" {
		return "dashboard"
	}
	return "library"
}

// cacheStatsBody renders the read-only status sections shown under the
// editable settings. It lives outside settingsModel because library and
// player state is owned by the parent. mpvReady reports whether the mpv
// subprocess started successfully; playerErr, if non-nil, is the reason
// it did not.
func cacheStatsBody(lib *library.Library, syncing bool, libErr error, sp spinner.Model, mpvReady bool, playerErr error) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Library cache"))
	b.WriteString("\n")
	if lib == nil {
		b.WriteString(settingRow("Status", helpStyle.Render("syncing…")))
		return b.String()
	}
	path, _ := library.CachePath(lib.SectionUUID)
	b.WriteString(settingRow("Path", path))
	b.WriteString(settingRow("Last synced", formatSyncedAt(lib.SyncedAt)))
	b.WriteString(settingRow("Entries", entryBreakdown(lib)))
	b.WriteString(settingRow("File size", cacheSize(path)))
	b.WriteString(settingRow("Schema", fmt.Sprintf("v%d", lib.SchemaVersion)))
	if syncing {
		b.WriteString(settingRow("", helpStyle.Render(sp.View()+"re-syncing from Plex…")))
	} else if libErr != nil {
		b.WriteString(settingRow("", errStyle.Render("error: "+libErr.Error())))
	}

	// Image cache footprint — same shape (path / files / size) as the
	// library cache above so users can see at a glance how much disk
	// the thumb cache is using and clear it manually if they want.
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("Image cache"))
	b.WriteString("\n")
	if imgStats, err := imgcache.GetStats(); err == nil {
		b.WriteString(settingRow("Path", imgStats.Path))
		switch {
		case imgStats.Missing:
			b.WriteString(settingRow("Status", helpStyle.Render("(empty — no thumbs fetched yet)")))
		default:
			b.WriteString(settingRow("Files", fmt.Sprintf("%d", imgStats.Files)))
			b.WriteString(settingRow("Disk size", humanBytes(imgStats.Bytes)))
			b.WriteString(settingRow("", helpStyle.Render("press C to clear (disk + terminal cache)")))
		}
	} else {
		b.WriteString(settingRow("", errStyle.Render("error: "+err.Error())))
	}

	// Playback dependency. The stderr warning from main.go scrolls off
	// behind the TUI, so this is the place users actually see why their
	// play / enqueue keys are silently no-opping.
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("Playback"))
	b.WriteString("\n")
	if mpvReady {
		b.WriteString(settingRow("mpv", "ready"))
	} else {
		reason := "not detected on PATH"
		if playerErr != nil {
			reason = playerErr.Error()
		}
		b.WriteString(settingRow("mpv", errStyle.Render(reason)))
		b.WriteString(settingRow("", helpStyle.Render("install mpv (https://mpv.io/) and relaunch to enable playback")))
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

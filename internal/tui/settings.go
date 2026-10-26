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

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/imgcache"
	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/textutil"
)

// settingsValues holds the strings bound to each huh field. Stored on the
// settings sub-model so the pointers remain stable across renders.
type settingsValues struct {
	Backend         string
	ServerURL       string
	Token           string
	Username        string
	Password        string
	DefaultLibrary  string
	ViewArtist      string
	ViewAlbum       string
	Home            string
	Images          bool
	DownloadFolder  string
	DownloadQuality string
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
// settingsField pairs a huh field with an optional visibility predicate.
// Fields whose visible() returns false (e.g. the Plex token while the
// Jellyfin backend is selected) are skipped by both navigation and
// rendering — they still exist and keep their bound value, they're just
// hidden until relevant. A nil predicate means always visible.
type settingsField struct {
	field   huh.Field
	visible func(v *settingsValues) bool
}

type settingsModel struct {
	fields  []settingsField
	values  *settingsValues
	cursor  int
	editing bool
	savedAt time.Time
	err     error
	// relaunch is set once the user changes a connection setting under a
	// running client; the View then shows a persistent relaunch notice.
	relaunch bool
	// before is the bound values as they were when editing started, so
	// esc can put them back (huh writes into them on every keystroke).
	before settingsValues
	// size is the last window size, replayed into a field rebuilt on
	// cancel so it lays out like its siblings.
	size tea.WindowSizeMsg
}

// fieldVisible reports whether the field at index i should be shown given
// the current values (the selected backend, mainly).
func (s settingsModel) fieldVisible(i int) bool {
	f := s.fields[i]
	return f.visible == nil || f.visible(s.values)
}

// nextVisible returns the next visible field index from `from` moving in
// dir (+1 / -1), or `from` itself if there's no visible field that way.
func (s settingsModel) nextVisible(from, dir int) int {
	for i := from + dir; i >= 0 && i < len(s.fields); i += dir {
		if s.fieldVisible(i) {
			return i
		}
	}
	return from
}

// newSettingsModel builds the sub-model from an initial config snapshot.
// The bound values are seeded from cfg; subsequent edits are read back
// out via values() when the parent commits.
func newSettingsModel(cfg config.Config) settingsModel {
	v := &settingsValues{
		Backend:        normalizeBackend(cfg.Backend),
		ServerURL:      cfg.ServerURL,
		Token:          cfg.PlexToken,
		Username:       cfg.JellyfinUsername,
		Password:       cfg.JellyfinPassword,
		DefaultLibrary: cfg.DefaultLibrary,
		ViewArtist:     normalizeView(cfg.DefaultViewArtist),
		ViewAlbum:      normalizeView(cfg.DefaultViewAlbum),
		Home:           normalizeHome(cfg.Home),
		Images:         cfg.Images,
		DownloadFolder: cfg.DownloadFolder,
		// Round-tripped through the parser so a stale or hand-typo'd
		// config value shows the picker on the option it actually
		// behaves as, rather than on nothing.
		DownloadQuality: media.ParseDownloadQuality(cfg.DownloadQuality).String(),
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
		cmds = append(cmds, f.field.Init())
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
		s.cursor = s.nextVisible(s.cursor, -1)
		return s, nil, settingsOutcomeNone
	case key.Matches(msg, km.Down):
		s.cursor = s.nextVisible(s.cursor, +1)
		return s, nil, settingsOutcomeNone
	case key.Matches(msg, km.Enter):
		if s.cursor < 0 || s.cursor >= len(s.fields) {
			return s, nil, settingsOutcomeNone
		}
		s.editing = true
		s.before = *s.values
		return s, s.fields[s.cursor].field.Focus(), settingsOutcomeNone
	case key.Matches(msg, km.Refresh):
		return s, nil, settingsOutcomeRefresh
	case key.Matches(msg, km.PurgeImgs):
		return s, nil, settingsOutcomePurgeImgs
	}
	return s, nil, settingsOutcomeNone
}

// handleEditKey processes a keystroke while a field is focused. Most
// keys forward to the huh field; InputEnter commits (the bound value is
// already up-to-date) and exits edit mode, refused while the field's own
// validation is failing; InputBack cancels, restoring the old value.
func (s settingsModel) handleEditKey(msg tea.KeyPressMsg, km KeyMap) (settingsModel, tea.Cmd, settingsOutcome) {
	if key.Matches(msg, km.Quit) {
		return s, tea.Quit, settingsOutcomeNone
	}

	updated, cmd := s.fields[s.cursor].field.Update(msg)
	if f, ok := updated.(huh.Field); ok {
		s.fields[s.cursor].field = f
	}

	// Esc abandons the edit: restore the values from before it began and
	// rebuild the field, whose own widget state (typed text, highlighted
	// option) would otherwise still show the abandoned input.
	if key.Matches(msg, km.InputBack) {
		*s.values = s.before
		f := buildSettingsFields(s.values)[s.cursor].field
		if sized, _ := f.Update(s.size); sized != nil {
			if hf, ok := sized.(huh.Field); ok {
				f = hf
			}
		}
		s.fields[s.cursor].field = f
		s.editing = false
		if !s.fieldVisible(s.cursor) {
			s.cursor = s.nextVisible(s.cursor, -1)
		}
		return s, nil, settingsOutcomeNone
	}

	// Input* bindings (arrows / enter / esc only) so vim-letter aliases
	// on Enter/Back don't trigger commit when the user types "l" or "h"
	// into the field they're editing.
	if key.Matches(msg, km.InputEnter) {
		if s.fields[s.cursor].field.Error() != nil {
			return s, cmd, settingsOutcomeNone
		}
		blurCmd := s.fields[s.cursor].field.Blur()
		s.editing = false
		// Editing the Backend select may have hidden the field the cursor
		// sits on (it doesn't today — backend is always visible — but keep
		// the cursor on a visible field defensively).
		if !s.fieldVisible(s.cursor) {
			s.cursor = s.nextVisible(s.cursor, -1)
		}
		return s, tea.Batch(cmd, blurCmd), settingsOutcomeCommit
	}
	return s, cmd, settingsOutcomeNone
}

// ForwardMsg fans a non-key message (window resize, huh init/focus
// cmds) to every field so each can advance its internal state. Safe to
// call from any context — fields ignore msgs they don't care about.
func (s settingsModel) ForwardMsg(msg tea.Msg) (settingsModel, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		s.size = ws
	}
	var cmds []tea.Cmd
	for i, f := range s.fields {
		updated, c := f.field.Update(msg)
		if fld, ok := updated.(huh.Field); ok {
			s.fields[i].field = fld
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

// NoteRelaunchRequired marks that a connection setting changed under a
// running client, so the View shows a persistent relaunch notice.
func (s *settingsModel) NoteRelaunchRequired() { s.relaunch = true }

// Values returns the current bound values so the parent can copy them
// into its own config on commit.
func (s settingsModel) Values() settingsValues { return *s.values }

// IsEditing reports whether a field currently has focus.
func (s settingsModel) IsEditing() bool { return s.editing }

// View renders the settings body. The caller composes header / now-playing
// / footer around this. spin and stats are pulled from parent state since
// the sub-model doesn't own the spinner or library snapshot.
func (s settingsModel) View(bodyHeight int, statsBody string) string {
	// The header and status notices stay pinned above the scrolling part:
	// a save error or the relaunch warning must be seen even when the
	// cursor is far down the page.
	var head strings.Builder
	head.WriteString(headerStyle.Render("amptui") + "  " + crumbStyle.Render("Settings /") + "\n\n")

	// Surface config-file parse errors so the user sees why their on-disk
	// values aren't taking effect (Load is lenient and would otherwise
	// silently drop everything from a malformed file).
	if w := config.LastLoadWarning; w != nil {
		head.WriteString(errStyle.Render("config file error: "+w.Err.Error()) + "\n")
		if w.BackupPath != "" {
			head.WriteString(helpStyle.Render("  the original file was saved to "+w.BackupPath+" before being overwritten") + "\n\n")
		} else {
			head.WriteString(helpStyle.Render("  fix the file or correct values below; saving here backs up the original first") + "\n\n")
		}
	}
	switch {
	case s.err != nil:
		head.WriteString(errStyle.Render("save error: "+s.err.Error()) + "\n")
	case time.Since(s.savedAt) < 2*time.Second:
		head.WriteString(npStyle.Render("saved ✓") + "\n")
	}
	// Persistent until relaunch — connection changes are saved but the
	// running client keeps the old ones.
	if s.relaunch {
		head.WriteString(errStyle.Render("⚠ relaunch amptui for the new server / credentials to take effect") + "\n")
	}

	var b strings.Builder
	b.WriteString(sectionStyle.Render("Server"))
	b.WriteString("\n")
	cursorTop, cursorBottom, lastVisible := 0, 0, -1
	for i := range s.fields {
		if !s.fieldVisible(i) {
			continue
		}
		row := s.renderField(i, s.fields[i].field)
		if i == s.cursor {
			cursorTop = strings.Count(b.String(), "\n")
			cursorBottom = cursorTop + strings.Count(row, "\n") - 1
		}
		lastVisible = i
		b.WriteString(row)
	}
	b.WriteString("\n")
	b.WriteString(statsBody)
	if s.cursor == lastVisible {
		// The cache stats below the last field have no cursor stop of
		// their own; reaching the last field scrolls them into view.
		cursorBottom = strings.Count(b.String(), "\n")
	}

	// Everything below the pinned part scrolls: the page is usually
	// taller than the terminal, and the window follows the cursor field,
	// staying at the top while it fits.
	lines := strings.Split(b.String(), "\n")
	window := max(bodyHeight-strings.Count(head.String(), "\n"), 1)
	offset := 0
	if cursorBottom >= window {
		offset = cursorBottom - window + 1
	}
	offset = min(offset, cursorTop, max(len(lines)-window, 0))
	lines = lines[offset:min(offset+window, len(lines))]

	// Pad up to bodyHeight so the parent's footer stays pinned to the
	// bottom of the terminal regardless of how much content fits.
	return lipgloss.NewStyle().Height(bodyHeight).Render(head.String() + strings.Join(lines, "\n"))
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
func buildSettingsFields(v *settingsValues) []settingsField {
	viewOpts := []huh.Option[string]{
		huh.NewOption("list", "list"),
		huh.NewOption("grid", "grid"),
	}
	isPlex := func(v *settingsValues) bool { return v.Backend != "jellyfin" }
	isJellyfin := func(v *settingsValues) bool { return v.Backend == "jellyfin" }
	fields := []settingsField{
		{field: huh.NewSelect[string]().Title("Backend").Height(3).Options(
			huh.NewOption("plex", "plex"),
			huh.NewOption("jellyfin", "jellyfin"),
		).Value(&v.Backend)},
		{field: huh.NewInput().Title("Server URL").Value(&v.ServerURL).Validate(validateServerURL)},
		{field: huh.NewInput().Title("Token (Plex)").EchoMode(huh.EchoModePassword).Value(&v.Token).Validate(validateToken),
			visible: isPlex},
		{field: huh.NewInput().Title("Username (Jellyfin)").Value(&v.Username),
			visible: isJellyfin},
		{field: huh.NewInput().Title("Password (Jellyfin)").EchoMode(huh.EchoModePassword).Value(&v.Password),
			visible: isJellyfin},
		{field: huh.NewInput().
			Title("Default library").
			Description("Section name or key. Leave empty to show the picker on startup.").
			Value(&v.DefaultLibrary)},
		{field: huh.NewSelect[string]().Title("Default view (Artists)").Height(3).Options(viewOpts...).Value(&v.ViewArtist)},
		{field: huh.NewSelect[string]().Title("Default view (Albums)").Height(3).Options(viewOpts...).Value(&v.ViewAlbum)},
		{field: huh.NewSelect[string]().Title("Home screen").Height(3).Options(
			huh.NewOption("dashboard", "dashboard"),
			huh.NewOption("library", "library"),
		).Value(&v.Home)},
		{field: huh.NewSelect[bool]().Title("Inline artwork").Height(3).Options(
			huh.NewOption("off", false),
			huh.NewOption("on", true),
		).Value(&v.Images)},
		{field: huh.NewInput().
			Title("Download folder").
			Description("Where `d` saves tracks and albums. Empty disables downloads.").
			Value(&v.DownloadFolder)},
		// Height counts the whole block, so it has to cover the title and
		// the description line on top of every option — one short and the
		// last preset scrolls out of view.
		{field: huh.NewSelect[string]().
			Title("Download quality").
			Description("What `d` asks the server for. Playback is always the original.").
			Height(len(media.DownloadQualities) + 2).
			Options(downloadQualityOptions()...).
			Value(&v.DownloadQuality)},
	}
	km := huh.NewDefaultKeyMap()
	for i := range fields {
		fields[i].field = fields[i].field.WithKeyMap(km)
	}
	return fields
}

// downloadQualityOptions turns the preset list into picker options, so
// the menu and the config vocabulary can't drift apart: the label is
// what the user reads, the value is exactly what lands in config.toml.
func downloadQualityOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(media.DownloadQualities))
	for _, q := range media.DownloadQualities {
		opts = append(opts, huh.NewOption(q.Label(), q.String()))
	}
	return opts
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

// normalizeBackend coerces a stored backend setting to a known option,
// defaulting to "plex" for empty / unknown values.
func normalizeBackend(v string) string {
	if v == "jellyfin" {
		return "jellyfin"
	}
	return "plex"
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
func cacheStatsBody(lib *library.Library, ds *diskStats, syncing bool, libErr error, sp spinner.Model, mpvReady bool, playerErr error) string {
	if ds == nil {
		ds = &diskStats{}
	}
	ds.refresh(lib)
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Library cache"))
	b.WriteString("\n")
	if lib == nil {
		b.WriteString(settingRow("Status", helpStyle.Render("syncing…")))
		return b.String()
	}
	b.WriteString(settingRow("Path", ds.libPath))
	b.WriteString(settingRow("Last synced", formatSyncedAt(lib.SyncedAt)))
	b.WriteString(settingRow("Entries", ds.entries))
	b.WriteString(settingRow("File size", ds.libSize))
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
	if imgStats, err := ds.img, ds.imgErr; err == nil {
		b.WriteString(settingRow("Path", imgStats.Path))
		switch {
		case imgStats.Files == 0:
			b.WriteString(settingRow("Status", helpStyle.Render("(empty — no thumbs fetched yet)")))
		default:
			b.WriteString(settingRow("Files", fmt.Sprintf("%d", imgStats.Files)))
			b.WriteString(settingRow("Disk size", textutil.HumanBytes(imgStats.Bytes)+
				helpStyle.Render(" of "+textutil.HumanBytes(imgStats.Max)+", oldest evicted first")))
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

	b.WriteString("\n")
	b.WriteString(settingRow("Config file", ds.cfgPath))
	return b.String()
}

// diskStats caches the parts of the settings stats that cost disk I/O
// (a stat per cached image, the cache file size) or a pass over every
// library entry. The settings screen renders on every frame, so these
// are re-read at most every diskStatsTTL, or sooner when the library
// changes or invalidate is called. Held by pointer on the Model so the
// cache survives Bubble Tea's per-update copies.
type diskStats struct {
	at      time.Time
	lib     *library.Library
	libPath string
	libSize string
	entries string
	img     imgcache.Stats
	imgErr  error
	cfgPath string
}

const diskStatsTTL = 2 * time.Second

func (ds *diskStats) refresh(lib *library.Library) {
	if lib == ds.lib && time.Since(ds.at) < diskStatsTTL {
		return
	}
	*ds = diskStats{at: time.Now(), lib: lib}
	if lib != nil {
		ds.libPath, _ = library.CachePath(lib.SectionUUID)
		ds.libSize = cacheSize(ds.libPath)
		ds.entries = entryBreakdown(lib)
	}
	ds.img, ds.imgErr = imgcache.GetStats()
	ds.cfgPath, _ = config.Path()
}

// invalidate forces the next render to re-read everything.
func (ds *diskStats) invalidate() { ds.at = time.Time{} }

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
	return t.Format("2006-01-02 15:04:05") + helpStyle.Render(fmt.Sprintf("  (%s ago)", textutil.HumanDuration(d)))
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
	return textutil.HumanBytes(info.Size())
}

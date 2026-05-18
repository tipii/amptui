// Package tui implements the Bubble Tea terminal UI: a drill-down browser
// over Plex music libraries (library -> artists -> albums -> tracks), plus a
// now-playing line backed by the mpv player.
//
// The package is split across several files:
//
//   - tui.go     Model, styles, New, Init, tick
//   - update.go  Update loop and input routing
//   - view.go    rendering: View, modals, footer
//   - browse.go  drill-down navigation and async Plex fetches
//   - queue.go   playback queue and modal operations
//   - items.go   bubbles/list item types
package tui

import (
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/theopalhol/amptui/internal/config"
	"github.com/theopalhol/amptui/internal/library"
	"github.com/theopalhol/amptui/internal/player"
	"github.com/theopalhol/amptui/internal/plex"
)

// tickMsg is delivered every second to refresh the now-playing line and
// auto-advance the queue.
type tickMsg time.Time

// screen is the top-level view (browser vs. settings). Modals overlay on
// the current screen.
type screen int

const (
	screenBrowser screen = iota
	screenSettings
)

type Model struct {
	cfg    config.Config
	client *plex.Client
	player *player.Player // may be nil if mpv is unavailable

	// keymap is the single source of truth for keybindings; Update routes
	// via key.Matches against it and helpModel renders footer/help-modal
	// text from its Help() output.
	keymap    KeyMap
	helpModel help.Model

	// libs is the full list of music libraries on the server, kept around
	// so search jumps can synthesize a Libraries crumb at any depth.
	libs []plex.MusicLibrary

	screen screen

	// Settings-screen state. Each field is a standalone huh.Field — we
	// drive navigation between them with j/k and per-field commit-on-save
	// ourselves. settingsValues holds stable pointers for the fields to
	// bind to; on commit we copy back to cfg and Save().
	settingsFields  []huh.Field
	settingsValues  *settingsValues
	settingsCursor  int  // which field is highlighted
	settingsEditing bool // true while keys are routed into the focused field
	settingsSavedAt time.Time
	settingsErr     error

	list         list.Model
	queueList    list.Model      // shown in the queue modal
	helpViewport viewport.Model  // scrollable body of the help modal
	spinner      spinner.Model

	level      level
	crumbs     []crumb
	loading    bool
	err        error
	nowPlaying *plex.Track

	// queue is the current playback queue; queueIdx is the playing track.
	// On track end the UI advances through it, clearing nowPlaying when
	// the queue is exhausted.
	queue    []plex.Track
	queueIdx int

	// showQueue / showHelp / showSearch are true while their modal is
	// open; an open modal owns input.
	showQueue  bool
	showHelp   bool
	showSearch bool

	// gridArtists / gridAlbums hold the current grid-vs-list preference for
	// each level. They start from cfg.DefaultViewArtist / DefaultViewAlbum
	// and flip on tab. gridCursor / gridScrollTop are shared — they're
	// only meaningful while the current level is actually in grid mode.
	gridArtists   bool
	gridAlbums    bool
	gridCursor    int
	gridScrollTop int

	// Search-modal state.
	searchInput   textinput.Model
	searchResults []library.Entry
	searchCursor  int
	searchFilter  int // index into searchFilters / searchFilterNames

	// library is the cache for the active section; nil until the background
	// loader resolves. librarySyncing drives the status-bar indicator.
	library        *library.Library
	libraryErr     error
	librarySyncing bool

	// startupLibrary, if set, is fetched on Init so the UI opens straight
	// into that library instead of the picker.
	startupLibrary *plex.MusicLibrary

	width, height int
}

// New builds the initial model showing the given music libraries. player may
// be nil, in which case browsing works but playback is disabled. If
// defaultLib is non-nil, the UI opens straight into that library, with a
// "Libraries" crumb pushed so the user can still go back to the picker.
// cfg is used by the settings screen (read-only display).
func New(cfg config.Config, client *plex.Client, p *player.Player, libs []plex.MusicLibrary, defaultLib *plex.MusicLibrary) Model {
	items := make([]list.Item, len(libs))
	for i, l := range libs {
		items[i] = libraryItem{lib: l}
	}

	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Libraries"
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)

	ql := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	ql.SetShowTitle(false) // the modal box draws its own title
	ql.SetShowHelp(false)
	ql.SetShowStatusBar(false)
	ql.SetFilteringEnabled(false) // cursor index must map 1:1 to the queue

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	si := textinput.New()
	si.Placeholder = "search artists, albums, tracks…"
	si.Prompt = "> "


	hv := viewport.New()
	hv.FillHeight = true

	m := Model{
		cfg:            cfg,
		client:         client,
		player:         p,
		keymap:         NewKeyMap(),
		helpModel:      help.New(),
		libs:           libs,
		list:           l,
		queueList:      ql,
		helpViewport:   hv,
		spinner:        sp,
		searchInput:    si,
		settingsValues: newSettingsValues(cfg),
		// settingsFields wired below — needs m.settingsValues' stable pointer.
		level:          levelLibraries,
		librarySyncing: true, // Init kicks off the background library sync
		gridArtists:    cfg.DefaultViewArtist == "grid",
		gridAlbums:     cfg.DefaultViewAlbum == "grid",
	}

	if defaultLib != nil {
		// Push a Libraries crumb (highlighting the default) so goBack from
		// the artist list still reaches the picker, then mark the artist
		// fetch pending — Init fires it.
		idx := 0
		for i := range libs {
			if libs[i].Key == defaultLib.Key {
				idx = i
				break
			}
		}
		m.crumbs = append(m.crumbs, crumb{
			level: levelLibraries,
			title: defaultLib.Title,
			items: items,
			index: idx,
		})
		m.loading = true
		m.startupLibrary = defaultLib
	}

	m.settingsFields = buildSettingsFields(m.settingsValues)
	m.helpViewport.SetContent(m.helpBodyContent())

	// If the config is missing/invalid (no server URL or token), there's
	// nothing to browse — open straight into settings so the user can
	// enter their credentials inline.
	if !cfg.IsValid() {
		m.screen = screenSettings
		m.librarySyncing = false
	}
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, tick()}
	for _, f := range m.settingsFields {
		cmds = append(cmds, f.Init())
	}
	// Kick off the library sync in the background only when we actually
	// have a Plex client and at least one library to sync. Missing config
	// drops us on the settings screen with no background work to do.
	if m.client != nil && len(m.libs) > 0 {
		active := m.libs[0]
		if m.startupLibrary != nil {
			active = *m.startupLibrary
		}
		cmds = append(cmds, loadOrSyncLibrary(m.client, active))
	}
	return tea.Batch(cmds...)
}

// tick drives a once-a-second refresh so the now-playing line stays current.
func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

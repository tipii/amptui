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
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

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
	screenDashboard screen = iota
	screenBrowser
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

	// settings is the sub-model that owns the settings screen state and
	// its huh fields. The parent forwards key/window-size msgs into it and
	// applies its outcomes (close / refresh / commit).
	settings settingsModel

	// dashboard is the home-screen sub-model: three live-fetched tiles
	// (recently played / recently added / recent playlists). Parent
	// forwards keys when on screenDashboard and acts on outcomes
	// (play track / open album / open playlist).
	dashboard dashboardModel

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

	// showQueue / showHelp are true while their modal is open; an open
	// modal owns input. The search modal's open state lives on m.search.
	showQueue bool
	showHelp  bool

	// search is the fuzzy-finder sub-model; the parent forwards keys via
	// routeSearchKey and applies its outcomes.
	search searchModel

	// gridArtists / gridAlbums hold the current grid-vs-list preference for
	// each level. They start from cfg.DefaultViewArtist / DefaultViewAlbum
	// and flip on tab. gridCursor / gridScrollTop are shared — they're
	// only meaningful while the current level is actually in grid mode.
	gridArtists   bool
	gridAlbums    bool
	gridCursor    int
	gridScrollTop int

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
		search:         newSearchModel(),
		settings:       newSettingsModel(cfg),
		dashboard:      newDashboardModel(),
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

	m.helpViewport.SetContent(m.helpBodyContent())

	// Honor the user's preferred home screen. screenDashboard is the
	// zero value (and the default); flip to screenBrowser when they've
	// asked for the library as their landing page.
	if cfg.Home == "library" {
		m.screen = screenBrowser
	}

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
	cmds = append(cmds, m.settings.Init())
	// Kick off the library sync AND the dashboard's three live fetches
	// in the background when we have a Plex client and at least one
	// library. Missing config drops us on the settings screen with no
	// background work to do.
	if m.client != nil && len(m.libs) > 0 {
		active := m.libs[0]
		if m.startupLibrary != nil {
			active = *m.startupLibrary
		}
		cmds = append(cmds, loadOrSyncLibrary(m.client, active))
		cmds = append(cmds, m.dashboard.Load(m.client, active.Key))
	}
	return tea.Batch(cmds...)
}

// tick drives a once-a-second refresh so the now-playing line stays current.
func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

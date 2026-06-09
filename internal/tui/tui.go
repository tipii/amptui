// Package tui implements the Bubble Tea terminal UI: a drill-down browser
// over Plex music libraries (library -> artists -> albums -> tracks), plus a
// now-playing line backed by the mpv player.
//
// The package is split across several files:
//
//   - tui.go        Model, New, Init, tick
//   - update.go     Update loop and input routing
//   - view.go       screen composition + shared layout helpers
//   - nowplaying.go the now-playing block and track-position bar
//   - info.go       artist/album metadata header + the `i` info modal
//   - modals.go     the shared modal frame + queue/search/help bodies
//   - browse.go     drill-down navigation and async Plex fetches
//   - queue.go      playback queue delegated to mpv
//   - grid.go       artist/album grid rendering
//   - theme.go      colors + styles
//   - items.go      bubbles/list item types
package tui

import (
	"crypto/sha1"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/NimbleMarkets/ntcharts/v2/picture"

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/imgcache"
	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/player"
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
	cfg config.Config
	// playerErr is captured when player.New() fails at startup (e.g. mpv not
	// on PATH). The TUI takes over the screen before the user can see the
	// stderr warning, so the settings screen surfaces this reason.
	playerErr error
	client    media.Backend
	player    *player.Player // may be nil if mpv is unavailable

	// keymap is the single source of truth for keybindings; Update routes
	// via key.Matches against it and helpModel renders footer/help-modal
	// text from its Help() output.
	keymap    KeyMap
	helpModel help.Model

	// libs is the full list of music libraries on the server, kept around
	// so search jumps can synthesize a Libraries crumb at any depth.
	libs []media.MusicLibrary

	// serverName is the backend's friendly name, fetched async at startup
	// and shown as the first segment of the browser breadcrumb.
	serverName string

	// Download state. downloadJobs is the worker's queue + history;
	// nextDownloadJobID hands out stable ids for tick messages to target.
	// showDownloads toggles the D-modal. downloadStatus is a transient
	// footer line (hints + progress + summary) styled by downloadErr.
	downloadJobs      []*downloadJob
	nextDownloadJobID int
	showDownloads     bool
	downloadStatus    string
	downloadErr       bool

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
	queueList    list.Model     // shown in the queue modal
	helpViewport viewport.Model // scrollable body of the help modal
	infoViewport viewport.Model // scrollable body of the artist/album info modal
	spinner      spinner.Model
	// progress is the now-playing track-position bar (original bubbles
	// styling: accent ▌ fill, ░ empty track). progressBar recolors the
	// buffered-ahead ░ cells to accent so they read as a faint dotted
	// region between the playhead and the unbuffered tail.
	progress progress.Model

	level      level
	crumbs     []crumb
	loading    bool
	err        error
	nowPlaying *media.Track

	// Rich metadata fetched lazily for the artist whose albums are
	// being browsed, or the album whose tracks are. Each is nil until
	// the per-screen fetch resolves; metaLoading drives a "loading…"
	// hint in the header during the fetch.
	artistMeta  *media.ArtistMetadata
	albumMeta   *media.AlbumMetadata
	metaLoading bool

	// Inline-artwork state. Each surface (artist/album hero header,
	// info modal, grid card, list row) owns a picture.Model — a
	// Bubble-Tea-aware component from ntcharts that handles glyph
	// (half-block) AND Kitty graphics protocol, including the
	// Ghostty redraw bug we hit when emitting Kitty sequences
	// ourselves. picMode is detected once at startup; surfaces fall
	// back to half-block on terminals without Kitty support.
	artistHeaderPic picture.Model
	artistModalPic  picture.Model
	albumHeaderPic  picture.Model
	albumModalPic   picture.Model
	gridPics        map[string]*picture.Model
	// listPics holds the small list-view variant of the same thumb,
	// sized for one bubbles/list row. Built alongside gridPics from the
	// same fetched image so toggling between list / grid never refetches.
	listPics map[string]*picture.Model
	picMode  picture.PictureMode

	// queue is the current playback queue; queueIdx is the playing track.
	// On track end the UI advances through it, clearing nowPlaying when
	// the queue is exhausted.
	queue    []media.Track
	queueIdx int

	// showQueue / showHelp / showInfo are true while their modal is
	// open; an open modal owns input. The search modal's open state
	// lives on m.search.
	showQueue bool
	showHelp  bool
	showInfo  bool

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
	startupLibrary *media.MusicLibrary

	width, height int
}

// New builds the initial model showing the given music libraries. player may
// be nil, in which case browsing works but playback is disabled. If
// defaultLib is non-nil, the UI opens straight into that library, with a
// "Libraries" crumb pushed so the user can still go back to the picker.
// cfg is used by the settings screen (read-only display). playerErr, if
// non-nil, is the reason mpv failed to start — surfaced in settings.
func New(cfg config.Config, client media.Backend, p *player.Player, playerErr error, libs []media.MusicLibrary, defaultLib *media.MusicLibrary) Model {
	items := make([]list.Item, len(libs))
	for i, l := range libs {
		items[i] = libraryItem{lib: l}
	}

	// gridPics holds the grid-card-sized thumb per RatingKey; listPics
	// holds the small list-row variant. The list delegate reads from
	// listPics so toggling list ⇄ grid renders the right size for the
	// current view. Both maps are populated from a single fetch (see
	// thumbReadyMsg "grid:…" handling) so switching never refetches.
	gridPics := map[string]*picture.Model{}
	listPics := map[string]*picture.Model{}
	l := list.New(items, newThumbDelegate(listPics), 0, 0)
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

	// Now-playing position bar — default bubbles styling (accent ▌
	// fill, ░ empty). progressBar tints the buffered-ahead empties.
	pr := progress.New(
		progress.WithoutPercentage(),
		progress.WithColors(theme.Accent),
	)

	hv := viewport.New()
	hv.FillHeight = true

	iv := viewport.New()
	iv.FillHeight = true

	m := Model{
		cfg:             cfg,
		playerErr:       playerErr,
		client:          client,
		player:          p,
		keymap:          NewKeyMap(),
		helpModel:       help.New(),
		libs:            libs,
		list:            l,
		queueList:       ql,
		helpViewport:    hv,
		infoViewport:    iv,
		spinner:         sp,
		progress:        pr,
		search:          newSearchModel(),
		settings:        newSettingsModel(cfg),
		dashboard:       newDashboardModel(),
		picMode:         pictureModeFromProtocol(imgcache.Detect()),
		gridPics:        gridPics,
		listPics:        listPics,
		artistHeaderPic: newSizedPicture(headerThumbCellsW, headerThumbCellsH),
		artistModalPic:  newSizedPicture(modalThumbCellsW, modalThumbCellsH),
		albumHeaderPic:  newSizedPicture(headerThumbCellsW, headerThumbCellsH),
		albumModalPic:   newSizedPicture(modalThumbCellsW, modalThumbCellsH),
		level:           levelLibraries,
		librarySyncing:  true, // Init kicks off the background library sync
		gridArtists:     cfg.DefaultViewArtist == "grid",
		gridAlbums:      cfg.DefaultViewAlbum == "grid",
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

	// Honor the user's preferred home screen. Library is the default
	// landing page; opt in to the dashboard via cfg.Home = "dashboard".
	m.screen = screenBrowser
	if cfg.Home == "dashboard" {
		m.screen = screenDashboard
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
	// Ask the terminal for its real cell-pixel dims so the Kitty
	// path renders at the right scale. picture.Models receive the
	// reply (uv.CellSizeEvent) via Update routing.
	cmds = append(cmds, picture.RequestCellSize())
	// Kick off the library sync AND the dashboard's three live fetches
	// in the background when we have a Plex client and at least one
	// library. Missing config drops us on the settings screen with no
	// background work to do.
	if m.client != nil {
		cmds = append(cmds, fetchServerName(m.client))
	}
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

// pictureID hands out monotonically-increasing Kitty image IDs for
// picture.Models that aren't tied to a specific item (header / modal
// singletons). Every picture.Model must hold a distinct ID — Kitty
// stores one image per ID, so sharing IDs causes the last SetImage
// to overwrite all prior placements. Glyph mode ignores the field,
// so this is harmless on terminals without Kitty support.
var pictureID atomic.Int32

func nextPictureID() int {
	return int(pictureID.Add(1)) + 43 // stay clear of well-known IDs
}

// kittyIDFor returns a deterministic Kitty image ID for the given
// inputs. The same parts always produce the same ID, so when the
// terminal has cached an image at this ID from a previous run, the
// placement renders the correct image immediately — no "fallback →
// stale image → real image" flicker from a randomly-reused kittyID.
//
// IDs are encoded in 24 bits (Kitty packs them into an RGB triple),
// so collisions are birthday-bounded around ~4k items. The 256 floor
// keeps us clear of low well-known IDs.
func kittyIDFor(parts ...string) int {
	h := sha1.Sum([]byte(strings.Join(parts, "|")))
	id := int(h[0])<<16 | int(h[1])<<8 | int(h[2])
	if id < 256 {
		id += 256
	}
	return id
}

// newKeyedPicture returns a picture.Model whose Kitty ID is derived
// from the (role, ratingKey) pair. Revisiting the same item across
// app runs hands the terminal the same ID — and the image cached at
// that ID is already the correct one — so the surface paints
// instantly without a "stale image" flicker from a randomly-reused
// kittyID. role disambiguates the multiple surfaces a single item
// can have (e.g. "artist-header" vs. "artist-modal").
func newKeyedPicture(role, ratingKey string, cols, rows int) picture.Model {
	p := picture.NewWithConfig(picture.Config{KittyID: kittyIDFor(role, ratingKey)})
	p.SetSize(cols, rows)
	return p
}

// newSizedPicture returns a picture.Model pre-sized to the given cell
// rectangle with a unique Kitty image ID. Used for the placeholder
// instances New() builds at startup; per-item surfaces are
// reconstructed on drill-down via newKeyedPicture.
func newSizedPicture(cols, rows int) picture.Model {
	p := picture.NewWithConfig(picture.Config{KittyID: nextPictureID()})
	p.SetSize(cols, rows)
	return p
}

// pictureModeFromProtocol maps the imgcache-side terminal protocol
// to the picture.Model mode. Kept here so the imgcache package
// doesn't need to depend on the picture types.
func pictureModeFromProtocol(p imgcache.Protocol) picture.PictureMode {
	if p == imgcache.ProtocolKitty {
		return picture.PictureKitty
	}
	return picture.PictureGlyph
}

// applyPicMode toggles a picture.Model into the parent's preferred
// mode if it's not already there. picture.New() defaults to glyph,
// so this is a no-op in glyph terminals. Returns any cmd produced
// by the toggle (the Kitty render kick-off when applicable).
func (m Model) applyPicMode(p *picture.Model) tea.Cmd {
	if p.Mode() == m.picMode {
		return nil
	}
	return p.Toggle()
}

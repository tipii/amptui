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

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/theopalhol/amptui/internal/index"
	"github.com/theopalhol/amptui/internal/player"
	"github.com/theopalhol/amptui/internal/plex"
)

// tickMsg is delivered every second to refresh the now-playing line and
// auto-advance the queue.
type tickMsg time.Time

type Model struct {
	client *plex.Client
	player *player.Player // may be nil if mpv is unavailable

	// libs is the full list of music libraries on the server, kept around
	// so search jumps can synthesize a Libraries crumb at any depth.
	libs []plex.MusicLibrary

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

	// Search-modal state.
	searchInput   textinput.Model
	searchResults []index.Entry
	searchCursor  int
	searchFilter  int // index into searchFilters / searchFilterNames

	// index is the fuzzy-search index for the active library; nil until the
	// background loader resolves. indexLoading drives the status-bar
	// indicator.
	index        *index.Index
	indexErr     error
	indexLoading bool

	// startupLibrary, if set, is fetched on Init so the UI opens straight
	// into that library instead of the picker.
	startupLibrary *plex.MusicLibrary

	width, height int
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	crumbStyle  = lipgloss.NewStyle().Faint(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	helpStyle   = lipgloss.NewStyle().Faint(true)
	npStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("121"))
	modalStyle  = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("213")).
			Padding(0, 1)
)

// New builds the initial model showing the given music libraries. player may
// be nil, in which case browsing works but playback is disabled. If
// defaultLib is non-nil, the UI opens straight into that library, with a
// "Libraries" crumb pushed so the user can still go back to the picker.
func New(client *plex.Client, p *player.Player, libs []plex.MusicLibrary, defaultLib *plex.MusicLibrary) Model {
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
	hv.SetContent(helpBodyContent())

	m := Model{
		client:       client,
		player:       p,
		libs:         libs,
		list:         l,
		queueList:    ql,
		helpViewport: hv,
		spinner:      sp,
		searchInput:  si,
		level:        levelLibraries,
		indexLoading: true, // Init kicks off the background index build
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
			title: "Libraries",
			items: items,
			index: idx,
		})
		m.loading = true
		m.startupLibrary = defaultLib
	}

	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, tick()}
	if m.startupLibrary != nil {
		cmds = append(cmds, m.fetchArtists(m.startupLibrary.Key))
	}
	// Kick off the search index in the background. For now the active
	// library is the default (or the first one); multi-library search is a
	// follow-up.
	if len(m.libs) > 0 {
		active := m.libs[0]
		if m.startupLibrary != nil {
			active = *m.startupLibrary
		}
		cmds = append(cmds, loadOrBuildIndex(m.client, active))
	}
	return tea.Batch(cmds...)
}

// tick drives a once-a-second refresh so the now-playing line stays current.
func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

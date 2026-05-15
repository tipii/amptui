// Package tui implements the Bubble Tea terminal UI: a drill-down browser
// over Plex music libraries (library -> artists -> albums -> tracks), plus a
// now-playing line backed by the mpv player.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/theopalhol/amptui/internal/player"
	"github.com/theopalhol/amptui/internal/plex"
)

// fetchTimeout bounds each Plex request fired from the UI.
const fetchTimeout = 15 * time.Second

type level int

const (
	levelLibraries level = iota
	levelArtists
	levelAlbums
	levelTracks
)

// crumb is a saved navigation frame, restored when the user goes back.
type crumb struct {
	level level
	title string
	items []list.Item
	index int
}

// Messages delivered by async fetch commands and the playback ticker.
type (
	artistsMsg []list.Item
	albumsMsg  []list.Item
	tracksMsg  []list.Item
	errMsg     struct{ err error }
	tickMsg    time.Time
)

type Model struct {
	client *plex.Client
	player *player.Player // may be nil if mpv is unavailable

	list      list.Model
	queueList list.Model // shown in the queue modal
	spinner   spinner.Model

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

	// showQueue is true while the queue modal is open; it then owns input.
	showQueue bool

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

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := Model{
		client:    client,
		player:    p,
		list:      l,
		queueList: ql,
		spinner:   sp,
		level:     levelLibraries,
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
	return tea.Batch(cmds...)
}

// tick drives a once-a-second refresh so the now-playing line stays current.
func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, m.listHeight())
		// The queue list lives inside the modal box: subtract the border
		// (2), horizontal padding (2), and one row for the box title.
		mw, mh := m.modalSize()
		m.queueList.SetSize(mw-4, mh-3)
		return m, nil

	case tea.KeyPressMsg:
		// The queue modal owns input while it is open.
		if m.showQueue {
			switch msg.String() {
			case "ctrl+c", "ctrl+q":
				return m, tea.Quit
			case "o", "esc":
				m.showQueue = false
				return m, nil
			}
			var cmd tea.Cmd
			m.queueList, cmd = m.queueList.Update(msg)
			return m, cmd
		}
		// Let the list own keys while it is filtering (typing a query).
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit
		case "enter", "l", "right":
			return m.drillDown()
		case "esc", "backspace", "h", "left":
			return m.goBack()
		case "q":
			return m.enqueueSelectedTrack(), nil
		case "Q":
			return m.enqueueSelectedAlbum(), nil
		case "o":
			m.openQueue()
			return m, nil
		case "space":
			if m.player != nil {
				_ = m.player.TogglePause()
			}
			return m, nil
		case ",":
			if m.player != nil {
				_ = m.player.Seek(-10 * time.Second)
			}
			return m, nil
		case ".":
			if m.player != nil {
				_ = m.player.Seek(10 * time.Second)
			}
			return m, nil
		}

	case artistsMsg:
		m.applyItems(levelArtists, []list.Item(msg))
		return m, nil
	case albumsMsg:
		m.applyItems(levelAlbums, []list.Item(msg))
		return m, nil
	case tracksMsg:
		m.applyItems(levelTracks, []list.Item(msg))
		return m, nil
	case errMsg:
		m.loading = false
		m.err = msg.err
		// Undo the crumb we optimistically pushed before fetching.
		if n := len(m.crumbs); n > 0 {
			m.crumbs = m.crumbs[:n-1]
		}
		return m, nil

	case tickMsg:
		m = m.advanceIfFinished()
		if m.showQueue {
			// Keep the modal's current-track marker in sync with playback,
			// preserving the user's scroll position.
			idx := m.queueList.Index()
			m.rebuildQueueList()
			m.queueList.Select(idx)
		}
		return m, tick()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// drillDown enters the selected item: fetches the next level, or for a track,
// starts playback.
func (m Model) drillDown() (tea.Model, tea.Cmd) {
	if m.loading {
		return m, nil
	}
	sel := m.list.SelectedItem()
	if sel == nil {
		return m, nil
	}

	switch it := sel.(type) {
	case libraryItem:
		m.pushCrumb()
		m.loading, m.err = true, nil
		return m, m.fetchArtists(it.lib.Key)
	case artistItem:
		m.pushCrumb()
		m.loading, m.err = true, nil
		return m, m.fetchAlbums(it.artist.RatingKey)
	case albumItem:
		m.pushCrumb()
		m.loading, m.err = true, nil
		return m, m.fetchTracks(it.album.RatingKey)
	case albumActionItem:
		return m.playTracks(it.tracks, 0)
	case trackItem:
		return m.playTracks(it.tracks, it.pos)
	}
	return m, nil
}

// playTracks sets the playback queue to tracks and starts at index start
// (playing from there to the end of the queue).
func (m Model) playTracks(tracks []plex.Track, start int) (tea.Model, tea.Cmd) {
	if m.player == nil {
		m.err = errors.New("playback unavailable: mpv is not running")
		return m, nil
	}
	if start < 0 || start >= len(tracks) {
		return m, nil
	}
	m.queue = tracks
	m.queueIdx = start
	m.loadCurrent()
	return m, nil
}

// loadCurrent loads queue[queueIdx] into the player and updates nowPlaying.
// On failure it sets m.err and leaves nowPlaying unchanged.
func (m *Model) loadCurrent() {
	t := m.queue[m.queueIdx]
	url := m.client.StreamURL(t)
	if url == "" {
		m.err = errors.New("track has no playable media")
		return
	}
	if err := m.player.Load(url); err != nil {
		m.err = fmt.Errorf("playback: %w", err)
		return
	}
	track := t
	m.nowPlaying = &track
	m.err = nil
}

// enqueue appends tracks to the playback queue. If nothing is currently
// playing, playback starts from the first appended track.
func (m *Model) enqueue(tracks ...plex.Track) {
	if len(tracks) == 0 {
		return
	}
	if m.player == nil {
		m.err = errors.New("playback unavailable: mpv is not running")
		return
	}
	wasEmpty := len(m.queue) == 0
	m.queue = append(m.queue, tracks...)
	if wasEmpty {
		m.queueIdx = 0
		m.loadCurrent()
	}
}

// enqueueSelectedTrack adds the highlighted track to the queue. It no-ops
// unless a track row is highlighted.
func (m Model) enqueueSelectedTrack() Model {
	if it, ok := m.list.SelectedItem().(trackItem); ok {
		m.enqueue(it.track)
	}
	return m
}

// enqueueSelectedAlbum adds every track of the current album to the queue.
// It works whether a track row or the "Play album" row is highlighted.
func (m Model) enqueueSelectedAlbum() Model {
	switch it := m.list.SelectedItem().(type) {
	case trackItem:
		m.enqueue(it.tracks...)
	case albumActionItem:
		m.enqueue(it.tracks...)
	}
	return m
}

// openQueue shows the queue modal, rebuilt from the current queue.
func (m *Model) openQueue() {
	m.showQueue = true
	m.rebuildQueueList()
	m.queueList.Select(m.queueIdx)
}

// rebuildQueueList repopulates the modal's list from m.queue.
func (m *Model) rebuildQueueList() {
	items := make([]list.Item, len(m.queue))
	playing := m.nowPlaying != nil
	for i, t := range m.queue {
		items[i] = queueItem{track: t, current: playing && i == m.queueIdx}
	}
	m.queueList.SetItems(items)
}

// advanceIfFinished checks whether the current track has ended and, if so,
// plays the next queued track or clears the now-playing state.
func (m Model) advanceIfFinished() Model {
	if m.player == nil || m.nowPlaying == nil {
		return m
	}
	if !m.player.State().Idle {
		return m
	}
	if m.queueIdx+1 < len(m.queue) {
		m.queueIdx++
		m.loadCurrent()
		return m
	}
	// Queue exhausted: clear the now-playing line.
	m.nowPlaying = nil
	m.queue = nil
	m.queueIdx = 0
	return m
}

// goBack restores the previous navigation frame.
func (m Model) goBack() (tea.Model, tea.Cmd) {
	if m.loading || len(m.crumbs) == 0 {
		return m, nil
	}
	c := m.crumbs[len(m.crumbs)-1]
	m.crumbs = m.crumbs[:len(m.crumbs)-1]

	m.level = c.level
	m.err = nil
	m.list.SetItems(c.items)
	m.list.Title = c.title
	m.list.Select(c.index)
	return m, nil
}

// pushCrumb saves the current frame so goBack can restore it.
func (m *Model) pushCrumb() {
	m.crumbs = append(m.crumbs, crumb{
		level: m.level,
		title: m.list.Title,
		items: m.list.Items(),
		index: m.list.Index(),
	})
}

// applyItems installs a freshly fetched level into the list.
func (m *Model) applyItems(lvl level, items []list.Item) {
	m.loading = false
	m.err = nil
	m.level = lvl
	m.list.SetItems(items)
	m.list.Select(0)
	m.list.Title = m.titleForLevel(lvl)
	m.list.SetSize(m.width, m.listHeight())
}

func (m Model) titleForLevel(lvl level) string {
	switch lvl {
	case levelArtists:
		return "Artists"
	case levelAlbums:
		return "Albums"
	case levelTracks:
		return "Tracks"
	default:
		return "Libraries"
	}
}

func (m Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	if m.width == 0 {
		v.SetContent("loading…")
		return v
	}

	background := m.browserView()
	if m.showQueue {
		v.SetContent(m.compositeModal(background))
	} else {
		v.SetContent(background)
	}
	return v
}

// browserView renders the full screen: header, browser list, now-playing
// line, and footer. The queue modal is composited on top of this.
func (m Model) browserView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("amptui"))
	if crumbs := m.crumbLine(); crumbs != "" {
		b.WriteString("  " + crumbStyle.Render(crumbs))
	}
	b.WriteString("\n\n")

	b.WriteString(m.list.View())
	b.WriteString("\n")

	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")

	switch {
	case m.showQueue:
		b.WriteString(helpStyle.Render("↑/↓ scroll · o/esc close · ctrl+q quit"))
	case m.loading:
		b.WriteString(m.spinner.View() + "loading…")
	case m.err != nil:
		b.WriteString(errStyle.Render("error: " + m.err.Error()))
	default:
		b.WriteString(helpStyle.Render(
			"enter open · esc back · space pause · ,/. seek · " +
				"q/Q queue track/album · o view queue · / filter · ctrl+q quit"))
	}
	return b.String()
}

// compositeModal overlays the queue modal box, centered, on top of the
// background view — the browser stays visible behind and around it.
func (m Model) compositeModal(background string) string {
	box := m.queueModalBox()
	x := (m.width - lipgloss.Width(box)) / 2
	y := (m.height - lipgloss.Height(box)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	bg := lipgloss.NewLayer(background)
	fg := lipgloss.NewLayer(box).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(bg, fg).Render()
}

// nowPlayingLine renders the current track plus elapsed/total time.
func (m Model) nowPlayingLine() string {
	if m.nowPlaying == nil {
		return helpStyle.Render("— nothing playing —")
	}
	t := m.nowPlaying

	var status, clock string
	if m.player != nil {
		s := m.player.State()
		clock = fmt.Sprintf("  %s / %s", fmtDur(s.Position), fmtDur(t.Duration))
		if s.Paused {
			status = " [paused]"
		}
	}
	return npStyle.Render(fmt.Sprintf("♪ %s — %s%s%s",
		t.Artist, t.Title, clock, status))
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

func (m Model) crumbLine() string {
	if len(m.crumbs) == 0 {
		return ""
	}
	parts := make([]string, len(m.crumbs))
	for i, c := range m.crumbs {
		parts[i] = c.title
	}
	return strings.Join(parts, " / ") + " /"
}

// listHeight reserves rows for the header (2), spacer (1), now-playing (1),
// and footer (1).
func (m Model) listHeight() int {
	h := m.height - 5
	if h < 1 {
		return 1
	}
	return h
}

// modalSize returns the outer width and height of the queue modal box,
// clamped to fit inside the content region.
func (m Model) modalSize() (w, h int) {
	w = m.width - 8
	if w > 60 {
		w = 60
	}
	if w < 16 {
		w = 16
	}
	h = m.listHeight() - 2
	if h < 5 {
		h = 5
	}
	return w, h
}

// queueModalBox renders the bordered queue box. Positioning is handled by
// the compositor in compositeModal.
func (m Model) queueModalBox() string {
	w, _ := m.modalSize()

	title := headerStyle.Render(fmt.Sprintf("Queue · %d track(s)", len(m.queue)))
	body := m.queueList.View()
	if len(m.queue) == 0 {
		body = helpStyle.Render("queue is empty — press q / Q to add tracks")
	}

	// Width(w-2): the style's width is the content box inside the border.
	return modalStyle.Width(w - 2).Render(title + "\n" + body)
}

// --- async fetch commands ---

func (m Model) fetchArtists(sectionKey string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		artists, err := client.Artists(ctx, sectionKey)
		if err != nil {
			return errMsg{fmt.Errorf("loading artists: %w", err)}
		}
		items := make([]list.Item, len(artists))
		for i, a := range artists {
			items[i] = artistItem{artist: a}
		}
		return artistsMsg(items)
	}
}

func (m Model) fetchAlbums(artistKey string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		albums, err := client.Albums(ctx, artistKey)
		if err != nil {
			return errMsg{fmt.Errorf("loading albums: %w", err)}
		}
		items := make([]list.Item, len(albums))
		for i, a := range albums {
			items[i] = albumItem{album: a}
		}
		return albumsMsg(items)
	}
}

func (m Model) fetchTracks(albumKey string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		tracks, err := client.Tracks(ctx, albumKey)
		if err != nil {
			return errMsg{fmt.Errorf("loading tracks: %w", err)}
		}
		items := make([]list.Item, 0, len(tracks)+1)
		if len(tracks) > 0 {
			items = append(items, albumActionItem{tracks: tracks})
		}
		for i, t := range tracks {
			items = append(items, trackItem{track: t, tracks: tracks, pos: i})
		}
		return tracksMsg(items)
	}
}

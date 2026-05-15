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

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/theopalhol/plexamp-tui/internal/player"
	"github.com/theopalhol/plexamp-tui/internal/plex"
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

	list    list.Model
	spinner spinner.Model

	level      level
	crumbs     []crumb
	loading    bool
	err        error
	nowPlaying *plex.Track

	width, height int
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	crumbStyle  = lipgloss.NewStyle().Faint(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	helpStyle   = lipgloss.NewStyle().Faint(true)
	npStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("121"))
)

// New builds the initial model showing the given music libraries. player may
// be nil, in which case browsing works but playback is disabled.
func New(client *plex.Client, p *player.Player, libs []plex.MusicLibrary) Model {
	items := make([]list.Item, len(libs))
	for i, l := range libs {
		items[i] = libraryItem{lib: l}
	}

	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Libraries"
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return Model{
		client:  client,
		player:  p,
		list:    l,
		spinner: sp,
		level:   levelLibraries,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tick())
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
		return m, nil

	case tea.KeyMsg:
		// Let the list own keys while it is filtering (typing a query).
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter", "l", "right":
			return m.drillDown()
		case "esc", "backspace", "h", "left":
			return m.goBack()
		case " ":
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
	case trackItem:
		return m.play(it.track)
	}
	return m, nil
}

// play starts playback of a track via mpv.
func (m Model) play(t plex.Track) (tea.Model, tea.Cmd) {
	if m.player == nil {
		m.err = errors.New("playback unavailable: mpv is not running")
		return m, nil
	}
	url := m.client.StreamURL(t)
	if url == "" {
		m.err = errors.New("track has no playable media")
		return m, nil
	}
	if err := m.player.Load(url); err != nil {
		m.err = fmt.Errorf("playback: %w", err)
		return m, nil
	}
	track := t
	m.nowPlaying = &track
	m.err = nil
	return m, nil
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

func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render("plexamp-tui"))
	if crumbs := m.crumbLine(); crumbs != "" {
		b.WriteString("  " + crumbStyle.Render(crumbs))
	}
	b.WriteString("\n\n")

	b.WriteString(m.list.View())
	b.WriteString("\n")

	b.WriteString(m.nowPlayingLine())
	b.WriteString("\n")

	switch {
	case m.loading:
		b.WriteString(m.spinner.View() + "loading…")
	case m.err != nil:
		b.WriteString(errStyle.Render("error: " + m.err.Error()))
	default:
		b.WriteString(helpStyle.Render(
			"enter/→ open · esc/← back · space pause · ,/. seek · / filter · q quit"))
	}
	return b.String()
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
		items := make([]list.Item, len(tracks))
		for i, t := range tracks {
			items[i] = trackItem{track: t}
		}
		return tracksMsg(items)
	}
}

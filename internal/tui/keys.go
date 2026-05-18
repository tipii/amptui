package tui

import (
	"charm.land/bubbles/v2/key"
)

// KeyMap is the single source of truth for keybindings. Every keystroke
// the app cares about is described as a key.Binding here — Update routes
// by key.Matches against these, and help.Model renders footer/help-modal
// text from their Help() output. To change a key, change it here.
type KeyMap struct {
	// --- app-wide ---
	Quit     key.Binding
	Help     key.Binding
	Settings key.Binding
	Refresh  key.Binding

	// --- generic navigation (re-used across screens / modals) ---
	Up, Down, Left, Right key.Binding
	Enter                 key.Binding
	Back                  key.Binding

	// --- browser ---
	Filter     key.Binding
	ToggleGrid key.Binding
	OpenQueue  key.Binding
	OpenSearch key.Binding

	// --- playback / queue actions (from browser) ---
	Pause        key.Binding
	NextTrack    key.Binding
	PrevTrack    key.Binding
	SeekBack     key.Binding
	SeekForward  key.Binding
	EnqueueTrack key.Binding
	EnqueueAlbum key.Binding

	// --- queue modal ---
	MoveDown   key.Binding
	MoveUp     key.Binding
	DeleteItem key.Binding

	// --- search modal ---
	// Text-input-friendly navigation: arrows / enter / esc ONLY, no vim
	// letters — so typing "look" in the search field doesn't trigger
	// Up/Back/Enter via the k/h/l aliases.
	InputUp           key.Binding
	InputDown         key.Binding
	InputEnter        key.Binding
	InputBack         key.Binding
	CycleFilter       key.Binding
	EnqueueFromSearch key.Binding
}

// NewKeyMap returns the default bindings.
func NewKeyMap() KeyMap {
	return KeyMap{
		Quit:     key.NewBinding(key.WithKeys("ctrl+c", "ctrl+q"), key.WithHelp("ctrl+q", "quit")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Settings: key.NewBinding(key.WithKeys(","), key.WithHelp(",", "settings")),
		Refresh:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh")),

		Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:  key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Left:  key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "left")),
		Right: key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "right")),
		Enter: key.NewBinding(key.WithKeys("enter", "l"), key.WithHelp("enter", "open")),
		Back:  key.NewBinding(key.WithKeys("esc", "backspace", "h"), key.WithHelp("esc", "back")),

		Filter:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		ToggleGrid: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "grid")),
		OpenQueue:  key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "queue")),
		OpenSearch: key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "search")),

		Pause:        key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "pause")),
		NextTrack:    key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next")),
		PrevTrack:    key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "prev")),
		SeekBack:     key.NewBinding(key.WithKeys("<"), key.WithHelp("<", "seek -10s")),
		SeekForward:  key.NewBinding(key.WithKeys(">"), key.WithHelp(">", "seek +10s")),
		EnqueueTrack: key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "queue track")),
		EnqueueAlbum: key.NewBinding(key.WithKeys("Q"), key.WithHelp("Q", "queue album")),

		MoveDown:   key.NewBinding(key.WithKeys("J"), key.WithHelp("J", "move down")),
		MoveUp:     key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "move up")),
		DeleteItem: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),

		InputUp:           key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
		InputDown:         key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
		InputEnter:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		InputBack:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		CycleFilter:       key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "filter")),
		EnqueueFromSearch: key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "queue track")),
	}
}

// helpView is the per-context view a help.Model can render. Each screen
// or modal returns one of these so the same help.Model renders the right
// thing for the user's current position in the app.
type helpView struct {
	short []key.Binding
	full  [][]key.Binding
}

func (h helpView) ShortHelp() []key.Binding  { return h.short }
func (h helpView) FullHelp() [][]key.Binding { return h.full }

// browserHelp is the help context for the main browser screen.
func (k KeyMap) browserHelp() helpView {
	return helpView{
		short: []key.Binding{k.Help, k.OpenSearch, k.Enter, k.ToggleGrid, k.OpenQueue, k.NextTrack, k.PrevTrack, k.Quit},
		full: [][]key.Binding{
			{k.Enter, k.Back, k.Up, k.Down, k.Filter, k.ToggleGrid},
			{k.Pause, k.NextTrack, k.PrevTrack, k.SeekBack, k.SeekForward},
			{k.EnqueueTrack, k.EnqueueAlbum, k.OpenQueue},
			{k.OpenSearch, k.Help, k.Settings, k.Refresh, k.Quit},
		},
	}
}

// queueModalHelp is the help context for the queue modal overlay.
func (k KeyMap) queueModalHelp() helpView {
	return helpView{
		short: []key.Binding{k.Up, k.Down, k.MoveUp, k.MoveDown, k.DeleteItem, k.Enter, k.Back},
	}
}

// searchModalHelp is the help context for the fuzzy-search modal.
// Uses Input* bindings (arrows only) because the field accepts typed text.
func (k KeyMap) searchModalHelp() helpView {
	return helpView{
		short: []key.Binding{k.CycleFilter, k.InputUp, k.InputDown, k.InputEnter, k.EnqueueFromSearch, k.InputBack},
	}
}

// helpModalHelp is shown while the keybindings modal itself is open.
func (k KeyMap) helpModalHelp() helpView {
	return helpView{
		short: []key.Binding{k.Up, k.Down, k.Help, k.Back},
	}
}

// settingsHelp is the help shown on the settings screen (navigation mode).
func (k KeyMap) settingsHelp() helpView {
	return helpView{
		short: []key.Binding{k.Up, k.Down, k.Enter, k.Back, k.Refresh, k.Quit},
	}
}

// settingsEditHelp is shown while a settings field is in edit mode.
// Uses Input* bindings (arrows only) — vim-letter aliases would type
// into the field instead of committing.
func (k KeyMap) settingsEditHelp() helpView {
	return helpView{
		short: []key.Binding{k.InputEnter, k.InputBack, k.Quit},
	}
}

// keySection is one titled group of bindings shown in the help modal body.
type keySection struct {
	title    string
	bindings [][]key.Binding
}

// helpModalSections returns the grouped bindings the help modal renders.
// Keep section titles short — they're headers in the modal body. The order
// here is the order the user sees.
func (k KeyMap) helpModalSections() []keySection {
	return []keySection{
		{title: "Browse", bindings: [][]key.Binding{
			{k.Enter, k.Back, k.Up, k.Down},
			{k.ToggleGrid, k.Filter},
		}},
		{title: "Playback", bindings: [][]key.Binding{
			{k.Pause, k.NextTrack, k.PrevTrack},
			{k.SeekBack, k.SeekForward},
		}},
		{title: "Queue", bindings: [][]key.Binding{
			{k.EnqueueTrack, k.EnqueueAlbum, k.OpenQueue},
			{k.MoveUp, k.MoveDown, k.DeleteItem},
		}},
		{title: "Search", bindings: [][]key.Binding{
			{k.OpenSearch, k.CycleFilter, k.EnqueueFromSearch},
		}},
		{title: "App", bindings: [][]key.Binding{
			{k.Settings, k.Refresh, k.Help, k.Quit},
		}},
	}
}

// currentHelp returns the helpView appropriate for the user's current
// position in the app — picks the right bindings for the visible context.
func (m Model) currentHelp() helpView {
	k := m.keymap
	switch {
	case m.showHelp:
		return k.helpModalHelp()
	case m.showSearch:
		return k.searchModalHelp()
	case m.showQueue:
		return k.queueModalHelp()
	case m.screen == screenSettings && m.settingsEditing:
		return k.settingsEditHelp()
	case m.screen == screenSettings:
		return k.settingsHelp()
	default:
		return k.browserHelp()
	}
}

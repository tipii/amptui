package tui

import (
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, m.listHeight())
		// Modal interiors: subtract the border (2), horizontal padding (2),
		// and one row for the box title.
		mw, mh := m.modalSize()
		m.queueList.SetSize(mw-4, mh-3)
		m.helpViewport.SetWidth(mw - 4)
		m.helpViewport.SetHeight(mh - 3)
		return m, nil

	case tea.KeyPressMsg:
		// The help modal owns input while it is open.
		if m.showHelp {
			switch msg.String() {
			case "ctrl+c", "ctrl+q":
				return m, tea.Quit
			case "?", "esc":
				m.showHelp = false
				return m, nil
			}
			// Forward scroll keys (↑/↓, j/k, pgup/pgdn, etc.) to viewport.
			var cmd tea.Cmd
			m.helpViewport, cmd = m.helpViewport.Update(msg)
			return m, cmd
		}
		// The search modal owns input while it is open. Most keys are
		// forwarded to the textinput so the user can type their query.
		if m.showSearch {
			switch msg.String() {
			case "ctrl+c", "ctrl+q":
				return m, tea.Quit
			case "esc":
				m.closeSearch()
				return m, nil
			case "tab":
				m.cycleSearchFilter()
				return m, nil
			case "up":
				m.moveSearchCursor(-1)
				return m, nil
			case "down":
				m.moveSearchCursor(1)
				return m, nil
			case "enter":
				return m.activateSearchResult()
			case "alt+enter":
				m.enqueueSearchResult()
				return m, nil
			}
			prev := m.searchInput.Value()
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			if m.searchInput.Value() != prev {
				m.runSearch()
			}
			return m, cmd
		}
		// The queue modal owns input while it is open.
		if m.showQueue {
			switch msg.String() {
			case "ctrl+c", "ctrl+q":
				return m, tea.Quit
			case "o", "esc":
				m.showQueue = false
				return m, nil
			case "J":
				m.moveQueueItem(1)
				return m, nil
			case "K":
				m.moveQueueItem(-1)
				return m, nil
			case "d":
				m.deleteQueueItem()
				return m, nil
			case "enter":
				m.playQueueItem()
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
		// Grid cursor navigation (only meaningful at the Artists level).
		if m.gridView && m.level == levelArtists {
			switch msg.String() {
			case "up", "k":
				m.moveGridCursor(-1, 0)
				return m, nil
			case "down", "j":
				m.moveGridCursor(1, 0)
				return m, nil
			case "left":
				m.moveGridCursor(0, -1)
				return m, nil
			case "right":
				m.moveGridCursor(0, 1)
				return m, nil
			}
		}
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit
		case "?":
			m.showHelp = true
			return m, nil
		case "tab":
			m.toggleGrid()
			return m, nil
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
		case "s":
			return m, m.openSearch()
		case "n":
			m.playNext()
			return m, nil
		case "p":
			m.playPrev()
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

	case indexReadyMsg:
		m.index = msg.idx
		m.indexLoading = false
		m.indexErr = nil
		// If the user already typed in the search modal while indexing was
		// in flight, surface their results now.
		if m.showSearch {
			m.runSearch()
		}
		return m, nil
	case indexErrMsg:
		m.indexLoading = false
		m.indexErr = msg.err
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

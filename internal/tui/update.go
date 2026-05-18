package tui

import (
	"time"

	"charm.land/bubbles/v2/key"
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
		m.helpModel.SetWidth(msg.Width)
		// huh fields need WindowSizeMsg too so they can size themselves.
		var fcmd tea.Cmd
		m.settings, fcmd = m.settings.ForwardMsg(msg)
		return m, fcmd

	case tea.KeyPressMsg:
		k := m.keymap

		// The help modal owns input while it is open.
		if m.showHelp {
			switch {
			case key.Matches(msg, k.Quit):
				return m, tea.Quit
			case key.Matches(msg, k.Help), key.Matches(msg, k.Back):
				m.showHelp = false
				return m, nil
			}
			var cmd tea.Cmd
			m.helpViewport, cmd = m.helpViewport.Update(msg)
			return m, cmd
		}
		// The search modal owns input while it is open. Navigation uses the
		// Input* bindings (arrows / enter / esc only) so vim-letter aliases
		// don't get swallowed when the user types them into the query.
		if m.showSearch {
			switch {
			case key.Matches(msg, k.Quit):
				return m, tea.Quit
			case key.Matches(msg, k.InputBack):
				m.closeSearch()
				return m, nil
			case key.Matches(msg, k.CycleFilter):
				m.cycleSearchFilter()
				return m, nil
			case key.Matches(msg, k.InputUp):
				m.moveSearchCursor(-1)
				return m, nil
			case key.Matches(msg, k.InputDown):
				m.moveSearchCursor(1)
				return m, nil
			case key.Matches(msg, k.InputEnter):
				return m.activateSearchResult()
			case key.Matches(msg, k.EnqueueFromSearch):
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
			switch {
			case key.Matches(msg, k.Quit):
				return m, tea.Quit
			case key.Matches(msg, k.OpenQueue), key.Matches(msg, k.Back):
				m.showQueue = false
				return m, nil
			case key.Matches(msg, k.MoveDown):
				m.moveQueueItem(1)
				return m, nil
			case key.Matches(msg, k.MoveUp):
				m.moveQueueItem(-1)
				return m, nil
			case key.Matches(msg, k.DeleteItem):
				m.deleteQueueItem()
				return m, nil
			case key.Matches(msg, k.Enter):
				m.playQueueItem()
				return m, nil
			}
			var cmd tea.Cmd
			m.queueList, cmd = m.queueList.Update(msg)
			return m, cmd
		}
		// Settings screen owns its own input set; route there first.
		if m.screen == screenSettings {
			return m.routeSettingsKey(msg)
		}
		// Let the list own keys while it is filtering (typing a query).
		if m.list.FilterState() == list.Filtering {
			break
		}
		// Grid cursor navigation (only meaningful at supported levels).
		if m.currentGridView() {
			switch {
			case key.Matches(msg, k.Up):
				m.moveGridCursor(-1, 0)
				return m, nil
			case key.Matches(msg, k.Down):
				m.moveGridCursor(1, 0)
				return m, nil
			case key.Matches(msg, k.Left):
				m.moveGridCursor(0, -1)
				return m, nil
			case key.Matches(msg, k.Right):
				m.moveGridCursor(0, 1)
				return m, nil
			}
		}
		switch {
		case key.Matches(msg, k.Quit):
			return m, tea.Quit
		case key.Matches(msg, k.Help):
			m.showHelp = true
			return m, nil
		case key.Matches(msg, k.ToggleGrid):
			m.toggleGrid()
			return m, nil
		case key.Matches(msg, k.Enter):
			return m.drillDown()
		case key.Matches(msg, k.Back):
			return m.goBack()
		case key.Matches(msg, k.EnqueueTrack):
			return m.enqueueSelectedTrack(), nil
		case key.Matches(msg, k.EnqueueAlbum):
			return m.enqueueSelectedAlbum(), nil
		case key.Matches(msg, k.OpenQueue):
			m.openQueue()
			return m, nil
		case key.Matches(msg, k.OpenSearch):
			return m, m.openSearch()
		case key.Matches(msg, k.Settings):
			m.screen = screenSettings
			return m, nil
		case key.Matches(msg, k.Refresh):
			if m.librarySyncing || len(m.libs) == 0 {
				return m, nil
			}
			active := m.libs[0]
			if m.startupLibrary != nil {
				active = *m.startupLibrary
			}
			m.librarySyncing = true
			m.libraryErr = nil
			return m, syncLibrary(m.client, active)
		case key.Matches(msg, k.NextTrack):
			m.playNext()
			return m, nil
		case key.Matches(msg, k.PrevTrack):
			m.playPrev()
			return m, nil
		case key.Matches(msg, k.Pause):
			if m.player != nil {
				_ = m.player.TogglePause()
			}
			return m, nil
		case key.Matches(msg, k.SeekBack):
			if m.player != nil {
				_ = m.player.Seek(-10 * time.Second)
			}
			return m, nil
		case key.Matches(msg, k.SeekForward):
			if m.player != nil {
				_ = m.player.Seek(10 * time.Second)
			}
			return m, nil
		}

	case libraryReadyMsg:
		m.library = msg.lib
		m.librarySyncing = false
		m.libraryErr = nil
		if m.showSearch {
			m.runSearch()
		}
		if m.startupLibrary != nil && m.level == levelLibraries {
			m.applyItems(levelArtists, m.artistItems())
		} else {
			m.refreshCurrentLevel()
		}
		return m, nil
	case libraryErrMsg:
		m.librarySyncing = false
		m.libraryErr = msg.err
		return m, nil

	case tickMsg:
		m = m.advanceIfFinished()
		if m.showQueue {
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

	// Forward any unhandled non-key message to the settings sub-model so
	// its fields' internal state advances (cursor blink, focus cmds from
	// Init, etc.).
	var fieldsCmd tea.Cmd
	if _, isKey := msg.(tea.KeyPressMsg); !isKey {
		m.settings, fieldsCmd = m.settings.ForwardMsg(msg)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, tea.Batch(cmd, fieldsCmd)
}

// routeSettingsKey dispatches a key to the settings sub-model and acts
// on the resulting outcome (close, refresh, commit). Kept on Model
// because the outcomes need parent state (cfg, libs, client, grid flags).
func (m Model) routeSettingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var (
		cmd     tea.Cmd
		outcome settingsOutcome
	)
	m.settings, cmd, outcome = m.settings.HandleKey(msg, m.keymap)
	switch outcome {
	case settingsOutcomeClose:
		m.screen = screenBrowser
	case settingsOutcomeRefresh:
		if !m.librarySyncing && len(m.libs) > 0 {
			active := m.libs[0]
			if m.startupLibrary != nil {
				active = *m.startupLibrary
			}
			m.librarySyncing = true
			m.libraryErr = nil
			return m, tea.Batch(cmd, syncLibrary(m.client, active))
		}
	case settingsOutcomeCommit:
		// Pull the new values from the sub-model, apply runtime effects
		// (grid view per level), persist, and tell the sub-model whether
		// the save succeeded so it can flash the right indicator.
		v := m.settings.Values()
		m.cfg.ServerURL = v.ServerURL
		m.cfg.Token = v.Token
		m.cfg.DefaultLibrary = v.DefaultLibrary
		m.cfg.DefaultViewArtist = v.ViewArtist
		m.cfg.DefaultViewAlbum = v.ViewAlbum
		m.gridArtists = m.cfg.DefaultViewArtist == "grid"
		m.gridAlbums = m.cfg.DefaultViewAlbum == "grid"
		m.settings.MarkSaved(m.cfg.Save())
	}
	return m, cmd
}

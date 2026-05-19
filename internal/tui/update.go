package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/theopalhol/amptui/internal/plex"
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
		m.infoViewport.SetWidth(mw - 4)
		m.infoViewport.SetHeight(mh - 3)
		// Progress bar fits between the left indent and the right edge.
		m.progress.SetWidth(msg.Width - 4)
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
		// The info modal owns input while it is open.
		if m.showInfo {
			switch {
			case key.Matches(msg, k.Quit):
				return m, tea.Quit
			case key.Matches(msg, k.Info), key.Matches(msg, k.Back):
				m.showInfo = false
				return m, nil
			}
			var cmd tea.Cmd
			m.infoViewport, cmd = m.infoViewport.Update(msg)
			return m, cmd
		}
		// The search modal owns input while it is open. The sub-model
		// drives navigation + typing; outcomes (close, play, enqueue,
		// jump) need parent state so we apply them here.
		if m.search.IsOpen() {
			return m.routeSearchKey(msg)
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
		// Dashboard screen has its own nav (cards + sections); route
		// there before falling through to browser keys.
		if m.screen == screenDashboard {
			return m.routeDashboardKey(msg)
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
		case key.Matches(msg, k.SwitchScreen):
			m.screen = screenDashboard
			return m, nil
		case key.Matches(msg, k.Info):
			if body := m.infoModalContent(); body != "" {
				// viewport doesn't soft-wrap; lipgloss with Width does.
				// Wrap to the viewport's visible width so bios reflow
				// instead of getting truncated mid-word at the edge.
				w := m.infoViewport.Width()
				if w < 10 {
					w = 60
				}
				wrapped := lipgloss.NewStyle().Width(w).Render(body)
				m.infoViewport.SetContent(wrapped)
				m.infoViewport.GotoTop()
				m.showInfo = true
			}
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
			var cmd tea.Cmd
			m.search, cmd = m.search.Open()
			return m, cmd
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

	case librariesReadyMsg:
		// Result of the first-time-setup fetch kicked off by the settings
		// commit. Populate the picker list, resolve the active library
		// (honoring cfg.DefaultLibrary if set), then chain into the same
		// cache-sync + dashboard-load that Init runs on a normal start.
		if msg.err != nil {
			m.librarySyncing = false
			m.libraryErr = msg.err
			return m, nil
		}
		m.libs = msg.libs
		items := make([]list.Item, len(m.libs))
		for i, l := range m.libs {
			items[i] = libraryItem{lib: l}
		}
		if m.level == levelLibraries {
			m.list.SetItems(items)
			m.list.Select(0)
		}
		if len(m.libs) == 0 {
			m.librarySyncing = false
			return m, nil
		}
		active := m.libs[0]
		if m.cfg.DefaultLibrary != "" {
			for i := range m.libs {
				if m.libs[i].Key == m.cfg.DefaultLibrary ||
					strings.EqualFold(m.libs[i].Title, m.cfg.DefaultLibrary) {
					active = m.libs[i]
					break
				}
			}
		}
		return m, tea.Batch(
			loadOrSyncLibrary(m.client, active),
			m.dashboard.Load(m.client, active.Key),
		)

	case libraryReadyMsg:
		m.library = msg.lib
		m.librarySyncing = false
		m.libraryErr = nil
		if m.search.IsOpen() {
			m.search.RunQuery(m.library)
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

	case artistMetaMsg:
		m.metaLoading = false
		m.artistMeta = msg.meta
		return m, nil
	case albumMetaMsg:
		m.metaLoading = false
		m.albumMeta = msg.meta
		return m, nil

	case dashboardPlaysMsg:
		m.dashboard.ApplyPlays(msg)
		return m, nil
	case dashboardAddedMsg:
		m.dashboard.ApplyAdded(msg)
		return m, nil
	case dashboardPlaylistsMsg:
		m.dashboard.ApplyPlaylists(msg)
		return m, nil

	case playlistTracksMsg:
		// User pressed enter on a playlist tile; tracks just arrived —
		// play them.
		if msg.err != nil || len(msg.tracks) == 0 {
			return m, nil
		}
		return m.playTracks(msg.tracks, 0)

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
		m.cfg.Home = v.Home
		m.gridArtists = m.cfg.DefaultViewArtist == "grid"
		m.gridAlbums = m.cfg.DefaultViewAlbum == "grid"
		m.settings.MarkSaved(m.cfg.Save())
		// First-time setup: app started without credentials, user has now
		// supplied them. Build the Plex client and fetch the library list
		// in the background so they can leave the settings screen and
		// browse without restarting. Re-edits after a successful startup
		// still require a relaunch (we don't tear down an existing client).
		if m.client == nil && m.cfg.IsValid() {
			m.client = plex.New(m.cfg.ServerURL, m.cfg.Token)
			m.librarySyncing = true
			m.libraryErr = nil
			return m, tea.Batch(cmd, fetchLibraries(m.client))
		}
	}
	return m, cmd
}

// routeSearchKey dispatches a key to the search sub-model and acts on
// the resulting outcome. Outcomes that touch parent state — the playback
// queue, the browser crumbs — are applied here so the sub-model stays
// decoupled from playback and navigation.
func (m Model) routeSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var (
		cmd     tea.Cmd
		outcome searchOutcome
	)
	m.search, cmd, outcome = m.search.HandleKey(msg, m.keymap, m.library)
	switch outcome {
	case searchOutcomeClose:
		m.search = m.search.Close()
	case searchOutcomePlayTrack:
		if e := m.search.SelectedEntry(); e != nil {
			t := entryToTrack(*e)
			m.search = m.search.Close()
			model, pcmd := m.playTracks([]plex.Track{t}, 0)
			return model, tea.Batch(cmd, pcmd)
		}
	case searchOutcomeEnqueueTrack:
		if e := m.search.SelectedEntry(); e != nil {
			m.enqueue(entryToTrack(*e))
		}
	case searchOutcomeJumpArtist:
		if e := m.search.SelectedEntry(); e != nil {
			model, jcmd := m.jumpToArtist(e.RatingKey)
			return model, tea.Batch(cmd, jcmd)
		}
	case searchOutcomeJumpAlbum:
		if e := m.search.SelectedEntry(); e != nil {
			model, jcmd := m.jumpToAlbum(e.RatingKey)
			return model, tea.Batch(cmd, jcmd)
		}
	}
	return m, cmd
}

// playlistTracksMsg arrives after the parent fires fetchPlaylistTracks
// in response to a dashboard playlist tile being activated.
type playlistTracksMsg struct {
	tracks []plex.Track
	err    error
}

func fetchPlaylistTracks(client *plex.Client, ratingKey string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), dashFetchTimeout)
		defer cancel()
		tracks, err := client.PlaylistTracks(ctx, ratingKey)
		return playlistTracksMsg{tracks: tracks, err: err}
	}
}

// routeDashboardKey dispatches a key while the dashboard is the active
// screen. Global media / modal / navigation keys are handled here;
// in-tile navigation delegates to the dashboard sub-model and the
// outcome (play track, open album, open playlist) is applied here
// because it touches parent state (player, browser crumbs).
func (m Model) routeDashboardKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keymap
	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Help):
		m.showHelp = true
		return m, nil
	case key.Matches(msg, k.Settings):
		m.screen = screenSettings
		return m, nil
	case key.Matches(msg, k.SwitchScreen):
		m.screen = screenBrowser
		return m, nil
	case key.Matches(msg, k.OpenSearch):
		var cmd tea.Cmd
		m.search, cmd = m.search.Open()
		return m, cmd
	case key.Matches(msg, k.OpenQueue):
		m.openQueue()
		return m, nil
	case key.Matches(msg, k.Refresh):
		// Re-fetch the three dashboard tiles. Only meaningful when we
		// have a client + a library to query against.
		if m.client == nil || len(m.libs) == 0 {
			return m, nil
		}
		active := m.libs[0]
		if m.startupLibrary != nil {
			active = *m.startupLibrary
		}
		return m, m.dashboard.Load(m.client, active.Key)
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

	// Anything not above belongs to the dashboard sub-model (nav +
	// enter). Outcomes touch parent state.
	var (
		cmd     tea.Cmd
		outcome dashboardOutcome
	)
	m.dashboard, cmd, outcome = m.dashboard.HandleKey(msg, m.keymap)
	switch outcome {
	case dashOutcomePlayTrack:
		if t, ok := m.dashboard.SelectedTrack(); ok {
			model, pcmd := m.playTracks([]plex.Track{t}, 0)
			return model, tea.Batch(cmd, pcmd)
		}
	case dashOutcomeOpenAlbum:
		if a, ok := m.dashboard.SelectedAlbum(); ok {
			// Jump the browser to this album's tracks (same pattern as
			// jumpToAlbum from search) and switch to the browser screen.
			model, jcmd := m.jumpToAlbum(a.RatingKey)
			model.screen = screenBrowser
			return model, tea.Batch(cmd, jcmd)
		}
	case dashOutcomeOpenPlaylist:
		if p, ok := m.dashboard.SelectedPlaylist(); ok {
			return m, tea.Batch(cmd, fetchPlaylistTracks(m.client, p.RatingKey))
		}
	}
	return m, cmd
}

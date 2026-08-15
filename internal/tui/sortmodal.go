package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/textutil"
)

// The sort modal (o) is the Plex-style ordering picker: it lists the
// orderings that apply to the level you're on, marks the active one, and
// applies your choice to the list you're looking at. Picking the already-
// active field flips its direction, so "o enter" is the quick way to
// reverse the current order.
//
// The chosen Spec per level lives on the Model (sortArtist / sortAlbum /
// sortTrack) and is persisted to config.toml on every change, so the app
// reopens in the order you left it.

// sortFieldsFor returns the orderings a level offers. The Libraries level
// has no meaningful sort (the picker is short and server-ordered), so it
// returns nil and the `o` key no-ops there.
func sortFieldsFor(lvl level) []library.SortField {
	switch lvl {
	case levelArtists:
		return library.ArtistSortFields
	case levelAlbums:
		return library.AlbumSortFields
	case levelTracks:
		return library.TrackSortFields
	}
	return nil
}

// sortSpec returns the active ordering for a level, and whether the level
// is sortable at all.
func (m Model) sortSpec(lvl level) (library.Spec, bool) {
	switch lvl {
	case levelArtists:
		return m.sortArtist, true
	case levelAlbums:
		return m.sortAlbum, true
	case levelTracks:
		return m.sortTrack, true
	}
	return library.Spec{}, false
}

// setSortSpec installs a new ordering for a level and mirrors it into cfg
// so the next Save persists it.
func (m *Model) setSortSpec(lvl level, s library.Spec) {
	switch lvl {
	case levelArtists:
		m.sortArtist = s
		m.cfg.SortArtist = s.String()
	case levelAlbums:
		m.sortAlbum = s
		m.cfg.SortAlbum = s.String()
	case levelTracks:
		m.sortTrack = s
		m.cfg.SortTrack = s.String()
	}
}

// openSortModal opens the picker with the cursor parked on the active
// field. No-op on levels that don't sort.
func (m *Model) openSortModal() {
	fields := sortFieldsFor(m.level)
	if len(fields) == 0 {
		return
	}
	spec, _ := m.sortSpec(m.level)
	m.sortCursor = 0
	for i, f := range fields {
		if f == spec.Field {
			m.sortCursor = i
			break
		}
	}
	m.showSort = true
}

// moveSortCursor walks the field list, clamped at both ends.
func (m *Model) moveSortCursor(delta int) {
	fields := sortFieldsFor(m.level)
	if len(fields) == 0 {
		return
	}
	m.sortCursor = clampInt(m.sortCursor+delta, 0, len(fields)-1)
}

// applySortSelection commits the highlighted field: a new field arrives at
// its natural direction (dates descending, everything else ascending),
// while re-picking the active field flips direction. The current level is
// re-ordered in place and the modal closes.
func (m Model) applySortSelection() (Model, tea.Cmd) {
	fields := sortFieldsFor(m.level)
	m.showSort = false
	if m.sortCursor < 0 || m.sortCursor >= len(fields) {
		return m, nil
	}
	f := fields[m.sortCursor]
	next := library.Spec{Field: f, Dir: library.DefaultDirection(f)}
	if cur, ok := m.sortSpec(m.level); ok && cur.Field == f {
		next.Dir = cur.Dir.Flip()
	}
	m.setSortSpec(m.level, next)
	// Best-effort persistence, matching how the library cache is saved: an
	// unwritable config shouldn't stop the sort from applying to this
	// session. The settings screen surfaces config problems separately.
	_ = m.cfg.Save()
	m.resortCurrentLevel()
	// Re-ordering scrolls different items into view; fetch their artwork
	// (deduped against what's already loaded).
	return m, m.visibleArtworkFetches()
}

// resortCurrentLevel rebuilds the current level's rows under the new
// ordering and returns the cursor to the top.
//
// Jumping to the top rather than following the previously-selected item is
// deliberate, and matches Plex: the reason you re-sort is to see what's now
// at the front. Keeping your place instead leaves you parked mid-list — ask
// for "most albums first" and you'd still be staring at the 2-album artist
// you happened to be on, which reads as a broken sort.
func (m *Model) resortCurrentLevel() {
	m.refreshCurrentLevel()
	m.list.Title = m.listTitleForLevel(m.level)
	m.list.Select(0)
	m.gridCursor = 0
	m.gridScrollTop = 0
}

// sortSuffix is the "· year ↑" tail appended to a level's list heading so
// the active ordering is visible without opening the modal.
func (m Model) sortSuffix(lvl level) string {
	spec, ok := m.sortSpec(lvl)
	if !ok {
		return ""
	}
	return " · " + strings.ToLower(spec.Field.Label()) + " " + spec.Dir.Arrow()
}

// sortModalBox renders the ordering picker. The active field carries the
// direction it's sorted in; the cursor row is accented.
func (m Model) sortModalBox() string {
	title := headerStyle.Render("Sort · " + m.titleForLevel(m.level))
	return m.modalFrame(title + "\n" + m.sortModalBody())
}

func (m Model) sortModalBody() string {
	fields := sortFieldsFor(m.level)
	if len(fields) == 0 {
		return helpStyle.Render("this level has no sort options")
	}
	active, _ := m.sortSpec(m.level)

	// Widest label ("Track number") plus breathing room, so the direction
	// marker forms a column instead of ragging against the labels.
	const labelCol = 15

	var b strings.Builder
	b.WriteString("\n")
	for i, f := range fields {
		row := "  " + textutil.PadRight(f.Label(), labelCol)
		if f == active.Field {
			row += active.Dir.Arrow() + " " + active.Dir.String()
		}
		row = strings.TrimRight(row, " ")
		if i == m.sortCursor {
			row = lipgloss.NewStyle().Foreground(theme.Accent).Bold(true).Render("›" + row[1:])
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓ move · enter apply · enter on the active field flips direction"))
	return b.String()
}

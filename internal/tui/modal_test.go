package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/theopalhol/amptui/internal/index"
	"github.com/theopalhol/amptui/internal/plex"
)

// newQueueModel builds a realistic model: a sized browser list in the
// background and the queue modal open on top.
func newQueueModel(t *testing.T) Model {
	t.Helper()

	libs := []plex.MusicLibrary{
		{Key: "1", Title: "Music"},
		{Key: "2", Title: "Soundtracks"},
		{Key: "3", Title: "Podcasts"},
	}
	m := New(nil, nil, libs, nil)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = updated.(Model)

	m.queue = []plex.Track{
		{Title: "I'm a Ram", Artist: "Al Green", Album: "Gets Next to You", Duration: 3 * time.Minute},
		{Title: "Tired of Being Alone", Artist: "Al Green", Album: "Gets Next to You", Duration: 162 * time.Second},
		{Title: "Driving Wheel", Artist: "Al Green", Album: "Gets Next to You", Duration: 200 * time.Second},
	}
	m.queueIdx = 1
	m.nowPlaying = &plex.Track{Title: "Tired of Being Alone", Artist: "Al Green"}
	m.openQueue()
	return m
}

func TestQueueModalRenders(t *testing.T) {
	m := newQueueModel(t)
	out := m.View().Content

	if !strings.Contains(out, "╭") || !strings.Contains(out, "╰") {
		t.Errorf("expected a rounded border in the modal view")
	}
	if !strings.Contains(out, "Queue · 3 track(s)") {
		t.Errorf("expected the modal title in the view")
	}
	// The browser list should still be visible behind the modal.
	if !strings.Contains(out, "Soundtracks") {
		t.Errorf("expected the background browser list to show through the overlay")
	}
	// Visual check: run `go test -run TestQueueModalRenders -v` to eyeball it.
	t.Log("\n" + out)
}

func TestQueueModalEmpty(t *testing.T) {
	m := newQueueModel(t)
	m.queue = nil
	m.nowPlaying = nil
	m.rebuildQueueList()

	out := m.View().Content
	if !strings.Contains(out, "queue is empty") {
		t.Errorf("expected empty-queue hint in the view")
	}
}

func TestHelpModalRenders(t *testing.T) {
	m := newQueueModel(t)
	m.showQueue = false
	m.showHelp = true

	out := m.View().Content
	if !strings.Contains(out, "Keybindings") {
		t.Errorf("expected the help modal title in the view")
	}
	if !strings.Contains(out, "Soundtracks") {
		t.Errorf("expected the background list to show through the overlay")
	}
	t.Log("\n" + out)
}

// TestMoveQueueItem covers reordering the currently-playing track.
func TestMoveQueueItem(t *testing.T) {
	m := newQueueModel(t)
	// queue = [I'm a Ram, Tired of Being Alone, Driving Wheel]; idx 1 plays.
	m.queueList.Select(1)
	m.moveQueueItem(1)

	if got := m.queue[2].Title; got != "Tired of Being Alone" {
		t.Errorf("moved track should be at idx 2, got %q", got)
	}
	if m.queueIdx != 2 {
		t.Errorf("queueIdx must follow the playing track, got %d", m.queueIdx)
	}
}

// TestSearchModalRenders verifies the search modal composites with a
// non-empty result list, including the kind filter bar and the cursor row.
func TestSearchModalRenders(t *testing.T) {
	m := newQueueModel(t)
	m.showQueue = false

	// Inject a small in-memory index, open the modal, and seed a query.
	m.index = &index.Index{Entries: []index.Entry{
		{Kind: index.KindArtist, Title: "Al Green", RatingKey: "ar1"},
		{Kind: index.KindAlbum, Title: "Gets Next to You", RatingKey: "al1", Artist: "Al Green"},
		{Kind: index.KindTrack, Title: "I'm a Ram", RatingKey: "t1", Album: "Gets Next to You", Artist: "Al Green"},
	}}
	m.indexLoading = false
	_ = m.openSearch()
	m.searchInput.SetValue("al green")
	m.runSearch()
	m.showSearch = true

	out := m.View().Content
	if !strings.Contains(out, "Search") {
		t.Errorf("expected search modal title in the view")
	}
	if !strings.Contains(out, "[All]") {
		t.Errorf("expected filter bar with [All] highlighted")
	}
	if !strings.Contains(out, "Al Green") {
		t.Errorf("expected the Al Green artist row in the results")
	}
	if !strings.Contains(out, "Soundtracks") {
		t.Errorf("expected the background browser list to show through")
	}
	t.Log("\n" + out)
}

// TestStatusBarIndexingIndicator verifies the right-aligned indexing
// indicator appears in the footer while the loader is running.
func TestStatusBarIndexingIndicator(t *testing.T) {
	m := newQueueModel(t)
	m.showQueue = false
	m.indexLoading = true

	out := m.View().Content
	if !strings.Contains(out, "indexing library") {
		t.Errorf("expected 'indexing library' indicator in the footer")
	}
	t.Log("\n" + out)
}

// TestArtistGridRenders verifies that toggling grid view at the Artists
// level produces a multi-column layout and highlights the cursor cell.
func TestArtistGridRenders(t *testing.T) {
	libs := []plex.MusicLibrary{{Key: "1", Title: "Music"}}
	m := New(nil, nil, libs, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = updated.(Model)

	// Populate the artists level as if a fetch had completed.
	items := []list.Item{
		artistItem{artist: plex.Artist{RatingKey: "ar1", Title: "Al Green"}},
		artistItem{artist: plex.Artist{RatingKey: "ar2", Title: "Led Zeppelin"}},
		artistItem{artist: plex.Artist{RatingKey: "ar3", Title: "Pink Floyd"}},
		artistItem{artist: plex.Artist{RatingKey: "ar4", Title: "Radiohead"}},
		artistItem{artist: plex.Artist{RatingKey: "ar5", Title: "The Beatles"}},
		artistItem{artist: plex.Artist{RatingKey: "ar6", Title: "Mac DeMarco"}},
		artistItem{artist: plex.Artist{RatingKey: "ar7", Title: "Arctic Monkeys"}},
	}
	m.applyItems(levelArtists, items)
	m.toggleGrid()
	if !m.gridView {
		t.Fatal("toggleGrid did not enable grid view")
	}

	out := m.View().Content
	// 110 / 25 (cellWidth + gap) = 4 columns. Expect at least two artists on
	// the same line — assert by checking they share a line.
	lines := strings.Split(out, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "Al Green") && strings.Contains(line, "Led Zeppelin") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Al Green and Led Zeppelin to share a row in grid view")
	}
	t.Log("\n" + out)
}

// TestDeleteQueueItemBeforePlaying covers deleting a non-playing track that
// sits before the playing one — queueIdx must decrement.
func TestDeleteQueueItemBeforePlaying(t *testing.T) {
	m := newQueueModel(t)
	m.queueList.Select(0) // cursor on "I'm a Ram" (not playing)
	m.deleteQueueItem()

	if len(m.queue) != 2 {
		t.Fatalf("expected 2 tracks left, got %d", len(m.queue))
	}
	if got := m.queue[0].Title; got != "Tired of Being Alone" {
		t.Errorf("expected playing track to shift to idx 0, got %q", got)
	}
	if m.queueIdx != 0 {
		t.Errorf("queueIdx should now be 0, got %d", m.queueIdx)
	}
}

package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
)

// dashTestNow is a Wednesday.
var dashTestNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func playsOn(n int, day time.Time, album string) []media.Play {
	out := make([]media.Play, n)
	for i := range out {
		out[i] = media.Play{At: day.Add(time.Duration(i) * time.Minute), AlbumKey: album, Album: "Album " + album, Artist: "Artist " + album}
	}
	return out
}

func utcDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 10, 0, 0, 0, time.UTC) }

// The heatmap: a column per week ending with this one, a row per weekday
// from Monday, nothing drawn for days still to come, months on top.
func TestHeatmapLines(t *testing.T) {
	var plays []media.Play
	plays = append(plays, playsOn(3, utcDay(2026, 11, 16), "a")...)  // Monday this week
	plays = append(plays, playsOn(1, utcDay(2026, 11, 10), "a")...)  // Tuesday last week
	plays = append(plays, playsOn(10, utcDay(2026, 11, 15), "b")...) // Sunday last week
	plays = append(plays, playsOn(5, utcDay(2026, 9, 1), "c")...)    // before the window

	lines := heatmapLines(plays, dashTestNow, 10)
	if len(lines) != 9 {
		t.Fatalf("%d lines, want months + 7 days + legend", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > heatmapWidth(10) {
			t.Errorf("line %d is %d wide, over %d", i, w, heatmapWidth(10))
		}
	}
	// Weeks from Monday Sep 14 to this one, Nov 16: each month's name
	// over its first week.
	if got := ansi.Strip(lines[0]); got != "    Sep   Oct     Nov" {
		t.Errorf("months = %q", got)
	}
	for r, want := range map[int]string{1: "Mon", 3: "Wed", 5: "Fri"} {
		if !strings.HasPrefix(ansi.Strip(lines[r]), want) {
			t.Errorf("row %d = %q, want it labeled %s", r, ansi.Strip(lines[r]), want)
		}
	}
	// Monday to Wednesday have this week's square, Thursday on don't yet.
	for r := 1; r <= 7; r++ {
		got, want := strings.Count(lines[r], "■"), 10
		if r > 3 {
			want = 9
		}
		if got != want {
			t.Errorf("row %d has %d days, want %d", r, got, want)
		}
	}
	// Sunday Nov 15 (10 plays) is in the top shade; Thursdays, never
	// played, in the empty one.
	if !strings.Contains(lines[7], heatCells[4]) {
		t.Error("the busiest day isn't in the top shade")
	}
	if strings.Count(lines[4], heatCells[0]) != 9 {
		t.Errorf("Thursday = %q", lines[4])
	}
	if !strings.Contains(ansi.Strip(lines[8]), "less") {
		t.Errorf("legend = %q", ansi.Strip(lines[8]))
	}

	// Too narrow for the legend: it's left out rather than overflowing.
	narrow := heatmapLines(plays, dashTestNow, 4)
	if len(narrow) != 8 {
		t.Errorf("4 weeks: %d lines, want 8 (no legend)", len(narrow))
	}
	if got := ansi.Strip(narrow[0]); got != "      Nov" {
		t.Errorf("4 weeks: months = %q (October's one column is too few for its name)", got)
	}
}

func TestHeatLevels(t *testing.T) {
	level := heatLeveler(map[int]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 20, 6: 0})
	// Quartiles of 1, 2, 3, 4, 20 are 2, 3, 4.
	for n, want := range map[int]int{0: 0, 1: 1, 2: 1, 3: 2, 4: 3, 20: 4} {
		if got := level(n); got != want {
			t.Errorf("level(%d) = %d, want %d", n, got, want)
		}
	}
	if heatLeveler(nil)(0) != 0 {
		t.Error("no plays at all")
	}
}

func TestMostPlayedAndRediscover(t *testing.T) {
	var plays []media.Play
	plays = append(plays, playsOn(2, utcDay(2026, 11, 17), "recent")...)
	plays = append(plays, playsOn(5, utcDay(2026, 11, 1), "top")...)
	plays = append(plays, playsOn(9, utcDay(2026, 9, 1), "old")...) // outside 30 days
	top := mostPlayedAlbums(plays, dashTestNow.AddDate(0, 0, -30), 10)
	if len(top) != 2 || top[0].RatingKey != "top" || top[0].Plays != 5 || top[1].RatingKey != "recent" || top[0].Artist != "Artist top" {
		t.Errorf("most played = %+v", top)
	}

	byPlays := []media.AlbumSummary{
		{RatingKey: "loved-recently", Plays: 50, LastPlayed: dashTestNow.AddDate(0, 0, -3)},
		{RatingKey: "loved-long-ago", Plays: 40, LastPlayed: dashTestNow.AddDate(0, -8, 0)},
		{RatingKey: "unknown", Plays: 30},
		{RatingKey: "liked-long-ago", Plays: 10, LastPlayed: dashTestNow.AddDate(-1, 0, 0)},
	}
	got := rediscoverAlbums(byPlays, dashTestNow.Add(-dashRediscoverAfter), 10)
	if len(got) != 2 || got[0].RatingKey != "loved-long-ago" || got[1].RatingKey != "liked-long-ago" {
		t.Errorf("rediscover = %+v", got)
	}
}

func albumsNamed(prefix string, n int) []media.AlbumSummary {
	out := make([]media.AlbumSummary, n)
	for i := range out {
		key := fmt.Sprintf("%s%d", prefix, i)
		out[i] = media.AlbumSummary{RatingKey: key, Title: "Title " + key, Artist: "Artist " + key, Year: 2000 + i,
			Plays: 10 - i, LastPlayed: dashTestNow.AddDate(0, -i, 0)}
	}
	return out
}

// loadedDashboard is a dashboard 110 wide (4 cards a row) with every
// feed in: Recently added came back empty.
func loadedDashboard(history error) dashboardModel {
	d := newDashboardModel()
	d.now = func() time.Time { return dashTestNow }
	d.SetWidth(110)
	d.ApplyAlbums(dashAlbumsMsg{feed: feedRecent, albums: albumsNamed("j", 6)})
	d.ApplyAlbums(dashAlbumsMsg{feed: feedAdded})
	d.ApplyAlbums(dashAlbumsMsg{feed: feedByPlays, albums: albumsNamed("p", 8)})
	d.ApplyAlbums(dashAlbumsMsg{feed: feedUnplayed, albums: albumsNamed("u", 2), total: 926})
	d.ApplyHistory(dashHistoryMsg{plays: playsOn(4, utcDay(2026, 11, 17), "h"), err: history})
	d.ApplyPlaylists(dashPlaylistsMsg{playlists: []media.Playlist{{RatingKey: "pl1", Title: "Mix", LeafCount: 12, Art: "/playlists/pl1/composite/1"}}})
	return d
}

var (
	keyUp   = tea.KeyPressMsg{Code: tea.KeyUp}
	keyDown = tea.KeyPressMsg{Code: tea.KeyDown}
	keyLeft = tea.KeyPressMsg{Code: tea.KeyLeft}
)

func TestDashboardRowsAndNavigation(t *testing.T) {
	d := loadedDashboard(nil)
	if d.Loading() {
		t.Fatal("still loading with every feed in")
	}
	if d.rowVisible(rowRecentlyAdded) {
		t.Error("an empty row is shown")
	}
	if d.unplayedTotal != 926 {
		t.Errorf("unplayed total = %d", d.unplayedTotal)
	}
	// Most played comes from the history; Rediscover from the albums
	// not played in 90 days (p3 on: last played 3+ months ago).
	if c := d.rows[rowMostPlayed].cards; len(c) != 1 || c[0].key != "h" || c[0].sub != "4 plays · Artist h" {
		t.Errorf("most played = %+v", c)
	}
	if c := d.rows[rowRediscover].cards; len(c) != 5 || c[0].key != "p3" {
		t.Errorf("rediscover = %+v", c)
	}

	km := NewKeyMap()
	step := func(k tea.KeyPressMsg) dashboardOutcome {
		var out dashboardOutcome
		d, out = d.HandleKey(k, km)
		return out
	}
	if d.focus != 1+int(rowJumpBackIn) {
		t.Fatalf("focus starts on %d", d.focus)
	}
	for range 5 {
		step(keyRight)
	}
	if s := d.rows[rowJumpBackIn]; s.cursor != 5 || s.offset != 2 {
		t.Errorf("after 5 rights: cursor %d offset %d, want 5 and 2 (4 cards in view)", s.cursor, s.offset)
	}
	step(keyLeft)
	if s := d.rows[rowJumpBackIn]; s.offset != 2 {
		t.Errorf("moving left within the view scrolled it: offset %d", s.offset)
	}
	if out := step(keyEnter); out != dashOutcomeOpenAlbum {
		t.Errorf("enter on an album = %v", out)
	}
	if c, _ := d.Selected(); c.key != "j4" {
		t.Errorf("selected %q", c.key)
	}

	step(keyDown) // skips the empty Recently added
	if d.focus != 1+int(rowMostPlayed) {
		t.Errorf("down went to section %d, want Most played", d.focus)
	}
	for range 3 {
		step(keyDown)
	}
	if d.focus != 1+int(rowPlaylists) {
		t.Fatalf("focus %d, want Playlists", d.focus)
	}
	if out := step(keyEnter); out != dashOutcomePlayPlaylist {
		t.Errorf("enter on a playlist = %v", out)
	}
	if out := step(keyQ); out != dashOutcomeEnqueue {
		t.Errorf("q on a playlist = %v", out)
	}
	step(keyDown)
	if d.focus != 1+int(rowPlaylists) {
		t.Error("down past the last row moved the focus")
	}

	for range 6 {
		step(keyUp)
	}
	if d.focus != 0 {
		t.Fatalf("focus %d, want the band", d.focus)
	}
	if out := step(keyEnter); out != dashOutcomeGoToPlaying {
		t.Errorf("enter on the band = %v", out)
	}
	if out := step(keyQ); out != dashOutcomeNone {
		t.Errorf("q on the band = %v", out)
	}
}

// Jellyfin keeps no history: Most played falls back to play counts
// ("all time"), and the heatmap gives way to the library's numbers.
func TestDashboardWithoutHistory(t *testing.T) {
	d := loadedDashboard(media.ErrNoHistory)
	if !d.noHistory || d.historyErr != nil {
		t.Fatalf("noHistory %v, err %v", d.noHistory, d.historyErr)
	}
	if c := d.rows[rowMostPlayed].cards; len(c) != 8 || c[0].key != "p0" {
		t.Errorf("most played = %+v", c)
	}
	if _, note := d.rowTitle(rowMostPlayed); note != "all time" {
		t.Errorf("note = %q", note)
	}
}

// A row that turns out empty can't keep the focus.
func TestDashboardFocusLeavesEmptyRow(t *testing.T) {
	d := newDashboardModel()
	d.ApplyAlbums(dashAlbumsMsg{feed: feedRecent}) // nothing played yet
	if d.focus != 1+int(rowRecentlyAdded) {
		t.Errorf("focus = %d, want the next row (Recently added)", d.focus)
	}
}

// newDashModel is the app on its dashboard with every feed in.
func newDashModel(t *testing.T, cfg config.Config, history error) Model {
	t.Helper()
	m := newArtistsModel(t, cfg)
	m.library.Entries = append(m.library.Entries,
		library.Entry{Kind: library.KindTrack, Title: "Dancing Queen", RatingKey: "t1", AlbumKey: "j0", ArtistKey: "ar1"})
	m.library.ArtistAlbumCount = map[string]int{"ar1": 1, "ar2": 1, "ar3": 1}
	m.library.AlbumTrackCount = map[string]int{"j0": 1, "al2": 9, "al3": 11}
	m.library.AlbumDuration = map[string]time.Duration{"j0": 3 * time.Hour}
	m.screen = screenDashboard
	m.dashboard = loadedDashboard(history)
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 60})
	return upd.(Model)
}

func TestDashboardView(t *testing.T) {
	m := newDashModel(t, config.Config{}, nil)
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"Now playing", "Nothing playing",
		"Listening · last", "Mon", "less", "more",
		"3 artists · 3 albums · 21 tracks", "3 never played",
		"▶ Jump back in  1–4 of 6 →", "Title j0", "Artist j0",
		"Most played · last 30 days", "4 plays · Artist h",
		"Rediscover · not played in 3 months",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from the dashboard", want)
		}
	}
	if strings.Contains(out, "Recently added") {
		t.Error("the empty Recently added row is shown")
	}
	for i, l := range strings.Split(m.View().Content, "\n") {
		if w := ansi.StringWidth(l); w > 110 {
			t.Errorf("line %d is %d wide: %q", i, w, ansi.Strip(l))
		}
	}

	m = newDashModel(t, config.Config{}, media.ErrNoHistory)
	out = ansi.Strip(m.View().Content)
	if strings.Contains(out, "Listening") || !strings.Contains(out, "Library") || !strings.Contains(out, "Most played · all time") {
		t.Errorf("without history:\n%s", out)
	}
}

// What's playing shows in the band, with what's next.
func TestDashboardNowPlaying(t *testing.T) {
	m := newDashModel(t, config.Config{}, nil)
	m.queue = []media.Track{
		{RatingKey: "t1", Title: "Dancing Queen", Artist: "Abba", Album: "Arrival", Year: 1976, AlbumRatingKey: "j0", Duration: 3 * time.Minute},
		{RatingKey: "t2", Title: "Knowing Me, Knowing You", Artist: "Abba"},
	}
	m.queueIdx = 0
	m.nowPlaying = &m.queue[0]
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 60})
	m = upd.(Model)
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"Dancing Queen", "Arrival · 1976", "Up next", "Knowing Me, Knowing You · Abba", "♪ Title j0"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
}

// enter on an album opens it in the browser; q queues it (here, with no
// mpv, that's the "playback unavailable" error — not "not found").
func TestDashboardAlbumKeys(t *testing.T) {
	m := newDashModel(t, config.Config{}, nil)
	m = press(m, keyQ)
	if m.err == nil || !strings.Contains(m.err.Error(), "playback unavailable") {
		t.Errorf("q on an album in the library: err = %v", m.err)
	}
	m.err = nil
	m = press(m, keyRight) // j1: not in the library
	m = press(m, keyQ)
	if m.err == nil || !strings.Contains(m.err.Error(), "library cache") {
		t.Errorf("q on an album missing from the library: err = %v", m.err)
	}
	m = press(m, keyLeft)
	m = press(m, keyEnter)
	if m.screen != screenBrowser || m.level != levelTracks || m.curAlbumKey != "j0" {
		t.Errorf("enter: screen %v level %v album %q", m.screen, m.level, m.curAlbumKey)
	}
}

// Each row's cards in view (plus the next) and what's playing get their
// artwork fetched; the rest wait until scrolled to.
func TestDashboardArtKeys(t *testing.T) {
	m := newDashModel(t, config.Config{Images: true}, nil)
	keys := strings.Join(m.dashArtKeys(1), " ")
	for _, want := range []string{"j0", "j4", "/playlists/pl1/composite/1", "u1"} {
		if !strings.Contains(keys, want) {
			t.Errorf("%q not fetched: %s", want, keys)
		}
	}
	if strings.Contains(keys, "j5") {
		t.Errorf("j5 is two cards out of view: %s", keys)
	}
}

func TestLibraryNumbers(t *testing.T) {
	if got := groupDigits(10541); got != "10,541" {
		t.Errorf("groupDigits = %q", got)
	}
	if got := groupDigits(1234567); got != "1,234,567" {
		t.Errorf("groupDigits = %q", got)
	}
	if got := groupDigits(999); got != "999" {
		t.Errorf("groupDigits = %q", got)
	}
	got := packItems([]string{"287 artists", "1,111 albums", "10,541 tracks", "28 days of music"}, 44)
	if len(got) != 2 || got[0] != "287 artists · 1,111 albums · 10,541 tracks" || got[1] != "28 days of music" {
		t.Errorf("packItems = %q", got)
	}
	l := &library.Library{
		ArtistAlbumCount: map[string]int{"a": 2},
		AlbumTrackCount:  map[string]int{"x": 10, "y": 12},
		AlbumDuration:    map[string]time.Duration{"x": 30 * time.Hour, "y": 40 * time.Hour},
	}
	if got := strings.Join(librarySummary(l, 900), " · "); got != "1 artist · 2 albums · 22 tracks · 3 days of music · 2 never played" {
		t.Errorf("summary = %q", got)
	}
}

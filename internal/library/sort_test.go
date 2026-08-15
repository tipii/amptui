package library

import (
	"testing"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// day is a fixed reference point — tests must not depend on wall-clock
// time, and buildFromTracks only stamps SyncedAt from it.
var day = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

// mkSortLib builds a library through the real derivation path so the
// AddedAt / AlbumDuration aggregates under test are the ones production
// computes, not hand-written fixtures.
//
//	ar1 Al Green      al1 Gets Next to You (1971)  2 tracks, 8m,  added day+2
//	ar2 Led Zeppelin  al2 Physical Graffiti (1975) 1 track,  8m,  added day
//	ar2               al3 Houses of the Holy (1973) 1 track, 3m,  added day+5
func mkSortLib() *Library {
	return buildFromTracks([]media.Track{
		{RatingKey: "t1", Title: "I'm a Ram", Album: "Gets Next to You", Artist: "Al Green",
			AlbumRatingKey: "al1", ArtistRatingKey: "ar1", Year: 1971, Index: 1,
			Duration: 3 * time.Minute, AddedAt: day},
		{RatingKey: "t2", Title: "Tired of Being Alone", Album: "Gets Next to You", Artist: "Al Green",
			AlbumRatingKey: "al1", ArtistRatingKey: "ar1", Year: 1971, Index: 2,
			Duration: 5 * time.Minute, AddedAt: day.AddDate(0, 0, 2)},
		{RatingKey: "t3", Title: "Kashmir", Album: "Physical Graffiti", Artist: "Led Zeppelin",
			AlbumRatingKey: "al2", ArtistRatingKey: "ar2", Year: 1975, Index: 1,
			Duration: 8 * time.Minute, AddedAt: day},
		{RatingKey: "t4", Title: "No Quarter", Album: "Houses of the Holy", Artist: "Led Zeppelin",
			AlbumRatingKey: "al3", ArtistRatingKey: "ar2", Year: 1973, Index: 1,
			Duration: 3 * time.Minute, AddedAt: day.AddDate(0, 0, 5)},
	})
}

// TestBuildDerivesAddedAndDuration covers the v3 aggregates: an album /
// artist is "added" when its most recent track was, and album duration is
// the sum of its tracks.
func TestBuildDerivesAddedAndDuration(t *testing.T) {
	l := mkSortLib()

	albums := l.Albums("ar1")
	if len(albums) != 1 {
		t.Fatalf("Albums(ar1) len = %d, want 1", len(albums))
	}
	if got, want := albums[0].Duration, 8*time.Minute; got != want {
		t.Errorf("album duration = %v, want %v", got, want)
	}
	if got, want := albums[0].AddedAt, day.AddDate(0, 0, 2); !got.Equal(want) {
		t.Errorf("album AddedAt = %v, want the most recent track's %v", got, want)
	}

	var zep Artist
	for _, a := range l.Artists() {
		if a.RatingKey == "ar2" {
			zep = a
		}
	}
	if got, want := zep.AddedAt, day.AddDate(0, 0, 5); !got.Equal(want) {
		t.Errorf("artist AddedAt = %v, want the most recent track's %v", got, want)
	}
}

// TestDefaultsPreserveLegacyOrder pins the promise that a user who never
// opens the sort modal sees exactly the ordering amptui had before sorting
// existed: artists A–Z, albums by year, tracks by track number.
func TestDefaultsPreserveLegacyOrder(t *testing.T) {
	l := mkSortLib()

	if artists := l.Artists(); artists[0].Title != "Al Green" {
		t.Errorf("default artist order should be A–Z, got %q first", artists[0].Title)
	}
	albums := l.Albums("ar2")
	if albums[0].Year != 1973 {
		t.Errorf("default album order should be year-ascending, got %d first", albums[0].Year)
	}
	tracks := l.Tracks("al1")
	if tracks[0].Index != 1 {
		t.Errorf("default track order should be by track number, got %d first", tracks[0].Index)
	}
}

func TestSortArtists(t *testing.T) {
	l := mkSortLib()

	byAddedDesc := l.Artists()
	SortArtists(byAddedDesc, Spec{Field: SortAdded, Dir: Desc})
	if byAddedDesc[0].Title != "Led Zeppelin" {
		t.Errorf("added:desc should put the most recent artist first, got %q", byAddedDesc[0].Title)
	}

	byAlbums := l.Artists()
	SortAlbums(nil, Spec{}) // no-op on nil, must not panic
	SortArtists(byAlbums, Spec{Field: SortAlbumCount, Dir: Desc})
	if byAlbums[0].AlbumCount != 2 {
		t.Errorf("albums:desc should lead with the 2-album artist, got %+v", byAlbums[0])
	}

	byTitleDesc := l.Artists()
	SortArtists(byTitleDesc, Spec{Field: SortTitle, Dir: Desc})
	if byTitleDesc[0].Title != "Led Zeppelin" {
		t.Errorf("title:desc should reverse A–Z, got %q", byTitleDesc[0].Title)
	}
}

func TestSortAlbums(t *testing.T) {
	l := mkSortLib()

	byAdded := l.Albums("ar2")
	SortAlbums(byAdded, Spec{Field: SortAdded, Dir: Desc})
	if byAdded[0].Title != "Houses of the Holy" {
		t.Errorf("added:desc should lead with the newest album, got %q", byAdded[0].Title)
	}

	byDuration := l.Albums("ar2")
	SortAlbums(byDuration, Spec{Field: SortDuration, Dir: Desc})
	if byDuration[0].Title != "Physical Graffiti" {
		t.Errorf("duration:desc should lead with the longest album, got %q", byDuration[0].Title)
	}

	byTitle := l.Albums("ar2")
	SortAlbums(byTitle, Spec{Field: SortTitle, Dir: Asc})
	if byTitle[0].Title != "Houses of the Holy" {
		t.Errorf("title:asc wrong, got %q", byTitle[0].Title)
	}
}

func TestSortTracks(t *testing.T) {
	l := mkSortLib()

	byDuration := l.Tracks("al1")
	SortTracks(byDuration, Spec{Field: SortDuration, Dir: Desc})
	if byDuration[0].Title != "Tired of Being Alone" {
		t.Errorf("duration:desc should lead with the longest track, got %q", byDuration[0].Title)
	}

	byTitle := l.Tracks("al1")
	SortTracks(byTitle, Spec{Field: SortTitle, Dir: Asc})
	if byTitle[0].Title != "I'm a Ram" {
		t.Errorf("title:asc wrong, got %q", byTitle[0].Title)
	}
}

// TestSortTracksRespectsDiscOrder covers multi-disc albums, where track
// numbers restart on each disc: sorting on Index alone interleaves them
// (disc 2's track 1 landing next to disc 1's).
func TestSortTracksRespectsDiscOrder(t *testing.T) {
	tracks := []Track{
		{Title: "Happy", Disc: 2, Index: 1},
		{Title: "Rocks Off", Disc: 1, Index: 1},
		{Title: "Turd on the Run", Disc: 2, Index: 2},
		{Title: "Rip This Joint", Disc: 1, Index: 2},
	}
	SortTracks(tracks, DefaultTrackSort)
	want := []string{"Rocks Off", "Rip This Joint", "Happy", "Turd on the Run"}
	for i, w := range want {
		if tracks[i].Title != w {
			t.Fatalf("track %d = %q, want %q (full order: %v)", i, tracks[i].Title, w, titles(tracks))
		}
	}
}

func titles(tracks []Track) []string {
	out := make([]string, len(tracks))
	for i, t := range tracks {
		out[i] = t.Title
	}
	return out
}

// TestSortTiebreakIsAlwaysAlphabetical guards the rule that flipping
// direction reverses the requested key only — the title fallback under it
// stays A–Z so equal-keyed rows don't scramble.
func TestSortTiebreakIsAlwaysAlphabetical(t *testing.T) {
	// Three albums, same year, same everything else.
	albums := []Album{
		{Title: "Zebra", Year: 1971},
		{Title: "Apple", Year: 1971},
		{Title: "Mango", Year: 1971},
	}
	for _, dir := range []Direction{Asc, Desc} {
		SortAlbums(albums, Spec{Field: SortYear, Dir: dir})
		got := []string{albums[0].Title, albums[1].Title, albums[2].Title}
		want := []string{"Apple", "Mango", "Zebra"}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("dir=%s: tiebreak order = %v, want %v", dir, got, want)
			}
		}
	}
}

// TestMissingAddedAtDegradesToTitle covers a backend that reports no
// timestamps (or a pre-v3 cache read back before a re-sync): every AddedAt
// is the zero time, so the ordering must fall through to the title
// tiebreak rather than coming out arbitrary.
func TestMissingAddedAtDegradesToTitle(t *testing.T) {
	artists := []Artist{{Title: "Zebra"}, {Title: "Apple"}, {Title: "Mango"}}
	SortArtists(artists, Spec{Field: SortAdded, Dir: Desc})
	if artists[0].Title != "Apple" {
		t.Errorf("with no timestamps the sort should fall back to title, got %q first", artists[0].Title)
	}
}

func TestSpecStringRoundTrip(t *testing.T) {
	for _, s := range []Spec{
		{Field: SortTitle, Dir: Asc},
		{Field: SortAdded, Dir: Desc},
		{Field: SortDuration, Dir: Asc},
		{Field: SortTrackNumber, Dir: Desc},
	} {
		got := ParseSpec(s.String(), DefaultAlbumSort, []SortField{s.Field})
		if got != s {
			t.Errorf("ParseSpec(%q) = %+v, want %+v", s.String(), got, s)
		}
	}
}

func TestParseSpec(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Spec
	}{
		{"empty falls back", "", DefaultAlbumSort},
		{"unknown field falls back", "bogus:desc", DefaultAlbumSort},
		{"field not valid for level falls back", "albums:asc", DefaultAlbumSort},
		{"bare field gets its default direction", "added", Spec{Field: SortAdded, Dir: Desc}},
		{"bare field defaults ascending", "title", Spec{Field: SortTitle, Dir: Asc}},
		{"explicit direction wins", "added:asc", Spec{Field: SortAdded, Dir: Asc}},
		{"case and space tolerant", "  Added:DESC ", Spec{Field: SortAdded, Dir: Desc}},
		{"garbage direction keeps field default", "added:sideways", Spec{Field: SortAdded, Dir: Desc}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseSpec(tt.in, DefaultAlbumSort, AlbumSortFields); got != tt.want {
				t.Errorf("ParseSpec(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

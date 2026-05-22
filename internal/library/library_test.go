package library

import (
	"testing"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// mkLib builds a small in-memory library for ranking and browse tests.
func mkLib() *Library {
	return &Library{
		Entries: []Entry{
			{Kind: KindArtist, Title: "Al Green", RatingKey: "ar1"},
			{Kind: KindArtist, Title: "Led Zeppelin", RatingKey: "ar2"},
			{Kind: KindAlbum, Title: "Gets Next to You", RatingKey: "al1", Artist: "Al Green", ArtistKey: "ar1", Year: 1971},
			{Kind: KindAlbum, Title: "Physical Graffiti", RatingKey: "al2", Artist: "Led Zeppelin", ArtistKey: "ar2", Year: 1975},
			{Kind: KindTrack, Title: "I'm a Ram", RatingKey: "t1", Album: "Gets Next to You", AlbumKey: "al1", Artist: "Al Green", ArtistKey: "ar1", Year: 1971, Index: 1, Duration: 3 * time.Minute},
			{Kind: KindTrack, Title: "Tired of Being Alone", RatingKey: "t2", Album: "Gets Next to You", AlbumKey: "al1", Artist: "Al Green", ArtistKey: "ar1", Year: 1971, Index: 2},
			{Kind: KindTrack, Title: "Kashmir", RatingKey: "t3", Album: "Physical Graffiti", AlbumKey: "al2", Artist: "Led Zeppelin", ArtistKey: "ar2", Year: 1975, Index: 3},
		},
		ArtistAlbumCount: map[string]int{"ar1": 1, "ar2": 1},
		ArtistTrackCount: map[string]int{"ar1": 2, "ar2": 1},
		AlbumTrackCount:  map[string]int{"al1": 2, "al2": 1},
	}
}

func TestSearchMatchesTypo(t *testing.T) {
	l := mkLib()
	results := l.Search("al gren", nil, 5)
	if len(results) == 0 {
		t.Fatal("expected matches for 'al gren'")
	}
	if results[0].Title != "Al Green" {
		t.Errorf("top result should be 'Al Green', got %q", results[0].Title)
	}
}

func TestSearchKindFilter(t *testing.T) {
	l := mkLib()
	tracks := l.Search("al green", []Kind{KindTrack}, 10)
	if len(tracks) == 0 {
		t.Fatal("expected track matches for 'al green'")
	}
	for _, r := range tracks {
		if r.Kind != KindTrack {
			t.Errorf("filter should restrict to tracks, got kind %s for %q", r.Kind, r.Title)
		}
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	l := mkLib()
	if results := l.Search("", nil, 10); results != nil {
		t.Errorf("empty query should return nil, got %d results", len(results))
	}
}

// TestBuildDerivesParents covers the dedup of artist/album entries from a
// flat track list (the path that actual library indexing takes).
func TestBuildDerivesParents(t *testing.T) {
	tracks := []media.Track{
		{RatingKey: "t1", Title: "I'm a Ram", Album: "Gets Next to You", Artist: "Al Green",
			AlbumRatingKey: "al1", ArtistRatingKey: "ar1", Year: 1971},
		{RatingKey: "t2", Title: "Tired of Being Alone", Album: "Gets Next to You", Artist: "Al Green",
			AlbumRatingKey: "al1", ArtistRatingKey: "ar1", Year: 1971},
		{RatingKey: "t3", Title: "Kashmir", Album: "Physical Graffiti", Artist: "Led Zeppelin",
			AlbumRatingKey: "al2", ArtistRatingKey: "ar2", Year: 1975},
	}
	l := buildFromTracks(tracks)

	counts := map[Kind]int{}
	for _, e := range l.Entries {
		counts[e.Kind]++
	}
	if counts[KindArtist] != 2 {
		t.Errorf("expected 2 unique artists, got %d", counts[KindArtist])
	}
	if counts[KindAlbum] != 2 {
		t.Errorf("expected 2 unique albums, got %d", counts[KindAlbum])
	}
	if counts[KindTrack] != 3 {
		t.Errorf("expected 3 tracks, got %d", counts[KindTrack])
	}
	if got := l.ArtistAlbumCount["ar1"]; got != 1 {
		t.Errorf("ArtistAlbumCount[ar1] = %d, want 1", got)
	}
	if got := l.ArtistTrackCount["ar1"]; got != 2 {
		t.Errorf("ArtistTrackCount[ar1] = %d, want 2", got)
	}
	if got := l.AlbumTrackCount["al1"]; got != 2 {
		t.Errorf("AlbumTrackCount[al1] = %d, want 2", got)
	}
}

// TestBrowseArtistsAlbumsTracks covers the cache-driven browse methods.
func TestBrowseArtistsAlbumsTracks(t *testing.T) {
	l := mkLib()
	artists := l.Artists()
	if len(artists) != 2 {
		t.Fatalf("Artists len = %d, want 2", len(artists))
	}
	if artists[0].Title != "Al Green" || artists[0].AlbumCount != 1 || artists[0].TrackCount != 2 {
		t.Errorf("Al Green row wrong: %+v", artists[0])
	}

	albums := l.Albums("ar1")
	if len(albums) != 1 || albums[0].Title != "Gets Next to You" || albums[0].TrackCount != 2 {
		t.Errorf("Albums(ar1) wrong: %+v", albums)
	}

	tracks := l.Tracks("al1")
	if len(tracks) != 2 || tracks[0].Title != "I'm a Ram" {
		t.Errorf("Tracks(al1) wrong: %+v", tracks)
	}
}

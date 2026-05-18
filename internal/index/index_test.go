package index

import (
	"testing"
	"time"

	"github.com/theopalhol/amptui/internal/plex"
)

// mkIndex builds a small in-memory index for ranking tests.
func mkIndex() *Index {
	return &Index{
		Entries: []Entry{
			{Kind: KindArtist, Title: "Al Green", RatingKey: "ar1"},
			{Kind: KindArtist, Title: "Led Zeppelin", RatingKey: "ar2"},
			{Kind: KindAlbum, Title: "Gets Next to You", RatingKey: "al1", Artist: "Al Green", ArtistKey: "ar1", Year: 1971},
			{Kind: KindAlbum, Title: "Physical Graffiti", RatingKey: "al2", Artist: "Led Zeppelin", ArtistKey: "ar2", Year: 1975},
			{Kind: KindTrack, Title: "I'm a Ram", RatingKey: "t1", Album: "Gets Next to You", Artist: "Al Green", Duration: 3 * time.Minute},
			{Kind: KindTrack, Title: "Tired of Being Alone", RatingKey: "t2", Album: "Gets Next to You", Artist: "Al Green"},
			{Kind: KindTrack, Title: "Kashmir", RatingKey: "t3", Album: "Physical Graffiti", Artist: "Led Zeppelin"},
		},
	}
}

func TestSearchMatchesTypo(t *testing.T) {
	idx := mkIndex()
	results := idx.Search("al gren", nil, 5)
	if len(results) == 0 {
		t.Fatal("expected matches for 'al gren'")
	}
	if results[0].Title != "Al Green" {
		t.Errorf("top result should be 'Al Green', got %q", results[0].Title)
	}
}

func TestSearchKindFilter(t *testing.T) {
	idx := mkIndex()
	tracks := idx.Search("al green", []Kind{KindTrack}, 10)
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
	idx := mkIndex()
	if results := idx.Search("", nil, 10); results != nil {
		t.Errorf("empty query should return nil, got %d results", len(results))
	}
}

// TestBuildDerivesParents covers the dedup of artist/album entries from a
// flat track list (the path that actual library indexing takes).
func TestBuildDerivesParents(t *testing.T) {
	// Two artists, two albums, three tracks (Al Green has two on the same
	// album; Led Zeppelin has one on a different album).
	tracks := []plex.Track{
		{RatingKey: "t1", Title: "I'm a Ram", Album: "Gets Next to You", Artist: "Al Green",
			AlbumRatingKey: "al1", ArtistRatingKey: "ar1", Year: 1971},
		{RatingKey: "t2", Title: "Tired of Being Alone", Album: "Gets Next to You", Artist: "Al Green",
			AlbumRatingKey: "al1", ArtistRatingKey: "ar1", Year: 1971},
		{RatingKey: "t3", Title: "Kashmir", Album: "Physical Graffiti", Artist: "Led Zeppelin",
			AlbumRatingKey: "al2", ArtistRatingKey: "ar2", Year: 1975},
	}
	idx := buildFromTracks(tracks)

	counts := map[Kind]int{}
	for _, e := range idx.Entries {
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
}

package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tipii/amptui/internal/media"
)

func TestTicksToDuration(t *testing.T) {
	// 3489333330 ticks (100ns units) ≈ 348.93s — the "Hot Motion" fixture.
	got := ticksToDuration(3489333330)
	if want := 348933333 * time.Microsecond; got != want {
		t.Fatalf("ticksToDuration = %v, want %v", got, want)
	}
	if secs := int(got.Seconds()); secs != 348 {
		t.Errorf("expected ~348s, got %ds", secs)
	}
}

func TestItemToTrack(t *testing.T) {
	it := itemDTO{
		Id: "trk1", Name: "Hot Motion", Album: "Hot Motion", AlbumId: "alb1",
		AlbumArtists: []nameID{{Name: "Temples", Id: "art1"}},
		// A featured guest on the track shouldn't override the album artist.
		ArtistItems:    []nameID{{Name: "Guest", Id: "g1"}},
		IndexNumber:    1,
		ProductionYear: 2019,
		RunTimeTicks:   3489333330,
	}
	tr := it.toTrack()
	if tr.RatingKey != "trk1" || tr.AlbumRatingKey != "alb1" {
		t.Errorf("ids: %+v", tr)
	}
	if tr.Artist != "Temples" || tr.ArtistRatingKey != "art1" {
		t.Errorf("album artist should win over ArtistItems, got %q/%q", tr.Artist, tr.ArtistRatingKey)
	}
	if tr.Index != 1 || tr.Year != 2019 {
		t.Errorf("index/year: %+v", tr)
	}
}

func TestAlbumArtistFallback(t *testing.T) {
	// No AlbumArtists → fall back to ArtistItems.
	it := itemDTO{ArtistItems: []nameID{{Name: "Solo", Id: "s1"}}}
	if n, id := it.albumArtist(); n != "Solo" || id != "s1" {
		t.Errorf("fallback to ArtistItems failed: %q/%q", n, id)
	}
	// Neither → bare AlbumArtist string, empty id.
	it = itemDTO{AlbumArtist: "Various"}
	if n, id := it.albumArtist(); n != "Various" || id != "" {
		t.Errorf("bare AlbumArtist fallback failed: %q/%q", n, id)
	}
}

const testToken = "test-access-token"

// newTestServer stands in for a Jellyfin server: it authenticates, then
// serves canned Views and Items responses, asserting the access token is
// presented on authenticated calls.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("auth method = %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"AccessToken": testToken,
			"User":        map[string]string{"Id": "user1"},
		})
	})
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Emby-Token") != testToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("/Users/user1/Views", auth(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(itemsResponse{Items: []itemDTO{
			{Id: "musiclib", Name: "Music", CollectionType: "music"},
			{Id: "pl", Name: "Playlists", CollectionType: "playlists"},
		}})
	}))
	mux.HandleFunc("/Items", auth(func(w http.ResponseWriter, r *http.Request) {
		// Single full page → LibraryTracks stops after one round.
		_ = json.NewEncoder(w).Encode(itemsResponse{
			TotalRecordCount: 2,
			Items: []itemDTO{
				{Id: "t1", Name: "A", Album: "Alb", AlbumId: "al1", AlbumArtists: []nameID{{Name: "Art", Id: "ar1"}}, RunTimeTicks: 10000000},
				{Id: "t2", Name: "B", Album: "Alb", AlbumId: "al1", AlbumArtists: []nameID{{Name: "Art", Id: "ar1"}}, RunTimeTicks: 20000000},
			},
		})
	}))
	return httptest.NewServer(mux)
}

func TestMusicLibrariesFiltersToMusic(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	c := New(srv.URL, "u", "p")
	libs, err := c.MusicLibraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) != 1 || libs[0].Key != "musiclib" || libs[0].Title != "Music" {
		t.Fatalf("expected just the music library, got %+v", libs)
	}
	if libs[0].ContentChangedAt != 0 {
		t.Errorf("Jellyfin has no content version; want 0, got %d", libs[0].ContentChangedAt)
	}
}

func TestLibraryTracksDecodes(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	c := New(srv.URL, "u", "p")
	tracks, err := c.LibraryTracks(context.Background(), "musiclib")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("want 2 tracks, got %d", len(tracks))
	}
	if tracks[0].Artist != "Art" || tracks[0].Album != "Alb" {
		t.Errorf("track mapping: %+v", tracks[0])
	}
	if tracks[1].Duration != 2*time.Second {
		t.Errorf("duration: want 2s, got %v", tracks[1].Duration)
	}
}

func TestStreamAndArtworkURL(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	c := New(srv.URL, "u", "p")
	// Force auth so the token is populated for the URL builders.
	if _, err := c.MusicLibraries(context.Background()); err != nil {
		t.Fatal(err)
	}
	stream := c.StreamURL(media.Track{RatingKey: "t1"})
	if !strings.Contains(stream, "/Audio/t1/stream?static=true") || !strings.Contains(stream, "api_key="+testToken) {
		t.Errorf("stream URL wrong: %s", stream)
	}
	art := c.ArtworkURL("art1")
	if !strings.Contains(art, "/Items/art1/Images/Primary") || !strings.Contains(art, "api_key="+testToken) {
		t.Errorf("artwork URL wrong: %s", art)
	}
	if c.ArtworkURL("") != "" {
		t.Errorf("empty key should yield empty artwork URL")
	}
}

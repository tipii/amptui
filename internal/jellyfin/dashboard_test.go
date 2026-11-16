package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tipii/amptui/internal/media"
)

func playedTrack(album, albumID string, plays int, last time.Time) itemDTO {
	it := itemDTO{Id: albumID + "-t", Album: album, AlbumId: albumID, AlbumArtists: []nameID{{Name: album + " artist"}}}
	it.UserData.PlayCount = plays
	it.UserData.Played = true
	it.UserData.LastPlayedDate = last
	return it
}

// Albums carry no play data on Jellyfin, so their stats come from their
// tracks: plays summed, the latest play kept.
func TestAlbumsOfTracks(t *testing.T) {
	jan, mar := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	albums := albumsOfTracks([]itemDTO{
		playedTrack("A", "a", 3, jan),
		playedTrack("B", "b", 1, mar),
		playedTrack("A", "a", 2, mar),
		playedTrack("B", "b", 0, jan), // marked played without a count
		{Id: "loose"},                 // no album
	})
	if len(albums) != 2 {
		t.Fatalf("albums = %+v", albums)
	}
	if a := albums[0]; a.RatingKey != "a" || a.Plays != 5 || !a.LastPlayed.Equal(mar) || a.Artist != "A artist" {
		t.Errorf("A = %+v", a)
	}
	if b := albums[1]; b.Plays != 2 {
		t.Errorf("B plays = %d, want 2 (a played track without a count counts once)", b.Plays)
	}
}

// played tracks: album a played most, album b most recently.
func dashboardServer(t *testing.T, itemsCalls *int) *httptest.Server {
	t.Helper()
	old, recent := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"AccessToken": testToken, "User": map[string]string{"Id": "user1"}})
	})
	mux.HandleFunc("/Items", func(w http.ResponseWriter, r *http.Request) {
		*itemsCalls++
		q := r.URL.Query()
		switch {
		case q.Get("Filters") == "IsPlayed":
			_ = json.NewEncoder(w).Encode(itemsResponse{TotalRecordCount: 2, Items: []itemDTO{
				playedTrack("A", "a", 9, old), playedTrack("B", "b", 1, recent),
			}})
		case q.Get("IncludeItemTypes") == "MusicAlbum":
			if q.Get("SortBy") != "Random" {
				t.Errorf("unplayed albums SortBy = %q", q.Get("SortBy"))
			}
			_ = json.NewEncoder(w).Encode(itemsResponse{TotalRecordCount: 5, Items: []itemDTO{
				{Id: "a", Name: "A"}, {Id: "c", Name: "C"}, {Id: "b", Name: "B"}, {Id: "d", Name: "D"},
			}})
		default:
			t.Errorf("unexpected /Items query %v", q)
		}
	})
	return httptest.NewServer(mux)
}

func TestJellyfinDashboardFeeds(t *testing.T) {
	calls := 0
	srv := dashboardServer(t, &calls)
	defer srv.Close()
	c := New(srv.URL, "u", "p")
	ctx := context.Background()

	byPlays, err := c.PlayedAlbums(ctx, "lib", media.ByPlays, 10)
	if err != nil {
		t.Fatal(err)
	}
	byLast, err := c.PlayedAlbums(ctx, "lib", media.ByLastPlayed, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(byPlays) != 2 || byPlays[0].RatingKey != "a" || byLast[0].RatingKey != "b" {
		t.Errorf("by plays %+v, by last played %+v", byPlays, byLast)
	}

	unplayed, total, err := c.UnplayedAlbums(ctx, "lib", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(unplayed) != 1 || unplayed[0].RatingKey != "c" || total != 3 {
		t.Errorf("unplayed %+v of %d, want [c] of 3 (5 albums, 2 played)", unplayed, total)
	}
	if calls != 2 {
		t.Errorf("%d /Items calls, want 2: the played tracks are fetched once for all three feeds", calls)
	}

	if _, err := c.PlayHistory(ctx, "lib", time.Now()); !errors.Is(err, media.ErrNoHistory) {
		t.Errorf("PlayHistory err = %v, want ErrNoHistory", err)
	}
}

func TestPlaylistArtOnlyWhenItHasAnImage(t *testing.T) {
	if got := (itemDTO{Id: "p1", ImageTags: map[string]string{"Primary": "abc"}}).primaryImage(); got != "p1" {
		t.Errorf("with an image: %q", got)
	}
	if got := (itemDTO{Id: "p2"}).primaryImage(); got != "" {
		t.Errorf("without one: %q", got)
	}
}

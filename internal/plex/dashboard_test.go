package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// albumServer answers section album listings with the given albums,
// recording each request's query.
func albumServer(t *testing.T, total int, albums []albumMetadata, queries *[]url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/sections/14/all" {
			http.NotFound(w, r)
			return
		}
		*queries = append(*queries, r.URL.Query())
		var body struct {
			MediaContainer struct {
				TotalSize int             `json:"totalSize"`
				Metadata  []albumMetadata `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		body.MediaContainer.TotalSize = total
		body.MediaContainer.Metadata = albums
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestPlayedAlbumsQueriesAndDedupes(t *testing.T) {
	var queries []url.Values
	srv := albumServer(t, 2, []albumMetadata{
		{RatingKey: "1", Title: "Ill Times", ParentTitle: "GUM", Type: "album", ViewCount: 188, LastViewedAt: 1700000000},
		{RatingKey: "1", Title: "Ill Times", ParentTitle: "GUM", Type: "album", ViewCount: 188, LastViewedAt: 1700000000},
		{RatingKey: "2", Title: "3D Country", ParentTitle: "Geese", Type: "album", ViewCount: 76},
	}, &queries)
	defer srv.Close()
	c := New(srv.URL, "tok")

	got, err := c.PlayedAlbums(context.Background(), "14", media.ByPlays, 24)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Plays != 188 || got[0].Artist != "GUM" || !got[0].LastPlayed.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("albums = %+v", got)
	}
	if q := queries[0]; q.Get("sort") != "viewCount:desc" || q.Get("viewCount>") != "1" || q.Get("type") != "9" || q.Get("X-Plex-Container-Size") != "24" {
		t.Errorf("by plays query = %v", q)
	}
	if _, err := c.PlayedAlbums(context.Background(), "14", media.ByLastPlayed, 24); err != nil {
		t.Fatal(err)
	}
	if q := queries[1]; q.Get("sort") != "lastViewedAt:desc" || q.Get("lastViewedAt>") != "1" {
		t.Errorf("by last played query = %v", q)
	}
}

func TestUnplayedAlbumsReturnsTotal(t *testing.T) {
	var queries []url.Values
	srv := albumServer(t, 926, []albumMetadata{{RatingKey: "5", Title: "Making Movies", Type: "album"}}, &queries)
	defer srv.Close()
	got, total, err := New(srv.URL, "tok").UnplayedAlbums(context.Background(), "14", 24)
	if err != nil {
		t.Fatal(err)
	}
	if total != 926 || len(got) != 1 {
		t.Errorf("total %d, albums %+v", total, got)
	}
	if q := queries[0]; q.Get("unwatched") != "1" || q.Get("sort") != "random" {
		t.Errorf("query = %v", q)
	}
}

// The owner's token sees every account's plays in the history, so it
// asks for the owner's (account 1) only; other tokens can't list the
// accounts, and get their own history unfiltered.
func TestPlayHistoryFiltersToOwnerAndParsesAlbum(t *testing.T) {
	for _, owner := range []bool{true, false} {
		var historyQuery url.Values
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/accounts":
				if !owner {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
				_, _ = w.Write([]byte(`{"MediaContainer":{"Account":[{"id":1}]}}`))
			case "/status/sessions/history/all":
				historyQuery = r.URL.Query()
				_, _ = w.Write([]byte(`{"MediaContainer":{"totalSize":2,"Metadata":[
					{"viewedAt":1700000000,"type":"track","parentKey":"/library/metadata/30864","parentTitle":"Now Would Be a Good Time","grandparentTitle":"Folk Bitch Trio"},
					{"viewedAt":1699990000,"type":"episode","parentKey":"/library/metadata/9"}]}}`))
			default:
				http.NotFound(w, r)
			}
		}))
		since := time.Unix(1690000000, 0)
		plays, err := New(srv.URL, "tok").PlayHistory(context.Background(), "14", since)
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(plays) != 1 || plays[0].AlbumKey != "30864" || plays[0].Artist != "Folk Bitch Trio" || !plays[0].At.Equal(time.Unix(1700000000, 0)) {
			t.Errorf("owner=%v: plays = %+v", owner, plays)
		}
		if historyQuery.Get("viewedAt>") != "1690000000" || historyQuery.Get("librarySectionID") != "14" {
			t.Errorf("owner=%v: query = %v", owner, historyQuery)
		}
		if got, want := historyQuery.Get("accountID"), map[bool]string{true: "1", false: ""}[owner]; got != want {
			t.Errorf("owner=%v: accountID = %q, want %q", owner, got, want)
		}
	}
}

// A playlist's cover is its collage, which lives at a server path rather
// than under /library/metadata/<key>/thumb (that 404s for playlists).
func TestArtworkURLTakesAServerPath(t *testing.T) {
	c := &Client{serverURL: "http://plex:32400", token: "tok"}
	u, err := url.Parse(c.ArtworkURL("/playlists/14450/composite/1788666351"))
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("url"); got != "/playlists/14450/composite/1788666351" {
		t.Errorf("url = %q", got)
	}
	if !strings.HasPrefix(u.Path, "/photo/") {
		t.Errorf("path = %q, want the photo transcoder", u.Path)
	}
}

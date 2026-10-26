package jellyfin

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
		DateCreated:    time.Date(2019, 9, 27, 0, 0, 0, 0, time.UTC),
	}
	tr := it.toTrack()
	if want := time.Date(2019, 9, 27, 0, 0, 0, 0, time.UTC); !tr.AddedAt.Equal(want) {
		t.Errorf("AddedAt = %v, want %v", tr.AddedAt, want)
	}
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
	mux.HandleFunc("/System/Info/Public", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"ServerName": "jellytest"})
	})
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
		// DateCreated isn't in the default projection — without it in
		// Fields the server returns zero timestamps and the date-added
		// sort silently degrades, so assert we ask for it.
		if fields := r.URL.Query().Get("Fields"); !strings.Contains(fields, "DateCreated") {
			t.Errorf("Fields = %q, want it to request DateCreated", fields)
		}
		// Single full page → LibraryTracks stops after one round.
		_ = json.NewEncoder(w).Encode(itemsResponse{
			TotalRecordCount: 2,
			Items: []itemDTO{
				{Id: "t1", Name: "A", Album: "Alb", AlbumId: "al1", AlbumArtists: []nameID{{Name: "Art", Id: "ar1"}}, RunTimeTicks: 10000000},
				{Id: "t2", Name: "B", Album: "Alb", AlbumId: "al1", AlbumArtists: []nameID{{Name: "Art", Id: "ar1"}}, RunTimeTicks: 20000000,
					DateCreated: time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)},
			},
		})
	}))
	return httptest.NewServer(mux)
}

func TestServerName(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	c := New(srv.URL, "u", "p")
	name, err := c.ServerName(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if name != "jellytest" {
		t.Errorf("ServerName = %q, want jellytest", name)
	}
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
	tracks, err := c.LibraryTracks(context.Background(), "musiclib", nil)
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
	if want := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC); !tracks[1].AddedAt.Equal(want) {
		t.Errorf("AddedAt = %v, want %v", tracks[1].AddedAt, want)
	}
	if !tracks[0].AddedAt.IsZero() {
		t.Errorf("a track with no DateCreated should have a zero AddedAt, got %v", tracks[0].AddedAt)
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

// TestDownloadURL covers both halves of the quality preset: the original
// is the same static direct-play file playback uses, and a transcode
// names its container in the path and drops static so Jellyfin actually
// re-encodes.
func TestDownloadURL(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	c := New(srv.URL, "u", "p")
	if _, err := c.MusicLibraries(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr := media.Track{RatingKey: "t1"}

	if got, want := c.DownloadURL(tr, media.DownloadQuality{}), c.StreamURL(tr); got != want {
		t.Errorf("original DownloadURL = %q, want the stream URL %q", got, want)
	}

	raw := c.DownloadURL(tr, media.DownloadQuality{Codec: media.CodecMP3, Bitrate: 320})
	base, query, ok := strings.Cut(raw, "?")
	if !ok {
		t.Fatalf("no query in %q", raw)
	}
	if want := srv.URL + "/Audio/t1/stream.mp3"; base != want {
		t.Errorf("endpoint = %q, want %q", base, want)
	}
	v, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"static":     "false",
		"audioCodec": "mp3",
		// Jellyfin counts this one in bits per second.
		"audioBitRate": "320000",
		"api_key":      testToken,
	} {
		if got := v.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	// A codec the path can't name falls back to the original rather than
	// producing a URL that lies about its contents.
	if got := c.DownloadURL(tr, media.DownloadQuality{Codec: "opus", Bitrate: 128}); got != c.StreamURL(tr) {
		t.Errorf("unsupported codec should fall back to the original, got %q", got)
	}
}

// fakeAuthServer issues "tok-1", "tok-2", … on each login, and serves
// /Users/u1/Views only to the most recently issued token — so revoking
// a token is just logging in again from elsewhere.
func fakeAuthServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	logins := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/AuthenticateByName":
			logins++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"AccessToken": "tok-" + string(rune('0'+logins)),
				"User":        map[string]string{"Id": "u1"},
			})
		case "/Users/u1/Views":
			if r.Header.Get("X-Emby-Token") != "tok-"+string(rune('0'+logins)) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"Items":[{"Id":"lib","Name":"Music","CollectionType":"music"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &logins
}

// A revoked token used to fail every request until a relaunch.
func TestReauthenticatesAfter401(t *testing.T) {
	srv, logins := fakeAuthServer(t)
	c := New(srv.URL, "me", "pw")
	if _, err := c.MusicLibraries(context.Background()); err != nil {
		t.Fatal(err)
	}
	*logins++ // another login elsewhere revokes tok-1

	libs, err := c.MusicLibraries(context.Background())
	if err != nil {
		t.Fatalf("after revocation: %v", err)
	}
	if len(libs) != 1 || *logins != 3 {
		t.Errorf("libs=%d logins=%d; want 1 library after one re-login", len(libs), *logins)
	}
}

// A failed login is reported as such, not as a 404 on "/Users//Views".
func TestAuthFailureIsReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Users/AuthenticateByName" {
			http.Error(w, "bad password", http.StatusUnauthorized)
			return
		}
		t.Errorf("request to %s without a successful login", r.URL.Path)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "me", "wrong").MusicLibraries(context.Background())
	if err == nil || !strings.Contains(err.Error(), "jellyfin auth") {
		t.Errorf("err = %v, want the auth failure", err)
	}
}

func TestURLsNeedAToken(t *testing.T) {
	c := New("http://jf", "me", "pw")
	if u := c.StreamURL(media.Track{RatingKey: "x"}); u != "" {
		t.Errorf("StreamURL before login = %q, want empty", u)
	}
	if u := c.ArtworkURL("x"); u != "" {
		t.Errorf("ArtworkURL before login = %q, want empty", u)
	}
}

func TestDeviceIDIsPerServerAndStable(t *testing.T) {
	a, b := deviceIDFor("http://a"), deviceIDFor("http://b")
	if a == b || a != deviceIDFor("http://a") || !strings.HasPrefix(a, "amptui-") {
		t.Errorf("device ids: %q %q", a, b)
	}
}

func TestPlaybackReportEndpoints(t *testing.T) {
	type call struct {
		path string
		body map[string]any
		auth string
	}
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Users/AuthenticateByName" {
			_ = json.NewEncoder(w).Encode(map[string]any{"AccessToken": "tok", "User": map[string]string{"Id": "u1"}})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, call{r.URL.Path, body, r.Header.Get("Authorization")})
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := New(srv.URL, "me", "pw")
	tr := media.Track{RatingKey: "item1"}
	for _, r := range []media.PlaybackReport{
		{Track: tr, State: media.PlaybackPlaying, Started: true},
		{Track: tr, State: media.PlaybackPaused, Position: 2 * time.Second},
		{Track: tr, State: media.PlaybackStopped, Position: 3 * time.Second},
	} {
		if err := c.ReportPlayback(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"/Sessions/Playing", "/Sessions/Playing/Progress", "/Sessions/Playing/Stopped"}
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	for i, w := range want {
		if calls[i].path != w || calls[i].body["ItemId"] != "item1" {
			t.Errorf("call %d = %s %v, want %s", i, calls[i].path, calls[i].body, w)
		}
	}
	if calls[1].body["IsPaused"] != true || calls[2].body["PositionTicks"] != float64(30_000_000) {
		t.Errorf("pause/stop bodies: %v / %v", calls[1].body, calls[2].body)
	}
	if !strings.Contains(calls[0].auth, `Token="tok"`) || !strings.Contains(calls[0].auth, "DeviceId=") {
		t.Errorf("Authorization = %q", calls[0].auth)
	}
}

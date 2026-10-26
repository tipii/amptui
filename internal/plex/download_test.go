package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// TestDownloadURLOriginalIsDirectPlay: the original file is the same
// part URL playback uses — no transcoder in the path.
func TestDownloadURLOriginalIsDirectPlay(t *testing.T) {
	c := New("http://plex.example:32400", "tok")
	tr := media.Track{RatingKey: "19782", PartKey: "/library/parts/1/2/file.flac"}
	if got, want := c.DownloadURL(tr, media.DownloadQuality{}), c.StreamURL(tr); got != want {
		t.Errorf("original DownloadURL = %q, want the stream URL %q", got, want)
	}
}

// TestDownloadURLTranscodeParams pins the query the universal transcoder
// needs. These were established against a live server: drop
// X-Plex-Platform, or send one the server has no client profile for, and
// the endpoint answers 400 Bad Request with no explanation.
func TestDownloadURLTranscodeParams(t *testing.T) {
	c := New("http://plex.example:32400", "tok")
	tr := media.Track{RatingKey: "19782", PartKey: "/library/parts/1/2/file.flac"}
	raw := c.DownloadURL(tr, media.DownloadQuality{Codec: media.CodecMP3, Bitrate: 320})

	base, query, ok := strings.Cut(raw, "?")
	if !ok {
		t.Fatalf("no query in %q", raw)
	}
	if want := "http://plex.example:32400/music/:/transcode/universal/start.mp3"; base != want {
		t.Errorf("endpoint = %q, want %q", base, want)
	}
	v, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"path":                     "/library/metadata/19782",
		"protocol":                 "http",
		"musicBitrate":             "320",
		"X-Plex-Platform":          transcodePlatform,
		"X-Plex-Client-Identifier": clientIdentifier,
		"X-Plex-Token":             "tok",
	} {
		if got := v.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// TestDownloadURLFallsBackToOriginal: this endpoint's extension fixes the
// output format, so a codec Plex isn't being asked to produce has to
// yield the original rather than a URL that lies about its contents.
func TestDownloadURLFallsBackToOriginal(t *testing.T) {
	c := New("http://plex.example:32400", "tok")
	tr := media.Track{RatingKey: "19782", PartKey: "/library/parts/1/2/file.flac"}
	if got := c.DownloadURL(tr, media.DownloadQuality{Codec: "opus", Bitrate: 128}); got != c.StreamURL(tr) {
		t.Errorf("unsupported codec should fall back to the original, got %q", got)
	}
	// No rating key means nothing to hand the transcoder.
	noKey := media.Track{PartKey: "/library/parts/1/2/file.flac"}
	if got := c.DownloadURL(noKey, media.DownloadQuality{Codec: media.CodecMP3, Bitrate: 320}); got != c.StreamURL(noKey) {
		t.Errorf("keyless track should fall back to the original, got %q", got)
	}
}

func TestArtworkURLIsTranscodedAndBounded(t *testing.T) {
	c := &Client{serverURL: "http://plex:32400", token: "tok&en"}
	u, err := url.Parse(c.ArtworkURL("123"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/photo/:/transcode" {
		t.Errorf("path = %q, want the photo transcoder", u.Path)
	}
	q := u.Query()
	if q.Get("url") != "/library/metadata/123/thumb" || q.Get("width") != "512" || q.Get("X-Plex-Token") != "tok&en" {
		t.Errorf("query = %v", q)
	}
}

func TestPlaybackReportingRequests(t *testing.T) {
	var got []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	tr := media.Track{RatingKey: "42", Duration: 3 * time.Minute}

	if err := c.ReportPlayback(context.Background(), media.PlaybackReport{
		Track: tr, State: media.PlaybackPaused, Position: 90 * time.Second, Duration: tr.Duration,
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.MarkPlayed(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d requests, want 2", len(got))
	}
	tl, sc := got[0], got[1]
	q := tl.URL.Query()
	if tl.URL.Path != "/:/timeline" || q.Get("ratingKey") != "42" || q.Get("key") != "/library/metadata/42" ||
		q.Get("state") != "paused" || q.Get("time") != "90000" || q.Get("duration") != "180000" {
		t.Errorf("timeline = %s", tl.URL)
	}
	if tl.Header.Get("X-Plex-Token") != "tok" || !strings.HasPrefix(tl.Header.Get("X-Plex-Client-Identifier"), "amptui-") {
		t.Errorf("timeline headers = %v", tl.Header)
	}
	if sc.URL.Path != "/:/scrobble" || sc.URL.Query().Get("key") != "42" ||
		sc.URL.Query().Get("identifier") != "com.plexapp.plugins.library" {
		t.Errorf("scrobble = %s", sc.URL)
	}
}

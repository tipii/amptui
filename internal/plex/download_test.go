package plex

import (
	"net/url"
	"strings"
	"testing"

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

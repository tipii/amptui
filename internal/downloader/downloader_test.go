package downloader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tipii/amptui/internal/media"
)

func TestExtensionFromContentType(t *testing.T) {
	cases := map[string]string{
		"audio/flac":                 ".flac",
		"audio/x-flac":               ".flac",
		"audio/mpeg":                 ".mp3",
		"audio/mpeg; charset=binary": ".mp3",
		"AUDIO/MP4":                  ".m4a",
		"audio/ogg":                  ".ogg",
		"audio/wav":                  ".wav",
		"application/octet-stream":   ".audio",
		"":                           ".audio",
	}
	for ct, want := range cases {
		if got := extensionFromContentType(ct); got != want {
			t.Errorf("extensionFromContentType(%q) = %q, want %q", ct, got, want)
		}
	}
}

func TestSanitizeStripsPathSeparatorsAndCollapsesRuns(t *testing.T) {
	cases := map[string]string{
		"AC/DC":            "AC-DC",
		"foo/bar\\baz":     "foo-bar-baz",
		"a:b*c?d\"e<f>g|h": "a-b-c-d-e-f-g-h",
		" . trailing .  ":  "trailing",
		"normal title":     "normal title",
		// Names that sanitize away must not vanish: ".." as an artist
		// used to land files one level up, "..." as a title made a
		// hidden ".flac".
		"":              "Unknown",
		"..":            "Unknown",
		" . ":           "Unknown",
		"line\nbreak\t": "linebreak",
		"CON":           "_CON",
		"com1.txt":      "_com1.txt",
		"Console":       "Console",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTrackBasenamePadsIndex(t *testing.T) {
	tr := media.Track{Title: "Hot Motion", Index: 1}
	if got, want := trackBasename(tr), "01 Hot Motion"; got != want {
		t.Errorf("trackBasename = %q, want %q", got, want)
	}
	tr.Index = 0 // no index → just title
	if got, want := trackBasename(tr), "Hot Motion"; got != want {
		t.Errorf("no-index trackBasename = %q, want %q", got, want)
	}
}

// TestDownloadWritesToCorrectPath spins up a fake backend that serves a
// few audio bytes, runs Download, and asserts the file lands at the
// expected <artist>/<album>/<NN Title>.ext location with the right body.
func TestDownloadWritesToCorrectPath(t *testing.T) {
	const body = "FAKEFLACDATA"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	root := t.TempDir()
	tr := media.Track{Title: "Hot Motion", Album: "Hot Motion", Artist: "Temples", Index: 1}

	res, err := Download(context.Background(), http.DefaultClient, Request{URL: srv.URL, Track: tr, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Error("first download should not be skipped")
	}
	want := filepath.Join(root, "Temples", "Hot Motion", "01 Hot Motion.flac")
	if res.Path != want {
		t.Errorf("dst path = %q, want %q", res.Path, want)
	}
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("file body = %q, want %q", got, body)
	}
}

// TestDownloadSkipsExisting verifies the second call is a no-op (no
// re-fetch, file untouched).
func TestDownloadSkipsExisting(t *testing.T) {
	const body = "FAKE"
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	root := t.TempDir()
	tr := media.Track{Title: "X", Album: "Y", Artist: "Z", Index: 2}

	first, err := Download(context.Background(), http.DefaultClient, Request{URL: srv.URL, Track: tr, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if first.Skipped {
		t.Error("first call should not be skipped")
	}
	second, err := Download(context.Background(), http.DefaultClient, Request{URL: srv.URL, Track: tr, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Skipped {
		t.Error("second call should be skipped")
	}
	if hits != 1 {
		t.Errorf("server should have been hit once, got %d", hits)
	}
}

// TestDownloadAtomicOnFailure confirms a mid-stream failure leaves no
// partial file at the destination (the .partial sidecar is removed).
func TestDownloadAtomicOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	root := t.TempDir()
	tr := media.Track{Title: "T", Album: "A", Artist: "Ar", Index: 1}
	_, err := Download(context.Background(), http.DefaultClient, Request{URL: srv.URL, Track: tr, Root: root})
	if err == nil {
		t.Fatal("expected error on 500")
	}
	// The album dir may exist (mkdir runs before the body), but no file
	// at the destination path or any .partial should remain.
	dir := filepath.Join(root, "Ar", "A")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".flac") || strings.HasSuffix(e.Name(), ".partial") {
			t.Errorf("stray file after failed download: %s", e.Name())
		}
	}
}

// TestDownloadReportsProgress checks the callback contract the UI relies
// on: the name and total arrive before any bytes do (so a row can render
// the moment the response lands), and the last call carries the complete
// byte count even though the reporting interval steps past it.
func TestDownloadReportsProgress(t *testing.T) {
	body := strings.Repeat("x", 3*progressInterval+17)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		// Explicit, the way a real direct-play file arrives: net/http only
		// derives Content-Length for bodies small enough to buffer.
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var got []Progress
	tr := media.Track{Title: "P", Album: "A", Artist: "Ar", Index: 3}
	_, err := Download(context.Background(), http.DefaultClient, Request{
		URL: srv.URL, Track: tr, Root: t.TempDir(),
		Progress: func(p Progress) { got = append(got, p) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 {
		t.Fatalf("expected several progress reports for a %d-byte body, got %d", len(body), len(got))
	}
	first, last := got[0], got[len(got)-1]
	if first.Written != 0 {
		t.Errorf("first report should precede the body, got %d bytes written", first.Written)
	}
	if first.Name != "03 P.flac" {
		t.Errorf("first report Name = %q, want the destination basename", first.Name)
	}
	if want := int64(len(body)); first.Total != want || last.Total != want {
		t.Errorf("Total = %d/%d, want %d from Content-Length", first.Total, last.Total, want)
	}
	if want := int64(len(body)); last.Written != want {
		t.Errorf("final report Written = %d, want the full %d", last.Written, want)
	}
	// Counts only ever move forward — the UI differences consecutive
	// readings to derive a rate.
	for i := 1; i < len(got); i++ {
		if got[i].Written < got[i-1].Written {
			t.Fatalf("progress went backwards: %d then %d", got[i-1].Written, got[i].Written)
		}
	}
}

// TestDownloadProgressWithoutContentLength: Plex's transcoder streams
// chunked, so there is no total to report. Written must still advance —
// it's all a caller has to show.
func TestDownloadProgressWithoutContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		// Writing more than the buffer forces a chunked response with no
		// Content-Length, the way a live transcode arrives.
		for i := 0; i < 4; i++ {
			_, _ = w.Write([]byte(strings.Repeat("y", progressInterval)))
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	var last Progress
	_, err := Download(context.Background(), http.DefaultClient, Request{
		URL:   srv.URL,
		Track: media.Track{Title: "T", Album: "A", Artist: "Ar", Index: 1},
		Root:  t.TempDir(),
		Ext:   ".mp3",
		Progress: func(p Progress) {
			if p.Total != 0 {
				t.Errorf("Total = %d, want 0 when the server sends no Content-Length", p.Total)
			}
			last = p
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(4 * progressInterval); last.Written != want {
		t.Errorf("final Written = %d, want %d", last.Written, want)
	}
}

// TestDownloadExtNamesTheFile: a requested transcode fixes the format, so
// Request.Ext wins over the Content-Type — a server labelling its
// transcoder output generically shouldn't get us a ".audio" file.
func TestDownloadExtNamesTheFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("MP3"))
	}))
	defer srv.Close()
	root := t.TempDir()
	res, err := Download(context.Background(), http.DefaultClient, Request{
		URL:   srv.URL,
		Track: media.Track{Title: "T", Album: "A", Artist: "Ar", Index: 1},
		Root:  root, Ext: ".mp3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "Ar", "A", "01 T.mp3"); res.Path != want {
		t.Errorf("dst = %q, want %q", res.Path, want)
	}
}

// TestDownloadExtSkipsOnlyMatchingExtension is the switch-your-preset
// case: with a FLAC already on disk, asking for the MP3 has to fetch it
// rather than reporting the FLAC as "already downloaded".
func TestDownloadExtSkipsOnlyMatchingExtension(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()
	root := t.TempDir()
	tr := media.Track{Title: "T", Album: "A", Artist: "Ar", Index: 1}

	// Pre-seed the original download.
	first, err := Download(context.Background(), http.DefaultClient, Request{URL: srv.URL, Track: tr, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(first.Path, ".mp3") {
		t.Fatalf("setup: expected an .mp3 from audio/mpeg, got %q", first.Path)
	}

	// Same extension → skip.
	same, err := Download(context.Background(), http.DefaultClient, Request{URL: srv.URL, Track: tr, Root: root, Ext: ".mp3"})
	if err != nil {
		t.Fatal(err)
	}
	if !same.Skipped {
		t.Error("an existing file with the requested extension should be skipped")
	}

	// Different extension → fetch, and land beside the first file.
	other, err := Download(context.Background(), http.DefaultClient, Request{URL: srv.URL, Track: tr, Root: root, Ext: ".m4a"})
	if err != nil {
		t.Fatal(err)
	}
	if other.Skipped {
		t.Error("a different requested extension should not match the existing file")
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Errorf("the original file should still be there: %v", err)
	}
}

// TestDownloadRetriesAfterInterruptedPartial: killing amptui mid-copy
// leaves a .partial behind, and it shares the destination's basename. It
// must not read as an existing download, or that track could never be
// fetched again.
func TestDownloadRetriesAfterInterruptedPartial(t *testing.T) {
	const body = "COMPLETE"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	root := t.TempDir()
	tr := media.Track{Title: "T", Album: "A", Artist: "Ar", Index: 1}

	dir := filepath.Join(root, "Ar", "A")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "01 T.flac"+partialSuffix)
	if err := os.WriteFile(stale, []byte("trunc"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Download(context.Background(), http.DefaultClient, Request{URL: srv.URL, Track: tr, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatal("a leftover .partial should not count as an existing download")
	}
	got, err := os.ReadFile(filepath.Join(dir, "01 T.flac"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("file body = %q, want the complete %q", got, body)
	}
}

func TestSanitizeCapsLengthOnRuneBoundary(t *testing.T) {
	got := sanitize(strings.Repeat("ж", 300)) // 2 bytes per rune
	if len(got) > maxNameBytes || !utf8.ValidString(got) {
		t.Errorf("sanitize kept %d bytes (valid UTF-8: %v), cap is %d", len(got), utf8.ValidString(got), maxNameBytes)
	}
}

func TestDownloadMultiDiscUsesDiscFolders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()
	root := t.TempDir()
	for _, disc := range []int{1, 2} {
		tr := media.Track{Title: "Intro", Album: "A", Artist: "Ar", Index: 1, Disc: disc}
		res, err := Download(context.Background(), http.DefaultClient, Request{URL: srv.URL, Track: tr, Root: root, MultiDisc: true})
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(root, "Ar", "A", fmt.Sprintf("Disc %d", disc), "01 Intro.flac")
		if res.Path != want || res.Skipped {
			t.Errorf("disc %d: path %q skipped=%v, want %q", disc, res.Path, res.Skipped, want)
		}
	}
}

// "01 Intro" must not count as downloaded because "01 Intro. Reprise"
// is, and an empty file left by a crash isn't a download either.
func TestFindExistingMatchesExactly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "01 Intro. Reprise.flac"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "02 Song.flac"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findExisting(dir, "01 Intro", ""); got != "" {
		t.Errorf("01 Intro matched %q", got)
	}
	if got := findExisting(dir, "02 Song", ""); got != "" {
		t.Errorf("empty file matched: %q", got)
	}
	if got := findExisting(dir, "01 Intro. Reprise", ""); got == "" {
		t.Error("exact match missed")
	}
}

// An interrupted original download resumes from its .partial with a
// Range request instead of starting over.
func TestDownloadResumesPartial(t *testing.T) {
	const body = "COMPLETE-FILE-BODY"
	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		w.Header().Set("Content-Type", "audio/flac")
		http.ServeContent(w, r, "", time.Time{}, strings.NewReader(body))
	}))
	defer srv.Close()
	root := t.TempDir()
	dir := filepath.Join(root, "Ar", "A")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01 T.flac"+partialSuffix), []byte(body[:8]), 0o644); err != nil {
		t.Fatal(err)
	}

	var last Progress
	tr := media.Track{Title: "T", Album: "A", Artist: "Ar", Index: 1}
	res, err := Download(context.Background(), http.DefaultClient, Request{
		URL: srv.URL, Track: tr, Root: root, Progress: func(p Progress) { last = p },
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotRange != "bytes=8-" {
		t.Errorf("Range = %q, want bytes=8-", gotRange)
	}
	if got, _ := os.ReadFile(res.Path); string(got) != body {
		t.Errorf("file = %q, want %q", got, body)
	}
	if last.Written != int64(len(body)) || last.Total != int64(len(body)) {
		t.Errorf("final progress %+v, want %d/%d", last, len(body), len(body))
	}
}

// A connection that stops delivering data is abandoned after
// StallTimeout, keeping what arrived so the retry can resume — instead
// of the old fixed 5-minute cap on the whole transfer.
func TestDownloadAbandonsStalledTransfer(t *testing.T) {
	old := StallTimeout
	StallTimeout = 150 * time.Millisecond
	defer func() { StallTimeout = old }()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("some bytes"))
		w.(http.Flusher).Flush()
		<-release
	}))
	defer srv.Close()
	defer close(release)

	root := t.TempDir()
	tr := media.Track{Title: "T", Album: "A", Artist: "Ar", Index: 1}
	_, err := Download(context.Background(), http.DefaultClient, Request{URL: srv.URL, Track: tr, Root: root})
	if !errors.Is(err, errStalled) {
		t.Fatalf("err = %v, want a stall", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "Ar", "A", "01 T.flac"+partialSuffix)); string(b) != "some bytes" {
		t.Errorf(".partial = %q, want the bytes received so far", b)
	}
}

package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tipii/amptui/internal/media"
)

func TestExtensionFromContentType(t *testing.T) {
	cases := map[string]string{
		"audio/flac":                    ".flac",
		"audio/x-flac":                  ".flac",
		"audio/mpeg":                    ".mp3",
		"audio/mpeg; charset=binary":    ".mp3",
		"AUDIO/MP4":                     ".m4a",
		"audio/ogg":                     ".ogg",
		"audio/wav":                     ".wav",
		"application/octet-stream":      ".audio",
		"":                              ".audio",
	}
	for ct, want := range cases {
		if got := extensionFromContentType(ct); got != want {
			t.Errorf("extensionFromContentType(%q) = %q, want %q", ct, got, want)
		}
	}
}

func TestSanitizeStripsPathSeparatorsAndCollapsesRuns(t *testing.T) {
	cases := map[string]string{
		"AC/DC":             "AC-DC",
		"foo/bar\\baz":      "foo-bar-baz",
		"a:b*c?d\"e<f>g|h":  "a-b-c-d-e-f-g-h",
		" . trailing .  ":   "trailing",
		"normal title":      "normal title",
		"":                  "",
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

	res, err := Download(context.Background(), http.DefaultClient, srv.URL, tr, root)
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

	first, err := Download(context.Background(), http.DefaultClient, srv.URL, tr, root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Skipped {
		t.Error("first call should not be skipped")
	}
	second, err := Download(context.Background(), http.DefaultClient, srv.URL, tr, root)
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
	_, err := Download(context.Background(), http.DefaultClient, srv.URL, tr, root)
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

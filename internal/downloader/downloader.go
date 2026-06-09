// Package downloader writes a media.Track to disk under a configured root,
// preserving the <artist>/<album>/ layout. It's backend-agnostic: callers
// hand in the stream URL (auth is already embedded in it by the backend),
// the downloader GETs and writes the response body.
package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tipii/amptui/internal/media"
)

// Result is one track's outcome — where it was written and whether the
// write was skipped because the destination already existed.
type Result struct {
	Path    string // absolute destination path
	Skipped bool   // true when the file already existed
}

// Download writes track to disk under root, returning the destination path
// (and whether the write was skipped). The destination is:
//
//	<root>/<artist>/<album>/<NN Title>.<ext>
//
// Artist / album / filename segments are sanitized. The extension is
// inferred from the response's Content-Type. Existing files are kept
// (the caller can surface that as "already downloaded"). Writes are
// atomic via a .partial sidecar so a crash never leaves a truncated file.
func Download(ctx context.Context, hc *http.Client, streamURL string, t media.Track, root string) (Result, error) {
	if root == "" {
		return Result{}, fmt.Errorf("download folder not configured")
	}
	if streamURL == "" {
		return Result{}, fmt.Errorf("track %q has no stream URL", t.Title)
	}

	dir := filepath.Join(root, sanitize(orUnknown(t.Artist)), sanitize(orUnknown(t.Album)))
	base := trackBasename(t)

	// Skip if a previously-downloaded version is on disk — match by
	// basename + ".*" since the extension depends on Content-Type and we
	// don't want to hit the network just to learn we already have it.
	if existing := findExisting(dir, base); existing != "" {
		return Result{Path: existing, Skipped: true}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return Result{}, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("GET %s: %s", streamURL, resp.Status)
	}

	ext := extensionFromContentType(resp.Header.Get("Content-Type"))
	dst := filepath.Join(dir, base+ext)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, err
	}
	tmp := dst + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return Result{}, err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return Result{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return Result{}, err
	}
	return Result{Path: dst}, nil
}

// trackBasename builds the filename without an extension: "NN Title" when
// an index is available, otherwise just "Title". The extension is decided
// later from the response's Content-Type.
func trackBasename(t media.Track) string {
	title := sanitize(orUnknown(t.Title))
	if t.Index > 0 {
		return fmt.Sprintf("%02d %s", t.Index, title)
	}
	return title
}

// findExisting returns the absolute path of any file in dir whose name
// starts with base + ".", or "" if there's no match (or dir is missing).
// Used to skip downloads we already have without burning a network call.
func findExisting(dir, base string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	prefix := base + "."
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// orUnknown returns s unless empty, in which case "Unknown" — keeps the
// path well-formed when a backend doesn't populate a field.
func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "Unknown"
	}
	return s
}

// sanitize strips characters that are illegal or awkward in filenames
// across common filesystems, and trims surrounding whitespace and dots.
// Multi-character runs are kept as single dashes for readability.
func sanitize(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		default:
			b.WriteRune(r)
			prevDash = false
		}
	}
	return strings.Trim(b.String(), " .")
}

// extensionFromContentType maps the common audio MIME types we'll see
// from Plex and Jellyfin to a file extension. Unknown types fall back to
// ".audio" so the file is still saved (the user can rename or remux).
func extensionFromContentType(ct string) string {
	mime := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	switch mime {
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return ".m4a"
	case "audio/ogg", "audio/vorbis":
		return ".ogg"
	case "audio/opus":
		return ".opus"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/aac":
		return ".aac"
	case "audio/webm":
		return ".webm"
	}
	return ".audio"
}

// Package downloader writes a media.Track to disk under a configured root,
// preserving the <artist>/<album>/ layout. It's backend-agnostic: callers
// hand in the stream URL (auth is already embedded in it by the backend),
// the downloader GETs and writes the response body, optionally reporting
// progress as it copies.
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

// Request describes one track download: where to fetch it, what to name
// it, and where to put it.
type Request struct {
	// URL is the authenticated source; the backend has already embedded
	// whatever credentials it needs.
	URL string
	// Track supplies the artist / album / disc index / title that the
	// destination path is built from.
	Track media.Track
	// Root is the download folder every file lands under.
	Root string
	// Ext, when set (".mp3"), is the extension the caller knows this URL
	// will produce because it asked the server to transcode. It names
	// the file and narrows the already-have-it check to that one
	// extension, so switching the quality preset re-downloads rather
	// than matching the FLAC sitting next to it. Callers fetching the
	// original leave it empty: the container is the server's business
	// until it answers, so the extension comes from the Content-Type and
	// the have-it check matches any extension.
	Ext string
	// Progress, if non-nil, is called as the body is copied — once when
	// the response arrives (so the name and total are known before the
	// first byte), roughly every progressInterval bytes after that, and
	// once when the copy finishes. It runs on the calling goroutine, so
	// an implementation that hands the snapshot to another one is
	// responsible for the synchronization.
	Progress func(Progress)
}

// Progress is a snapshot of one in-flight track download.
type Progress struct {
	// Name is the destination file's basename.
	Name string
	// Written is the bytes copied so far. Total is the response's
	// Content-Length, or 0 when the server didn't send one — a
	// transcode is generated on the fly, so its length isn't known
	// until it's finished, and consumers can show a rate but not a
	// percentage.
	Written int64
	Total   int64
}

// partialSuffix marks the sidecar a download is written to before it's
// renamed into place, so a crash leaves an obviously-incomplete file
// rather than a truncated one under the real name.
const partialSuffix = ".partial"

// progressInterval is how many bytes accumulate between Progress calls:
// small enough that a slow link still animates, large enough that a fast
// one isn't spending its time in the callback.
const progressInterval = 64 << 10

// Download writes req.Track to disk under req.Root, returning the
// destination path (and whether the write was skipped). The destination
// is:
//
//	<root>/<artist>/<album>/<NN Title>.<ext>
//
// Artist / album / filename segments are sanitized. The extension is
// req.Ext when the caller knows it, otherwise it's inferred from the
// response's Content-Type. Existing files are kept (the caller can
// surface that as "already downloaded"). Writes are atomic via a
// .partial sidecar so a crash never leaves a truncated file.
func Download(ctx context.Context, hc *http.Client, req Request) (Result, error) {
	if req.Root == "" {
		return Result{}, fmt.Errorf("download folder not configured")
	}
	if req.URL == "" {
		return Result{}, fmt.Errorf("track %q has no stream URL", req.Track.Title)
	}

	dir := filepath.Join(req.Root, sanitize(orUnknown(req.Track.Artist)), sanitize(orUnknown(req.Track.Album)))
	base := trackBasename(req.Track)

	// Skip if a previously-downloaded copy is on disk, before spending a
	// request to learn the same thing.
	if existing := findExisting(dir, base, req.Ext); existing != "" {
		return Result{Path: existing, Skipped: true}, nil
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return Result{}, err
	}
	resp, err := hc.Do(hreq)
	if err != nil {
		return Result{}, media.RedactErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("GET %s: %s", media.RedactURL(req.URL), resp.Status)
	}

	// A requested transcode fixes the format, so req.Ext wins: the
	// server may well label its transcoder output with a generic
	// Content-Type, and naming a known MP3 ".audio" would be worse than
	// trusting what we asked for.
	ext := req.Ext
	if ext == "" {
		ext = extensionFromContentType(resp.Header.Get("Content-Type"))
	}
	dst := filepath.Join(dir, base+ext)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, err
	}
	tmp := dst + partialSuffix
	f, err := os.Create(tmp)
	if err != nil {
		return Result{}, err
	}
	// resp.ContentLength is -1 when the server streams without one;
	// Progress reports that as 0 so consumers have a single "unknown".
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	pw := &progressWriter{w: f, fn: req.Progress, name: filepath.Base(dst), total: total}
	pw.report() // publish the name and total before the first byte lands
	if _, err := io.Copy(pw, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return Result{}, err
	}
	pw.report() // final count, which the interval may have skipped past
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return Result{}, err
	}
	return Result{Path: dst}, nil
}

// progressWriter counts bytes on their way to the file and reports them
// through the Request's callback at a coarse interval.
type progressWriter struct {
	w        io.Writer
	fn       func(Progress)
	name     string
	total    int64
	written  int64
	reported int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.written += int64(n)
	if p.written-p.reported >= progressInterval {
		p.report()
	}
	return n, err
}

func (p *progressWriter) report() {
	if p.fn == nil {
		return
	}
	p.reported = p.written
	p.fn(Progress{Name: p.name, Written: p.written, Total: p.total})
}

// trackBasename builds the filename without an extension: "NN Title" when
// an index is available, otherwise just "Title". The extension is decided
// by the caller's Request.Ext or the response's Content-Type.
func trackBasename(t media.Track) string {
	title := sanitize(orUnknown(t.Title))
	if t.Index > 0 {
		return fmt.Sprintf("%02d %s", t.Index, title)
	}
	return title
}

// findExisting returns the absolute path of a previously-downloaded copy
// of base in dir, or "" if there isn't one (or dir is missing). Used to
// skip downloads we already have without burning a network call.
//
// With ext set the match is exact: the caller asked the server for that
// format, and someone switching the quality preset from original to MP3
// wants the MP3 — not a skip because the FLAC is sitting next to it.
// With ext empty any extension counts, since an original download can't
// know its container without asking.
func findExisting(dir, base, ext string) string {
	if ext != "" {
		p := filepath.Join(dir, base+ext)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	prefix := base + "."
	for _, e := range entries {
		// A leftover .partial is not a download we have: the process was
		// killed mid-copy, so the file is truncated. It matches the
		// prefix, though, so without this it would look like a finished
		// download and block every retry.
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && !strings.HasSuffix(e.Name(), partialSuffix) {
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

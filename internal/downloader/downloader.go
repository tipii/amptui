// Package downloader writes a media.Track to disk under a configured root,
// preserving the <artist>/<album>/ layout. It's backend-agnostic: callers
// hand in the stream URL (auth is already embedded in it by the backend),
// the downloader GETs and writes the response body, optionally reporting
// progress as it copies.
package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

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
	// MultiDisc puts the track under a "Disc N" folder inside the album,
	// so two discs' "01 Intro" don't collide. Set it for every track of
	// an album with more than one disc — the caller knows the album, the
	// downloader only sees one track.
	MultiDisc bool
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

// StallTimeout is how long a download may go without receiving a byte
// (including waiting for the response) before it's abandoned. It bounds
// a dead connection, not the transfer: a large hi-res file on a slow
// link can take as long as it needs while data keeps arriving.
var StallTimeout = 60 * time.Second

// progressInterval is how many bytes accumulate between Progress calls:
// small enough that a slow link still animates, large enough that a fast
// one isn't spending its time in the callback.
const progressInterval = 64 << 10

// Download writes req.Track to disk under req.Root, returning the
// destination path (and whether the write was skipped). The destination
// is:
//
//	<root>/<artist>/<album>/<NN Title>.<ext>
//	<root>/<artist>/<album>/Disc <N>/<NN Title>.<ext>   (req.MultiDisc)
//
// Artist / album / filename segments are sanitized. The extension is
// req.Ext when the caller knows it, otherwise it's inferred from the
// response's Content-Type. Existing files are kept (the caller can
// surface that as "already downloaded"). Writes are atomic via a
// .partial sidecar so a crash never leaves a truncated file, and a
// .partial left by an interrupted original-quality download is resumed
// with a Range request where the server supports it.
func Download(ctx context.Context, hc *http.Client, req Request) (Result, error) {
	if req.Root == "" {
		return Result{}, fmt.Errorf("download folder not configured")
	}
	if req.URL == "" {
		return Result{}, fmt.Errorf("track %q has no stream URL", req.Track.Title)
	}

	dir := filepath.Join(req.Root, sanitize(req.Track.Artist), sanitize(req.Track.Album))
	if req.MultiDisc && req.Track.Disc > 0 {
		dir = filepath.Join(dir, fmt.Sprintf("Disc %d", req.Track.Disc))
	}
	base := trackBasename(req.Track)

	// Skip if a previously-downloaded copy is on disk, before spending a
	// request to learn the same thing.
	if existing := findExisting(dir, base, req.Ext); existing != "" {
		return Result{Path: existing, Skipped: true}, nil
	}

	// The stall timer cancels the request if no byte arrives for
	// StallTimeout; every write to disk pushes it back.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(StallTimeout, func() { cancel(errStalled) })
	defer stall.Stop()

	// Only an original can be resumed: a transcode is generated on the
	// fly, so byte N of a retry isn't byte N of the first attempt.
	var resumeFrom int64
	resumePath := ""
	if req.Ext == "" {
		resumePath, resumeFrom = findPartial(dir, base)
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return Result{}, err
	}
	if resumeFrom > 0 {
		hreq.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
	}
	resp, err := hc.Do(hreq)
	if err != nil {
		return Result{}, stalled(ctx, media.RedactErr(err))
	}
	defer resp.Body.Close()
	resumed := resp.StatusCode == http.StatusPartialContent && contentRangeStart(resp) == resumeFrom
	if resp.StatusCode != http.StatusOK && !resumed {
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
	tmp := dst + partialSuffix
	if resumed && resumePath != tmp {
		resumed = false // the partial was for another format
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, err
	}
	var f *os.File
	if resumed {
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_APPEND, 0o644)
	} else {
		f, err = os.Create(tmp)
		resumeFrom = 0
	}
	if err != nil {
		return Result{}, err
	}
	// resp.ContentLength is -1 when the server streams without one;
	// Progress reports that as 0 so consumers have a single "unknown".
	total := resp.ContentLength
	if total < 0 {
		total = 0
	} else if resumed {
		total += resumeFrom
	}
	pw := &progressWriter{w: f, fn: req.Progress, name: filepath.Base(dst), total: total,
		written: resumeFrom, reported: resumeFrom, onWrite: func() { stall.Reset(StallTimeout) }}
	pw.report() // publish the name and total before the first byte lands
	if _, err := io.Copy(pw, resp.Body); err != nil {
		// Keep the .partial: the next attempt resumes from it.
		_ = f.Close()
		return Result{}, stalled(ctx, err)
	}
	pw.report() // final count, which the interval may have skipped past
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return Result{}, err
	}
	if err := f.Close(); err != nil {
		return Result{}, err
	}
	if total > 0 && pw.written != total {
		_ = os.Remove(tmp)
		return Result{}, fmt.Errorf("short download: got %d of %d bytes", pw.written, total)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return Result{}, err
	}
	return Result{Path: dst}, nil
}

// errStalled is the cancel cause the stall timer sets.
var errStalled = errors.New("download stalled")

// stalled rewrites a cancellation caused by the stall timer into an
// error that says so (the parent context's own errors pass through).
func stalled(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), errStalled) {
		return fmt.Errorf("%w: no data for %v", errStalled, StallTimeout)
	}
	return err
}

// findPartial returns a leftover "<base>.<ext>.partial" in dir and its
// size, or ("", 0).
func findPartial(dir, base string) (string, int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, partialSuffix) {
			continue
		}
		if !isBasePlusExt(strings.TrimSuffix(name, partialSuffix), base) {
			continue
		}
		if info, err := e.Info(); err == nil && info.Size() > 0 {
			return filepath.Join(dir, name), info.Size()
		}
	}
	return "", 0
}

// contentRangeStart parses the first byte offset of a 206's
// Content-Range ("bytes 100-999/1000"), or -1.
func contentRangeStart(resp *http.Response) int64 {
	var start, end, size int64
	cr := resp.Header.Get("Content-Range")
	if _, err := fmt.Sscanf(cr, "bytes %d-%d/%d", &start, &end, &size); err == nil {
		return start
	}
	if _, err := fmt.Sscanf(cr, "bytes %d-%d/*", &start, &end); err == nil {
		return start
	}
	return -1
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
	onWrite  func() // called after every write; resets the stall timer
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.written += int64(n)
	if p.onWrite != nil {
		p.onWrite()
	}
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
	title := sanitize(t.Title)
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
	for _, e := range entries {
		// A leftover .partial is not a download we have: the process was
		// killed mid-copy, so the file is truncated.
		if e.IsDir() || strings.HasSuffix(e.Name(), partialSuffix) || !isBasePlusExt(e.Name(), base) {
			continue
		}
		// Neither is an empty file (a crash between create and write).
		if info, err := e.Info(); err == nil && info.Size() > 0 {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// isBasePlusExt reports whether name is base followed by exactly one
// extension: "01 Intro.flac" for base "01 Intro", but not
// "01 Intro. Reprise.flac", which a plain prefix match also accepted.
func isBasePlusExt(name, base string) bool {
	ext, ok := strings.CutPrefix(name, base+".")
	return ok && ext != "" && !strings.ContainsAny(ext, ". ")
}

// maxNameBytes caps a sanitized path segment, leaving room under the
// common 255-byte NAME_MAX for "NN " in front and ".flac.partial" behind.
const maxNameBytes = 200

// sanitize turns s into a safe path segment: characters illegal or
// awkward on common filesystems become dashes (runs collapse to one),
// control characters are dropped, surrounding spaces and dots are
// trimmed, the result is capped at maxNameBytes on a rune boundary, and
// Windows device names get a leading underscore (download folders on
// NTFS / exFAT / SMB refuse them). It never returns "" — an artist named
// ".." used to collapse to nothing and put files one level up — and
// returns "Unknown" for anything that sanitizes away.
func sanitize(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case strings.ContainsRune(`/\:*?"<>|`, r):
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		case r < 0x20 || r == 0x7f:
			// control characters: dropped
		default:
			b.WriteRune(r)
			prevDash = false
		}
	}
	out := strings.Trim(b.String(), " .")
	if len(out) > maxNameBytes {
		cut := maxNameBytes
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out = strings.TrimRight(out[:cut], " .")
	}
	if out == "" {
		return "Unknown"
	}
	if isWindowsReserved(out) {
		out = "_" + out
	}
	return out
}

// isWindowsReserved reports whether name (ignoring any extension) is a
// DOS device name like CON or COM1.
func isWindowsReserved(name string) bool {
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		return stem[3] >= '1' && stem[3] <= '9'
	}
	return false
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

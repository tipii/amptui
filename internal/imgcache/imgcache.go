// Package imgcache stores fetched artwork on disk so repeat views don't
// refetch. Files are keyed by a stable hash of the source path and
// dimensions; lookups are O(1) stat calls.
//
// The cache is bounded: once it grows past MaxBytes, the least recently
// used images (by modification time, which Get refreshes) are evicted.
package imgcache

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxBytes is the cache's size budget. Eviction trims back to 90% of it
// so a full cache doesn't evict on every single Put.
var MaxBytes int64 = 500 << 20

var (
	mu sync.Mutex
	// namespace scopes keys to one server: item IDs (Plex ratingKeys
	// especially, small integers) collide across servers, and switching
	// servers would otherwise show the old one's covers.
	namespace string
	// total is the cache's size in bytes, measured on first Put; -1
	// until then.
	total int64 = -1
)

// SetNamespace scopes every key to the given server identity (e.g. its
// backend and URL). Call once the server is known, before any Get/Put.
func SetNamespace(ns string) {
	mu.Lock()
	namespace = ns
	mu.Unlock()
}

// Dir returns the on-disk cache directory, creating it if needed.
// Defaults to $XDG_CACHE_HOME/amptui/img (falling back to ~/.cache).
func Dir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "amptui", "img")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

// keyFor builds the cache filename for one (thumbPath, width, height)
// triple. Hashing keeps the filename short and filesystem-safe.
func keyFor(thumbPath string, width, height int) string {
	mu.Lock()
	ns := namespace
	mu.Unlock()
	id := fmt.Sprintf("%s|%d|%d", thumbPath, width, height)
	if ns != "" {
		id = ns + "|" + id
	}
	sum := sha1.Sum([]byte(id))
	return hex.EncodeToString(sum[:])
}

// Path returns the on-disk path the cache would use for this image.
// The file may or may not exist — callers should Get/Put around it.
func Path(thumbPath string, width, height int) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, keyFor(thumbPath, width, height)), nil
}

// Get returns the cached bytes for a (thumbPath, width, height)
// triple, or (nil, nil) on cache miss. Errors are reserved for
// actual I/O failures. A hit marks the entry as recently used.
func Get(thumbPath string, width, height int) ([]byte, error) {
	p, err := Path(thumbPath, width, height)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err == nil {
		now := time.Now()
		_ = os.Chtimes(p, now, now)
	}
	return b, err
}

// Put writes the bytes for a (thumbPath, width, height) triple via a
// unique temp file and rename, so a partial write never serves a
// corrupt image and two concurrent writers of the same key can't
// interleave into one file. Evicts old entries if the cache is over
// budget.
func Put(thumbPath string, width, height int, data []byte) error {
	p, err := Path(thumbPath, width, height)
	if err != nil {
		return err
	}
	var prev int64
	if st, err := os.Stat(p); err == nil {
		prev = st.Size()
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	grow(filepath.Dir(p), int64(len(data))-prev)
	return nil
}

// Delete removes one entry, e.g. a cached image that no longer decodes,
// so the next fetch goes back to the server instead of failing forever.
func Delete(thumbPath string, width, height int) error {
	p, err := Path(thumbPath, width, height)
	if err != nil {
		return err
	}
	st, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	grow(filepath.Dir(p), -st.Size())
	return nil
}

// grow adjusts the running total by delta and evicts if over budget.
func grow(dir string, delta int64) {
	mu.Lock()
	defer mu.Unlock()
	if total < 0 {
		total = dirSize(dir)
	} else {
		total += delta
	}
	if total > MaxBytes {
		total = evict(dir, MaxBytes*9/10)
	}
}

// dirSize sums the sizes of the cache files in dir.
func dirSize(dir string) int64 {
	var n int64
	for _, f := range cacheFiles(dir) {
		n += f.size
	}
	return n
}

type cacheFile struct {
	path  string
	size  int64
	mtime time.Time
}

// cacheFiles lists the cache entries in dir, skipping in-flight temp
// files and directories.
func cacheFiles(dir string) []cacheFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]cacheFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, cacheFile{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
	}
	return out
}

// evict removes least-recently-used files until the cache is at most
// target bytes, returning the new total. Caller holds mu.
func evict(dir string, target int64) int64 {
	files := cacheFiles(dir)
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.Before(files[j].mtime) })
	var size int64
	for _, f := range files {
		size += f.size
	}
	for _, f := range files {
		if size <= target {
			break
		}
		if os.Remove(f.path) == nil {
			size -= f.size
		}
	}
	return size
}

// Purge removes every cached image file. The directory itself stays
// so callers don't have to recreate it before the next Put. Errors
// unlinking individual files are returned.
func Purge() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == ".format" {
			continue
		}
		if rerr := os.Remove(filepath.Join(dir, e.Name())); rerr != nil && err == nil {
			err = rerr
		}
	}
	mu.Lock()
	total = -1
	mu.Unlock()
	return err
}

// formatVersion names the cache's key scheme. Bump it when keys change
// in a way that orphans existing files, so Migrate clears them instead
// of leaving them to sit unread until eviction.
//
//	2: keys are namespaced per server (and Plex art is 512px, not full size)
const formatVersion = "2"

// Migrate clears a cache written under an older key scheme, then records
// the current one. Cheap when already current: one small file read.
func Migrate() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	marker := filepath.Join(dir, ".format")
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == formatVersion {
		return nil
	}
	if err := Purge(); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(formatVersion+"\n"), 0o644)
}

// Stats is a snapshot of the on-disk cache footprint.
type Stats struct {
	Path  string // cache directory
	Files int    // number of cached images
	Bytes int64  // total size on disk
	Max   int64  // the size budget (MaxBytes)
}

// GetStats walks the cache directory and returns a footprint summary.
// Costs a stat per cached file; the settings screen caches the result.
func GetStats() (Stats, error) {
	dir, err := Dir()
	if err != nil {
		return Stats{}, err
	}
	s := Stats{Path: dir, Max: MaxBytes}
	for _, f := range cacheFiles(dir) {
		s.Files++
		s.Bytes += f.size
	}
	return s, nil
}

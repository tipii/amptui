// Package imgcache stores fetched Plex artwork on disk so repeat views
// don't refetch. Files are keyed by a stable hash of the source path
// and dimensions; lookups are O(1) stat calls.
package imgcache

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

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
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", thumbPath, width, height)))
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
// actual I/O failures.
func Get(thumbPath string, width, height int) ([]byte, error) {
	p, err := Path(thumbPath, width, height)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// Put writes the bytes for a (thumbPath, width, height) triple using
// atomic temp-file-then-rename so a partial write never serves a
// corrupt image on the next Get.
func Put(thumbPath string, width, height int, data []byte) error {
	p, err := Path(thumbPath, width, height)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Stats is a snapshot of the on-disk cache footprint.
type Stats struct {
	Path    string // cache directory
	Files   int    // number of cached images
	Bytes   int64  // total size on disk
	Missing bool   // true when the directory doesn't exist yet
}

// GetStats walks the cache directory and returns a footprint summary.
// Cheap enough to call on every settings render — the cache has at
// most one tiny file per library item and the dir is local.
func GetStats() (Stats, error) {
	dir, err := Dir()
	if err != nil {
		return Stats{}, err
	}
	s := Stats{Path: dir}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		s.Missing = true
		return s, nil
	}
	if err != nil {
		return s, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		s.Files++
		s.Bytes += info.Size()
	}
	return s, nil
}

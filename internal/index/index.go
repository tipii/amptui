// Package index builds and persists a flat, searchable view of a Plex music
// library — every artist, album, and track as a single Entry — for use by
// the fuzzy-finder search modal in the TUI.
package index

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sahilm/fuzzy"

	"github.com/theopalhol/amptui/internal/plex"
)

// Kind is the type of a library entry.
type Kind int

const (
	KindArtist Kind = iota
	KindAlbum
	KindTrack
)

func (k Kind) String() string {
	switch k {
	case KindArtist:
		return "artist"
	case KindAlbum:
		return "album"
	case KindTrack:
		return "track"
	}
	return "?"
}

// Entry is one searchable item. Fields are populated according to Kind:
//   - artist: Kind, Title, RatingKey
//   - album:  Kind, Title, RatingKey, Artist, ArtistKey, Year
//   - track:  Kind, Title, RatingKey, Album, AlbumKey, Artist, ArtistKey,
//     Year, Index, Duration, PartKey
type Entry struct {
	Kind      Kind          `json:"k"`
	Title     string        `json:"t"`
	RatingKey string        `json:"r"`
	Album     string        `json:"al,omitempty"`
	AlbumKey  string        `json:"alk,omitempty"`
	Artist    string        `json:"ar,omitempty"`
	ArtistKey string        `json:"ark,omitempty"`
	Year      int           `json:"y,omitempty"`
	Index     int           `json:"i,omitempty"`
	Duration  time.Duration `json:"d,omitempty"`
	PartKey   string        `json:"p,omitempty"`
}

// Index is a flat list of Entries plus the metadata needed to know whether
// it's still in sync with the server.
type Index struct {
	SectionUUID      string    `json:"section_uuid"`
	ContentChangedAt int64     `json:"content_changed_at"`
	IndexedAt        time.Time `json:"indexed_at"`
	Entries          []Entry   `json:"entries"`
}

// Build fetches the full library track list, then derives artist and album
// entries by dedup. The returned index is in memory only — call Save() to
// persist it.
func Build(ctx context.Context, client *plex.Client, lib plex.MusicLibrary) (*Index, error) {
	tracks, err := client.LibraryTracks(ctx, lib.Key)
	if err != nil {
		return nil, fmt.Errorf("fetching library tracks: %w", err)
	}
	idx := buildFromTracks(tracks)
	idx.SectionUUID = lib.UUID
	idx.ContentChangedAt = lib.ContentChangedAt
	return idx, nil
}

// buildFromTracks is the pure derivation step: one Entry per track, plus
// deduped artist and album entries from the parent context. Split out so
// tests can exercise it without hitting the network.
func buildFromTracks(tracks []plex.Track) *Index {
	artistTitle := map[string]string{}
	albumByKey := map[string]Entry{}
	trackEntries := make([]Entry, 0, len(tracks))

	for _, t := range tracks {
		trackEntries = append(trackEntries, Entry{
			Kind:      KindTrack,
			Title:     t.Title,
			RatingKey: t.RatingKey,
			Album:     t.Album,
			AlbumKey:  t.AlbumRatingKey,
			Artist:    t.Artist,
			ArtistKey: t.ArtistRatingKey,
			Year:      t.Year,
			Index:     t.Index,
			Duration:  t.Duration,
			PartKey:   t.PartKey,
		})
		if t.ArtistRatingKey != "" {
			if _, ok := artistTitle[t.ArtistRatingKey]; !ok {
				artistTitle[t.ArtistRatingKey] = t.Artist
			}
		}
		if t.AlbumRatingKey != "" {
			if _, ok := albumByKey[t.AlbumRatingKey]; !ok {
				albumByKey[t.AlbumRatingKey] = Entry{
					Kind:      KindAlbum,
					Title:     t.Album,
					RatingKey: t.AlbumRatingKey,
					Artist:    t.Artist,
					ArtistKey: t.ArtistRatingKey,
					Year:      t.Year,
				}
			}
		}
	}

	entries := make([]Entry, 0, len(artistTitle)+len(albumByKey)+len(trackEntries))
	for key, title := range artistTitle {
		entries = append(entries, Entry{Kind: KindArtist, Title: title, RatingKey: key})
	}
	for _, a := range albumByKey {
		entries = append(entries, a)
	}
	entries = append(entries, trackEntries...)

	return &Index{IndexedAt: time.Now(), Entries: entries}
}

// IsFresh reports whether this index still matches the server's current
// content version for lib.
func (idx *Index) IsFresh(lib plex.MusicLibrary) bool {
	return idx != nil &&
		idx.SectionUUID == lib.UUID &&
		idx.ContentChangedAt == lib.ContentChangedAt
}

// CachePath is the on-disk location for an index of the given section UUID.
func CachePath(sectionUUID string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "amptui", sectionUUID+".json"), nil
}

// Save writes the index to ~/.cache/amptui/<uuid>.json, creating the dir.
func (idx *Index) Save() error {
	path, err := CachePath(idx.SectionUUID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Write to a temp file then rename so a crash never leaves a half-written
	// cache that would deserialize partially.
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(idx); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// searchKey is the text we feed to the fuzzy matcher for an entry: an
// artist's name, an album's "Title Artist", or a track's "Title Album Artist"
// so that querying for any of those words surfaces the right rows.
func searchKey(e Entry) string {
	switch e.Kind {
	case KindArtist:
		return e.Title
	case KindAlbum:
		return e.Title + " " + e.Artist
	case KindTrack:
		return e.Title + " " + e.Album + " " + e.Artist
	}
	return e.Title
}

type searchSource struct {
	entries []Entry
	keys    []string
}

func (s searchSource) Len() int            { return len(s.entries) }
func (s searchSource) String(i int) string { return s.keys[i] }

// Search ranks entries against query with fuzzy subsequence matching, best
// score first. If kinds is non-empty, only entries of those kinds are
// considered. limit caps the number of returned matches (<=0 means no cap).
// An empty query returns nil.
func (idx *Index) Search(query string, kinds []Kind, limit int) []Entry {
	if idx == nil || query == "" {
		return nil
	}

	pool := idx.Entries
	if len(kinds) > 0 {
		allow := make(map[Kind]bool, len(kinds))
		for _, k := range kinds {
			allow[k] = true
		}
		pool = make([]Entry, 0, len(idx.Entries))
		for _, e := range idx.Entries {
			if allow[e.Kind] {
				pool = append(pool, e)
			}
		}
	}

	src := searchSource{entries: pool, keys: make([]string, len(pool))}
	for i, e := range pool {
		src.keys[i] = searchKey(e)
	}

	matches := fuzzy.FindFrom(query, src)
	n := len(matches)
	if limit > 0 && n > limit {
		n = limit
	}
	out := make([]Entry, n)
	for i := 0; i < n; i++ {
		out[i] = pool[matches[i].Index]
	}
	return out
}

// Load reads an index from disk. Returns (nil, nil) when no cache exists; an
// error is reserved for I/O or decode failures.
func Load(sectionUUID string) (*Index, error) {
	path, err := CachePath(sectionUUID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var idx Index
	if err := json.NewDecoder(f).Decode(&idx); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return &idx, nil
}

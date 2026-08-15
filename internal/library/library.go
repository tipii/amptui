// Package library is the cache layer for a music library. It owns the
// on-disk snapshot of the library and is the single source of truth for the
// rest of the app — browse, search, counts, everything reads from here.
// The backend (Plex or Jellyfin) is only contacted during Sync.
package library

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sahilm/fuzzy"

	"github.com/tipii/amptui/internal/media"
)

// Kind is the type of a library entry (used by Search to filter).
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

// Entry is one item in the cache. Fields are populated according to Kind:
//   - artist: Kind, Title, RatingKey, AddedAt
//   - album:  Kind, Title, RatingKey, Artist, ArtistKey, Year, AddedAt
//   - track:  Kind, Title, RatingKey, Album, AlbumKey, Artist, ArtistKey,
//     Year, Index, Duration, PartKey, AddedAt
//
// On artist and album entries AddedAt is derived — the most recent AddedAt
// across the entry's tracks — so a "date added" sort surfaces whatever the
// server saw most recently, which is what "recently added" means to a user
// who just imported a new album by an artist they already had.
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
	Disc      int           `json:"dc,omitempty"`
	Duration  time.Duration `json:"d,omitempty"`
	PartKey   string        `json:"p,omitempty"`
	AddedAt   time.Time     `json:"ad,omitempty"`
}

// CacheSchemaVersion is the on-disk schema version. Bump when adding new
// derived fields (count maps, etc.) so old caches force a re-sync instead
// of silently serving stale-shape data.
//
// v3 added Entry.AddedAt and Library.AlbumDuration, both of which back the
// sort layer (see sort.go) and can only be filled from a fresh fetch.
const CacheSchemaVersion = 3

// Library is the in-memory cache of a music library, persisted to
// ~/.cache/amptui/<sectionUUID>.json.
type Library struct {
	SchemaVersion    int       `json:"schema_version"`
	SectionUUID      string    `json:"section_uuid"`
	ContentChangedAt int64     `json:"content_changed_at"`
	SyncedAt         time.Time `json:"synced_at"`
	Entries          []Entry   `json:"entries"`

	// Pre-derived counts so the UI can show "12 albums · 87 tracks" on
	// artist cards and "1971 · 12 tracks" on album cards without scanning
	// Entries on every render. Keyed by RatingKey.
	ArtistAlbumCount map[string]int `json:"artist_album_count,omitempty"`
	ArtistTrackCount map[string]int `json:"artist_track_count,omitempty"`
	AlbumTrackCount  map[string]int `json:"album_track_count,omitempty"`
	// AlbumDuration is the summed runtime of each album's tracks, derived
	// at sync time so the duration sort doesn't rescan Entries per compare.
	AlbumDuration map[string]time.Duration `json:"album_duration,omitempty"`
}

// Artist is a browse-facing artist row with the counts the UI needs.
type Artist struct {
	RatingKey  string
	Title      string
	AlbumCount int
	TrackCount int
	AddedAt    time.Time
}

// Album is a browse-facing album row.
type Album struct {
	RatingKey  string
	Title      string
	Artist     string
	ArtistKey  string
	Year       int
	TrackCount int
	Duration   time.Duration
	AddedAt    time.Time
}

// Track aliases media.Track so callers don't need to import the media
// package just for the type. The library returns rich tracks (PartKey
// populated) suitable for handing to the player.
type Track = media.Track

// Sync fetches the latest tracks from the backend, builds the derived
// caches, and persists the result. Returns the fresh, ready-to-use library.
func Sync(ctx context.Context, backend media.Backend, lib media.MusicLibrary) (*Library, error) {
	tracks, err := backend.LibraryTracks(ctx, lib.Key)
	if err != nil {
		return nil, fmt.Errorf("fetching library tracks: %w", err)
	}
	l := buildFromTracks(tracks)
	l.SchemaVersion = CacheSchemaVersion
	l.SectionUUID = lib.UUID
	l.ContentChangedAt = lib.ContentChangedAt
	_ = l.Save() // best effort; an unwritable cache shouldn't block usage
	return l, nil
}

// buildFromTracks is the pure derivation step: one Entry per track, plus
// deduped artist and album entries from the parent context, plus the count
// maps. Split out so tests can exercise it without hitting the network.
func buildFromTracks(tracks []media.Track) *Library {
	artistTitle := map[string]string{}
	albumByKey := map[string]Entry{}
	trackEntries := make([]Entry, 0, len(tracks))
	artistAlbums := map[string]map[string]struct{}{} // artistKey -> set of albumKeys
	artistTrackCount := map[string]int{}
	albumTrackCount := map[string]int{}
	albumDuration := map[string]time.Duration{}
	artistAdded := map[string]time.Time{} // most recent track AddedAt
	albumAdded := map[string]time.Time{}

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
			Disc:      t.Disc,
			Duration:  t.Duration,
			PartKey:   t.PartKey,
			AddedAt:   t.AddedAt,
		})
		if t.ArtistRatingKey != "" {
			if _, ok := artistTitle[t.ArtistRatingKey]; !ok {
				artistTitle[t.ArtistRatingKey] = t.Artist
			}
			artistTrackCount[t.ArtistRatingKey]++
			if t.AddedAt.After(artistAdded[t.ArtistRatingKey]) {
				artistAdded[t.ArtistRatingKey] = t.AddedAt
			}
			if t.AlbumRatingKey != "" {
				if artistAlbums[t.ArtistRatingKey] == nil {
					artistAlbums[t.ArtistRatingKey] = map[string]struct{}{}
				}
				artistAlbums[t.ArtistRatingKey][t.AlbumRatingKey] = struct{}{}
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
			albumTrackCount[t.AlbumRatingKey]++
			albumDuration[t.AlbumRatingKey] += t.Duration
			if t.AddedAt.After(albumAdded[t.AlbumRatingKey]) {
				albumAdded[t.AlbumRatingKey] = t.AddedAt
			}
		}
	}
	artistAlbumCount := make(map[string]int, len(artistAlbums))
	for k, set := range artistAlbums {
		artistAlbumCount[k] = len(set)
	}

	entries := make([]Entry, 0, len(artistTitle)+len(albumByKey)+len(trackEntries))
	for key, title := range artistTitle {
		entries = append(entries, Entry{
			Kind:      KindArtist,
			Title:     title,
			RatingKey: key,
			AddedAt:   artistAdded[key],
		})
	}
	for key, a := range albumByKey {
		a.AddedAt = albumAdded[key]
		entries = append(entries, a)
	}
	entries = append(entries, trackEntries...)

	return &Library{
		SyncedAt:         time.Now(),
		Entries:          entries,
		ArtistAlbumCount: artistAlbumCount,
		ArtistTrackCount: artistTrackCount,
		AlbumTrackCount:  albumTrackCount,
		AlbumDuration:    albumDuration,
	}
}

// IsFresh reports whether this cache still matches the server's current
// content version for plexLib AND was built with the current schema.
func (l *Library) IsFresh(lib media.MusicLibrary) bool {
	return l != nil &&
		l.SchemaVersion == CacheSchemaVersion &&
		l.SectionUUID == lib.UUID &&
		l.ContentChangedAt == lib.ContentChangedAt
}

// CachePath is the on-disk location for a cache of the given section UUID.
func CachePath(sectionUUID string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "amptui", sectionUUID+".json"), nil
}

// Save writes the cache to disk atomically (temp file + rename).
func (l *Library) Save() error {
	path, err := CachePath(l.SectionUUID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(l); err != nil {
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

// Load reads a cache from disk. Returns (nil, nil) when no cache exists;
// an error is reserved for I/O or decode failures.
func Load(sectionUUID string) (*Library, error) {
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
	var l Library
	if err := json.NewDecoder(f).Decode(&l); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return &l, nil
}

// --- Browse ---

// Artists returns every artist in the section, in the default order
// (title, A–Z). Callers wanting another order pass the result through
// SortArtists.
func (l *Library) Artists() []Artist {
	if l == nil {
		return nil
	}
	var out []Artist
	for _, e := range l.Entries {
		if e.Kind != KindArtist {
			continue
		}
		out = append(out, Artist{
			RatingKey:  e.RatingKey,
			Title:      e.Title,
			AlbumCount: l.ArtistAlbumCount[e.RatingKey],
			TrackCount: l.ArtistTrackCount[e.RatingKey],
			AddedAt:    e.AddedAt,
		})
	}
	SortArtists(out, DefaultArtistSort)
	return out
}

// Albums returns the albums for one artist, in the default order (year,
// then title). Callers wanting another order pass the result through
// SortAlbums.
func (l *Library) Albums(artistKey string) []Album {
	if l == nil {
		return nil
	}
	var out []Album
	for _, e := range l.Entries {
		if e.Kind != KindAlbum || e.ArtistKey != artistKey {
			continue
		}
		out = append(out, Album{
			RatingKey:  e.RatingKey,
			Title:      e.Title,
			Artist:     e.Artist,
			ArtistKey:  e.ArtistKey,
			Year:       e.Year,
			TrackCount: l.AlbumTrackCount[e.RatingKey],
			Duration:   l.AlbumDuration[e.RatingKey],
			AddedAt:    e.AddedAt,
		})
	}
	SortAlbums(out, DefaultAlbumSort)
	return out
}

// Tracks returns the tracks of an album, in the default order (album
// running order). Callers wanting another order pass the result through
// SortTracks.
func (l *Library) Tracks(albumKey string) []Track {
	if l == nil {
		return nil
	}
	var out []Track
	for _, e := range l.Entries {
		if e.Kind != KindTrack || e.AlbumKey != albumKey {
			continue
		}
		out = append(out, Track{
			RatingKey:       e.RatingKey,
			Title:           e.Title,
			Album:           e.Album,
			Artist:          e.Artist,
			AlbumRatingKey:  e.AlbumKey,
			ArtistRatingKey: e.ArtistKey,
			Index:           e.Index,
			Disc:            e.Disc,
			Year:            e.Year,
			Duration:        e.Duration,
			PartKey:         e.PartKey,
			AddedAt:         e.AddedAt,
		})
	}
	SortTracks(out, DefaultTrackSort)
	return out
}

// --- Search ---

// searchKey is the text we feed to the fuzzy matcher for an entry: an
// artist's name, an album's "Title Artist", or a track's "Title Album Artist"
// so a query like "al green stay" surfaces tracks even when their name
// doesn't contain the artist.
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
func (l *Library) Search(query string, kinds []Kind, limit int) []Entry {
	if l == nil || query == "" {
		return nil
	}

	pool := l.Entries
	if len(kinds) > 0 {
		allow := make(map[Kind]bool, len(kinds))
		for _, k := range kinds {
			allow[k] = true
		}
		pool = make([]Entry, 0, len(l.Entries))
		for _, e := range l.Entries {
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

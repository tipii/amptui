// Package media defines the backend-neutral domain types and the
// Backend interface that amptui drives. Plex and Jellyfin each provide
// a Backend implementation that maps their API onto these types, so the
// cache (internal/library) and the UI (internal/tui) never depend on a
// specific server.
//
// Field names lean on Plex's vocabulary (RatingKey, PartKey, …) for
// historical reasons, but they're generic: RatingKey is "the item's
// opaque ID" on either server, PartKey is whatever a backend needs to
// build a stream URL.
package media

import (
	"context"
	"time"
)

// MusicLibrary is a music library/section on the server.
type MusicLibrary struct {
	// Key is the library ID used in subsequent calls.
	Key   string
	Title string
	UUID  string
	// ContentChangedAt is a monotonic content-version counter used to
	// invalidate a local cache when it changes. Plex provides one;
	// backends without an equivalent leave it zero (the cache then
	// relies on TTL / manual refresh).
	ContentChangedAt int64
}

// Track is a single playable track.
type Track struct {
	RatingKey string
	Title     string
	Album     string
	Artist    string
	// AlbumRatingKey / ArtistRatingKey identify the track's parent
	// album and artist; useful for jumping back to them from a search
	// result.
	AlbumRatingKey  string
	ArtistRatingKey string
	// Index is the track number within its album.
	Index    int
	Year     int
	Duration time.Duration
	// PartKey is the server-relative media path. Combined with a
	// backend's StreamURL it yields something mpv can play.
	PartKey string
}

// ArtistMetadata is the rich-metadata view of an artist — bio, tags,
// origin, similar acts.
type ArtistMetadata struct {
	RatingKey string
	Title     string
	Summary   string
	Thumb     string // server-relative artwork path
	Genres    []string
	Moods     []string
	Countries []string
	Styles    []string
	Similar   []string
}

// AlbumMetadata is the rich-metadata view of an album.
type AlbumMetadata struct {
	RatingKey string
	Title     string
	Artist    string
	Year      int
	Summary   string
	Thumb     string // server-relative artwork path
	Studio    string
	Genres    []string
	Moods     []string
	Styles    []string
}

// RecentlyAddedAlbum is one entry on the Recently-Added dashboard tile.
type RecentlyAddedAlbum struct {
	RatingKey string
	Title     string
	Artist    string
	Year      int
	AddedAt   time.Time
}

// Playlist is an audio playlist on the server.
type Playlist struct {
	RatingKey string
	Title     string
	Summary   string
	LeafCount int // track count
	Duration  time.Duration
	UpdatedAt time.Time
	Smart     bool
}

// Backend is the server abstraction amptui drives. Plex and Jellyfin
// each implement it; the cache and UI depend only on this interface.
type Backend interface {
	// ServerName returns the server's friendly/display name (shown in the
	// breadcrumb). Empty string if the server doesn't advertise one.
	ServerName(ctx context.Context) (string, error)
	// MusicLibraries returns the server's music libraries.
	MusicLibraries(ctx context.Context) ([]MusicLibrary, error)
	// LibraryTracks returns every track in a library (paginated
	// internally), with full parent context so the cache can derive
	// artists and albums by deduplication.
	LibraryTracks(ctx context.Context, libraryKey string) ([]Track, error)

	// ArtistMetadata / AlbumMetadata fetch one item's rich metadata.
	ArtistMetadata(ctx context.Context, ratingKey string) (*ArtistMetadata, error)
	AlbumMetadata(ctx context.Context, ratingKey string) (*AlbumMetadata, error)

	// Dashboard feeds.
	RecentlyAddedAlbums(ctx context.Context, libraryKey string, limit int) ([]RecentlyAddedAlbum, error)
	RecentlyPlayedTracks(ctx context.Context, libraryKey string, limit int) ([]Track, error)
	AudioPlaylists(ctx context.Context, limit int) ([]Playlist, error)
	PlaylistTracks(ctx context.Context, ratingKey string) ([]Track, error)

	// StreamURL returns an absolute, authenticated URL mpv can play for
	// the given track, or "" if it has no playable media.
	StreamURL(t Track) string
	// ArtworkURL returns an absolute, authenticated URL for an item's
	// default artwork, looked up by ratingKey.
	ArtworkURL(ratingKey string) string
	// FetchImage GETs an absolute URL (typically from ArtworkURL) and
	// returns the bytes.
	FetchImage(ctx context.Context, absURL string) ([]byte, error)
}

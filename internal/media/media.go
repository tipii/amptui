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
	"errors"
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
	// Index is the track number within its disc; Disc is the disc number
	// within the album (1 for single-disc releases, 0 if the server
	// doesn't say). Track numbers restart per disc, so album running
	// order is (Disc, Index) — sorting on Index alone interleaves the
	// discs of a double album.
	Index    int
	Disc     int
	Year     int
	Duration time.Duration
	// PartKey is the server-relative media path. Combined with a
	// backend's StreamURL it yields something mpv can play.
	PartKey string
	// AddedAt is when the server first saw this track. Backends that
	// don't report it leave it zero, in which case a "date added" sort
	// degrades to the tiebreak (title) rather than misordering.
	AddedAt time.Time
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

// AlbumSummary is an album as a dashboard row shows it: enough to draw
// a card and open the album, plus the listening stats the row is about
// (zero where a feed doesn't have them).
type AlbumSummary struct {
	RatingKey string
	Title     string
	Artist    string
	Year      int
	AddedAt   time.Time

	Plays      int       // how many times its tracks were played
	LastPlayed time.Time // the latest play of any of its tracks
}

// AlbumOrder is the order PlayedAlbums returns albums in.
type AlbumOrder int

const (
	ByLastPlayed AlbumOrder = iota // most recently played first
	ByPlays                        // most played first
)

// Play is one listen in the server's play history.
type Play struct {
	At       time.Time
	AlbumKey string
	Album    string
	Artist   string
}

// ErrNoHistory is PlayHistory's answer from a server that keeps no
// play-by-play history (Jellyfin keeps only per-track counts and the
// last play).
var ErrNoHistory = errors.New("the server keeps no play history")

// Playlist is an audio playlist on the server.
type Playlist struct {
	RatingKey string
	Title     string
	Summary   string
	LeafCount int // track count
	Duration  time.Duration
	UpdatedAt time.Time
	Smart     bool
	// Art is the key ArtworkURL takes for the playlist's cover: its
	// RatingKey on Jellyfin, the server path of its collage on Plex.
	// Empty when it has none.
	Art string
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
	// progress, if non-nil, is called after each page.
	LibraryTracks(ctx context.Context, libraryKey string, progress Progress) ([]Track, error)

	// ArtistMetadata / AlbumMetadata fetch one item's rich metadata.
	ArtistMetadata(ctx context.Context, ratingKey string) (*ArtistMetadata, error)
	AlbumMetadata(ctx context.Context, ratingKey string) (*AlbumMetadata, error)

	// Dashboard feeds.
	RecentlyAddedAlbums(ctx context.Context, libraryKey string, limit int) ([]AlbumSummary, error)
	// PlayedAlbums returns up to limit albums the user has played, in
	// order, with their Plays and LastPlayed.
	PlayedAlbums(ctx context.Context, libraryKey string, order AlbumOrder, limit int) ([]AlbumSummary, error)
	// UnplayedAlbums returns up to limit albums the user has never
	// played, in random order, and how many there are in all.
	UnplayedAlbums(ctx context.Context, libraryKey string, limit int) ([]AlbumSummary, int, error)
	// PlayHistory returns the user's plays since a time, newest first,
	// or ErrNoHistory if the server doesn't keep them.
	PlayHistory(ctx context.Context, libraryKey string, since time.Time) ([]Play, error)
	AudioPlaylists(ctx context.Context, limit int) ([]Playlist, error)
	PlaylistTracks(ctx context.Context, ratingKey string) ([]Track, error)

	// StreamURL returns an absolute, authenticated URL mpv can play for
	// the given track, or "" if it has no playable media. Always direct
	// play: mpv handles every container both servers store, so there is
	// nothing to gain from putting a transcoder in the path.
	StreamURL(t Track) string
	// DownloadURL returns an absolute, authenticated URL to fetch the
	// track's audio at the requested quality. The zero DownloadQuality
	// means the original file, for which this is StreamURL; anything
	// else routes through the server's transcoder. Backends fall back to
	// the original for a quality they can't serve, so a download always
	// produces a file.
	DownloadURL(t Track, q DownloadQuality) string
	// ArtworkURL returns an absolute, authenticated URL for an item's
	// default artwork, looked up by ratingKey.
	ArtworkURL(ratingKey string) string
	// FetchImage GETs an absolute URL (typically from ArtworkURL) and
	// returns the bytes.
	FetchImage(ctx context.Context, absURL string) ([]byte, error)

	// ReportPlayback tells the server what's playing, so it shows in
	// its Now Playing / sessions view: once on start, on pause and
	// resume, periodically while playing, and on stop.
	ReportPlayback(ctx context.Context, r PlaybackReport) error
	// MarkPlayed records a completed play (play count, last played).
	// Called once per play, past 90% of the track. A backend whose
	// server already infers it from the stop report does nothing.
	MarkPlayed(ctx context.Context, t Track) error
}

// PlaybackState is the state carried by a PlaybackReport.
type PlaybackState string

const (
	PlaybackPlaying PlaybackState = "playing"
	PlaybackPaused  PlaybackState = "paused"
	PlaybackStopped PlaybackState = "stopped"
)

// PlaybackReport is one playback progress update for the server.
// Started is set on the first report of a play.
type PlaybackReport struct {
	Track    Track
	State    PlaybackState
	Position time.Duration
	Duration time.Duration
	Started  bool
}

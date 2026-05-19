package plex

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Playlist is an audio playlist on the server. Smart playlists are not
// distinguished from manual ones at the read API — they look identical
// once populated.
type Playlist struct {
	RatingKey string
	Title     string
	Summary   string
	LeafCount int // track count
	Duration  time.Duration
	UpdatedAt time.Time
	Smart     bool
}

// AudioPlaylists returns audio playlists sorted by most-recently-updated,
// useful for a "Recent playlists" dashboard tile.
func (c *Client) AudioPlaylists(ctx context.Context, limit int) ([]Playlist, error) {
	var body struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey    string `json:"ratingKey"`
				Title        string `json:"title"`
				Summary      string `json:"summary"`
				LeafCount    int    `json:"leafCount"`
				Duration     int64  `json:"duration"`
				UpdatedAt    int64  `json:"updatedAt"`
				Smart        int    `json:"smart"`
				PlaylistType string `json:"playlistType"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	path := fmt.Sprintf(
		"/playlists?playlistType=audio&sort=updatedAt:desc&X-Plex-Container-Size=%d", limit,
	)
	if err := c.getJSON(ctx, path, &body); err != nil {
		return nil, err
	}
	out := make([]Playlist, 0, len(body.MediaContainer.Metadata))
	for _, m := range body.MediaContainer.Metadata {
		if m.PlaylistType != "audio" {
			continue
		}
		out = append(out, Playlist{
			RatingKey: m.RatingKey,
			Title:     m.Title,
			Summary:   m.Summary,
			LeafCount: m.LeafCount,
			Duration:  time.Duration(m.Duration) * time.Millisecond,
			UpdatedAt: time.Unix(m.UpdatedAt, 0),
			Smart:     m.Smart == 1,
		})
	}
	return out, nil
}

// PlaylistTracks returns every track in a playlist, in playlist order.
func (c *Client) PlaylistTracks(ctx context.Context, ratingKey string) ([]Track, error) {
	var body struct {
		MediaContainer struct {
			Metadata []trackMetadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	path := fmt.Sprintf("/playlists/%s/items", url.PathEscape(ratingKey))
	if err := c.getJSON(ctx, path, &body); err != nil {
		return nil, err
	}
	out := make([]Track, 0, len(body.MediaContainer.Metadata))
	for _, m := range body.MediaContainer.Metadata {
		if m.Type != "track" {
			continue
		}
		out = append(out, m.toTrack())
	}
	return out, nil
}

package jellyfin

import (
	"context"
	"fmt"
	"net/url"

	"github.com/tipii/amptui/internal/media"
)

// AudioPlaylists returns the server's playlists, most recently changed
// first (the dashboard tile is "recent playlists"; without a sort,
// Jellyfin returned them by name). Jellyfin's per-playlist MediaType is
// unreliable (populated audio playlists report "Unknown"), so we don't
// filter on it — all playlists are returned, and PlaylistTracks skips
// anything that isn't audio. Jellyfin has no smart-playlist concept, so
// Smart is always false.
func (c *Client) AudioPlaylists(ctx context.Context, limit int) ([]media.Playlist, error) {
	userID, err := c.userIDAfterAuth(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("userId", userID)
	q.Set("IncludeItemTypes", "Playlist")
	q.Set("Recursive", "true")
	q.Set("Fields", "Overview,ChildCount,DateCreated")
	q.Set("SortBy", "DateLastContentAdded,DateCreated")
	q.Set("SortOrder", "Descending")
	q.Set("Limit", fmt.Sprintf("%d", limit))
	var body itemsResponse
	if err := c.get(ctx, "/Items", q, &body); err != nil {
		return nil, err
	}
	out := make([]media.Playlist, 0, len(body.Items))
	for _, it := range body.Items {
		out = append(out, media.Playlist{
			RatingKey: it.Id,
			Title:     it.Name,
			Summary:   it.Overview,
			LeafCount: it.ChildCount,
			Duration:  ticksToDuration(it.RunTimeTicks),
			UpdatedAt: it.DateCreated,
			Smart:     false,
		})
	}
	return out, nil
}

// PlaylistTracks returns every track in a playlist, in playlist order.
// Videos in a mixed playlist are skipped: they'd otherwise be queued
// (and sent to mpv) as if they were tracks.
func (c *Client) PlaylistTracks(ctx context.Context, ratingKey string) ([]media.Track, error) {
	userID, err := c.userIDAfterAuth(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{"userId": {userID}, "Fields": {"ProductionYear"}}
	var body itemsResponse
	if err := c.get(ctx, "/Playlists/"+ratingKey+"/Items", q, &body); err != nil {
		return nil, err
	}
	out := make([]media.Track, 0, len(body.Items))
	for _, it := range body.Items {
		if it.Type != "Audio" {
			continue
		}
		out = append(out, it.toTrack())
	}
	return out, nil
}

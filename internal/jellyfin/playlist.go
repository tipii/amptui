package jellyfin

import (
	"context"
	"fmt"
	"net/url"

	"github.com/tipii/amptui/internal/media"
)

// AudioPlaylists returns the server's playlists. Jellyfin's per-playlist
// MediaType is unreliable (populated audio playlists report "Unknown"),
// so we don't filter on it — all playlists are returned. Jellyfin has no
// smart-playlist concept, so Smart is always false.
func (c *Client) AudioPlaylists(ctx context.Context, limit int) ([]media.Playlist, error) {
	_, userID := c.credsAfterAuth(ctx)
	q := url.Values{}
	q.Set("userId", userID)
	q.Set("IncludeItemTypes", "Playlist")
	q.Set("Recursive", "true")
	q.Set("Fields", "Overview,ChildCount")
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
			Smart:     false,
		})
	}
	return out, nil
}

// PlaylistTracks returns every track in a playlist, in playlist order.
func (c *Client) PlaylistTracks(ctx context.Context, ratingKey string) ([]media.Track, error) {
	_, userID := c.credsAfterAuth(ctx)
	q := url.Values{"userId": {userID}, "Fields": {"ProductionYear"}}
	var body itemsResponse
	if err := c.get(ctx, "/Playlists/"+ratingKey+"/Items", q, &body); err != nil {
		return nil, err
	}
	out := make([]media.Track, 0, len(body.Items))
	for _, it := range body.Items {
		out = append(out, it.toTrack())
	}
	return out, nil
}

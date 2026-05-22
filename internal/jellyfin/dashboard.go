package jellyfin

import (
	"context"
	"fmt"
	"net/url"

	"github.com/tipii/amptui/internal/media"
)

// RecentlyAddedAlbums returns the most-recently-added albums in a library.
// /Items/Latest returns a bare array (not the paginated wrapper).
func (c *Client) RecentlyAddedAlbums(ctx context.Context, libraryKey string, limit int) ([]media.RecentlyAddedAlbum, error) {
	_, userID := c.credsAfterAuth(ctx)
	q := url.Values{}
	q.Set("IncludeItemTypes", "MusicAlbum")
	q.Set("ParentId", libraryKey)
	q.Set("Fields", "DateCreated")
	q.Set("Limit", fmt.Sprintf("%d", limit))
	var items []itemDTO
	if err := c.get(ctx, "/Users/"+userID+"/Items/Latest", q, &items); err != nil {
		return nil, err
	}
	out := make([]media.RecentlyAddedAlbum, 0, len(items))
	for _, it := range items {
		artist, _ := it.albumArtist()
		out = append(out, media.RecentlyAddedAlbum{
			RatingKey: it.Id,
			Title:     it.Name,
			Artist:    artist,
			Year:      it.ProductionYear,
			AddedAt:   it.DateCreated,
		})
	}
	return out, nil
}

// RecentlyPlayedTracks returns played tracks sorted by most-recently
// played. Filters=IsPlayed drops never-played material.
func (c *Client) RecentlyPlayedTracks(ctx context.Context, libraryKey string, limit int) ([]media.Track, error) {
	_, userID := c.credsAfterAuth(ctx)
	q := url.Values{}
	q.Set("userId", userID)
	q.Set("ParentId", libraryKey)
	q.Set("IncludeItemTypes", "Audio")
	q.Set("Recursive", "true")
	q.Set("Filters", "IsPlayed")
	q.Set("SortBy", "DatePlayed")
	q.Set("SortOrder", "Descending")
	q.Set("Fields", "ProductionYear")
	q.Set("Limit", fmt.Sprintf("%d", limit))
	var body itemsResponse
	if err := c.get(ctx, "/Items", q, &body); err != nil {
		return nil, err
	}
	out := make([]media.Track, 0, len(body.Items))
	for _, it := range body.Items {
		out = append(out, it.toTrack())
	}
	return out, nil
}

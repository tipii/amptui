package jellyfin

import (
	"context"
	"net/url"

	"github.com/tipii/amptui/internal/media"
)

// itemFields is the metadata field set we ask for on a single-item fetch.
// Jellyfin omits Overview/Genres/Studios unless requested; even then they
// are often empty until the server has scraped metadata for the item.
const itemFields = "Overview,Genres,Studios"

// ArtistMetadata fetches one artist's metadata. Genres/Overview may be
// empty when the server hasn't scraped them — the UI renders that
// gracefully. Similar artists come from a separate endpoint.
func (c *Client) ArtistMetadata(ctx context.Context, ratingKey string) (*media.ArtistMetadata, error) {
	_, userID := c.credsAfterAuth(ctx)
	q := url.Values{"Fields": {itemFields}}
	var it itemDTO
	if err := c.get(ctx, "/Users/"+userID+"/Items/"+ratingKey, q, &it); err != nil {
		return nil, err
	}
	similar, _ := c.similarArtists(ctx, ratingKey)
	return &media.ArtistMetadata{
		RatingKey: it.Id,
		Title:     it.Name,
		Summary:   it.Overview,
		Genres:    it.Genres,
		Similar:   similar,
	}, nil
}

// similarArtists returns the names of artists Jellyfin considers similar.
// Best-effort: an error or empty result just yields no "similar" list.
func (c *Client) similarArtists(ctx context.Context, ratingKey string) ([]string, error) {
	_, userID := c.creds()
	q := url.Values{"userId": {userID}, "Limit": {"8"}}
	var body itemsResponse
	if err := c.get(ctx, "/Artists/"+ratingKey+"/Similar", q, &body); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(body.Items))
	for _, it := range body.Items {
		names = append(names, it.Name)
	}
	return names, nil
}

// AlbumMetadata fetches one album's metadata.
func (c *Client) AlbumMetadata(ctx context.Context, ratingKey string) (*media.AlbumMetadata, error) {
	_, userID := c.credsAfterAuth(ctx)
	q := url.Values{"Fields": {itemFields}}
	var it itemDTO
	if err := c.get(ctx, "/Users/"+userID+"/Items/"+ratingKey, q, &it); err != nil {
		return nil, err
	}
	artist, _ := it.albumArtist()
	var studio string
	if len(it.Studios) > 0 {
		studio = it.Studios[0].Name
	}
	return &media.AlbumMetadata{
		RatingKey: it.Id,
		Title:     it.Name,
		Artist:    artist,
		Year:      it.ProductionYear,
		Summary:   it.Overview,
		Studio:    studio,
		Genres:    it.Genres,
	}, nil
}

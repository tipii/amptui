package jellyfin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// artworkMaxDim caps the artwork the server renders; the picture layer
// downscales to the card/header cell size client-side, so a mid-size
// image keeps downloads small without visibly softening cards.
const artworkMaxDim = 512

// ArtworkURL returns a URL for an item's primary image, looked up by
// ratingKey. Returns "" for an empty key. Items without a primary image
// 404 on fetch, which the caller treats as "no artwork".
func (c *Client) ArtworkURL(ratingKey string) string {
	if ratingKey == "" {
		return ""
	}
	token, _ := c.creds()
	return fmt.Sprintf("%s/Items/%s/Images/Primary?fillWidth=%d&fillHeight=%d&quality=90&api_key=%s",
		c.serverURL, url.PathEscape(ratingKey), artworkMaxDim, artworkMaxDim, url.QueryEscape(token))
}

// FetchImage GETs an absolute URL (typically from ArtworkURL) and returns
// the response bytes. Used by the on-disk image cache.
func (c *Client) FetchImage(ctx context.Context, absURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, absURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("GET %s: %s: %s", absURL, resp.Status, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(resp.Body)
}

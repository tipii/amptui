package plex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ArtworkURL returns a direct (non-transcoded) URL for an item's
// default thumb, looked up by ratingKey. Useful for grid cards where
// we don't have the full thumb path on hand. The server returns the
// original image at full size; the renderer downscales client-side.
func (c *Client) ArtworkURL(ratingKey string) string {
	if ratingKey == "" {
		return ""
	}
	return fmt.Sprintf("%s/library/metadata/%s/thumb?X-Plex-Token=%s",
		c.serverURL, url.PathEscape(ratingKey), url.QueryEscape(c.token),
	)
}

// FetchImage GETs an absolute URL (typically one returned by
// ThumbURL) and returns the response bytes. Used by the on-disk
// image cache.
func (c *Client) FetchImage(ctx context.Context, absURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, absURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Plex-Client-Identifier", clientIdentifier)
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

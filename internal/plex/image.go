package plex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ThumbURL builds an authenticated URL for a Plex image at the given
// pixel dimensions. Plex's /photo/:/transcode endpoint resizes any
// server-relative image (artist/album thumb, art, etc.) on the fly —
// it expects the source as a RELATIVE path in the url= parameter,
// not a fully-qualified URL.
// thumbPath is typically the value of the Thumb field on a metadata
// object, e.g. "/library/metadata/12345/thumb/1700000000".
func (c *Client) ThumbURL(thumbPath string, width, height int) string {
	if thumbPath == "" {
		return ""
	}
	return fmt.Sprintf("%s/photo/:/transcode?width=%d&height=%d&url=%s&X-Plex-Token=%s",
		c.serverURL, width, height,
		url.QueryEscape(thumbPath),
		url.QueryEscape(c.token),
	)
}

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

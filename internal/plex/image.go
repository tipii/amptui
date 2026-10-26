package plex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tipii/amptui/internal/media"
)

// artworkSize is the bounding box, in pixels, artwork is fetched at —
// the same as the Jellyfin backend. Comfortably above the largest
// on-screen use (the info-modal thumb at Kitty resolution).
const artworkSize = 512

// maxImageBytes caps an artwork response, so a misbehaving server can't
// make the image cache or a decode buffer grow without bound.
const maxImageBytes = 16 << 20

// ArtworkURL returns a URL for an item's default thumb, looked up by
// ratingKey, scaled by Plex's photo transcoder to fit artworkSize. The
// direct /library/metadata/<key>/thumb returns the original upload —
// often several MB — which made browsing a grid over a slow link mostly
// a matter of downloading wallpapers.
func (c *Client) ArtworkURL(ratingKey string) string {
	if ratingKey == "" {
		return ""
	}
	q := url.Values{}
	q.Set("width", fmt.Sprint(artworkSize))
	q.Set("height", fmt.Sprint(artworkSize))
	q.Set("minSize", "1") // fill the box, don't letterbox inside it
	q.Set("upscale", "0") // small originals stay small
	q.Set("url", "/library/metadata/"+url.PathEscape(ratingKey)+"/thumb")
	q.Set("X-Plex-Token", c.token)
	return c.serverURL + "/photo/:/transcode?" + q.Encode()
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
		return nil, media.RedactErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("GET %s: %s: %s", media.RedactURL(absURL), resp.Status, strings.TrimSpace(string(body)))
	}
	return readCapped(resp.Body, maxImageBytes)
}

// readCapped reads all of r, failing rather than truncating past limit.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, fmt.Errorf("image larger than %d bytes", limit)
	}
	return b, err
}

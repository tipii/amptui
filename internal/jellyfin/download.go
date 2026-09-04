package jellyfin

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/tipii/amptui/internal/media"
)

// DownloadURL returns the URL to fetch t's audio at quality q.
//
// The original is the same static direct-play file StreamURL hands mpv.
// A transcode drops static=true and names the container in the path —
// /Audio/{id}/stream.mp3 — which is the endpoint Jellyfin's own clients
// use when a device can't play the source. Unlike the /universal
// endpoint it needs no device or play-session id, so a plain GET with an
// api_key is enough.
//
// Any codec other than MP3 falls back to the original, matching Plex:
// the container in the path fixes the output format.
func (c *Client) DownloadURL(t media.Track, q media.DownloadQuality) string {
	if q.IsOriginal() || q.Codec != media.CodecMP3 || t.RatingKey == "" {
		return c.StreamURL(t)
	}
	token, _ := c.creds()
	v := url.Values{}
	v.Set("static", "false")
	v.Set("audioCodec", q.Codec)
	// Jellyfin counts this one in bits per second, not kbps.
	v.Set("audioBitRate", strconv.Itoa(q.Bitrate*1000))
	v.Set("api_key", token)
	return fmt.Sprintf("%s/Audio/%s/stream.%s?%s",
		c.serverURL, url.PathEscape(t.RatingKey), q.Codec, v.Encode())
}

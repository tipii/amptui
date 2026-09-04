package plex

import (
	"net/url"
	"strconv"

	"github.com/tipii/amptui/internal/media"
)

// transcodePlatform is sent as X-Plex-Platform on transcode requests. It
// picks the client profile the server reasons about, and it has to name
// a profile the server actually has: omit it, or send something honest
// like "Linux", and the universal transcoder answers 400 Bad Request
// with no explanation. "Chrome" is the profile Plex's own web client
// uses. The profile only seeds the transcode decision, which the
// explicit musicBitrate below overrides anyway.
const transcodePlatform = "Chrome"

// DownloadURL returns the URL to fetch t's audio at quality q.
//
// The original is the same direct-play part StreamURL hands mpv. A
// transcode goes to /music/:/transcode/universal/start.mp3 — the
// endpoint behind Plex's own "convert automatically" quality menu. It
// answers a chunked MP3 stream with no Content-Length, so callers
// showing progress can report bytes and rate but not a percentage.
//
// Any codec other than MP3 falls back to the original: this endpoint's
// extension fixes the output format, so there'd be nothing honest to
// return for a target Plex isn't being asked to produce.
func (c *Client) DownloadURL(t media.Track, q media.DownloadQuality) string {
	if q.IsOriginal() || q.Codec != media.CodecMP3 || t.RatingKey == "" {
		return c.StreamURL(t)
	}
	v := url.Values{}
	v.Set("path", "/library/metadata/"+t.RatingKey)
	v.Set("protocol", "http")
	v.Set("musicBitrate", strconv.Itoa(q.Bitrate))
	v.Set("X-Plex-Platform", transcodePlatform)
	v.Set("X-Plex-Client-Identifier", clientIdentifier)
	v.Set("X-Plex-Token", c.token)
	return c.serverURL + "/music/:/transcode/universal/start.mp3?" + v.Encode()
}

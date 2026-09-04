package media

import (
	"fmt"
	"strconv"
	"strings"
)

// DownloadQuality selects the encoding amptui asks a server for when
// saving a track to disk. The zero value means the original file — no
// transcode, whatever container the server holds (usually FLAC). Any
// other value asks the backend's transcoder to re-encode on the fly,
// which is how Plex's and Jellyfin's own apps implement their quality
// menus.
//
// Playback is deliberately unaffected: mpv plays every container either
// server stores, so StreamURL always direct-plays. Only downloads read
// this — the point is a smaller file on disk, not a smaller stream.
type DownloadQuality struct {
	// Codec is the target audio codec ("mp3"), or "" for the original.
	Codec string
	// Bitrate is the target in kbps, 0 alongside an empty codec. Both
	// servers encode VBR around this target rather than pinning to it
	// exactly.
	Bitrate int
}

// DownloadQualities are the presets the settings picker offers, in menu
// order. They mirror the rungs Plex and Jellyfin expose in their own
// clients: 320 is "keep it transparent", 128 is "fit more on the phone".
var DownloadQualities = []DownloadQuality{
	{},
	{Codec: CodecMP3, Bitrate: 320},
	{Codec: CodecMP3, Bitrate: 256},
	{Codec: CodecMP3, Bitrate: 192},
	{Codec: CodecMP3, Bitrate: 128},
}

// CodecMP3 is the one transcode target amptui offers. Both backends can
// produce it for any source in their libraries, and every device that
// plays audio at all plays it — the reason to transcode in the first
// place.
const CodecMP3 = "mp3"

// minBitrate / maxBitrate bound a hand-edited config value. Outside this
// range the setting is treated as a typo (and falls back to the
// original) rather than passed to the server, which would either clamp
// it silently or refuse the request.
const (
	minBitrate = 32
	maxBitrate = 320
)

// IsOriginal reports whether q asks for the untouched source file.
func (q DownloadQuality) IsOriginal() bool { return q.Codec == "" }

// String is the config-file form: "original" or "<codec>-<kbps>".
func (q DownloadQuality) String() string {
	if q.IsOriginal() {
		return "original"
	}
	return fmt.Sprintf("%s-%d", q.Codec, q.Bitrate)
}

// Label is the human form, shown in the settings picker and the
// downloads modal header.
func (q DownloadQuality) Label() string {
	if q.IsOriginal() {
		return "original (no transcode)"
	}
	return fmt.Sprintf("%s %d kbps", strings.ToUpper(q.Codec), q.Bitrate)
}

// Ext is the file extension a download at this quality will produce, or
// "" for the original — whose container is the server's business and
// isn't known until it answers. Callers use it both to name the file and
// to decide whether they already have it on disk.
func (q DownloadQuality) Ext() string {
	if q.IsOriginal() {
		return ""
	}
	return "." + q.Codec
}

// ParseDownloadQuality reads the config form ("mp3-320"). Anything
// unrecognized falls back to the original file rather than erroring: a
// typo in config.toml should cost you a transcode, not your downloads.
func ParseDownloadQuality(s string) DownloadQuality {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "original" {
		return DownloadQuality{}
	}
	codec, rate, ok := strings.Cut(s, "-")
	if !ok || codec != CodecMP3 {
		return DownloadQuality{}
	}
	kbps, err := strconv.Atoi(rate)
	if err != nil || kbps < minBitrate || kbps > maxBitrate {
		return DownloadQuality{}
	}
	return DownloadQuality{Codec: codec, Bitrate: kbps}
}

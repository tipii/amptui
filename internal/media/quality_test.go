package media

import "testing"

func TestParseDownloadQuality(t *testing.T) {
	cases := []struct {
		in   string
		want DownloadQuality
	}{
		{"", DownloadQuality{}},
		{"original", DownloadQuality{}},
		{"ORIGINAL", DownloadQuality{}},
		{"  mp3-320  ", DownloadQuality{Codec: "mp3", Bitrate: 320}},
		{"MP3-128", DownloadQuality{Codec: "mp3", Bitrate: 128}},
		// A bitrate that isn't a preset is still honored — hand-editing
		// the config shouldn't be limited to the picker's five rungs.
		{"mp3-96", DownloadQuality{Codec: "mp3", Bitrate: 96}},
		// Nonsense falls back to the original rather than erroring, so a
		// typo costs a transcode and not the download.
		{"flac-1000", DownloadQuality{}},
		{"mp3", DownloadQuality{}},
		{"mp3-", DownloadQuality{}},
		{"mp3-abc", DownloadQuality{}},
		{"mp3-0", DownloadQuality{}},
		{"mp3-9999", DownloadQuality{}}, // above maxBitrate
		{"mp3-8", DownloadQuality{}},    // below minBitrate
	}
	for _, c := range cases {
		if got := ParseDownloadQuality(c.in); got != c.want {
			t.Errorf("ParseDownloadQuality(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// TestDownloadQualityRoundTrips is what keeps the settings picker honest:
// its option values are String() and its stored value is re-parsed on the
// next launch, so any preset that didn't survive the trip would silently
// reset itself.
func TestDownloadQualityRoundTrips(t *testing.T) {
	for _, q := range DownloadQualities {
		if got := ParseDownloadQuality(q.String()); got != q {
			t.Errorf("round trip of %+v via %q = %+v", q, q.String(), got)
		}
	}
}

func TestDownloadQualityStringAndExt(t *testing.T) {
	orig := DownloadQuality{}
	if !orig.IsOriginal() {
		t.Error("zero value should be the original")
	}
	if got := orig.String(); got != "original" {
		t.Errorf("original String() = %q", got)
	}
	// The original's container is the server's business until it
	// answers, so there's no extension to predict.
	if got := orig.Ext(); got != "" {
		t.Errorf("original Ext() = %q, want empty", got)
	}

	mp3 := DownloadQuality{Codec: CodecMP3, Bitrate: 320}
	if mp3.IsOriginal() {
		t.Error("a transcode preset is not the original")
	}
	if got, want := mp3.String(), "mp3-320"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := mp3.Ext(), ".mp3"; got != want {
		t.Errorf("Ext() = %q, want %q", got, want)
	}
	if got, want := mp3.Label(), "MP3 320 kbps"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
}

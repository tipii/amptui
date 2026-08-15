package plex

import (
	"testing"
	"time"
)

// TestTrackMetadataToTrack covers the JSON→domain mapping, including
// Plex's unix-seconds addedAt (which backs the date-added sort) and the
// nested Media/Part lookup that yields a playable key.
func TestTrackMetadataToTrack(t *testing.T) {
	m := trackMetadata{
		RatingKey:            "123",
		Title:                "Kashmir",
		ParentTitle:          "Physical Graffiti",
		GrandparentTitle:     "Led Zeppelin",
		ParentRatingKey:      "45",
		GrandparentRatingKey: "6",
		Index:                1,
		ParentYear:           1975,
		Duration:             508000,
		AddedAt:              1709294400, // 2024-03-01T12:00:00Z
		Type:                 "track",
	}
	m.Media = []struct {
		Part []struct {
			Key string `json:"key"`
		} `json:"Part"`
	}{{Part: []struct {
		Key string `json:"key"`
	}{{Key: "/library/parts/1/file.flac"}}}}

	tr := m.toTrack()
	if tr.RatingKey != "123" || tr.Album != "Physical Graffiti" || tr.Artist != "Led Zeppelin" {
		t.Errorf("identity/parent mapping: %+v", tr)
	}
	if tr.Duration != 508*time.Second {
		t.Errorf("duration = %v, want 508s (millis → Duration)", tr.Duration)
	}
	if want := time.Unix(1709294400, 0); !tr.AddedAt.Equal(want) {
		t.Errorf("AddedAt = %v, want %v", tr.AddedAt, want)
	}
	if tr.PartKey != "/library/parts/1/file.flac" {
		t.Errorf("PartKey = %q", tr.PartKey)
	}
}

// A server that doesn't report addedAt must leave AddedAt zero rather than
// mapping to the unix epoch, which would sort as "added in 1970".
func TestTrackMetadataMissingAddedAt(t *testing.T) {
	tr := trackMetadata{RatingKey: "1", Title: "x"}.toTrack()
	if !tr.AddedAt.IsZero() {
		t.Errorf("AddedAt = %v, want zero time", tr.AddedAt)
	}
}

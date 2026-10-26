package plex

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// trackMetadata is the JSON shape for the section-wide LibraryTracks fetch.
type trackMetadata struct {
	RatingKey            string `json:"ratingKey"`
	Title                string `json:"title"`
	ParentTitle          string `json:"parentTitle"`
	GrandparentTitle     string `json:"grandparentTitle"`
	ParentRatingKey      string `json:"parentRatingKey"`
	GrandparentRatingKey string `json:"grandparentRatingKey"`
	Index                int    `json:"index"`
	ParentIndex          int    `json:"parentIndex"` // disc number
	ParentYear           int    `json:"parentYear"`
	Duration             int64  `json:"duration"`
	AddedAt              int64  `json:"addedAt"` // unix seconds
	Type                 string `json:"type"`
	Media                []struct {
		Part []struct {
			Key string `json:"key"`
		} `json:"Part"`
	} `json:"Media"`
}

func (m trackMetadata) toTrack() media.Track {
	t := media.Track{
		RatingKey:       m.RatingKey,
		Title:           m.Title,
		Album:           m.ParentTitle,
		Artist:          m.GrandparentTitle,
		AlbumRatingKey:  m.ParentRatingKey,
		ArtistRatingKey: m.GrandparentRatingKey,
		Index:           m.Index,
		Disc:            m.ParentIndex,
		Year:            m.ParentYear,
		Duration:        time.Duration(m.Duration) * time.Millisecond,
	}
	t.AddedAt = unixOrZero(m.AddedAt)
	if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
		t.PartKey = m.Media[0].Part[0].Key
	}
	return t
}

// LibraryTracks fetches every track in a music library section, a page
// at a time (see media.FetchAll for the timeout / retry policy), until
// the section's totalSize is reached. The returned tracks carry full
// parent context (album+artist titles AND ratingKeys + year), so a caller
// can derive artist and album entries by deduplication.
func (c *Client) LibraryTracks(ctx context.Context, sectionKey string, progress media.Progress) ([]media.Track, error) {
	items, err := media.FetchAll(ctx, func(ctx context.Context, start int) ([]trackMetadata, int, error) {
		var body struct {
			MediaContainer struct {
				TotalSize *int            `json:"totalSize"`
				Metadata  []trackMetadata `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		path := fmt.Sprintf(
			"/library/sections/%s/all?type=10&X-Plex-Container-Start=%d&X-Plex-Container-Size=%d",
			url.PathEscape(sectionKey), start, media.PageSize,
		)
		if err := c.getJSONWith(ctx, c.pageHTTP, path, &body); err != nil {
			return nil, 0, err
		}
		total := -1
		if body.MediaContainer.TotalSize != nil {
			total = *body.MediaContainer.TotalSize
		}
		return body.MediaContainer.Metadata, total, nil
	}, progress)
	if err != nil {
		return nil, err
	}
	// Filtered after paging, not per page: the offset has to advance by
	// what the server returned, tracks or not.
	tracks := make([]media.Track, 0, len(items))
	for _, m := range items {
		if m.Type == "track" {
			tracks = append(tracks, m.toTrack())
		}
	}
	return tracks, nil
}

// StreamURL builds an absolute, authenticated URL for a track's media part,
// suitable for handing to mpv. Returns "" if the track has no playable part.
func (c *Client) StreamURL(t media.Track) string {
	if t.PartKey == "" {
		return ""
	}
	return fmt.Sprintf("%s%s?X-Plex-Token=%s", c.serverURL, t.PartKey, url.QueryEscape(c.token))
}

// unixOrZero converts a Plex epoch-seconds field, mapping the 0 Plex
// sends for "unknown" to the zero time rather than 1970-01-01, which
// would sort as the oldest item and render as a real date.
func unixOrZero(secs int64) time.Time {
	if secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}

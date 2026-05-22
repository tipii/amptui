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
	ParentYear           int    `json:"parentYear"`
	Duration             int64  `json:"duration"`
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
		Year:            m.ParentYear,
		Duration:        time.Duration(m.Duration) * time.Millisecond,
	}
	if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
		t.PartKey = m.Media[0].Part[0].Key
	}
	return t
}

// libraryTracksPageSize bounds each /library/sections/{key}/all?type=10 page.
const libraryTracksPageSize = 500

// LibraryTracks fetches every track in a music library section, paginating
// until the server returns a short page. The returned tracks carry full
// parent context (album+artist titles AND ratingKeys + year), so a caller
// can derive artist and album entries by deduplication.
func (c *Client) LibraryTracks(ctx context.Context, sectionKey string) ([]media.Track, error) {
	var all []media.Track
	start := 0
	for {
		var body struct {
			MediaContainer struct {
				Metadata []trackMetadata `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		path := fmt.Sprintf(
			"/library/sections/%s/all?type=10&X-Plex-Container-Start=%d&X-Plex-Container-Size=%d",
			url.PathEscape(sectionKey), start, libraryTracksPageSize,
		)
		if err := c.getJSON(ctx, path, &body); err != nil {
			return nil, err
		}
		page := body.MediaContainer.Metadata
		for _, m := range page {
			if m.Type != "track" {
				continue
			}
			all = append(all, m.toTrack())
		}
		if len(page) < libraryTracksPageSize {
			return all, nil
		}
		start += libraryTracksPageSize
	}
}

// StreamURL builds an absolute, authenticated URL for a track's media part,
// suitable for handing to mpv. Returns "" if the track has no playable part.
func (c *Client) StreamURL(t media.Track) string {
	if t.PartKey == "" {
		return ""
	}
	return fmt.Sprintf("%s%s?X-Plex-Token=%s", c.serverURL, t.PartKey, url.QueryEscape(c.token))
}

package plex

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// RecentlyAddedAlbum is one entry on the Recently-Added dashboard tile.
type RecentlyAddedAlbum struct {
	RatingKey string
	Title     string
	Artist    string
	Year      int
	AddedAt   time.Time
}

// RecentlyAddedAlbums fetches the most-recently-added albums in a section.
// limit caps the page size; pass 20-50 for a typical dashboard tile.
func (c *Client) RecentlyAddedAlbums(ctx context.Context, sectionKey string, limit int) ([]RecentlyAddedAlbum, error) {
	var body struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey   string `json:"ratingKey"`
				Title       string `json:"title"`
				ParentTitle string `json:"parentTitle"`
				Year        int    `json:"year"`
				AddedAt     int64  `json:"addedAt"`
				Type        string `json:"type"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	path := fmt.Sprintf(
		"/library/sections/%s/all?type=9&sort=addedAt:desc&X-Plex-Container-Start=0&X-Plex-Container-Size=%d",
		url.PathEscape(sectionKey), limit,
	)
	if err := c.getJSON(ctx, path, &body); err != nil {
		return nil, err
	}
	out := make([]RecentlyAddedAlbum, 0, len(body.MediaContainer.Metadata))
	for _, m := range body.MediaContainer.Metadata {
		if m.Type != "album" {
			continue
		}
		out = append(out, RecentlyAddedAlbum{
			RatingKey: m.RatingKey,
			Title:     m.Title,
			Artist:    m.ParentTitle,
			Year:      m.Year,
			AddedAt:   time.Unix(m.AddedAt, 0),
		})
	}
	return out, nil
}

// RecentlyPlayedTracks returns tracks in a section sorted by last-played
// time, descending. Tracks that have never been played are filtered out
// by the lastViewedAt>0 query parameter so the page isn't padded with
// unplayed material.
func (c *Client) RecentlyPlayedTracks(ctx context.Context, sectionKey string, limit int) ([]Track, error) {
	var body struct {
		MediaContainer struct {
			Metadata []trackMetadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	path := fmt.Sprintf(
		"/library/sections/%s/all?type=10&sort=lastViewedAt:desc&lastViewedAt%%3E=1&X-Plex-Container-Start=0&X-Plex-Container-Size=%d",
		url.PathEscape(sectionKey), limit,
	)
	if err := c.getJSON(ctx, path, &body); err != nil {
		return nil, err
	}
	out := make([]Track, 0, len(body.MediaContainer.Metadata))
	for _, m := range body.MediaContainer.Metadata {
		if m.Type != "track" {
			continue
		}
		out = append(out, m.toTrack())
	}
	return out, nil
}

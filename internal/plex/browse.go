package plex

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Artist is a music artist in a library section.
type Artist struct {
	// RatingKey is the metadata ID; pass it to Albums.
	RatingKey string
	Title     string
	Thumb     string
}

// Album is a release by an artist.
type Album struct {
	// RatingKey is the metadata ID; pass it to Tracks.
	RatingKey string
	Title     string
	Artist    string
	Year      int
	Thumb     string
}

// Track is a single playable track.
type Track struct {
	RatingKey string
	Title     string
	Album     string
	Artist    string
	// Index is the track number within its album.
	Index    int
	Duration time.Duration
	// PartKey is the server-relative media path (e.g. /library/parts/123/.../file.flac).
	// Combine with StreamURL to get something mpv can play.
	PartKey string
}

// Artists returns the artists in a music library section, ordered by the
// server's default sort (title).
func (c *Client) Artists(ctx context.Context, sectionKey string) ([]Artist, error) {
	var body struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey string `json:"ratingKey"`
				Title     string `json:"title"`
				Thumb     string `json:"thumb"`
				Type      string `json:"type"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	path := fmt.Sprintf("/library/sections/%s/all", url.PathEscape(sectionKey))
	if err := c.getJSON(ctx, path, &body); err != nil {
		return nil, err
	}

	var artists []Artist
	for _, m := range body.MediaContainer.Metadata {
		if m.Type != "artist" {
			continue
		}
		artists = append(artists, Artist{RatingKey: m.RatingKey, Title: m.Title, Thumb: m.Thumb})
	}
	return artists, nil
}

// Albums returns the albums for an artist.
func (c *Client) Albums(ctx context.Context, artistKey string) ([]Album, error) {
	var body struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey   string `json:"ratingKey"`
				Title       string `json:"title"`
				ParentTitle string `json:"parentTitle"`
				Year        int    `json:"year"`
				Thumb       string `json:"thumb"`
				Type        string `json:"type"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	path := fmt.Sprintf("/library/metadata/%s/children", url.PathEscape(artistKey))
	if err := c.getJSON(ctx, path, &body); err != nil {
		return nil, err
	}

	var albums []Album
	for _, m := range body.MediaContainer.Metadata {
		if m.Type != "album" {
			continue
		}
		albums = append(albums, Album{
			RatingKey: m.RatingKey,
			Title:     m.Title,
			Artist:    m.ParentTitle,
			Year:      m.Year,
			Thumb:     m.Thumb,
		})
	}
	return albums, nil
}

// Tracks returns the tracks on an album, in album order.
func (c *Client) Tracks(ctx context.Context, albumKey string) ([]Track, error) {
	var body struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey        string `json:"ratingKey"`
				Title            string `json:"title"`
				ParentTitle      string `json:"parentTitle"`
				GrandparentTitle string `json:"grandparentTitle"`
				Index            int    `json:"index"`
				Duration         int64  `json:"duration"`
				Type             string `json:"type"`
				Media            []struct {
					Part []struct {
						Key string `json:"key"`
					} `json:"Part"`
				} `json:"Media"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	path := fmt.Sprintf("/library/metadata/%s/children", url.PathEscape(albumKey))
	if err := c.getJSON(ctx, path, &body); err != nil {
		return nil, err
	}

	var tracks []Track
	for _, m := range body.MediaContainer.Metadata {
		if m.Type != "track" {
			continue
		}
		t := Track{
			RatingKey: m.RatingKey,
			Title:     m.Title,
			Album:     m.ParentTitle,
			Artist:    m.GrandparentTitle,
			Index:     m.Index,
			Duration:  time.Duration(m.Duration) * time.Millisecond,
		}
		if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
			t.PartKey = m.Media[0].Part[0].Key
		}
		tracks = append(tracks, t)
	}
	return tracks, nil
}

// StreamURL builds an absolute, authenticated URL for a track's media part,
// suitable for handing to mpv. Returns "" if the track has no playable part.
func (c *Client) StreamURL(t Track) string {
	if t.PartKey == "" {
		return ""
	}
	return fmt.Sprintf("%s%s?X-Plex-Token=%s", c.serverURL, t.PartKey, url.QueryEscape(c.token))
}

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
	// AlbumRatingKey / ArtistRatingKey identify the track's parent album and
	// artist; useful for jumping back to them from a search result.
	AlbumRatingKey  string
	ArtistRatingKey string
	// Index is the track number within its album.
	Index    int
	Year     int
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

// trackMetadata is the shared JSON shape for both single-album track lists
// and the section-wide LibraryTracks fetch.
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

func (m trackMetadata) toTrack() Track {
	t := Track{
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

// Tracks returns the tracks on an album, in album order.
func (c *Client) Tracks(ctx context.Context, albumKey string) ([]Track, error) {
	var body struct {
		MediaContainer struct {
			Metadata []trackMetadata `json:"Metadata"`
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
		tracks = append(tracks, m.toTrack())
	}
	return tracks, nil
}

// libraryTracksPageSize bounds each /library/sections/{key}/all?type=10 page.
const libraryTracksPageSize = 500

// LibraryTracks fetches every track in a music library section, paginating
// until the server returns a short page. The returned tracks carry full
// parent context (album+artist titles AND ratingKeys + year), so a caller
// can derive artist and album entries by deduplication.
func (c *Client) LibraryTracks(ctx context.Context, sectionKey string) ([]Track, error) {
	var all []Track
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
func (c *Client) StreamURL(t Track) string {
	if t.PartKey == "" {
		return ""
	}
	return fmt.Sprintf("%s%s?X-Plex-Token=%s", c.serverURL, t.PartKey, url.QueryEscape(c.token))
}

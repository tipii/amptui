package plex

import (
	"context"
	"fmt"
	"net/url"
)

// ArtistMetadata is the rich-metadata view of an artist — bio, tags,
// origin, similar acts. Returned by ArtistMetadata(ratingKey).
type ArtistMetadata struct {
	RatingKey string
	Title     string
	Summary   string
	Genres    []string
	Moods     []string
	Countries []string
	Styles    []string
	Similar   []string
}

// AlbumMetadata is the rich-metadata view of an album.
type AlbumMetadata struct {
	RatingKey string
	Title     string
	Artist    string
	Year      int
	Summary   string
	Studio    string
	Genres    []string
	Moods     []string
	Styles    []string
}

// metadataResp is the JSON shape Plex returns for both artist and album
// metadata calls — the tagged subarrays are present per entity but unused
// ones simply decode as nil slices.
type metadataResp struct {
	MediaContainer struct {
		Metadata []struct {
			RatingKey   string  `json:"ratingKey"`
			Title       string  `json:"title"`
			ParentTitle string  `json:"parentTitle"`
			Year        int     `json:"year"`
			Studio      string  `json:"studio"`
			Summary     string  `json:"summary"`
			Genre       []tag   `json:"Genre"`
			Mood        []tag   `json:"Mood"`
			Country     []tag   `json:"Country"`
			Style       []tag   `json:"Style"`
			Similar     []tag   `json:"Similar"`
		} `json:"Metadata"`
	} `json:"MediaContainer"`
}

type tag struct {
	Tag string `json:"tag"`
}

func tagsToStrings(ts []tag) []string {
	if len(ts) == 0 {
		return nil
	}
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		if t.Tag != "" {
			out = append(out, t.Tag)
		}
	}
	return out
}

// ArtistMetadata fetches one artist's full metadata. Genres, Moods,
// Country, Style and Similar require the includeBands / includeRelated
// query parameters; we ask for both unconditionally — Plex ignores
// unknown flags and the response is small.
func (c *Client) ArtistMetadata(ctx context.Context, ratingKey string) (*ArtistMetadata, error) {
	path := fmt.Sprintf("/library/metadata/%s?includeBands=1&includeRelated=1",
		url.PathEscape(ratingKey))
	var body metadataResp
	if err := c.getJSON(ctx, path, &body); err != nil {
		return nil, err
	}
	if len(body.MediaContainer.Metadata) == 0 {
		return nil, fmt.Errorf("artist %s: no metadata returned", ratingKey)
	}
	m := body.MediaContainer.Metadata[0]
	return &ArtistMetadata{
		RatingKey: m.RatingKey,
		Title:     m.Title,
		Summary:   m.Summary,
		Genres:    tagsToStrings(m.Genre),
		Moods:     tagsToStrings(m.Mood),
		Countries: tagsToStrings(m.Country),
		Styles:    tagsToStrings(m.Style),
		Similar:   tagsToStrings(m.Similar),
	}, nil
}

// AlbumMetadata fetches one album's full metadata.
func (c *Client) AlbumMetadata(ctx context.Context, ratingKey string) (*AlbumMetadata, error) {
	path := fmt.Sprintf("/library/metadata/%s", url.PathEscape(ratingKey))
	var body metadataResp
	if err := c.getJSON(ctx, path, &body); err != nil {
		return nil, err
	}
	if len(body.MediaContainer.Metadata) == 0 {
		return nil, fmt.Errorf("album %s: no metadata returned", ratingKey)
	}
	m := body.MediaContainer.Metadata[0]
	return &AlbumMetadata{
		RatingKey: m.RatingKey,
		Title:     m.Title,
		Artist:    m.ParentTitle,
		Year:      m.Year,
		Summary:   m.Summary,
		Studio:    m.Studio,
		Genres:    tagsToStrings(m.Genre),
		Moods:     tagsToStrings(m.Mood),
		Styles:    tagsToStrings(m.Style),
	}, nil
}

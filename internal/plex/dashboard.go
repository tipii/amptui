package plex

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// albumMetadata is an album as the section listings return it.
type albumMetadata struct {
	RatingKey    string `json:"ratingKey"`
	Title        string `json:"title"`
	ParentTitle  string `json:"parentTitle"`
	Year         int    `json:"year"`
	AddedAt      int64  `json:"addedAt"`
	ViewCount    int    `json:"viewCount"`
	LastViewedAt int64  `json:"lastViewedAt"`
	Type         string `json:"type"`
}

func (m albumMetadata) toSummary() media.AlbumSummary {
	return media.AlbumSummary{
		RatingKey:  m.RatingKey,
		Title:      m.Title,
		Artist:     m.ParentTitle,
		Year:       m.Year,
		AddedAt:    unixOrZero(m.AddedAt),
		Plays:      m.ViewCount,
		LastPlayed: unixOrZero(m.LastViewedAt),
	}
}

// albums lists a section's albums with extra query parameters (sort,
// filters), up to limit, and the section's total for that query. Plex
// can list an album twice (seen with a deluxe edition); those are
// dropped.
func (c *Client) albums(ctx context.Context, sectionKey string, query string, limit int) ([]media.AlbumSummary, int, error) {
	var body struct {
		MediaContainer struct {
			TotalSize int             `json:"totalSize"`
			Metadata  []albumMetadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	p := fmt.Sprintf("/library/sections/%s/all?type=9&%s&X-Plex-Container-Start=0&X-Plex-Container-Size=%d",
		url.PathEscape(sectionKey), query, limit)
	if err := c.getJSON(ctx, p, &body); err != nil {
		return nil, 0, err
	}
	out := make([]media.AlbumSummary, 0, len(body.MediaContainer.Metadata))
	seen := map[string]bool{}
	for _, m := range body.MediaContainer.Metadata {
		if m.Type != "album" || seen[m.RatingKey] {
			continue
		}
		seen[m.RatingKey] = true
		out = append(out, m.toSummary())
	}
	return out, body.MediaContainer.TotalSize, nil
}

// RecentlyAddedAlbums fetches the most-recently-added albums in a section.
func (c *Client) RecentlyAddedAlbums(ctx context.Context, sectionKey string, limit int) ([]media.AlbumSummary, error) {
	out, _, err := c.albums(ctx, sectionKey, "sort=addedAt:desc", limit)
	return out, err
}

// PlayedAlbums lists the albums the user has played: Plex keeps a play
// count and last-played time on the album itself.
func (c *Client) PlayedAlbums(ctx context.Context, sectionKey string, order media.AlbumOrder, limit int) ([]media.AlbumSummary, error) {
	query := "sort=lastViewedAt:desc&lastViewedAt%3E=1"
	if order == media.ByPlays {
		query = "sort=viewCount:desc&viewCount%3E=1"
	}
	out, _, err := c.albums(ctx, sectionKey, query, limit)
	return out, err
}

// UnplayedAlbums picks albums the user has never played, at random.
func (c *Client) UnplayedAlbums(ctx context.Context, sectionKey string, limit int) ([]media.AlbumSummary, int, error) {
	return c.albums(ctx, sectionKey, "unwatched=1&sort=random", limit)
}

// PlayHistory pages through the server's play history since a time.
// The server owner sees every account's plays there, so their own are
// picked out: the owner is always account 1 on their server, and only
// an owner or admin token may list the accounts. Anyone else's history
// is already just their own.
func (c *Client) PlayHistory(ctx context.Context, sectionKey string, since time.Time) ([]media.Play, error) {
	account := ""
	var accounts struct{}
	if c.getJSON(ctx, "/accounts", &accounts) == nil {
		account = "&accountID=1"
	}
	type historyItem struct {
		ViewedAt         int64  `json:"viewedAt"`
		Type             string `json:"type"`
		ParentKey        string `json:"parentKey"` // the album: /library/metadata/<ratingKey>
		ParentTitle      string `json:"parentTitle"`
		GrandparentTitle string `json:"grandparentTitle"`
	}
	items, err := media.FetchAll(ctx, func(ctx context.Context, start int) ([]historyItem, int, error) {
		var body struct {
			MediaContainer struct {
				TotalSize *int          `json:"totalSize"`
				Metadata  []historyItem `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		p := fmt.Sprintf(
			"/status/sessions/history/all?librarySectionID=%s&sort=viewedAt:desc&viewedAt%%3E=%d%s&X-Plex-Container-Start=%d&X-Plex-Container-Size=%d",
			url.QueryEscape(sectionKey), since.Unix(), account, start, media.PageSize,
		)
		if err := c.getJSONWith(ctx, c.pageHTTP, p, &body); err != nil {
			return nil, 0, err
		}
		total := -1
		if body.MediaContainer.TotalSize != nil {
			total = *body.MediaContainer.TotalSize
		}
		return body.MediaContainer.Metadata, total, nil
	}, nil)
	if err != nil {
		return nil, err
	}
	out := make([]media.Play, 0, len(items))
	for _, it := range items {
		if it.Type != "track" || it.ViewedAt <= 0 {
			continue
		}
		key := ""
		if it.ParentKey != "" {
			key = path.Base(it.ParentKey)
		}
		out = append(out, media.Play{
			At:       time.Unix(it.ViewedAt, 0),
			AlbumKey: key,
			Album:    it.ParentTitle,
			Artist:   it.GrandparentTitle,
		})
	}
	return out, nil
}

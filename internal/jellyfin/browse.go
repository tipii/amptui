package jellyfin

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// itemDTO is the shared Jellyfin BaseItemDto shape across the calls we
// make — tracks, albums, artists, playlists, views. Fields absent for a
// given item type stay zero.
type itemDTO struct {
	Id             string    `json:"Id"`
	Name           string    `json:"Name"`
	Type           string    `json:"Type"`
	CollectionType string    `json:"CollectionType"`
	Album          string    `json:"Album"`
	AlbumId        string    `json:"AlbumId"`
	AlbumArtist    string    `json:"AlbumArtist"`
	AlbumArtists   []nameID  `json:"AlbumArtists"`
	ArtistItems    []nameID  `json:"ArtistItems"`
	IndexNumber    int       `json:"IndexNumber"`
	ProductionYear int       `json:"ProductionYear"`
	RunTimeTicks   int64     `json:"RunTimeTicks"`
	Overview       string    `json:"Overview"`
	Genres         []string  `json:"Genres"`
	Studios        []nameID  `json:"Studios"`
	ChildCount     int       `json:"ChildCount"`
	DateCreated    time.Time `json:"DateCreated"`
}

// albumArtist resolves the parent artist's name and id, preferring the
// album artist (stable grouping, like Plex's grandparent) over per-track
// ArtistItems which may include featured guests.
func (it itemDTO) albumArtist() (name, id string) {
	if len(it.AlbumArtists) > 0 {
		return it.AlbumArtists[0].Name, it.AlbumArtists[0].Id
	}
	if len(it.ArtistItems) > 0 {
		return it.ArtistItems[0].Name, it.ArtistItems[0].Id
	}
	return it.AlbumArtist, ""
}

// ticksToDuration converts Jellyfin's RunTimeTicks (100-nanosecond units)
// to a time.Duration.
func ticksToDuration(ticks int64) time.Duration {
	return time.Duration(ticks) * 100 * time.Nanosecond
}

func (it itemDTO) toTrack() media.Track {
	artist, artistKey := it.albumArtist()
	return media.Track{
		RatingKey:       it.Id,
		Title:           it.Name,
		Album:           it.Album,
		Artist:          artist,
		AlbumRatingKey:  it.AlbumId,
		ArtistRatingKey: artistKey,
		Index:           it.IndexNumber,
		Year:            it.ProductionYear,
		Duration:        ticksToDuration(it.RunTimeTicks),
	}
}

// libraryTracksPageSize bounds each /Items page during a full sync.
const libraryTracksPageSize = 500

// LibraryTracks fetches every audio track in a music library, paginating
// on StartIndex until TotalRecordCount is reached. Each track carries its
// album and album-artist (name + id), so the cache derives artists and
// albums by deduplication exactly as it does for Plex.
func (c *Client) LibraryTracks(ctx context.Context, libraryKey string) ([]media.Track, error) {
	_, userID := c.credsAfterAuth(ctx)
	var all []media.Track
	for start := 0; ; start += libraryTracksPageSize {
		q := url.Values{}
		q.Set("userId", userID)
		q.Set("ParentId", libraryKey)
		q.Set("IncludeItemTypes", "Audio")
		q.Set("Recursive", "true")
		q.Set("Fields", "ProductionYear")
		q.Set("StartIndex", fmt.Sprintf("%d", start))
		q.Set("Limit", fmt.Sprintf("%d", libraryTracksPageSize))
		var body itemsResponse
		if err := c.get(ctx, "/Items", q, &body); err != nil {
			return nil, err
		}
		for _, it := range body.Items {
			all = append(all, it.toTrack())
		}
		if len(body.Items) < libraryTracksPageSize || len(all) >= body.TotalRecordCount {
			return all, nil
		}
	}
}

// StreamURL builds a direct-play URL (static=true → the original file, no
// transcode) for the track, authenticated by the access token. mpv plays
// every container Jellyfin serves, so direct play is preferred over the
// transcoding /universal endpoint.
func (c *Client) StreamURL(t media.Track) string {
	if t.RatingKey == "" {
		return ""
	}
	token, _ := c.creds()
	return fmt.Sprintf("%s/Audio/%s/stream?static=true&api_key=%s",
		c.serverURL, url.PathEscape(t.RatingKey), url.QueryEscape(token))
}

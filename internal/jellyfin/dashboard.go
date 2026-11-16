package jellyfin

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// RecentlyAddedAlbums returns the most-recently-added albums in a library.
// /Items/Latest returns a bare array (not the paginated wrapper).
func (c *Client) RecentlyAddedAlbums(ctx context.Context, libraryKey string, limit int) ([]media.AlbumSummary, error) {
	userID, err := c.userIDAfterAuth(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("IncludeItemTypes", "MusicAlbum")
	q.Set("ParentId", libraryKey)
	q.Set("Fields", "DateCreated")
	q.Set("Limit", fmt.Sprintf("%d", limit))
	var items []itemDTO
	if err := c.get(ctx, "/Users/"+userID+"/Items/Latest", q, &items); err != nil {
		return nil, err
	}
	out := make([]media.AlbumSummary, 0, len(items))
	for _, it := range items {
		out = append(out, it.albumSummary())
	}
	return out, nil
}

func (it itemDTO) albumSummary() media.AlbumSummary {
	artist, _ := it.albumArtist()
	return media.AlbumSummary{
		RatingKey: it.Id,
		Title:     it.Name,
		Artist:    artist,
		Year:      it.ProductionYear,
		AddedAt:   it.DateCreated,
	}
}

// primaryImage is the key ArtworkURL takes for the item's cover, or ""
// when it has none (fetching it would just 404).
func (it itemDTO) primaryImage() string {
	if it.ImageTags["Primary"] == "" {
		return ""
	}
	return it.Id
}

// playedCacheTTL is how long one fetch of the played tracks serves the
// feeds built from it.
const playedCacheTTL = 30 * time.Second

type playedCache struct {
	library string
	at      time.Time
	tracks  []itemDTO
}

// playedTracks returns every track in the library the user has played,
// with its play count and last play. Jellyfin keeps those per track
// only (an album's own play data stays empty), so album stats are
// built from these.
func (c *Client) playedTracks(ctx context.Context, libraryKey string) ([]itemDTO, error) {
	c.playedMu.Lock()
	defer c.playedMu.Unlock()
	if p := c.played; p.library == libraryKey && time.Since(p.at) < playedCacheTTL {
		return p.tracks, nil
	}
	userID, err := c.userIDAfterAuth(ctx)
	if err != nil {
		return nil, err
	}
	tracks, err := media.FetchAll(ctx, func(ctx context.Context, start int) ([]itemDTO, int, error) {
		q := url.Values{}
		q.Set("userId", userID)
		q.Set("ParentId", libraryKey)
		q.Set("IncludeItemTypes", "Audio")
		q.Set("Recursive", "true")
		q.Set("Filters", "IsPlayed")
		q.Set("Fields", "ProductionYear")
		q.Set("EnableUserData", "true")
		q.Set("SortBy", "SortName,Id") // stable, for offset paging
		q.Set("StartIndex", fmt.Sprintf("%d", start))
		q.Set("Limit", fmt.Sprintf("%d", media.PageSize))
		var body itemsResponse
		if err := c.getWith(ctx, c.pageHTTP, "/Items", q, &body); err != nil {
			return nil, 0, err
		}
		return body.Items, body.TotalRecordCount, nil
	}, nil)
	if err != nil {
		return nil, err
	}
	c.played = playedCache{library: libraryKey, at: time.Now(), tracks: tracks}
	return tracks, nil
}

// albumsOfTracks folds played tracks into their albums: an album's
// plays are its tracks' summed, its last play their latest.
func albumsOfTracks(tracks []itemDTO) []media.AlbumSummary {
	var out []media.AlbumSummary
	at := map[string]int{} // AlbumId → index in out
	for _, t := range tracks {
		if t.AlbumId == "" {
			continue
		}
		i, ok := at[t.AlbumId]
		if !ok {
			artist, _ := t.albumArtist()
			i = len(out)
			at[t.AlbumId] = i
			out = append(out, media.AlbumSummary{RatingKey: t.AlbumId, Title: t.Album, Artist: artist, Year: t.ProductionYear})
		}
		a := &out[i]
		a.Plays += max(t.UserData.PlayCount, 1) // marked played counts once
		if t.UserData.LastPlayedDate.After(a.LastPlayed) {
			a.LastPlayed = t.UserData.LastPlayedDate
		}
	}
	return out
}

// PlayedAlbums lists the albums the user has played, from their tracks'
// play data.
func (c *Client) PlayedAlbums(ctx context.Context, libraryKey string, order media.AlbumOrder, limit int) ([]media.AlbumSummary, error) {
	tracks, err := c.playedTracks(ctx, libraryKey)
	if err != nil {
		return nil, err
	}
	albums := albumsOfTracks(tracks)
	slices.SortStableFunc(albums, func(a, b media.AlbumSummary) int {
		if order == media.ByPlays {
			if n := cmp.Compare(b.Plays, a.Plays); n != 0 {
				return n
			}
		}
		return b.LastPlayed.Compare(a.LastPlayed)
	})
	return albums[:min(limit, len(albums))], nil
}

// UnplayedAlbums picks albums none of whose tracks the user has played.
// Jellyfin's own IsUnplayed filter can't tell, as albums carry no play
// data: a random sample big enough to hold limit albums after the
// played ones are taken out is filtered here instead.
func (c *Client) UnplayedAlbums(ctx context.Context, libraryKey string, limit int) ([]media.AlbumSummary, int, error) {
	tracks, err := c.playedTracks(ctx, libraryKey)
	if err != nil {
		return nil, 0, err
	}
	played := map[string]bool{}
	for _, t := range tracks {
		if t.AlbumId != "" {
			played[t.AlbumId] = true
		}
	}
	userID, err := c.userIDAfterAuth(ctx)
	if err != nil {
		return nil, 0, err
	}
	q := url.Values{}
	q.Set("userId", userID)
	q.Set("ParentId", libraryKey)
	q.Set("IncludeItemTypes", "MusicAlbum")
	q.Set("Recursive", "true")
	q.Set("SortBy", "Random")
	q.Set("Limit", fmt.Sprintf("%d", min(limit+len(played), 1000)))
	var body itemsResponse
	if err := c.get(ctx, "/Items", q, &body); err != nil {
		return nil, 0, err
	}
	out := make([]media.AlbumSummary, 0, limit)
	for _, it := range body.Items {
		if len(out) == limit {
			break
		}
		if !played[it.Id] {
			out = append(out, it.albumSummary())
		}
	}
	return out, max(body.TotalRecordCount-len(played), 0), nil
}

// PlayHistory: Jellyfin keeps only each track's play count and last
// play, not a play-by-play history.
func (c *Client) PlayHistory(context.Context, string, time.Time) ([]media.Play, error) {
	return nil, media.ErrNoHistory
}

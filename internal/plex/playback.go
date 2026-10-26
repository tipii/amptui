package plex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tipii/amptui/internal/media"
)

// ReportPlayback sends a timeline update, which is what puts amptui in
// the server's Now Playing (with progress) and ends the session there on
// "stopped". Plays are counted separately by MarkPlayed.
func (c *Client) ReportPlayback(ctx context.Context, r media.PlaybackReport) error {
	if r.Track.RatingKey == "" {
		return nil
	}
	q := url.Values{}
	q.Set("ratingKey", r.Track.RatingKey)
	q.Set("key", "/library/metadata/"+r.Track.RatingKey)
	q.Set("state", string(r.State))
	q.Set("time", fmt.Sprint(r.Position.Milliseconds()))
	q.Set("duration", fmt.Sprint(r.Duration.Milliseconds()))
	return c.send(ctx, "/:/timeline?"+q.Encode())
}

// MarkPlayed scrobbles the track: bumps its play count and last-played
// time, which is what the dashboard's "Recent plays" sorts on.
func (c *Client) MarkPlayed(ctx context.Context, t media.Track) error {
	if t.RatingKey == "" {
		return nil
	}
	q := url.Values{}
	q.Set("key", t.RatingKey)
	q.Set("identifier", "com.plexapp.plugins.library")
	return c.send(ctx, "/:/scrobble?"+q.Encode())
}

// send issues a GET for its side effect, discarding the body.
func (c *Client) send(ctx context.Context, pathAndQuery string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+pathAndQuery, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return media.RedactErr(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		path, _, _ := strings.Cut(pathAndQuery, "?")
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return nil
}

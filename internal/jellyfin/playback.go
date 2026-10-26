package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// ReportPlayback maps a report onto Jellyfin's session endpoints:
// /Sessions/Playing when a play starts, /Sessions/Playing/Progress for
// pause, resume and periodic updates, /Sessions/Playing/Stopped at the
// end. Jellyfin marks the item played itself when a stop report's
// position is far enough in, so MarkPlayed has nothing to add.
func (c *Client) ReportPlayback(ctx context.Context, r media.PlaybackReport) error {
	if r.Track.RatingKey == "" {
		return nil
	}
	body := map[string]any{
		"ItemId":        r.Track.RatingKey,
		"PositionTicks": durationToTicks(r.Position),
		"IsPaused":      r.State == media.PlaybackPaused,
		"CanSeek":       true,
		"PlayMethod":    "DirectPlay",
	}
	path := "/Sessions/Playing/Progress"
	switch {
	case r.State == media.PlaybackStopped:
		path = "/Sessions/Playing/Stopped"
	case r.Started:
		path = "/Sessions/Playing"
	case r.State == media.PlaybackPaused:
		body["EventName"] = "Pause"
	default:
		body["EventName"] = "TimeUpdate"
	}
	return c.post(ctx, path, body)
}

// MarkPlayed is a no-op: the stop report covers it (see ReportPlayback).
func (c *Client) MarkPlayed(context.Context, media.Track) error { return nil }

// durationToTicks converts to Jellyfin's 100ns ticks.
func durationToTicks(d time.Duration) int64 { return int64(d / 100) }

// post sends a JSON body for its side effect, retrying once after a 401
// like get does.
func (c *Client) post(ctx context.Context, path string, body any) error {
	err := c.postOnce(ctx, path, body)
	var unauth errUnauthorized
	if errors.As(err, &unauth) {
		c.dropToken(unauth.token)
		err = c.postOnce(ctx, path, body)
	}
	return err
}

func (c *Client) postOnce(ctx context.Context, path string, body any) error {
	if err := c.ensureAuth(ctx); err != nil {
		return err
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serverURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	token, _ := c.creds()
	req.Header.Set("Content-Type", "application/json")
	// The full MediaBrowser header (not just the token) is what ties
	// the report to this device's session.
	req.Header.Set("Authorization", c.authValue(token))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return errUnauthorized{path: path, token: token}
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("POST %s: %s", path, resp.Status)
	}
	return nil
}

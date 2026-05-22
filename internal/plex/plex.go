// Package plex is a small Plex Media Server client scoped to music libraries.
//
// Every endpoint goes through a raw-HTTP helper that decodes into minimal
// structs picking only the fields we use — Plex's JSON has enough quirks
// (e.g. integer 0/1 in fields that should be booleans) that ignoring unknown
// fields beats fighting a generated SDK's strict models.
package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// clientIdentifier is sent as X-Plex-Client-Identifier. Plex uses it to track
// this app as a distinct device; keep it stable across runs.
const clientIdentifier = "amptui"

// Client is a thin HTTP wrapper around a Plex Media Server.
type Client struct {
	http      *http.Client
	serverURL string
	token     string
}

// New builds a Client pointed at serverURL and authenticated with token.
func New(serverURL, token string) *Client {
	return &Client{
		http:      &http.Client{Timeout: 15 * time.Second},
		serverURL: strings.TrimRight(serverURL, "/"),
		token:     token,
	}
}

// getJSON performs a GET against the server with auth + JSON headers and
// decodes the body into v.
func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("X-Plex-Client-Identifier", clientIdentifier)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// MusicLibraries returns the server's music library sections.
func (c *Client) MusicLibraries(ctx context.Context) ([]media.MusicLibrary, error) {
	var body struct {
		MediaContainer struct {
			Directory []struct {
				Key              string `json:"key"`
				Type             string `json:"type"`
				Title            string `json:"title"`
				UUID             string `json:"uuid"`
				ContentChangedAt int64  `json:"contentChangedAt"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := c.getJSON(ctx, "/library/sections/all", &body); err != nil {
		return nil, err
	}

	var libs []media.MusicLibrary
	for _, d := range body.MediaContainer.Directory {
		if d.Type != "artist" {
			continue
		}
		libs = append(libs, media.MusicLibrary{
			Key:              d.Key,
			Title:            d.Title,
			UUID:             d.UUID,
			ContentChangedAt: d.ContentChangedAt,
		})
	}
	return libs, nil
}

// Package plex wraps the plexgo SDK with helpers scoped to music libraries.
//
// Some Plex endpoints return JSON that the Speakeasy-generated plexgo models
// reject (e.g. integer 0/1 where the SDK expects a bool). For those we fall
// back to a direct HTTP call with a struct that only picks the fields we need,
// so unmodeled quirky fields are simply ignored.
package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LukeHagar/plexgo"
)

// clientIdentifier is sent as X-Plex-Client-Identifier. Plex uses it to track
// this app as a distinct device; keep it stable across runs.
const clientIdentifier = "plexamp-tui"

// Client is a thin wrapper around the plexgo SDK plus a raw HTTP escape hatch.
type Client struct {
	api       *plexgo.PlexAPI
	http      *http.Client
	serverURL string
	token     string
}

// New builds a Client pointed at serverURL and authenticated with token.
func New(serverURL, token string) *Client {
	api := plexgo.New(
		plexgo.WithServerURL(serverURL),
		plexgo.WithSecurity(token),
		plexgo.WithClientIdentifier(clientIdentifier),
		plexgo.WithProduct("plexamp-tui"),
	)
	return &Client{
		api:       api,
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

// MusicLibrary is a Plex library section of type "artist" (a music library).
type MusicLibrary struct {
	// Key is the section ID used in subsequent library calls.
	Key   string
	Title string
	UUID  string
}

// MusicLibraries returns the server's music library sections.
func (c *Client) MusicLibraries(ctx context.Context) ([]MusicLibrary, error) {
	var body struct {
		MediaContainer struct {
			Directory []struct {
				Key   string `json:"key"`
				Type  string `json:"type"`
				Title string `json:"title"`
				UUID  string `json:"uuid"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := c.getJSON(ctx, "/library/sections/all", &body); err != nil {
		return nil, err
	}

	var libs []MusicLibrary
	for _, d := range body.MediaContainer.Directory {
		if d.Type != "artist" {
			continue
		}
		libs = append(libs, MusicLibrary{Key: d.Key, Title: d.Title, UUID: d.UUID})
	}
	return libs, nil
}

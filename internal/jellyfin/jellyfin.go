// Package jellyfin is a hand-written client for a Jellyfin media server
// that implements media.Backend, so amptui can drive Jellyfin through the
// same cache and UI as Plex.
//
// Auth differs from Plex: Jellyfin exchanges a username/password for an
// access token AND a userId (most item calls are user-scoped). The
// exchange happens lazily on the first call via ensureAuth and is then
// memoized, so the rest of the methods — and the no-context StreamURL /
// ArtworkURL helpers — can assume a token is present (the startup
// MusicLibraries call always triggers it for a valid config).
package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// clientName / deviceID identify amptui to Jellyfin. A stable deviceID
// keeps the server from accumulating a new session per launch.
const (
	clientName = "amptui"
	deviceID   = "amptui"
	version    = "0.1"
)

// Compile-time assertion that the client satisfies the backend interface.
var _ media.Backend = (*Client)(nil)

// Client is a thin HTTP wrapper around a Jellyfin server. token and userID
// are populated by ensureAuth and guarded by mu (Bubble Tea fires fetches
// from multiple goroutines).
type Client struct {
	http      *http.Client
	serverURL string
	username  string
	password  string

	mu     sync.Mutex
	token  string
	userID string
}

// New returns a Jellyfin client for serverURL authenticating as the given
// user. No network call is made until the first request.
func New(serverURL, username, password string) *Client {
	return &Client{
		http:      &http.Client{Timeout: 30 * time.Second},
		serverURL: strings.TrimRight(serverURL, "/"),
		username:  username,
		password:  password,
	}
}

// authValue builds the MediaBrowser authorization header. token is included
// only once we have one (the auth request itself sends it empty).
func authValue(token string) string {
	v := fmt.Sprintf(`MediaBrowser Client=%q, Device=%q, DeviceId=%q, Version=%q`,
		clientName, clientName, deviceID, version)
	if token != "" {
		v += fmt.Sprintf(`, Token=%q`, token)
	}
	return v
}

// ensureAuth performs the username/password exchange once, caching the
// access token and userId. Safe for concurrent callers — the first wins
// the lock and authenticates; the rest return immediately afterwards.
func (c *Client) ensureAuth(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"Username": c.username, "Pw": c.password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.serverURL+"/Users/AuthenticateByName", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authValue(""))
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin auth: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("jellyfin auth: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var ar struct {
		AccessToken string `json:"AccessToken"`
		User        struct {
			Id string `json:"Id"`
		} `json:"User"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return fmt.Errorf("jellyfin auth: decoding response: %w", err)
	}
	if ar.AccessToken == "" || ar.User.Id == "" {
		return fmt.Errorf("jellyfin auth: server returned no token/userId")
	}
	c.token, c.userID = ar.AccessToken, ar.User.Id
	return nil
}

// creds returns the cached token and userId. Callers must have triggered
// ensureAuth (directly or via an earlier request) first.
func (c *Client) creds() (token, userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token, c.userID
}

// get authenticates if needed, performs a GET against path with the given
// query, and decodes the JSON response into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if err := c.ensureAuth(ctx); err != nil {
		return err
	}
	u := c.serverURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	token, _ := c.creds()
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// nameID is Jellyfin's {Name, Id} reference used for artists, studios, etc.
type nameID struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
}

// itemsResponse wraps the paginated /Items shape. Endpoints that return a
// bare array (e.g. /Items/Latest) are decoded into []itemDTO directly.
type itemsResponse struct {
	Items            []itemDTO `json:"Items"`
	TotalRecordCount int       `json:"TotalRecordCount"`
}

// ServerName returns the server's display name from the public system-info
// endpoint. It needs no auth, so it doubles as an early connectivity check.
func (c *Client) ServerName(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/System/Info/Public", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET /System/Info/Public: %s", resp.Status)
	}
	var info struct {
		ServerName string `json:"ServerName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	return info.ServerName, nil
}

// MusicLibraries returns the server's music libraries (Views whose
// CollectionType is "music").
func (c *Client) MusicLibraries(ctx context.Context) ([]media.MusicLibrary, error) {
	_, userID := c.credsAfterAuth(ctx)
	var body itemsResponse
	if err := c.get(ctx, "/Users/"+userID+"/Views", nil, &body); err != nil {
		return nil, err
	}
	var libs []media.MusicLibrary
	for _, it := range body.Items {
		if it.CollectionType != "music" {
			continue
		}
		libs = append(libs, media.MusicLibrary{
			Key:   it.Id,
			Title: it.Name,
			UUID:  it.Id,
			// Jellyfin has no monotonic content-version counter; leave it
			// zero. The cache then stays fresh until a manual refresh (R).
			ContentChangedAt: 0,
		})
	}
	return libs, nil
}

// credsAfterAuth forces auth then returns the token/userId, for callers
// that need the userId to build a path before issuing the request.
func (c *Client) credsAfterAuth(ctx context.Context) (token, userID string) {
	_ = c.ensureAuth(ctx)
	return c.creds()
}

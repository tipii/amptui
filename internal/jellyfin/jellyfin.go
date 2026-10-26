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
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tipii/amptui/internal/media"
)

// clientName identifies amptui to Jellyfin.
const (
	clientName = "amptui"
	version    = "0.2"
)

// deviceIDFor derives the DeviceId amptui reports: stable for one
// machine + OS user + server, so relaunches reuse a single session entry
// on the server, but different per machine. A constant id made every
// install look like one device, and Jellyfin signs out a device's other
// sessions when it logs in again, so two machines on one account kept
// logging each other out.
func deviceIDFor(serverURL string) string {
	host, _ := os.Hostname()
	user := os.Getenv("USER")
	sum := sha1.Sum([]byte(host + "|" + user + "|" + serverURL))
	return "amptui-" + hex.EncodeToString(sum[:8])
}

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
	deviceID  string

	// authMu serializes logins; mu guards token / userID only briefly,
	// so the URL builders (called on the UI goroutine) never wait on a
	// login's network round-trip.
	authMu sync.Mutex
	mu     sync.Mutex
	token  string
	userID string
}

// New returns a Jellyfin client for serverURL authenticating as the given
// user. No network call is made until the first request.
func New(serverURL, username, password string) *Client {
	serverURL = strings.TrimRight(serverURL, "/")
	return &Client{
		http:      &http.Client{Timeout: 30 * time.Second},
		serverURL: serverURL,
		username:  username,
		password:  password,
		deviceID:  deviceIDFor(serverURL),
	}
}

// authValue builds the MediaBrowser authorization header. token is included
// only once we have one (the auth request itself sends it empty).
func (c *Client) authValue(token string) string {
	v := fmt.Sprintf(`MediaBrowser Client=%q, Device=%q, DeviceId=%q, Version=%q`,
		clientName, clientName, c.deviceID, version)
	if token != "" {
		v += fmt.Sprintf(`, Token=%q`, token)
	}
	return v
}

// ensureAuth performs the username/password exchange once, caching the
// access token and userId. Safe for concurrent callers — the first wins
// authMu and authenticates; the rest find the token once it's done.
func (c *Client) ensureAuth(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if token, _ := c.creds(); token != "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"Username": c.username, "Pw": c.password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.serverURL+"/Users/AuthenticateByName", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authValue(""))
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
	c.mu.Lock()
	c.token, c.userID = ar.AccessToken, ar.User.Id
	c.mu.Unlock()
	return nil
}

// dropToken forgets token after the server rejected it, so the next
// ensureAuth logs in again. A token that has already been replaced (by
// a concurrent re-login) is left alone.
func (c *Client) dropToken(token string) {
	c.mu.Lock()
	if c.token == token {
		c.token = ""
	}
	c.mu.Unlock()
}

// creds returns the cached token and userId. Callers must have triggered
// ensureAuth (directly or via an earlier request) first.
func (c *Client) creds() (token, userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token, c.userID
}

// get authenticates if needed, performs a GET against path with the given
// query, and decodes the JSON response into out. A 401 means the token
// was revoked (signed out from the dashboard, password changed, device
// removed): it logs in again and retries once, rather than failing
// every request until a relaunch.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	err := c.getOnce(ctx, path, q, out)
	var unauth errUnauthorized
	if errors.As(err, &unauth) {
		c.dropToken(unauth.token)
		err = c.getOnce(ctx, path, q, out)
	}
	return err
}

// errUnauthorized is a 401 from the server, carrying the rejected token.
type errUnauthorized struct {
	path  string
	token string
}

func (e errUnauthorized) Error() string { return "GET " + e.path + ": 401 Unauthorized" }

func (c *Client) getOnce(ctx context.Context, path string, q url.Values, out any) error {
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
	if resp.StatusCode == http.StatusUnauthorized {
		return errUnauthorized{path: path, token: token}
	}
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
	userID, err := c.userIDAfterAuth(ctx)
	if err != nil {
		return nil, err
	}
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

// userIDAfterAuth forces auth then returns the userId, for callers that
// need it to build a path before issuing the request. An auth failure
// is returned rather than producing a "/Users//…" path that fails with
// a confusing 404.
func (c *Client) userIDAfterAuth(ctx context.Context) (string, error) {
	if err := c.ensureAuth(ctx); err != nil {
		return "", err
	}
	_, userID := c.creds()
	return userID, nil
}

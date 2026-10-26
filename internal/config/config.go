// Package config loads amptui settings from a TOML file with env overrides.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	// Backend selects which server amptui drives: "plex" (default) or
	// "jellyfin". It picks both the client implementation and which of
	// the credential fields below are required (see IsValid).
	Backend string `toml:"backend,omitempty"`
	// ServerURL is the base URL of the media server, e.g.
	// http://192.168.1.10:32400 (Plex) or http://192.168.1.10:8096 (Jellyfin).
	ServerURL string `toml:"server_url"`
	// PlexToken is the X-Plex-Token used to authenticate against a Plex
	// server (backend = "plex").
	PlexToken string `toml:"plex_token,omitempty"`
	// JellyfinUsername / JellyfinPassword authenticate against a Jellyfin
	// server (backend = "jellyfin"). Jellyfin exchanges them for an access
	// token (and a userId, which most of its API calls require) at startup.
	JellyfinUsername string `toml:"jellyfin_username,omitempty"`
	JellyfinPassword string `toml:"jellyfin_password,omitempty"`
	// DefaultLibrary, if set, makes the UI open straight into that music
	// library instead of the library picker. Matched against a section's
	// key or title (case-insensitive). Optional.
	DefaultLibrary string `toml:"default_library"`
	// DefaultViewArtist / DefaultViewAlbum select the initial render mode
	// for those browser levels: "list" (default) or "grid".
	DefaultViewArtist string `toml:"default_view_artist,omitempty"`
	DefaultViewAlbum  string `toml:"default_view_album,omitempty"`
	// SortArtist / SortAlbum / SortTrack are the browse ordering for each
	// level, as "<field>:<direction>" (e.g. "added:desc"). Written by the
	// sort modal (o) whenever the user picks an ordering, so the choice
	// survives a relaunch the way it does in Plex. Unparseable or
	// inapplicable values fall back to the level's default — see
	// library.ParseSpec for the accepted fields.
	SortArtist string `toml:"sort_artist,omitempty"`
	SortAlbum  string `toml:"sort_album,omitempty"`
	SortTrack  string `toml:"sort_track,omitempty"`
	// Home selects which screen the app opens on: "dashboard" (default,
	// recent plays / added / playlists) or "library" (artist browser).
	Home string `toml:"home,omitempty"`
	// Images toggles inline artwork (artist / album thumbs) in the
	// info header and modal. Off by default; flip on if your terminal
	// supports the Kitty graphics protocol or you're happy with the
	// half-block ANSI fallback.
	Images bool `toml:"images,omitempty"`
	// DownloadFolder is the root directory under which `d` saves
	// tracks/albums (as <root>/<artist>/<album>/<NN Title>.<ext>). Empty
	// disables the feature — pressing `d` flashes a hint to set this.
	DownloadFolder string `toml:"download_folder,omitempty"`
	// DownloadQuality is the encoding `d` asks the server for:
	// "original" (default — the untouched source file) or "mp3-<kbps>"
	// for a server-side transcode. Playback is unaffected; only
	// downloads read this. See media.ParseDownloadQuality for the
	// accepted values — an unrecognized one falls back to the original.
	DownloadQuality string `toml:"download_quality,omitempty"`
	// ReportPlayback tells the server what's playing (its Now Playing
	// view) and records plays (play counts, "Recent plays"). On unless
	// set to false; a pointer so an absent key reads as the default.
	ReportPlayback *bool `toml:"report_playback,omitempty"`

	// overrides records the env vars Load applied (see Save). Unexported,
	// so the TOML encoder never sees it.
	overrides []override
}

// Path returns the config file location: $XDG_CONFIG_HOME/amptui/config.toml,
// falling back to ~/.config/amptui/config.toml. We resolve this manually
// (instead of os.UserConfigDir) because the Go stdlib points at
// ~/Library/Application Support on macOS, and amptui standardizes on the
// XDG-style ~/.config layout across platforms.
func Path() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "amptui", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "amptui", "config.toml"), nil
}

// LoadWarning describes a non-fatal problem encountered during Load — e.g.
// a malformed TOML file. The app surfaces this in the settings screen so
// the user knows why their config isn't taking effect.
type LoadWarning struct {
	Path string
	Err  error
	// BackupPath is set once Save has copied the unparseable file aside
	// before overwriting it, so the settings screen can say where it went.
	BackupPath string
}

func (w LoadWarning) Error() string { return w.Err.Error() }

// LastLoadWarning is set by Load when something non-fatal goes wrong
// (TOML parse error, unreadable file). nil when the file was missing or
// parsed cleanly.
var LastLoadWarning *LoadWarning

// envVars are the AMPTUI_* overrides Load applies on top of the file.
var envVars = []struct {
	name  string
	field func(*Config) *string
}{
	{"AMPTUI_BACKEND", func(c *Config) *string { return &c.Backend }},
	{"AMPTUI_SERVER_URL", func(c *Config) *string { return &c.ServerURL }},
	{"AMPTUI_PLEX_TOKEN", func(c *Config) *string { return &c.PlexToken }},
	{"AMPTUI_JELLYFIN_USERNAME", func(c *Config) *string { return &c.JellyfinUsername }},
	{"AMPTUI_JELLYFIN_PASSWORD", func(c *Config) *string { return &c.JellyfinPassword }},
	{"AMPTUI_DEFAULT_LIBRARY", func(c *Config) *string { return &c.DefaultLibrary }},
	{"AMPTUI_DOWNLOAD_FOLDER", func(c *Config) *string { return &c.DownloadFolder }},
	{"AMPTUI_DOWNLOAD_QUALITY", func(c *Config) *string { return &c.DownloadQuality }},
}

// override remembers one env var Load applied, and what the file said
// underneath it, so Save can write the file's value back instead of the
// env's. Without this, a token kept out of the file on purpose (env from
// a secret manager) would land in it the first time the sort modal or
// settings screen saved.
type override struct {
	field   func(*Config) *string
	fileVal string
	envVal  string
}

// Load reads the config file and applies AMPTUI_* env overrides. It is
// deliberately lenient: a missing file, a malformed TOML, or absent
// required fields ARE NOT errors — the caller gets back whatever could be
// parsed plus LastLoadWarning set if something was salvageably wrong. The
// TUI surfaces that warning in the settings screen so the user knows why
// their config isn't taking effect.
func Load() (Config, error) {
	var c Config
	LastLoadWarning = nil

	path, err := Path()
	if err != nil {
		return c, nil // best-effort
	}
	if st, statErr := os.Stat(path); statErr == nil {
		// Older versions wrote this 0644; it holds credentials, so
		// tighten it now rather than waiting for the next Save.
		if st.Mode().Perm()&0o077 != 0 {
			_ = os.Chmod(path, 0o600)
		}
		if _, decodeErr := toml.DecodeFile(path, &c); decodeErr != nil {
			LastLoadWarning = &LoadWarning{Path: path, Err: decodeErr}
			fmt.Fprintln(os.Stderr, "warning: invalid config file at", path+":", decodeErr)
		}
	}
	c.Backend = normalizeBackend(c.Backend)
	c.ServerURL = NormalizeServerURL(c.ServerURL)

	for _, e := range envVars {
		if v := os.Getenv(e.name); v != "" {
			switch e.name {
			case "AMPTUI_BACKEND":
				v = normalizeBackend(v)
			case "AMPTUI_SERVER_URL":
				v = NormalizeServerURL(v)
			}
			f := e.field(&c)
			c.overrides = append(c.overrides, override{field: e.field, fileVal: *f, envVal: v})
			*f = v
		}
	}
	return c, nil
}

// ReportsPlayback reports whether playback reporting is on (the default).
func (c Config) ReportsPlayback() bool { return c.ReportPlayback == nil || *c.ReportPlayback }

// IsJellyfin reports whether the configured backend is Jellyfin. Plex is
// the default for any unset or unrecognized value.
func (c Config) IsJellyfin() bool { return c.Backend == "jellyfin" }

// normalizeBackend lower-cases and trims a backend name, so "Jellyfin"
// selects Jellyfin rather than silently falling back to Plex.
func normalizeBackend(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// NormalizeServerURL trims whitespace (a pasted URL often carries a
// trailing newline or space) and trailing slashes.
func NormalizeServerURL(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }

// ValidateServerURL reports why s can't be used as a server URL, or nil.
// Empty is allowed (unset). "192.168.1.10:32400" without a scheme failed
// every request with an opaque "first path segment in URL cannot contain
// colon" before this existed.
func ValidateServerURL(s string) error {
	if s == "" {
		return nil
	}
	if !strings.Contains(s, "://") {
		return fmt.Errorf("must start with http:// or https://")
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("not a valid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("must start with http:// or https://")
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}

// IsValid reports whether the config has the minimum required fields to
// reach its backend: URL + token for Plex, URL + username + password for
// Jellyfin. Used by the app to decide whether to connect at startup or
// open the settings screen for the user to fill in.
func (c Config) IsValid() bool {
	if c.ServerURL == "" || ValidateServerURL(c.ServerURL) != nil {
		return false
	}
	if c.IsJellyfin() {
		return c.JellyfinUsername != "" && c.JellyfinPassword != ""
	}
	return c.PlexToken != ""
}

// Save writes c to the config file, creating the directory if needed.
// Uses an atomic temp-file-then-rename so a crash never leaves a half-
// written config that would fail to parse on next launch.
//
// The file holds credentials, so it's written 0600 in a 0700 directory
// (and an existing file is tightened, since rename keeps the temp
// file's mode). Fields that came from AMPTUI_* env vars are written with
// the file's original value unless the user has since changed them.
func (c Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700) // created 0755 by older versions

	out := c
	for _, o := range c.overrides {
		if f := o.field(&out); *f == o.envVal {
			*f = o.fileVal
		}
	}

	// Load salvages what it can from a malformed file, so writing now
	// would replace the user's hand-edited values with that partial
	// read. Keep the original next to it first.
	if w := LastLoadWarning; w != nil && w.BackupPath == "" {
		backup := path + ".broken"
		if err := copyFile(path, backup); err != nil {
			return fmt.Errorf("backing up unparseable config before overwriting it: %w", err)
		}
		w.BackupPath = backup
	}

	f, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if err := toml.NewEncoder(f).Encode(out); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// copyFile copies src to dst with owner-only permissions.
func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

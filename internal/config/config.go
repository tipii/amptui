// Package config loads amptui settings from a TOML file with env overrides.
package config

import (
	"fmt"
	"os"
	"path/filepath"

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
}

func (w LoadWarning) Error() string { return w.Err.Error() }

// LastLoadWarning is set by Load when something non-fatal goes wrong
// (TOML parse error, unreadable file). nil when the file was missing or
// parsed cleanly.
var LastLoadWarning *LoadWarning

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
	if _, statErr := os.Stat(path); statErr == nil {
		if _, decodeErr := toml.DecodeFile(path, &c); decodeErr != nil {
			LastLoadWarning = &LoadWarning{Path: path, Err: decodeErr}
			fmt.Fprintln(os.Stderr, "warning: invalid config file at", path+":", decodeErr)
		}
	}

	if v := os.Getenv("AMPTUI_BACKEND"); v != "" {
		c.Backend = v
	}
	if v := os.Getenv("AMPTUI_SERVER_URL"); v != "" {
		c.ServerURL = v
	}
	if v := os.Getenv("AMPTUI_PLEX_TOKEN"); v != "" {
		c.PlexToken = v
	}
	if v := os.Getenv("AMPTUI_JELLYFIN_USERNAME"); v != "" {
		c.JellyfinUsername = v
	}
	if v := os.Getenv("AMPTUI_JELLYFIN_PASSWORD"); v != "" {
		c.JellyfinPassword = v
	}
	if v := os.Getenv("AMPTUI_DEFAULT_LIBRARY"); v != "" {
		c.DefaultLibrary = v
	}
	if v := os.Getenv("AMPTUI_DOWNLOAD_FOLDER"); v != "" {
		c.DownloadFolder = v
	}
	return c, nil
}

// IsJellyfin reports whether the configured backend is Jellyfin. Plex is
// the default for any unset or unrecognized value.
func (c Config) IsJellyfin() bool { return c.Backend == "jellyfin" }

// IsValid reports whether the config has the minimum required fields to
// reach its backend: URL + token for Plex, URL + username + password for
// Jellyfin. Used by the app to decide whether to connect at startup or
// open the settings screen for the user to fill in.
func (c Config) IsValid() bool {
	if c.ServerURL == "" {
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
func (c Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

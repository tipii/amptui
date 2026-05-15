// Package config loads amptui settings from a TOML file with env overrides.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	// ServerURL is the base URL of the Plex Media Server, e.g. http://192.168.1.10:32400
	ServerURL string `toml:"server_url"`
	// Token is the X-Plex-Token used to authenticate against the server.
	Token string `toml:"token"`
	// DefaultLibrary, if set, makes the UI open straight into that music
	// library instead of the library picker. Matched against a section's
	// key or title (case-insensitive). Optional.
	DefaultLibrary string `toml:"default_library"`
}

// Path returns the config file location: $XDG_CONFIG_HOME/amptui/config.toml
// (falling back to ~/.config).
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "amptui", "config.toml"), nil
}

// Load reads the config file, then applies AMPTUI_SERVER_URL / AMPTUI_TOKEN
// env overrides. A missing file is not an error as long as the env vars supply
// both values.
func Load() (Config, error) {
	var c Config

	path, err := Path()
	if err != nil {
		return c, err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		if _, err := toml.DecodeFile(path, &c); err != nil {
			return c, fmt.Errorf("parsing %s: %w", path, err)
		}
	} else if !os.IsNotExist(statErr) {
		return c, statErr
	}

	if v := os.Getenv("AMPTUI_SERVER_URL"); v != "" {
		c.ServerURL = v
	}
	if v := os.Getenv("AMPTUI_TOKEN"); v != "" {
		c.Token = v
	}
	if v := os.Getenv("AMPTUI_DEFAULT_LIBRARY"); v != "" {
		c.DefaultLibrary = v
	}

	if c.ServerURL == "" {
		return c, fmt.Errorf("no server_url set (config: %s, or AMPTUI_SERVER_URL)", path)
	}
	if c.Token == "" {
		return c, fmt.Errorf("no token set (config: %s, or AMPTUI_TOKEN)", path)
	}
	return c, nil
}

// Package config loads plexamp-tui settings from a TOML file with env overrides.
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
}

// Path returns the config file location: $XDG_CONFIG_HOME/plexamp-tui/config.toml
// (falling back to ~/.config).
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "plexamp-tui", "config.toml"), nil
}

// Load reads the config file, then applies PLEXAMP_SERVER_URL / PLEXAMP_TOKEN
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

	if v := os.Getenv("PLEXAMP_SERVER_URL"); v != "" {
		c.ServerURL = v
	}
	if v := os.Getenv("PLEXAMP_TOKEN"); v != "" {
		c.Token = v
	}

	if c.ServerURL == "" {
		return c, fmt.Errorf("no server_url set (config: %s, or PLEXAMP_SERVER_URL)", path)
	}
	if c.Token == "" {
		return c, fmt.Errorf("no token set (config: %s, or PLEXAMP_TOKEN)", path)
	}
	return c, nil
}

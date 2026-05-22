// Package backend constructs the media.Backend implementation a Config
// selects (Plex or Jellyfin). It's the one place that depends on both
// concrete clients, so the rest of the app picks a backend by config
// alone.
package backend

import (
	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/jellyfin"
	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/plex"
)

// New builds the backend for cfg. It assumes cfg.IsValid() — callers that
// might not have credentials should guard with that first so they keep a
// true-nil media.Backend rather than a non-nil interface wrapping an
// unusable client.
func New(cfg config.Config) media.Backend {
	if cfg.IsJellyfin() {
		return jellyfin.New(cfg.ServerURL, cfg.JellyfinUsername, cfg.JellyfinPassword)
	}
	return plex.New(cfg.ServerURL, cfg.PlexToken)
}

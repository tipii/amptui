// Command amptui is a terminal Plex music client.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/theopalhol/amptui/internal/config"
	"github.com/theopalhol/amptui/internal/player"
	"github.com/theopalhol/amptui/internal/plex"
	"github.com/theopalhol/amptui/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, _ := config.Load()

	// Only try to connect when we have credentials. A missing/invalid
	// config still launches the TUI so the user can fix it from the
	// settings screen.
	var (
		client     *plex.Client
		libs       []plex.MusicLibrary
		defaultLib *plex.MusicLibrary
	)
	if cfg.IsValid() {
		client = plex.New(cfg.ServerURL, cfg.Token)

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var err error
		libs, err = client.MusicLibraries(ctx)
		cancel()
		if err != nil {
			fmt.Fprintln(os.Stderr,
				"warning: could not connect to", cfg.ServerURL, "-", err)
		}

		if cfg.DefaultLibrary != "" && len(libs) > 0 {
			for i := range libs {
				if libs[i].Key == cfg.DefaultLibrary ||
					strings.EqualFold(libs[i].Title, cfg.DefaultLibrary) {
					defaultLib = &libs[i]
					break
				}
			}
		}
	}

	// Playback is best-effort: if mpv can't start, browsing still works.
	p, err := player.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: playback disabled:", err)
	} else {
		defer p.Close()
	}

	prog := tea.NewProgram(tui.New(cfg, client, p, libs, defaultLib))
	_, err = prog.Run()
	return err
}

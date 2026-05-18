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
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	client := plex.New(cfg.ServerURL, cfg.Token)

	// Fetch the music libraries up front so the UI has something to show
	// immediately; this also surfaces auth/connection errors before we
	// take over the terminal.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	libs, err := client.MusicLibraries(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", cfg.ServerURL, err)
	}
	if len(libs) == 0 {
		return fmt.Errorf("no music libraries found on %s", cfg.ServerURL)
	}

	// If a default library is configured, resolve it so the UI can open
	// straight into it. A non-matching value is a warning, not fatal.
	var defaultLib *plex.MusicLibrary
	if cfg.DefaultLibrary != "" {
		for i := range libs {
			if libs[i].Key == cfg.DefaultLibrary ||
				strings.EqualFold(libs[i].Title, cfg.DefaultLibrary) {
				defaultLib = &libs[i]
				break
			}
		}
		if defaultLib == nil {
			fmt.Fprintf(os.Stderr,
				"warning: default_library %q not found, showing library picker\n",
				cfg.DefaultLibrary)
		}
	}

	// Playback is best-effort: if mpv can't start, browsing still works.
	p, err := player.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: playback disabled:", err)
	} else {
		defer p.Close()
	}

	// Alt-screen is set declaratively in the model's View() in v2.
	prog := tea.NewProgram(tui.New(cfg, client, p, libs, defaultLib))
	_, err = prog.Run()
	return err
}

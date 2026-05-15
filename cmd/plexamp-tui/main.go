// Command plexamp-tui is a terminal Plex music client.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/theopalhol/plexamp-tui/internal/config"
	"github.com/theopalhol/plexamp-tui/internal/player"
	"github.com/theopalhol/plexamp-tui/internal/plex"
	"github.com/theopalhol/plexamp-tui/internal/tui"
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

	// Playback is best-effort: if mpv can't start, browsing still works.
	p, err := player.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: playback disabled:", err)
	} else {
		defer p.Close()
	}

	prog := tea.NewProgram(tui.New(client, p, libs), tea.WithAltScreen())
	_, err = prog.Run()
	return err
}

package tui

import (
	"fmt"

	"github.com/theopalhol/plexamp-tui/internal/plex"
)

// Each list row implements bubbles/list.Item. Title is the primary line,
// Description the dimmed secondary line, FilterValue what "/" search matches.

type libraryItem struct{ lib plex.MusicLibrary }

func (i libraryItem) Title() string       { return i.lib.Title }
func (i libraryItem) Description() string { return "music library" }
func (i libraryItem) FilterValue() string { return i.lib.Title }

type artistItem struct{ artist plex.Artist }

func (i artistItem) Title() string       { return i.artist.Title }
func (i artistItem) Description() string { return "artist" }
func (i artistItem) FilterValue() string { return i.artist.Title }

type albumItem struct{ album plex.Album }

func (i albumItem) Title() string { return i.album.Title }
func (i albumItem) Description() string {
	if i.album.Year > 0 {
		return fmt.Sprintf("%d", i.album.Year)
	}
	return "album"
}
func (i albumItem) FilterValue() string { return i.album.Title }

type trackItem struct{ track plex.Track }

func (i trackItem) Title() string {
	return fmt.Sprintf("%2d. %s", i.track.Index, i.track.Title)
}
func (i trackItem) Description() string {
	d := i.track.Duration.Round(1e9) // whole seconds
	return fmt.Sprintf("%s · %02d:%02d", i.track.Album,
		int(d.Minutes()), int(d.Seconds())%60)
}
func (i trackItem) FilterValue() string { return i.track.Title }

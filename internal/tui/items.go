package tui

import (
	"fmt"

	"github.com/theopalhol/amptui/internal/library"
	"github.com/theopalhol/amptui/internal/plex"
)

// Each list row implements bubbles/list.Item. Title is the primary line,
// Description the dimmed secondary line, FilterValue what "/" search matches.

type libraryItem struct{ lib plex.MusicLibrary }

func (i libraryItem) Title() string       { return i.lib.Title }
func (i libraryItem) Description() string { return "music library" }
func (i libraryItem) FilterValue() string { return i.lib.Title }

type artistItem struct{ artist library.Artist }

func (i artistItem) Title() string { return i.artist.Title }
func (i artistItem) Description() string {
	switch {
	case i.artist.AlbumCount == 0 && i.artist.TrackCount == 0:
		return "artist"
	case i.artist.AlbumCount == 0:
		return fmt.Sprintf("%d tracks", i.artist.TrackCount)
	default:
		return fmt.Sprintf("%d albums · %d tracks", i.artist.AlbumCount, i.artist.TrackCount)
	}
}
func (i artistItem) FilterValue() string { return i.artist.Title }

type albumItem struct{ album library.Album }

func (i albumItem) Title() string { return i.album.Title }
func (i albumItem) Description() string {
	var parts []string
	if i.album.Year > 0 {
		parts = append(parts, fmt.Sprintf("%d", i.album.Year))
	}
	if i.album.TrackCount > 0 {
		parts = append(parts, fmt.Sprintf("%d tracks", i.album.TrackCount))
	}
	if len(parts) == 0 {
		return "album"
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " · "
		}
		out += p
	}
	return out
}
func (i albumItem) FilterValue() string { return i.album.Title }

// albumActionItem is the "Play album" row shown above an album's track list.
type albumActionItem struct{ tracks []plex.Track }

func (i albumActionItem) Title() string { return "▶  Play album" }
func (i albumActionItem) Description() string {
	if len(i.tracks) == 1 {
		return "1 track"
	}
	return fmt.Sprintf("%d tracks", len(i.tracks))
}
func (i albumActionItem) FilterValue() string { return "Play album" }

type trackItem struct {
	track plex.Track
	// tracks is the full album track list (shared backing array); pos is
	// this track's index within it. Together they let "enter" play from
	// this track to the end of the album.
	tracks []plex.Track
	pos    int
}

func (i trackItem) Title() string {
	return fmt.Sprintf("%2d. %s", i.track.Index, i.track.Title)
}
func (i trackItem) Description() string {
	d := i.track.Duration.Round(1e9) // whole seconds
	return fmt.Sprintf("%s · %02d:%02d", i.track.Album,
		int(d.Minutes()), int(d.Seconds())%60)
}
func (i trackItem) FilterValue() string { return i.track.Title }

// queueItem is a row in the queue modal. current marks the playing track.
type queueItem struct {
	track   plex.Track
	current bool
}

func (i queueItem) Title() string {
	marker := "   "
	if i.current {
		marker = "▶  "
	}
	return marker + i.track.Title
}
func (i queueItem) Description() string {
	return "   " + i.track.Artist + " · " + i.track.Album
}
func (i queueItem) FilterValue() string { return i.track.Title }

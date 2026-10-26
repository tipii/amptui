package tui

import (
	"charm.land/bubbles/v2/list"
	"fmt"

	"github.com/tipii/amptui/internal/library"
	"github.com/tipii/amptui/internal/media"
)

// Each list row implements bubbles/list.Item. Title is the primary line,
// Description the dimmed secondary line, FilterValue what "/" search matches.

type libraryItem struct{ lib media.MusicLibrary }

func (i libraryItem) Title() string       { return i.lib.Title }
func (i libraryItem) Description() string { return "music library" }
func (i libraryItem) FilterValue() string { return i.lib.Title }

type artistItem struct{ artist library.Artist }

func (i artistItem) Title() string { return i.artist.Title }
func (i artistItem) Description() string {
	if i.artist.AlbumCount == 0 {
		return "artist"
	}
	if i.artist.AlbumCount == 1 {
		return "1 album"
	}
	return fmt.Sprintf("%d albums", i.artist.AlbumCount)
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
type albumActionItem struct{ tracks []media.Track }

func (i albumActionItem) Title() string { return "▶  Play album" }

// Description lines up under "Play album", past the "▶  " marker.
func (i albumActionItem) Description() string {
	if len(i.tracks) == 1 {
		return "   1 track"
	}
	return fmt.Sprintf("   %d tracks", len(i.tracks))
}
func (i albumActionItem) FilterValue() string { return "Play album" }

type trackItem struct {
	track media.Track
	// tracks is the full album track list (shared backing array); pos is
	// this track's index within it. Together they let "enter" play from
	// this track to the end of the album.
	tracks []media.Track
	pos    int
}

func (i trackItem) Title() string {
	return fmt.Sprintf("%2d. %s", i.track.Index, i.track.Title)
}

// Description is the duration (and the disc, on a multi-disc album),
// indented to line up under the title text past the "NN. " number. It
// used to repeat the album name, which on the album's own track list
// is the page heading.
func (i trackItem) Description() string {
	d := i.track.Duration.Round(1e9) // whole seconds
	out := fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
	if i.track.Disc > 0 && multiDisc(i.tracks) {
		out = fmt.Sprintf("disc %d · %s", i.track.Disc, out)
	}
	return "    " + out
}

// multiDisc reports whether an album's tracks span more than one disc.
func multiDisc(tracks []media.Track) bool {
	for _, t := range tracks {
		if t.Disc > 1 {
			return true
		}
	}
	return false
}
func (i trackItem) FilterValue() string { return i.track.Title }

// queueItem is a row in the queue modal. current marks the playing track.
type queueItem struct {
	track   media.Track
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

// playingKeys is what's playing, for marking it in lists and grids: the
// track, its album and its artist. The Model and the list delegate share
// one (by pointer); Update refreshes it after every message.
type playingKeys struct{ track, album, artist string }

func (p *playingKeys) set(t *media.Track) {
	if t == nil {
		*p = playingKeys{}
		return
	}
	*p = playingKeys{t.RatingKey, t.AlbumRatingKey, t.ArtistRatingKey}
}

// playingTitle is item's title marked with ♪ when it's what's playing,
// and ok reports whether it is. A track's number gives way to the note,
// keeping the column; an album or artist gets the note in front.
func (p *playingKeys) playingTitle(item list.Item) (title string, ok bool) {
	if p == nil {
		return "", false
	}
	switch it := item.(type) {
	case trackItem:
		if p.track != "" && it.track.RatingKey == p.track {
			return fmt.Sprintf("%2s  %s", "♪", it.track.Title), true
		}
	case albumItem:
		if p.album != "" && it.album.RatingKey == p.album {
			return "♪ " + it.album.Title, true
		}
	case artistItem:
		if p.artist != "" && it.artist.RatingKey == p.artist {
			return "♪ " + it.artist.Title, true
		}
	}
	return "", false
}

// retitled is a list item shown with a different title.
type retitled struct {
	list.DefaultItem
	title string
}

func (r retitled) Title() string { return r.title }

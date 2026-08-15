package library

import (
	"sort"
	"strings"
	"time"
)

// Sorting mirrors what a Plex client offers on a music library: each browse
// level has its own set of orderings and its own direction, chosen by the
// user and remembered across launches (the TUI persists a Spec per level to
// config.toml).
//
// Every ordering here is answered from the cache alone — the fields it reads
// (title, year, duration, counts, AddedAt) are all derived at sync time — so
// changing sort never touches the network.

// SortField identifies what a level is ordered by. Not every field applies
// to every level; ArtistSortFields / AlbumSortFields / TrackSortFields list
// the valid ones and the UI only offers those.
type SortField int

const (
	SortTitle SortField = iota
	SortYear
	SortAdded
	SortDuration
	SortTrackCount
	SortAlbumCount
	SortTrackNumber
)

// tokens are the stable config.toml spellings; labels are what the sort
// modal shows. Keeping them separate means the UI wording can change
// without invalidating anyone's saved config.
var (
	sortTokens = map[SortField]string{
		SortTitle:       "title",
		SortYear:        "year",
		SortAdded:       "added",
		SortDuration:    "duration",
		SortTrackCount:  "tracks",
		SortAlbumCount:  "albums",
		SortTrackNumber: "number",
	}
	sortLabels = map[SortField]string{
		SortTitle:       "Title",
		SortYear:        "Year",
		SortAdded:       "Date added",
		SortDuration:    "Duration",
		SortTrackCount:  "Track count",
		SortAlbumCount:  "Album count",
		SortTrackNumber: "Track number",
	}
)

// String is the config token for the field ("added", "year", …).
func (f SortField) String() string {
	if s, ok := sortTokens[f]; ok {
		return s
	}
	return "title"
}

// Label is the human-readable name shown in the sort modal.
func (f SortField) Label() string {
	if s, ok := sortLabels[f]; ok {
		return s
	}
	return "Title"
}

// Direction is ascending or descending.
type Direction int

const (
	Asc Direction = iota
	Desc
)

func (d Direction) String() string {
	if d == Desc {
		return "desc"
	}
	return "asc"
}

// Arrow is the one-glyph indicator shown next to the active sort.
func (d Direction) Arrow() string {
	if d == Desc {
		return "↓"
	}
	return "↑"
}

func (d Direction) Flip() Direction {
	if d == Desc {
		return Asc
	}
	return Desc
}

// Spec is a complete ordering: a field plus a direction.
type Spec struct {
	Field SortField
	Dir   Direction
}

// The per-level defaults reproduce amptui's pre-sorting behaviour exactly,
// so an existing user who never opens the sort modal sees no change.
var (
	DefaultArtistSort = Spec{Field: SortTitle, Dir: Asc}
	DefaultAlbumSort  = Spec{Field: SortYear, Dir: Asc}
	DefaultTrackSort  = Spec{Field: SortTrackNumber, Dir: Asc}
)

// The orderings each level offers, in the order the sort modal lists them.
var (
	ArtistSortFields = []SortField{SortTitle, SortAdded, SortAlbumCount, SortTrackCount}
	AlbumSortFields  = []SortField{SortYear, SortTitle, SortAdded, SortDuration, SortTrackCount}
	TrackSortFields  = []SortField{SortTrackNumber, SortTitle, SortDuration, SortAdded}
)

// String renders a Spec as its config.toml value, "<field>:<direction>"
// (e.g. "added:desc"). ParseSpec is the inverse.
func (s Spec) String() string { return s.Field.String() + ":" + s.Dir.String() }

// DefaultDirection is the direction a field gets when the user first picks
// it. Dates read most-recent-first — picking "Date added" and landing on the
// oldest import in the library would be useless — everything else reads
// naturally ascending.
func DefaultDirection(f SortField) Direction {
	if f == SortAdded {
		return Desc
	}
	return Asc
}

// ParseSpec reads a "<field>:<direction>" config value, falling back to def
// for anything unparseable or not in allowed. Config is hand-editable, so a
// typo degrades to the level's default rather than erroring the app out.
// The direction half is optional: "added" means "added" at its default
// direction.
func ParseSpec(v string, def Spec, allowed []SortField) Spec {
	fieldTok, dirTok, hasDir := strings.Cut(strings.TrimSpace(strings.ToLower(v)), ":")
	if fieldTok == "" {
		return def
	}
	field, ok := fieldFromToken(fieldTok)
	if !ok || !fieldAllowed(field, allowed) {
		return def
	}
	s := Spec{Field: field, Dir: DefaultDirection(field)}
	if hasDir {
		switch strings.TrimSpace(dirTok) {
		case "desc":
			s.Dir = Desc
		case "asc":
			s.Dir = Asc
		}
	}
	return s
}

func fieldFromToken(tok string) (SortField, bool) {
	for f, t := range sortTokens {
		if t == tok {
			return f, true
		}
	}
	return SortTitle, false
}

func fieldAllowed(f SortField, allowed []SortField) bool {
	for _, a := range allowed {
		if a == f {
			return true
		}
	}
	return false
}

// --- comparators ---

// cmpTitle orders titles case-insensitively. It doubles as the universal
// tiebreak so equal primary keys (three albums from 1971, a hundred artists
// with no AddedAt) still come out in a stable, meaningful order instead of
// map-iteration order.
func cmpTitle(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpDuration(a, b time.Duration) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpTime(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	}
	return 0
}

// less folds a primary comparison, the direction, and the title tiebreak
// into the boolean sort.SliceStable wants. The tiebreak is deliberately
// NOT inverted by Dir: flipping to descending should reverse the thing you
// asked for, not scramble the alphabetical fallback underneath it.
func less(primary int, dir Direction, titleA, titleB string) bool {
	if dir == Desc {
		primary = -primary
	}
	if primary != 0 {
		return primary < 0
	}
	return cmpTitle(titleA, titleB) < 0
}

// SortArtists orders artists in place by s.
func SortArtists(items []Artist, s Spec) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		var c int
		switch s.Field {
		case SortAdded:
			c = cmpTime(a.AddedAt, b.AddedAt)
		case SortAlbumCount:
			c = cmpInt(a.AlbumCount, b.AlbumCount)
		case SortTrackCount:
			c = cmpInt(a.TrackCount, b.TrackCount)
		default:
			c = cmpTitle(a.Title, b.Title)
		}
		return less(c, s.Dir, a.Title, b.Title)
	})
}

// SortAlbums orders albums in place by s.
func SortAlbums(items []Album, s Spec) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		var c int
		switch s.Field {
		case SortYear:
			c = cmpInt(a.Year, b.Year)
		case SortAdded:
			c = cmpTime(a.AddedAt, b.AddedAt)
		case SortDuration:
			c = cmpDuration(a.Duration, b.Duration)
		case SortTrackCount:
			c = cmpInt(a.TrackCount, b.TrackCount)
		default:
			c = cmpTitle(a.Title, b.Title)
		}
		return less(c, s.Dir, a.Title, b.Title)
	})
}

// SortTracks orders tracks in place by s.
func SortTracks(items []Track, s Spec) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		var c int
		switch s.Field {
		case SortTrackNumber:
			// Album running order is (disc, track): numbers restart on
			// each disc, so comparing Index alone interleaves the discs
			// of a double album.
			if c = cmpInt(a.Disc, b.Disc); c == 0 {
				c = cmpInt(a.Index, b.Index)
			}
		case SortDuration:
			c = cmpDuration(a.Duration, b.Duration)
		case SortAdded:
			c = cmpTime(a.AddedAt, b.AddedAt)
		default:
			c = cmpTitle(a.Title, b.Title)
		}
		return less(c, s.Dir, a.Title, b.Title)
	})
}

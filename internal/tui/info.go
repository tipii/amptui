package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/NimbleMarkets/ntcharts/v2/picture"

	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/textutil"
)

// headerThumbCellsW / H is the cell footprint of the hero thumbnail
// shown on artist / album screens. It lives in a dedicated block
// *under* the breadcrumb row (not beside it), so it can be sizeable
// without crowding the title. 2:1 cell aspect → W=2×H stays
// visually square.
const (
	headerThumbCellsW = 14
	headerThumbCellsH = 7
)

// modalThumb* are the cell footprint of the artwork shown above the
// bio in the info modal. Cells are ~2:1 tall:wide so we double the
// width relative to height to land near square.
const (
	modalThumbCellsW = 24
	modalThumbCellsH = 12
)

// headerThumb returns the rendered hero thumbnail for the current
// artist / album screen, or "" when artwork is off / unavailable /
// not on a screen that has one.
func (m Model) headerThumb() string {
	if !m.cfg.Images {
		return ""
	}
	var p *picture.Model
	switch m.level {
	case levelAlbums:
		p = &m.artistHeaderPic
	case levelTracks:
		p = &m.albumHeaderPic
	}
	if p == nil {
		return ""
	}
	return p.View().Content
}

// infoHeaderBlock renders the multi-line panel docked next to the
// hero thumb on artist / album screens: the one-line tag summary,
// then a soft-wrapped bio teaser, then a hint at the info modal
// shortcut. width is the available column count for the right side
// of the JoinHorizontal — we wrap inside it so long lines don't
// spill into the next row.
func (m Model) infoHeaderBlock(width int) string {
	if width < 10 {
		width = 10
	}
	switch m.level {
	case levelAlbums:
		if m.metaLoading && m.artistMeta == nil {
			return helpStyle.Render(m.spinner.View() + "loading artist info…")
		}
		if a := m.artistMeta; a != nil {
			return m.composeInfoBlock(width, artistHeaderSummary(a), a.Summary)
		}
	case levelTracks:
		if m.metaLoading && m.albumMeta == nil {
			return helpStyle.Render(m.spinner.View() + "loading album info…")
		}
		if a := m.albumMeta; a != nil {
			return m.composeInfoBlock(width, albumHeaderSummary(a), a.Summary)
		}
	}
	return ""
}

// composeInfoBlock stacks summary + bio teaser + shortcut hint. The
// teaser is 3 wrapped lines max; the hint only renders when there's
// actually a bio (otherwise there's nothing to "read more" of).
func (m Model) composeInfoBlock(width int, summary, bio string) string {
	var b strings.Builder
	if summary != "" {
		b.WriteString(helpStyle.Render(summary))
	}
	teaser, hasBio := bioTeaser(bio, width, 3)
	if teaser != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(teaser)
	}
	if hasBio {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		hintKey := m.keymap.Info.Help().Key
		b.WriteString(helpStyle.Render("press " + hintKey + " for the full bio"))
	}
	return b.String()
}

// bioTeaser collapses bio whitespace, wraps to width, and returns the
// first maxLines (with a trailing ellipsis if truncated). hasBio
// reports whether there was a bio at all so the caller can decide
// whether to show the "read more" hint.
func bioTeaser(bio string, width, maxLines int) (text string, hasBio bool) {
	flat := strings.Join(strings.Fields(bio), " ")
	if flat == "" {
		return "", false
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(flat)
	lines := strings.Split(wrapped, "\n")
	if len(lines) <= maxLines {
		return wrapped, true
	}
	out := strings.Join(lines[:maxLines], "\n")
	out = strings.TrimRight(out, " ") + " …"
	return out, true
}

// infoHeaderLine renders the one-line summary shown under the
// breadcrumbs on screens that have rich metadata (artist's albums or
// album's tracks). Returns the empty string on other levels so the
// chrome height stays constant.
func (m Model) infoHeaderLine() string {
	switch m.level {
	case levelAlbums:
		if m.metaLoading && m.artistMeta == nil {
			return helpStyle.Render("  " + m.spinner.View() + "loading artist info…")
		}
		if a := m.artistMeta; a != nil {
			return "  " + helpStyle.Render(artistHeaderSummary(a))
		}
	case levelTracks:
		if m.metaLoading && m.albumMeta == nil {
			return helpStyle.Render("  " + m.spinner.View() + "loading album info…")
		}
		if a := m.albumMeta; a != nil {
			return "  " + helpStyle.Render(albumHeaderSummary(a))
		}
	}
	return ""
}

func artistHeaderSummary(a *media.ArtistMetadata) string {
	parts := make([]string, 0, 3)
	if len(a.Countries) > 0 {
		parts = append(parts, a.Countries[0])
	}
	tags := append([]string{}, a.Genres...)
	tags = append(tags, a.Moods...)
	if n := 3; len(tags) > n {
		tags = tags[:n]
	}
	if len(tags) > 0 {
		parts = append(parts, strings.Join(tags, ", "))
	}
	return strings.Join(parts, " · ")
}

func albumHeaderSummary(a *media.AlbumMetadata) string {
	parts := make([]string, 0, 4)
	if a.Artist != "" {
		parts = append(parts, a.Artist)
	}
	if a.Year > 0 {
		parts = append(parts, fmt.Sprintf("%d", a.Year))
	}
	if a.Studio != "" {
		parts = append(parts, a.Studio)
	}
	tags := append([]string{}, a.Genres...)
	tags = append(tags, a.Moods...)
	if n := 2; len(tags) > n {
		tags = tags[:n]
	}
	if len(tags) > 0 {
		parts = append(parts, strings.Join(tags, ", "))
	}
	return strings.Join(parts, " · ")
}

// infoModalBox wraps the per-level metadata body (set via SetContent
// when the modal opens) in the shared modal frame.
func (m Model) infoModalBox() string {
	var heading string
	switch m.level {
	case levelAlbums:
		if a := m.artistMeta; a != nil {
			heading = headerStyle.Render(a.Title)
		} else {
			heading = headerStyle.Render("Artist")
		}
	case levelTracks:
		if a := m.albumMeta; a != nil {
			heading = headerStyle.Render(a.Title)
			if a.Artist != "" {
				heading += helpStyle.Render("  " + a.Artist)
			}
		} else {
			heading = headerStyle.Render("Album")
		}
	default:
		heading = headerStyle.Render("Info")
	}
	return m.modalFrame(heading + "\n" + m.infoViewport.View())
}

// infoModalContent assembles the modal body for whichever level the
// user is on. Returns "" if there's nothing to show — caller uses that
// as a "don't open the modal" signal. When artwork is on and we have
// the bytes, the rendered image is prepended so the bio appears below
// the thumbnail.
func (m Model) infoModalContent() string {
	var (
		meta string
		pic  *picture.Model
	)
	switch m.level {
	case levelAlbums:
		if a := m.artistMeta; a != nil {
			meta = formatArtistInfo(a)
			pic = &m.artistModalPic
		}
	case levelTracks:
		if a := m.albumMeta; a != nil {
			meta = formatAlbumInfo(a)
			pic = &m.albumModalPic
		}
	}
	if meta == "" {
		return ""
	}
	if m.cfg.Images && pic != nil {
		if img := pic.View().Content; img != "" {
			return img + "\n\n" + meta
		}
	}
	return meta
}

func formatArtistInfo(a *media.ArtistMetadata) string {
	var b strings.Builder
	if a.Summary != "" {
		b.WriteString(textutil.ReflowParagraphs(a.Summary))
		b.WriteString("\n\n")
	}
	writeTags(&b, "Genres", a.Genres)
	writeTags(&b, "Moods", a.Moods)
	writeTags(&b, "Styles", a.Styles)
	writeTags(&b, "Country", a.Countries)
	writeTags(&b, "Similar", a.Similar)
	return strings.TrimRight(b.String(), "\n")
}

func formatAlbumInfo(a *media.AlbumMetadata) string {
	var b strings.Builder
	if a.Summary != "" {
		b.WriteString(textutil.ReflowParagraphs(a.Summary))
		b.WriteString("\n\n")
	}
	if a.Year > 0 {
		b.WriteString(helpStyle.Render("Year: "))
		b.WriteString(fmt.Sprintf("%d\n", a.Year))
	}
	if a.Studio != "" {
		b.WriteString(helpStyle.Render("Studio: "))
		b.WriteString(a.Studio + "\n")
	}
	if a.Artist != "" {
		b.WriteString(helpStyle.Render("Artist: "))
		b.WriteString(a.Artist + "\n")
	}
	writeTags(&b, "Genres", a.Genres)
	writeTags(&b, "Moods", a.Moods)
	writeTags(&b, "Styles", a.Styles)
	return strings.TrimRight(b.String(), "\n")
}

func writeTags(b *strings.Builder, label string, tags []string) {
	if len(tags) == 0 {
		return
	}
	b.WriteString(helpStyle.Render(label + ": "))
	b.WriteString(strings.Join(tags, ", "))
	b.WriteString("\n")
}

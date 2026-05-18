package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Theme is the single source of truth for colors and reusable styles.
// Named tokens (Accent, Muted, Error, NowPlaying) describe roles, not
// hex values — pick a new palette by changing NewTheme() and every
// styled surface follows. Styles below are the canonical compositions
// used across the package; widths / borders specific to a single
// surface are applied at the render site on top of these.
type Theme struct {
	// Tokens — roles, not specific colors.
	Accent     color.Color // primary highlight (selected, headers, modal border)
	Muted      color.Color // subdued surfaces (idle card borders)
	Error      color.Color // error / failure foreground
	NowPlaying color.Color // currently-playing track indicator

	// Styles — pre-built compositions.
	Header  lipgloss.Style // bold accent text (titles)
	Crumb   lipgloss.Style // faint breadcrumb / metadata
	Help    lipgloss.Style // faint help-line text
	Err     lipgloss.Style // error text
	NP      lipgloss.Style // now-playing text
	Section lipgloss.Style // section header within a panel
	Modal   lipgloss.Style // rounded modal box (caller sets width/height)
	Card    lipgloss.Style // grid card, idle (caller sets width)
	CardSel lipgloss.Style // grid card, selected (caller sets width)
}

// NewTheme returns the default theme. Change a token here and every
// styled surface in the TUI follows — there are no stray color literals
// scattered across the package.
func NewTheme() Theme {
	accent := lipgloss.Color("213")
	muted := lipgloss.Color("240")
	errC := lipgloss.Color("203")
	np := lipgloss.Color("121")

	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(muted).
		Height(cardOuterH).
		AlignHorizontal(lipgloss.Center).
		AlignVertical(lipgloss.Center)

	return Theme{
		Accent:     accent,
		Muted:      muted,
		Error:      errC,
		NowPlaying: np,

		Header:  lipgloss.NewStyle().Bold(true).Foreground(accent),
		Crumb:   lipgloss.NewStyle().Faint(true),
		Help:    lipgloss.NewStyle().Faint(true),
		Err:     lipgloss.NewStyle().Foreground(errC),
		NP:      lipgloss.NewStyle().Foreground(np),
		Section: lipgloss.NewStyle().Bold(true).Foreground(accent),
		Modal: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(0, 1),
		Card: card,
		CardSel: card.
			BorderForeground(accent).
			Foreground(accent).
			Bold(true),
	}
}

// theme is the package-wide default. Surfaces that need a style read
// from here; tests can stub it if a non-default palette is needed.
var theme = NewTheme()

// Package-level aliases preserved so existing call sites compile
// untouched. They point at the active theme's styles.
var (
	headerStyle     = theme.Header
	crumbStyle      = theme.Crumb
	helpStyle       = theme.Help
	errStyle        = theme.Err
	npStyle         = theme.NP
	sectionStyle    = theme.Section
	modalStyle      = theme.Modal
	cardStyle       = theme.Card
	cardCursorStyle = theme.CardSel
)

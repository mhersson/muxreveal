package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// styles are the Lip Gloss styles the shell draws with, all coloured from one
// theme.
type styles struct {
	body       lipgloss.Style
	bar        lipgloss.Style
	badge      lipgloss.Style
	barTitle   lipgloss.Style
	card       lipgloss.Style
	cardTitle  lipgloss.Style
	cardMuted  lipgloss.Style
	cardFiller lipgloss.Style
	cardKey    lipgloss.Style
	barKey     lipgloss.Style
	barNotice  lipgloss.Style
	popup      lipgloss.Style
	popupKey   lipgloss.Style
	popupTitle lipgloss.Style
	// border draws unfocused frame lines; borderAgent and borderShell the
	// focused pane's.
	border, borderAgent, borderShell lipgloss.Style
	// kindAgent and kindShell are bold in the kind colour: icon and focused title.
	kindAgent, kindShell                          lipgloss.Style
	titleMuted, cwdFocus, cwdSubtle, dot, tagZoom lipgloss.Style
	// popupBorder draws the popup frame on the panel colour.
	popupBorder lipgloss.Style
}

func newStyles(t theme.Theme) styles {
	ui := t.UI
	onBg := lipgloss.NewStyle().Background(ui.Bg).Foreground(ui.Fg)
	onPanel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	return styles{
		body:        onBg,
		bar:         lipgloss.NewStyle().Background(ui.Panel),
		badge:       lipgloss.NewStyle().Background(ui.Accent).Foreground(ui.OnAccent).Bold(true),
		barTitle:    lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Muted),
		card:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).BorderBackground(ui.Bg),
		cardTitle:   onPanel.Foreground(ui.Accent).Bold(true),
		cardMuted:   onPanel.Foreground(ui.Muted),
		cardFiller:  onPanel,
		cardKey:     onPanel.Foreground(ui.Key),
		barKey:      lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Key),
		barNotice:   lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Warning),
		popup:       onPanel,
		popupKey:    onPanel.Foreground(ui.Key),
		popupTitle:  onPanel.Foreground(ui.BorderFocus).Bold(true),
		border:      onBg.Foreground(ui.Border),
		borderAgent: onBg.Foreground(ui.Agent),
		borderShell: onBg.Foreground(ui.Shell),
		kindAgent:   onBg.Foreground(ui.Agent).Bold(true),
		kindShell:   onBg.Foreground(ui.Shell).Bold(true),
		titleMuted:  onBg.Foreground(ui.Muted),
		cwdFocus:    onBg,
		cwdSubtle:   onBg.Foreground(ui.Subtle),
		dot:         onBg.Foreground(ui.Subtle),
		tagZoom:     onBg.Foreground(ui.Accent).Bold(true),
		popupBorder: lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.BorderFocus),
	}
}

func (s styles) borderStyle(own uint8) lipgloss.Style {
	switch own {
	case ownAgent:
		return s.borderAgent
	case ownShell:
		return s.borderShell
	}
	return s.border
}

func (s styles) kind(k core.PaneKind) lipgloss.Style {
	if k == core.KindAgent {
		return s.kindAgent
	}
	return s.kindShell
}

package ui

import (
	"charm.land/lipgloss/v2"

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
	// popupBorder draws the popup frame on the panel colour.
	popupBorder lipgloss.Style
}

func newStyles(t theme.Theme) styles {
	ui := t.UI
	onPanel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	return styles{
		body:        lipgloss.NewStyle().Background(ui.Bg).Foreground(ui.Fg),
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
		popupBorder: lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.BorderFocus),
	}
}

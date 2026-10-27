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
	card       lipgloss.Style
	cardTitle  lipgloss.Style
	cardMuted  lipgloss.Style
	cardFiller lipgloss.Style
	cardKey    lipgloss.Style
	popup      lipgloss.Style
	popupKey   lipgloss.Style
	// popupHead is the accent bold title and group head; popupRule draws the divider lines.
	popupHead, popupRule lipgloss.Style
	// border draws unfocused frame lines; borderAgent and borderShell the
	// focused pane's.
	border, borderAgent, borderShell lipgloss.Style
	// kindAgent and kindShell are bold in the kind colour: icon and focused title.
	kindAgent, kindShell                          lipgloss.Style
	titleMuted, cwdFocus, cwdSubtle, dot, tagZoom lipgloss.Style
	tagScroll                                     lipgloss.Style
	// popupBorder draws the popup frame on the panel colour.
	popupBorder lipgloss.Style
	// popupMuted and popupSubtle are secondary text on the panel colour.
	popupMuted, popupSubtle lipgloss.Style
	// The confirm box: its Error border and title, bold question and bold keys.
	confirmBorder, confirmTitle, confirmQuestion, confirmKey lipgloss.Style
	// runs memoizes rendered border runs; it is shared by copies of the styles.
	runs *runCache
}

func newStyles(t theme.Theme) styles {
	ui := t.UI
	onBg := lipgloss.NewStyle().Background(ui.Bg).Foreground(ui.Fg)
	onPanel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	return styles{
		body:        onBg,
		card:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).BorderBackground(ui.Bg),
		cardTitle:   onPanel.Foreground(ui.Accent).Bold(true),
		cardMuted:   onPanel.Foreground(ui.Muted),
		cardFiller:  onPanel,
		cardKey:     onPanel.Foreground(ui.Key),
		popup:       onPanel,
		popupKey:    onPanel.Foreground(ui.Key).Bold(true),
		popupHead:   onPanel.Foreground(ui.Accent).Bold(true),
		popupRule:   onPanel.Foreground(ui.Border),
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
		tagScroll:   onBg.Foreground(ui.Accent).Bold(true),
		popupBorder: lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.BorderFocus),
		popupMuted:  onPanel.Foreground(ui.Muted),
		popupSubtle: onPanel.Foreground(ui.Subtle),

		confirmBorder:   onPanel.Foreground(ui.Error),
		confirmTitle:    onPanel.Foreground(ui.Error).Bold(true),
		confirmQuestion: onPanel.Bold(true),
		confirmKey:      onPanel.Foreground(ui.Key).Bold(true),
		runs:            &runCache{},
	}
}

// runKey identifies a rendered border run: the style owner and its glyphs.
type runKey struct {
	own    uint8
	glyphs string
}

// runCache holds rendered border runs, which repeat on every frame. It is only
// used on the update loop and is dropped whole when it passes runCacheMax.
type runCache struct {
	m map[runKey]string
}

const runCacheMax = 1024

func (s styles) borderRun(own uint8, glyphs string) string {
	k := runKey{own, glyphs}
	if r, ok := s.runs.m[k]; ok {
		return r
	}
	if s.runs.m == nil || len(s.runs.m) >= runCacheMax {
		s.runs.m = make(map[runKey]string)
	}
	r := s.borderStyle(own).Render(glyphs)
	s.runs.m[k] = r
	return r
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

package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const welcomeSample = `package main

// Agents run in panes; the review sits beside them.
func review(turn int) string {
	return fmt.Sprintf("turn %d ready", turn)
}
`

// barState is what the status bar shows beyond the theme title.
type barState struct {
	leader  string
	pending bool   // the leader was pressed and awaits a key
	notice  string // replaces the theme title until the next key press
}

// shell draws the app frame: a body with the welcome card and a status bar on
// the last row. Every returned line is exactly width cells wide.
func shell(width, height int, t theme.Theme, st styles, sample []string, hints []hint, bar barState) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	rows := make([]string, 0, height)
	if body := height - 1; body > 0 {
		rows = append(rows, strings.Split(renderBody(width, body, st, sample, hints), "\n")...)
	}
	rows = append(rows, statusBar(width, t, st, bar))
	return strings.Join(rows, "\n")
}

func renderBody(width, height int, st styles, sample []string, hints []hint) string {
	card := welcomeCard(st, sample, hints)
	if lipgloss.Width(card) > width || lipgloss.Height(card) > height {
		card = ""
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, card,
		lipgloss.WithWhitespaceStyle(st.body))
}

func welcomeCard(st styles, sample []string, hints []hint) string {
	lines := []string{
		st.cardTitle.Render("MuxReveal"),
		st.cardMuted.Render("No panes yet."),
		"",
	}
	keyW := 0
	for _, h := range hints {
		keyW = max(keyW, lipgloss.Width(h.key))
	}
	for _, h := range hints {
		lines = append(lines, st.cardKey.Render(h.key)+
			st.cardFiller.Render(strings.Repeat(" ", keyW-lipgloss.Width(h.key)+2)+h.title))
	}
	if len(hints) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, sample...)
	inner := 0
	for _, l := range lines {
		inner = max(inner, lipgloss.Width(l))
	}
	const padX = 1
	for i, l := range lines {
		pad := inner - lipgloss.Width(l)
		lines[i] = st.cardFiller.Render(strings.Repeat(" ", padX)) + l +
			st.cardFiller.Render(strings.Repeat(" ", pad+padX))
	}
	return st.card.Render(strings.Join(lines, "\n"))
}

// statusBar draws the left side, then the leader hint on the right when it
// fits beside the left side with a gap; otherwise the left side truncates.
func statusBar(width int, t theme.Theme, st styles, bar barState) string {
	title := st.barTitle.Render(" " + t.Title)
	if bar.notice != "" {
		title = st.barNotice.Render(" " + bar.notice)
	}
	left := st.badge.Render(" MuxReveal ") + title
	key := st.barKey.Render(bar.leader)
	if bar.pending {
		key = st.badge.Render(bar.leader)
	}
	right := key + st.barTitle.Render(" commands ")
	if lipgloss.Width(left)+1+lipgloss.Width(right) > width {
		right = ""
		left = ansi.Truncate(left, width, "")
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	return left + st.bar.Render(strings.Repeat(" ", max(gap, 0))) + right
}

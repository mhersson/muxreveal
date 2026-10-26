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

// shell draws the app frame: a body with the welcome card and a status bar on
// the last row. Every returned line is exactly width cells wide.
func shell(width, height int, t theme.Theme, st styles, sample []string) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	rows := make([]string, 0, height)
	if body := height - 1; body > 0 {
		rows = append(rows, strings.Split(renderBody(width, body, st, sample), "\n")...)
	}
	rows = append(rows, statusBar(width, t, st))
	return strings.Join(rows, "\n")
}

func renderBody(width, height int, st styles, sample []string) string {
	card := welcomeCard(st, sample)
	if lipgloss.Width(card) > width || lipgloss.Height(card) > height {
		card = ""
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, card,
		lipgloss.WithWhitespaceStyle(st.body))
}

func welcomeCard(st styles, sample []string) string {
	lines := append([]string{
		st.cardTitle.Render("MuxReveal"),
		st.cardMuted.Render("No panes yet."),
		"",
	}, sample...)
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

func statusBar(width int, t theme.Theme, st styles) string {
	left := st.badge.Render(" MuxReveal ") + st.barTitle.Render(" "+t.Title)
	left = ansi.Truncate(left, width, "")
	if gap := width - lipgloss.Width(left); gap > 0 {
		left += st.bar.Render(strings.Repeat(" ", gap))
	}
	return left
}

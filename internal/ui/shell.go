package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

const welcomeSample = `package main

// Agents run in panes; the review sits beside them.
func review(turn int) string {
	return fmt.Sprintf("turn %d ready", turn)
}
`

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

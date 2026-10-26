package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
)

// confirm asks a yes/no question. It owns every key while open. pane is the
// pane the question is about, or empty; the confirm closes when that pane does.
type confirm struct {
	prompt string
	pane   core.PaneID
	yes    func(a *App) tea.Cmd
}

// openConfirm shows c and ends a pending leader sequence.
func (a *App) openConfirm(c confirm) {
	a.pending, a.popup = false, false
	a.confirm = &c
}

// handleConfirmKey answers the open confirm; any other key is swallowed.
func (a *App) handleConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	c := a.confirm

	switch msg.String() {
	case "y", "enter":
		a.confirm = nil

		return c.yes(a)
	case "n", "esc":
		a.confirm = nil
	}

	return nil
}

// confirmBox draws the bordered prompt with its answer keys.
func confirmBox(c confirm, st styles) []string {
	answers := st.popupKey.Render("y") + st.popup.Render(" / ") + st.popupKey.Render("n")
	rows := []string{
		st.popup.Render(" " + c.prompt + " "),
		st.popup.Render(" ") + answers + st.popup.Render(" "),
	}

	inner := 0
	for _, r := range rows {
		inner = max(inner, lipgloss.Width(r))
	}

	b := lipgloss.RoundedBorder()
	lines := make([]string, 0, len(rows)+2)
	lines = append(lines, st.popupBorder.Render(b.TopLeft+strings.Repeat(b.Top, inner)+b.TopRight))

	for _, r := range rows {
		pad := st.popup.Render(strings.Repeat(" ", inner-lipgloss.Width(r)))
		lines = append(lines, st.popupBorder.Render(b.Left)+r+pad+st.popupBorder.Render(b.Right))
	}

	return append(lines, st.popupBorder.Render(b.BottomLeft+strings.Repeat(b.Bottom, inner)+b.BottomRight))
}

// overlayConfirm draws the confirm box over base, like the palette, clipped to
// the terminal.
func overlayConfirm(base string, width, height int, c confirm, st styles) string {
	lines := confirmBox(c, st)
	pw := 0

	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
		pw = max(pw, lipgloss.Width(lines[i]))
	}

	return overlayLines(base, width, height, lines, pw)
}

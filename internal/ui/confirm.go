package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
)

// maxConfirmWidth caps the confirm box width.
const maxConfirmWidth = 46

// confirm asks a yes/no question. It owns every key while open. action names
// the answer and titles the box, prompt is the question and detail the line
// under it. pane and tab are what the question is about, or empty; the confirm
// closes when that pane or tab does.
type confirm struct {
	action, prompt, detail string
	pane                   core.PaneID
	tab                    core.TabID
	yes                    func(a *App) tea.Cmd
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

// confirmBox draws the box w cells wide: a square border in Error with the
// action set into the top edge, the question, its detail and the answer keys.
func confirmBox(c confirm, w int, icons bool, st styles) []string {
	inner := w - 2

	// row indents its content two cells from the border and fills to the edge.
	row := func(content string) string {
		content = ansi.Truncate(st.popup.Render("  ")+content, inner, "")
		fill := st.popup.Render(strings.Repeat(" ", inner-ansi.StringWidth(content)))

		return st.confirmBorder.Render("│") + content + fill + st.confirmBorder.Render("│")
	}

	title := " " + c.action + " "
	if icons {
		title = " " + iconAlert + " " + c.action + " "
	}

	// The title starts two cells from the corner: the corner, one dash, then the title.
	title = ansi.Truncate(title, max(inner-1, 0), "")
	top := st.confirmBorder.Render("┌─") + st.confirmTitle.Render(title) +
		st.confirmBorder.Render(strings.Repeat("─", max(inner-1-ansi.StringWidth(title), 0))+"┐")

	answer := func(key, label string) string {
		return st.popupSubtle.Render("[") + st.confirmKey.Render(key) + st.popupSubtle.Render("]") +
			st.popup.Render(" "+label)
	}

	return []string{
		top,
		row(""),
		row(st.confirmQuestion.Render(c.prompt)),
		row(st.popupMuted.Render(c.detail)),
		row(""),
		row(answer("y", c.action) + st.popup.Render("    ") + answer("n", "Cancel")),
		st.confirmBorder.Render("└" + strings.Repeat("─", inner) + "┘"),
	}
}

// overlayConfirm draws the confirm box over base, centred with its top at a
// third of the height, clipped to the terminal.
func overlayConfirm(base string, width, height int, c confirm, icons bool, st styles) string {
	w := min(maxConfirmWidth, width-2)
	if w < 4 {
		return base
	}

	return overlayLines(base, width, height, max(1, height/3), confirmBox(c, w, icons, st), w)
}

package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// toastRows is the height of the toast box: a border, one text row, a border.
const toastRows = 3

// overlayToast draws the notice as a rounded box at the bottom right of the
// body, one cell from the right edge, its bottom border on the last row. It is
// drawn into the frame only, so it takes no keys and no mouse events. Nothing
// is drawn when the box does not fit the body.
func overlayToast(base string, width, height int, notice string, icons bool, st styles) string {
	text := printable(notice)
	if text == "" || height-2 < toastRows {
		return base
	}

	// Blanks around the text, and the icon with its own blank.
	chrome := 2
	if icons {
		chrome += 2
	}

	room := width - 2 - 2 - chrome
	if room < 1 {
		return base
	}

	text = ansi.Truncate(text, room, "…")
	inner := chrome + ansi.StringWidth(text)
	w := inner + 2

	mid := st.toastBorder.Render("│") + st.toastText.Render(" ")
	if icons {
		mid += st.toastIcon.Render(iconAlert) + st.toastText.Render(" ")
	}

	mid += st.toastText.Render(text+" ") + st.toastBorder.Render("│")

	lines := []string{
		st.toastBorder.Render("╭" + strings.Repeat("─", inner) + "╮"),
		mid,
		st.toastBorder.Render("╰" + strings.Repeat("─", inner) + "╯"),
	}

	return overlayAt(base, width, height, width-1-w, height-toastRows, lines)
}

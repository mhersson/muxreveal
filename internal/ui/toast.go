package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// toastRows is the height of the toast box: a border, one text row, a border.
const toastRows = 3

// toastLayout is the notice text as it is drawn and the box's left column,
// top row and width; ok is false when no box is drawn.
func toastLayout(width, height int, notice string, icons bool) (text string, x, y, w int, ok bool) {
	text = printable(notice)
	if text == "" || height-2 < toastRows {
		return "", 0, 0, 0, false
	}

	// Blanks around the text, and the icon with its own blank.
	chrome := 2
	if icons {
		chrome += 2
	}

	room := width - 2 - 2 - chrome
	if room < 1 {
		return "", 0, 0, 0, false
	}

	text = ansi.Truncate(text, room, "…")
	w = chrome + ansi.StringWidth(text) + 2

	return text, width - 1 - w, height - toastRows, w, true
}

// toastCovers reports whether the toast box for notice covers cell (cx, cy).
func toastCovers(width, height int, notice string, icons bool, cx, cy int) bool {
	_, x, y, w, ok := toastLayout(width, height, notice, icons)
	return ok && cx >= x && cx < x+w && cy >= y && cy < y+toastRows
}

// overlayToast draws the notice as a rounded box at the bottom right of the
// body, one cell from the right edge, its bottom border on the last row. It is
// drawn into the frame only, so it takes no keys and no mouse events. Nothing
// is drawn when the box does not fit the body.
func overlayToast(base string, width, height int, notice string, icons bool, st styles) string {
	text, x, y, w, ok := toastLayout(width, height, notice, icons)
	if !ok {
		return base
	}

	inner := w - 2

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

	return overlayAt(base, width, height, x, y, lines)
}

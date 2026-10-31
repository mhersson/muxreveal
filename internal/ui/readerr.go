package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// wrapRows word-wraps s to width cells, breaking a word longer than that. Past
// rows lines the rest is cut with an ellipsis on the last kept line.
func wrapRows(s string, width, rows int) []string {
	lines := strings.Split(ansi.Wrap(s, max(width, 1), ""), "\n")
	if rows = max(rows, 1); len(lines) > rows {
		lines = lines[:rows]
		lines[rows-1] = ellipsize(lines[rows-1], width)
	}

	return lines
}

// ellipsize ends line with an ellipsis within width cells.
func ellipsize(line string, width int) string {
	return ansi.Truncate(line, max(width-1, 0), "") + "…"
}

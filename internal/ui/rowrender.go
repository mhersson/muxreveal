package ui

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/term"
)

// renderRow renders live row y of the emulator; see renderCells.
func renderRow(v term.VT, y, w int) string {
	return renderCells(func(x int) *uv.Cell { return v.CellAt(x, y) }, w, nil)
}

// spanPaint paints the background of columns from..to (inclusive) of a row.
type spanPaint struct {
	from, to int
	bg       color.Color
}

// renderCells returns a row as a string exactly w cells wide, with style and
// link sequences closed at the end so rows compose independently. at returns
// nil for a column without a cell. Each span of hl repaints its columns, wide
// cells included when any of their columns is inside one.
func renderCells(at func(x int) *uv.Cell, w int, hl []spanPaint) string {
	var (
		sb    strings.Builder
		style uv.Style
		link  uv.Link
		cells int
	)

	for x := range w {
		c := at(x)
		if c == nil {
			c = &uv.Cell{Content: " ", Width: 1}
		}

		if c.Width == 0 {
			continue
		}

		if c.Width > 1 && x+c.Width > w {
			// A wide cell cut by the right edge would overflow the row.
			c = &uv.Cell{Content: " ", Width: 1, Style: c.Style}
		}

		for _, h := range hl {
			if x <= h.to && x+c.Width > h.from {
				painted := *c
				painted.Style.Bg = h.bg
				c = &painted

				break
			}
		}

		if c.Link != link {
			if link != (uv.Link{}) {
				sb.WriteString(ansi.ResetHyperlink())
			}

			if c.Link != (uv.Link{}) {
				sb.WriteString(ansi.SetHyperlink(c.Link.URL, c.Link.Params))
			}

			link = c.Link
		}

		sb.WriteString(uv.StyleDiff(&style, &c.Style))

		style = c.Style

		if c.Content == "" {
			sb.WriteByte(' ')
		} else {
			sb.WriteString(c.Content)
		}

		cells += c.Width
	}

	if link != (uv.Link{}) {
		sb.WriteString(ansi.ResetHyperlink())
	}

	if style != (uv.Style{}) {
		sb.WriteString(ansi.ResetStyle)
	}

	if cells < w {
		sb.WriteString(strings.Repeat(" ", w-cells))
	}

	return sb.String()
}

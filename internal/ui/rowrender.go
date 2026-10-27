package ui

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/term"
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
// nil for a column without a cell. A non-nil hl repaints its span, wide cells
// included when any of their columns is inside it.
func renderCells(at func(x int) *uv.Cell, w int, hl *spanPaint) string {
	var (
		sb    strings.Builder
		style uv.Style
		link  uv.Link
		cells int
	)

	for x := 0; x < w; x++ {
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

		if hl != nil && x <= hl.to && x+c.Width > hl.from {
			painted := *c
			painted.Style.Bg = hl.bg
			c = &painted
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

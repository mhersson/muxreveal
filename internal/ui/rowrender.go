package ui

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/term"
)

// renderRow returns row y as a string exactly w cells wide, with style and
// link sequences closed at the end so rows compose independently.
func renderRow(v term.VT, y, w int) string {
	var (
		sb    strings.Builder
		style uv.Style
		link  uv.Link
		cells int
	)

	for x := 0; x < w; x++ {
		c := v.CellAt(x, y)
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

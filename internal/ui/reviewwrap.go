package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// wrapReach is how far back from a part's edge a space may end it.
const wrapReach = 16

// codeWidths are the code columns that wrap a diff line, by the way the row
// draws it: unified lines, the full view and ghost lines, and the two sides of
// a side by side row. Zero means no wrapping.
type codeWidths struct{ line, full, left, right int }

// wrapPart is the units [u0, u1) of a line and the cells [c0, c1) they take,
// counted from the line's first cell.
type wrapPart struct{ u0, u1, c0, c1 int }

// lineUnits is what a code row draws for a raw line: the line without its
// final CR cut to the budget, then the cut's ellipsis and the CR ending tag.
func lineUnits(raw string, tagCR bool) []diffUnit {
	line := strings.TrimSuffix(raw, "\r")
	kept, cut := cutRunes(line, lineBudget)
	units := diffUnits(kept)

	if cut {
		units = append(units, diffUnit{raw: len(kept), cells: 1, whole: true, tail: true})
	}

	if tagCR {
		units = append(units, diffUnit{raw: len(line), cells: ansi.StringWidth(endingTag), text: endingTag, whole: true, tail: true})
	}

	return units
}

// wrapLine splits units into rows of at most width cells, ending a row after
// a space within the last wrapReach cells when a unit does not fit. A unit
// wider than width takes a row alone, as does a tail unit that does not fit.
// It returns at least one part.
func wrapLine(units []diffUnit, width int) []wrapPart {
	if width <= 0 {
		total := 0
		for _, u := range units {
			total += u.cells
		}

		return []wrapPart{{u0: 0, u1: len(units), c0: 0, c1: total}}
	}

	var (
		parts []wrapPart
		start wrapPart
	)

	ends := make([]int, len(units)+1) // ends[i] is the cell where unit i-1 ends
	for i, u := range units {
		ends[i+1] = ends[i] + u.cells
	}

	for i := 0; i < len(units); {
		if ends[i+1]-start.c0 <= width {
			i++

			continue
		}

		end := i

		switch {
		case i == start.u0:
			end = i + 1
		case units[i].tail:
			// The ending tag and budget ellipsis wrap alone, never at a reach space.
		default:
			for j := i - 1; j >= start.u0 && ends[j+1] > start.c0+width-wrapReach; j-- {
				if units[j].text == " " {
					end = j + 1

					break
				}
			}
		}

		parts = append(parts, wrapPart{u0: start.u0, u1: end, c0: start.c0, c1: ends[end]})
		start = wrapPart{u0: end, c0: ends[end]}
		i = end
	}

	if len(parts) > 0 && start.u0 == len(units) {
		return parts
	}

	return append(parts, wrapPart{u0: start.u0, u1: len(units), c0: start.c0, c1: ends[len(units)]})
}

// lineParts is wrapLine of a raw line's units, built only when wholePart
// declines the line.
func lineParts(raw string, tagCR bool, width int) []wrapPart {
	if p, ok := wholePart(raw, tagCR, width); ok {
		return []wrapPart{p}
	}

	return wrapLine(lineUnits(raw, tagCR), width)
}

// wholePart is the one part of an untagged line of printable ASCII and tabs
// that fits width, as wrapLine gives it: each byte is a unit, and a tab takes
// the cells to the next tab stop, as diffUnits counts them. ok is false for
// any other line.
func wholePart(raw string, tagCR bool, width int) (wrapPart, bool) {
	if tagCR || width <= 0 {
		return wrapPart{}, false
	}

	// Every byte takes a cell or more, so a longer line cannot fit, and a line
	// over the budget is cut and gains the ellipsis unit.
	line := strings.TrimSuffix(raw, "\r")
	if len(line) > min(width, lineBudget) {
		return wrapPart{}, false
	}

	cells := 0

	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\t':
			cells += tabStop - cells%tabStop
		case c < 0x20 || c > 0x7e:
			return wrapPart{}, false
		default:
			cells++
		}
	}

	if cells > width {
		return wrapPart{}, false
	}

	return wrapPart{u0: 0, u1: len(line), c0: 0, c1: cells}, true
}

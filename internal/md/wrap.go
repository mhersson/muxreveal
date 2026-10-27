package md

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	cellWord      uint8 = iota // part of a word
	cellSpace                  // a wrap point that collapses with its neighbours
	cellKeep                   // a wrap point that is kept
	cellBreakHard              // a hard line break
)

// cell is one grapheme cluster with the style it is drawn in.
type cell struct {
	g  string
	si uint32 // index into the styles of the wrap
	w  uint8
	k  uint8
}

// cellsOf splits segs into cells. Adjacent segs in one style share a styles entry.
func cellsOf(segs []seg) ([]cell, []style) {
	var (
		cells  []cell
		styles []style
	)

	for _, sg := range segs {
		if sg.img != nil {
			continue
		}

		if sg.brk {
			cells = append(cells, cell{k: cellBreakHard})

			continue
		}

		if len(styles) == 0 || styles[len(styles)-1] != sg.st {
			styles = append(styles, sg.st)
		}

		si := uint32(len(styles) - 1) //nolint:gosec // one entry per seg, and a Doc is at most 4 MiB

		for s := sg.text; s != ""; {
			var (
				g string
				w int
			)

			if s[0] < 0x80 && (len(s) == 1 || s[1] < 0x80) {
				g, w = s[:1], 1
			} else {
				g, w = ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
				if g == "" {
					g, w = s[:1], 0
				}
			}

			s = s[len(g):]
			c := cell{g: g, w: uint8(min(w, 255)), si: si}

			if g == " " {
				switch sg.sp {
				case spaceBreak:
					c.k = cellSpace
				case spaceKeep:
					c.k = cellKeep
				}
			}

			cells = append(cells, c)
		}
	}

	return cells, styles
}

// lineBuilder accumulates one line's runs.
type lineBuilder struct {
	styles []style
	runs   []Run
	buf    strings.Builder
	si     uint32
	w, n   int
}

func (b *lineBuilder) put(c cell) {
	if b.buf.Len() > 0 && c.si != b.si {
		b.flushRun()
	}

	b.si = c.si
	b.buf.WriteString(c.g)
	b.w += int(c.w)
	b.n++
}

func (b *lineBuilder) flushRun() {
	if b.buf.Len() == 0 {
		return
	}

	r := b.styles[b.si].run(b.buf.String())
	b.buf.Reset()

	if n := len(b.runs); n > 0 && sameStyle(b.runs[n-1], r) {
		b.runs[n-1].Text += r.Text

		return
	}

	b.runs = append(b.runs, r)
}

// take returns the line built so far and starts the next.
func (b *lineBuilder) take() Line {
	b.flushRun()

	line := Line(b.runs)
	b.runs, b.w, b.n = nil, 0, 0

	return line
}

func sameStyle(a, b Run) bool {
	a.Text, b.Text = "", ""

	return a == b
}

// wrap lays segs out in lines no wider than width cells. It breaks at spaces and
// cuts a word wider than the width by cells; a cell wider than the width is
// replaced by an ellipsis.
func wrap(segs []seg, width int) []Line {
	cells, styles := cellsOf(segs)
	b := &lineBuilder{styles: styles}

	var (
		lines []Line
		group []cell
	)

	for i := 0; i < len(cells); {
		group = group[:0]

		for i < len(cells) && (cells[i].k == cellSpace || cells[i].k == cellKeep) {
			c := cells[i]
			i++

			if c.k == cellSpace && len(group) > 0 && group[len(group)-1].k == cellSpace {
				continue
			}

			group = append(group, c)
		}

		if i >= len(cells) {
			break
		}

		if cells[i].k == cellBreakHard {
			lines = append(lines, b.take())
			i++

			continue
		}

		j, ww := i, 0
		for j < len(cells) && cells[j].k == cellWord {
			ww += int(cells[j].w)
			j++
		}

		word := cells[i:j]
		i = j

		switch {
		case b.n == 0:
		case b.w+len(group)+ww <= width:
			for _, c := range group {
				b.put(c)
			}
		default:
			lines = append(lines, b.take())
		}

		for _, c := range word {
			if b.n > 0 && b.w+int(c.w) > width {
				lines = append(lines, b.take())
			}

			if int(c.w) > width {
				c = cell{g: "…", w: 1, si: c.si}
			}

			b.put(c)
		}
	}

	if b.n > 0 {
		lines = append(lines, b.take())
	}

	for len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}

	return lines
}

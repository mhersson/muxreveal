package md

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// maxJoin caps the bytes of one cell that spans segs, so each seg boundary costs bounded work.
const maxJoin = 64

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

// nextCluster returns the leading grapheme cluster of s and its width in cells.
// It is the one width measure of the package: wrapping, cutting, code blocks and
// table cells all count cells through it. s must not be empty.
func nextCluster(s string) (string, int) {
	if s[0] < 0x80 && (len(s) == 1 || s[1] < 0x80) {
		return s[:1], 1
	}

	g, w := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
	if g == "" {
		return s[:1], 0
	}

	return g, w
}

// cutCells returns the longest prefix of s at most n cells wide, cut between
// clusters.
func cutCells(s string, n int) string {
	w := 0

	for i := 0; i < len(s); {
		g, cw := nextCluster(s[i:])
		if w+cw > n {
			return s[:i]
		}

		w += cw
		i += len(g)
	}

	return s
}

// textWidth is the width of s in cells, the sum of its clusters' widths.
func textWidth(s string) int {
	w := 0

	for s != "" {
		g, cw := nextCluster(s)
		w += cw
		s = s[len(g):]
	}

	return w
}

// joinNext extends the cluster g that ends text seg k with the text segs that
// follow, when a style change fell inside one cluster. The cluster stays one
// cell in the style of its first byte. It returns the cluster, its width and
// where the walk resumes: whole segs before skipTo are consumed, and seg skipTo
// from byte skipBytes on.
func joinNext(segs []seg, k int, g string, w int) (string, int, int, int) {
	skipTo, skipBytes := 0, 0

	for j := k + 1; j < len(segs); j++ {
		nx := segs[j]
		if nx.img != nil || nx.brk {
			break
		}

		if nx.text == "" {
			skipTo = j + 1

			continue
		}

		if nx.text[0] < 0x80 {
			break
		}

		room := maxJoin - len(g)
		if room <= 0 {
			break
		}

		n := min(len(nx.text), room)
		for n < len(nx.text) && !utf8.RuneStart(nx.text[n]) {
			n--
		}

		ng, nw := nextCluster(g + nx.text[:n])

		used := len(ng) - len(g)
		if used <= 0 {
			break
		}

		g, w = ng, nw

		if used < len(nx.text) {
			skipTo, skipBytes = j, used

			break
		}

		skipTo = j + 1
	}

	return g, w, skipTo, skipBytes
}

// cellsOf splits segs into cells. Adjacent segs in one style share a styles entry.
func cellsOf(segs []seg) ([]cell, []style) {
	n := 0
	for _, sg := range segs {
		n += len(sg.text) + 1
	}

	var (
		cells  = make([]cell, 0, n) // one cell per byte at most, so appends never regrow
		styles []style
	)

	skipTo, skipBytes := 0, 0

	for k, sg := range segs {
		if k < skipTo {
			continue
		}

		text := sg.text
		if k == skipTo {
			text, skipBytes = text[skipBytes:], 0
		}

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

		for s := text; s != ""; {
			g, w := nextCluster(s)
			s = s[len(g):]

			if s == "" {
				g, w, skipTo, skipBytes = joinNext(segs, k, g, w)
			}

			c := cell{g: g, w: uint8(min(w, 255)), si: si} //nolint:gosec // non-negative and clamped to 255

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
	// next is the join the next line's first run carries.
	next Join
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
	if len(line) > 0 {
		line[0].join = b.next
	}

	b.runs, b.w, b.n, b.next = nil, 0, 0, JoinNone

	return line
}

// sameStyle reports whether a and b merge. A tab run never does: each tab copies as one.
func sameStyle(a, b Run) bool {
	if a.tab || b.tab {
		return false
	}

	a.Text, b.Text = "", ""

	return a == b
}

// wrap lays segs out in lines no wider than width cells. It breaks at spaces and
// cuts a word wider than the width by cells; a cell wider than the width is
// replaced by an ellipsis.
func wrap(segs []seg, width int) []Line { return wrapLead(segs, width, 0) }

// wrapLead is wrap with lead cells kept free on the first line, for a marker
// drawn there.
func wrapLead(segs []seg, width, lead int) []Line {
	cells, styles := cellsOf(segs)
	b := &lineBuilder{styles: styles}
	first := max(width-lead, 1)

	var (
		lines []Line
		group []cell
	)

	limit := func() int {
		if len(lines) == 0 {
			return first
		}

		return width
	}

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
			b.next = JoinBreak
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
		case b.w+len(group)+ww <= limit():
			for _, c := range group {
				b.put(c)
			}
		default:
			lines = append(lines, b.take())
			b.next = JoinSpace
		}

		for _, c := range word {
			if b.n > 0 && b.w+int(c.w) > limit() {
				lines = append(lines, b.take())
				b.next = JoinTight
			}

			if int(c.w) > limit() {
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

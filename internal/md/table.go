package md

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/v2/ast"
	east "github.com/yuin/goldmark/v2/extension/ast"
)

const (
	// maxTableCols is the widest grid; wider tables lay out as paragraphs.
	maxTableCols = 64
	// maxTableCells bounds the cells a table lays out, header included.
	maxTableCells = 4096
	// minColWidth is the width past which a column no longer shrinks.
	minColWidth = 6
	// unbounded is a wrap width no cell reaches.
	unbounded = 1 << 30
)

// tableCell is one cell: its segs, and its lines laid out unwrapped.
type tableCell struct {
	segs  []seg
	lines []Line
}

// tableRows collects the header and the body rows that fit the cell bound, and
// counts the body rows left out.
func (l *layouter) tableRows(t *east.Table, text Role) (aligns []east.Alignment, rows [][]tableCell, left int) {
	var header ast.Node

	for ch := t.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if _, ok := ch.(*east.TableHeader); ok {
			header = ch
		}
	}

	if header == nil {
		return nil, nil, 0
	}

	n := header.ChildCount()

	for c := header.FirstChild(); c != nil; c = c.NextSibling() {
		a := east.AlignNone
		if tc, ok := c.(*east.TableCell); ok {
			a = tc.Alignment
		}

		aligns = append(aligns, a)
	}

	rows = append(rows, l.cellRow(header, n, RoleTableHeader, true))
	budget := rowBudget(n)

	for ch := t.FirstChild(); ch != nil; ch = ch.NextSibling() {
		body, ok := ch.(*east.TableBody)
		if !ok {
			continue
		}

		for r := body.FirstChild(); r != nil; r = r.NextSibling() {
			if len(rows)-1 >= budget {
				left++

				continue
			}

			rows = append(rows, l.cellRow(r, n, text, false))
		}
	}

	return aligns, rows, left
}

// rowBudget is how many body rows fit in maxTableCells next to the header of a
// table with n columns.
func rowBudget(n int) int { return max(maxTableCells/n-1, 0) }

// moreRows appends the count of the rows a table left out.
func moreRows(out *frag, left, width int) {
	noun := " more rows"
	if left == 1 {
		noun = " more row"
	}

	out.lines = append(out.lines, fit(Line{styled(RoleFoldHint).run(strconv.Itoa(left) + noun)}, width))
}

// cellRow lays out the cells of row, padded with empty cells to n.
func (l *layouter) cellRow(row ast.Node, n int, text Role, bold bool) []tableCell {
	cells := make([]tableCell, n)
	i := 0

	for c := row.FirstChild(); c != nil && i < n; c = c.NextSibling() {
		segs := l.cellImages(restyle(l.inlines(c), text, bold))
		cells[i] = tableCell{segs: segs, lines: wrap(segs, unbounded)}
		i++
	}

	return cells
}

// cellImages replaces each image seg with its text: a grid cell cannot hold a
// picture, so the image line wraps with the cell's other text.
func (l *layouter) cellImages(segs []seg) []seg {
	hasImage := false

	for _, sg := range segs {
		if sg.img != nil {
			hasImage = true

			break
		}
	}

	if !hasImage {
		return segs
	}

	out := make([]seg, 0, len(segs)+4)

	for _, sg := range segs {
		if sg.img == nil {
			out = append(out, sg)

			continue
		}

		for _, r := range l.imageLine(sg) {
			out = append(out, seg{
				text: r.Text, sp: spaceKeep,
				st: style{role: r.Role, underline: r.Underline, link: r.Link, path: r.Path},
			})
		}
	}

	return out
}

// table appends a table as a rounded grid in at most width cells; a grid wider
// than that is cut with an ellipsis.
func (l *layouter) table(out *frag, t *east.Table, width int, text Role) {
	n := 0

	for ch := t.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if h, ok := ch.(*east.TableHeader); ok {
			n = h.ChildCount()
		}
	}

	if n == 0 {
		return
	}

	if n > maxTableCols {
		l.tableAsText(out, t, width, text, n)

		return
	}

	aligns, rows, left := l.tableRows(t, text)
	widths := make([]int, n)

	for _, row := range rows {
		for j, c := range row {
			for _, line := range c.lines {
				widths[j] = max(widths[j], lineWidth(line))
			}
		}
	}

	for j := range widths {
		widths[j] = max(widths[j], 1)
	}

	shrinkColumns(widths, width-(3*n+1))

	emit := func(line Line) { out.lines = append(out.lines, fit(line, width)) }

	emit(tableRule("╭", "┬", "╮", widths))

	for i, row := range rows {
		for _, line := range tableRowLines(row, widths, aligns, i == 0) {
			emit(line)
		}

		if i == 0 {
			emit(tableRule("├", "┼", "┤", widths))
		}
	}

	emit(tableRule("╰", "┴", "╯", widths))

	if left > 0 {
		moreRows(out, left, width)
	}
}

// shrinkColumns narrows widths until they sum to at most room: the widest
// column loses a cell at a time, the leftmost of equals first, and none goes
// below minColWidth. It computes that result directly, so a huge cell costs
// no more than a small one.
func shrinkColumns(widths []int, room int) {
	limit := max(room, minColWidth)
	sum := 0

	for i := range widths {
		widths[i] = min(widths[i], limit)
		sum += widths[i]
	}

	if sum <= room {
		return
	}

	total := func(level int) int {
		s := 0
		for _, w := range widths {
			s += min(w, level)
		}

		return s
	}

	level := limit
	for level > minColWidth && total(level) > room {
		level--
	}

	if total(level) > room {
		for i := range widths {
			widths[i] = min(widths[i], minColWidth)
		}

		return
	}

	wider := 0

	for _, w := range widths {
		if w > level {
			wider++
		}
	}

	// Columns above level meet at level+1; the leftmost of them give up one
	// more cell until the sum fits.
	down := wider - (room - total(level))

	for i, w := range widths {
		if w <= level {
			continue
		}

		if down > 0 {
			widths[i] = level
			down--
		} else {
			widths[i] = level + 1
		}
	}
}

// tableRule is a horizontal border line.
func tableRule(left, mid, right string, widths []int) Line {
	var b strings.Builder

	b.WriteString(left)

	for i, w := range widths {
		if i > 0 {
			b.WriteString(mid)
		}

		b.WriteString(strings.Repeat("─", w+2))
	}

	b.WriteString(right)

	return Line{styled(RoleTableFrame).run(b.String())}
}

// tableRowLines lays out one row: cells wrap in their columns, the shorter
// ones are blank below their text, and each line is aligned by its column.
func tableRowLines(row []tableCell, widths []int, aligns []east.Alignment, header bool) []Line {
	cols := make([][]Line, len(row))
	height := 1

	for j, c := range row {
		cols[j] = c.lines

		for _, line := range c.lines {
			if lineWidth(line) > widths[j] {
				cols[j] = wrap(c.segs, widths[j])

				break
			}
		}

		height = max(height, len(cols[j]))
	}

	frame := styled(RoleTableFrame)
	pad := styled(RoleText)

	if header {
		pad = styled(RoleTableHeader)
		pad.bold = true
	}

	lines := make([]Line, height)

	for k := range height {
		line := Line{frame.run("│ ")}

		for j, w := range widths {
			var cell Line
			if k < len(cols[j]) {
				cell = cols[j][k]
			}

			gap := max(w-lineWidth(cell), 0)
			lead := 0

			switch aligns[j] {
			case east.AlignRight:
				lead = gap
			case east.AlignCenter:
				lead = gap / 2
			}

			line = appendRun(line, pad.run(strings.Repeat(" ", lead)))

			for _, r := range cell {
				line = appendRun(line, r)
			}

			line = appendRun(line, pad.run(strings.Repeat(" ", gap-lead)))

			if j < len(widths)-1 {
				line = appendRun(line, frame.run(" │ "))
			} else {
				line = appendRun(line, frame.run(" │"))
			}
		}

		lines[k] = line
	}

	return lines
}

// tableAsText lays a table with too many columns (n, the header's cells) out as
// paragraphs: each source row from its start to the end of its line, wrapped,
// with no markdown read. Rows stop at the same cell bound as a grid.
func (l *layouter) tableAsText(out *frag, t *east.Table, width int, text Role, n int) {
	row := func(node ast.Node) {
		pos := node.Pos()
		src := l.d.src

		if pos < 0 || pos > len(src) {
			return
		}

		line := src[pos:]
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}

		out.lines = append(out.lines, wrap([]seg{{text: cleanProse(string(line)), st: styled(text)}}, width)...)
	}

	budget, left := rowBudget(n), 0

	for ch := t.FirstChild(); ch != nil; ch = ch.NextSibling() {
		switch ch.(type) {
		case *east.TableHeader:
			row(ch)
		case *east.TableBody:
			for r := ch.FirstChild(); r != nil; r = r.NextSibling() {
				if budget == 0 {
					left++

					continue
				}

				budget--

				row(r)
			}
		}
	}

	if left > 0 {
		moreRows(out, left, width)
	}
}

package ui

import (
	"bytes"

	"github.com/mhersson/muxreveal/internal/term"
)

// copyAction tells the caller what a copy-mode key asks of the app.
type copyAction int

const (
	copyNone copyAction = iota
	copyYank
	copyExit
	copySearch
	copySearchBack
)

// copyMode is a pure vim-style cursor model over a grid. top is the absolute
// first row of the view, kept so H/M/L and paging know what is visible.
type copyMode struct {
	cursor     point
	top        int
	sel        *selection
	pendingG   bool
	lastSearch string
	backward   bool
}

func newCopyMode(g grid, viewTop, viewRows int) copyMode {
	row := max(min(viewTop+max(viewRows, 1)-1, g.Rows()-1), 0)

	return copyMode{cursor: point{row, 0}, top: viewTop}
}

// viewTop scrolls the minimum needed to keep the cursor row visible.
func (m copyMode) viewTop(viewRows, current int) int {
	viewRows = max(viewRows, 1)
	switch {
	case m.cursor.row < current:
		return m.cursor.row
	case m.cursor.row > current+viewRows-1:
		return m.cursor.row - viewRows + 1
	}

	return current
}

func (m copyMode) key(g grid, key string, viewRows int) (copyMode, copyAction) {
	viewRows = max(viewRows, 1)
	rows := g.Rows()
	maxTop := max(0, rows-viewRows)
	wasG := m.pendingG
	m.pendingG = false
	c := m.cursor
	paged := false

	switch key {
	case "h":
		c.col--
	case "l":
		if cell := g.Cell(c.row, c.col); cell != nil && cell.Width >= 2 {
			c.col += cell.Width
		} else {
			c.col++
		}

		if c.col > g.Cols()-1 {
			c.col = m.cursor.col
		}
	case "j":
		c.row++
	case "k":
		c.row--
	case "0":
		c.col = 0
	case "^":
		c.col = firstNonBlank(g, c.row)
	case "$":
		c.col = lastNonBlank(g, c.row)
	case "w":
		c = wordForward(g, c)
	case "e":
		c = wordEnd(g, c)
	case "b":
		c = wordBack(g, c)
	case "G":
		c.row = rows - 1
		c.col = firstNonBlank(g, max(c.row, 0))
	case "g":
		if !wasG {
			m.pendingG = true

			return m, copyNone
		}

		c = point{0, firstNonBlank(g, 0)}
	case "H", "M", "L":
		offset := map[string]int{"H": 0, "M": (viewRows - 1) / 2, "L": viewRows - 1}[key]
		c.row = min(m.top+offset, rows-1)
		c.col = firstNonBlank(g, max(c.row, 0))
	case "ctrl+u", "ctrl+d", "ctrl+b", "ctrl+f":
		delta := max(1, viewRows/2)
		if key == "ctrl+b" || key == "ctrl+f" {
			delta = viewRows
		}

		if key == "ctrl+u" || key == "ctrl+b" {
			delta = -delta
		}

		c.row += delta
		m.top = min(max(m.top+delta, 0), maxTop)
		paged = true
	case "v", "V", "ctrl+v":
		mode := map[string]selMode{"v": selStream, "V": selLine, "ctrl+v": selBlock}[key]
		switch {
		case m.sel == nil:
			m.sel = &selection{mode: mode, anchor: m.cursor, head: m.cursor}
		case m.sel.mode == mode:
			m.sel = nil
		default:
			s := *m.sel
			s.mode = mode
			m.sel = &s
		}

		return m, copyNone
	case "y":
		return m, copyYank
	case "q", "esc":
		return m, copyExit
	case "/":
		return m, copySearch
	case "?":
		return m, copySearchBack
	case "n", "N":
		if m.lastSearch == "" {
			return m, copyNone
		}

		dir := m.backward
		next, _ := m.search(g, m.lastSearch, dir != (key == "N"))
		next.backward = dir
		next.top = next.viewTop(viewRows, next.top)

		return next, copyNone
	default:
		return m, copyNone
	}

	c = clampPoint(g, c)
	if rows > 0 {
		c = normalizeCol(g, c)
	}

	m.cursor = c
	if paged {
		m.top = min(max(m.top, 0), maxTop)
	}

	m.top = m.viewTop(viewRows, m.top)
	if m.sel != nil {
		s := *m.sel
		s.head = m.cursor
		m.sel = &s
	}

	return m, copyNone
}

// search moves to the next match strictly after (or before) the cursor,
// wrapping once. It leaves top to the caller.
func (m copyMode) search(g grid, query string, backward bool) (copyMode, bool) {
	rows := g.Rows()
	if query == "" || rows == 0 {
		return m, false
	}

	cur := point{row: ((m.cursor.row % rows) + rows) % rows, col: m.cursor.col}

	var hit point

	found := false

	if backward {
		// The last match before the cursor, else the last one in the grid.
		scanMatches(g, 0, cur.row+1, query, func(row int, cols []int) bool {
			for _, c := range cols {
				if row == cur.row && c >= cur.col {
					break
				}

				hit, found = point{row, c}, true
			}

			return true
		})

		if !found {
			scanMatches(g, cur.row, rows, query, func(row int, cols []int) bool {
				hit, found = point{row, cols[len(cols)-1]}, true

				return true
			})
		}
	} else {
		// The first match after the cursor, else the first one in the grid.
		scanMatches(g, cur.row, rows, query, func(row int, cols []int) bool {
			for _, c := range cols {
				if row == cur.row && c <= cur.col {
					continue
				}

				hit, found = point{row, c}, true

				return false
			}

			return true
		})

		if !found {
			scanMatches(g, 0, cur.row+1, query, func(row int, cols []int) bool {
				hit, found = point{row, cols[0]}, true

				return false
			})
		}
	}

	if !found {
		return m, false
	}

	m.cursor = hit

	m.lastSearch, m.backward = query, backward
	if m.sel != nil {
		s := *m.sel
		s.head = m.cursor
		m.sel = &s
	}

	return m, true
}

// textScanner is a grid that reads its history rows as text without building
// cells, which keeps a scan of a long history cheap.
type textScanner interface {
	// historyRows is the number of leading rows scanText serves.
	historyRows() int
	// scanText calls fn with each non-blank history row in [from, end), in
	// order, until fn returns false. Cells are valid only inside fn.
	scanText(from, end int, fn func(row int, cells []term.TextCell) bool)
}

// scanMatches calls fn with the first column of every plain-substring match,
// for each row of [from, end) that has one, in row order, until fn returns
// false. cols is valid only inside fn.
func scanMatches(g grid, from, end int, query string, fn func(row int, cols []int) bool) {
	mt := rowMatcher{query: []byte(query)}

	if ts, ok := g.(textScanner); ok && from < ts.historyRows() {
		hist := min(end, ts.historyRows())
		stop := false

		ts.scanText(from, hist, func(row int, cells []term.TextCell) bool {
			mt.reset()

			for col, c := range cells[:min(len(cells), g.Cols())] {
				if c.Width == 0 && col > 0 && cells[col-1].Width >= 2 {
					continue
				}

				mt.add(col, c.Content)
			}

			if cols := mt.matches(); len(cols) > 0 && !fn(row, cols) {
				stop = true

				return false
			}

			return true
		})

		if stop {
			return
		}

		from = hist
	}

	for row := from; row < end; row++ {
		mt.reset()

		prevWidth := 0

		for col := 0; col < g.Cols(); col++ {
			c := g.Cell(row, col)
			if c == nil {
				prevWidth = 0

				continue
			}

			w := c.Width
			if w == 0 && col > 0 && prevWidth >= 2 {
				prevWidth = 0

				continue
			}

			prevWidth = w

			mt.add(col, []byte(c.Content))
		}

		if cols := mt.matches(); len(cols) > 0 && !fn(row, cols) {
			return
		}
	}
}

// rowMatcher finds a query in one row's text. Its buffers are reused from row
// to row.
type rowMatcher struct {
	query      []byte
	text       []byte
	offs, cols []int
	out        []int
}

func (r *rowMatcher) reset() { r.text, r.offs, r.cols = r.text[:0], r.offs[:0], r.cols[:0] }

// add appends the cell at col; an empty cell reads as a space.
func (r *rowMatcher) add(col int, content []byte) {
	r.offs = append(r.offs, len(r.text))

	r.cols = append(r.cols, col)
	if len(content) == 0 {
		r.text = append(r.text, ' ')
	} else {
		r.text = append(r.text, content...)
	}
}

// matches returns the column of every cell the query starts at.
func (r *rowMatcher) matches() []int {
	r.out = r.out[:0]
	if !bytes.Contains(r.text, r.query) {
		return r.out
	}

	for i, off := range r.offs {
		if bytes.HasPrefix(r.text[off:], r.query) {
			r.out = append(r.out, r.cols[i])
		}
	}

	return r.out
}

// normalizeCol moves a cursor off a wide char's continuation cell.
func normalizeCol(g grid, p point) point {
	if isContinuation(g, p.row, p.col) {
		p.col--
	}

	return p
}

func firstNonBlank(g grid, row int) int {
	for col := 0; col < g.Cols(); col++ {
		if cellClass(g, row, col) != classBlank {
			return col
		}
	}

	return 0
}

func lastNonBlank(g grid, row int) int {
	for col := g.Cols() - 1; col >= 0; col-- {
		if cellClass(g, row, col) != classBlank {
			return col
		}
	}

	return 0
}

func nextPos(g grid, p point) (point, bool) {
	if p.col+1 < g.Cols() {
		return point{p.row, p.col + 1}, true
	}

	if p.row+1 < g.Rows() {
		return point{p.row + 1, 0}, true
	}

	return p, false
}

func prevPos(g grid, p point) (point, bool) {
	if p.col > 0 {
		return point{p.row, p.col - 1}, true
	}

	if p.row > 0 {
		return point{p.row - 1, g.Cols() - 1}, true
	}

	return p, false
}

func classAt(g grid, p point) int { return cellClass(g, p.row, p.col) }

// runEnd returns the last column of the run containing p, within its row.
func runEnd(g grid, p point) point {
	cls := classAt(g, p)
	for p.col+1 < g.Cols() && cellClass(g, p.row, p.col+1) == cls {
		p.col++
	}

	return p
}

// runStart returns the first column of the run containing p, within its row.
func runStart(g grid, p point) point {
	cls := classAt(g, p)
	for p.col > 0 && cellClass(g, p.row, p.col-1) == cls {
		p.col--
	}

	return p
}

// wordForward is vim's w; with no later run it settles at the end of the
// current one.
func wordForward(g grid, p point) point {
	if g.Rows() == 0 {
		return p
	}

	cur := p
	if classAt(g, p) != classBlank {
		p = runEnd(g, p)
	}

	for {
		next, ok := nextPos(g, p)
		if !ok {
			if classAt(g, cur) == classBlank {
				return cur
			}

			return runEnd(g, cur)
		}

		p = next
		if classAt(g, p) != classBlank {
			return p
		}
	}
}

// wordEnd is vim's e.
func wordEnd(g grid, p point) point {
	if g.Rows() == 0 {
		return p
	}

	q := p
	for {
		next, ok := nextPos(g, q)
		if !ok {
			return p
		}

		q = next
		if classAt(g, q) != classBlank {
			return runEnd(g, q)
		}
	}
}

// wordBack is vim's b.
func wordBack(g grid, p point) point {
	if g.Rows() == 0 {
		return p
	}

	q := p
	for {
		prev, ok := prevPos(g, q)
		if !ok {
			return p
		}

		q = prev
		if classAt(g, q) != classBlank {
			return runStart(g, q)
		}
	}
}

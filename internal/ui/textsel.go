package ui

import (
	"github.com/charmbracelet/x/ansi"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/md"
)

// textGrid is the grid of a text view's lines, read from the copy model so
// word and line selection follow what a copy takes.
type textGrid struct {
	lines      func(row int) md.LineText
	rows, cols int
}

func (g textGrid) Rows() int { return g.rows }
func (g textGrid) Cols() int { return g.cols }

// Cell reads one cell: a text piece's grapheme, one character of class other
// for a whole piece (blank for a tab), and blank anywhere else.
func (g textGrid) Cell(row, col int) *uv.Cell {
	if row < 0 || row >= g.rows || col < 0 || col >= g.cols {
		return nil
	}

	blank := &uv.Cell{Content: " ", Width: 1}

	for _, p := range g.lines(row).Pieces {
		if col < p.Col || col >= p.Col+max(p.Cells, 1) {
			continue
		}

		if p.Whole {
			if p.Text == "\t" {
				return blank
			}

			return &uv.Cell{Content: "*", Width: 1}
		}

		at := p.Col

		for s := p.Text; s != ""; {
			cl, w, rest := cluster(s)
			if col >= at && col < at+max(w, 1) {
				if col > at {
					return &uv.Cell{Width: 0}
				}

				return &uv.Cell{Content: cl, Width: w}
			}

			at += w
			s = rest
		}
	}

	return blank
}

// Wrapped reports whether the line below continues row.
func (g textGrid) Wrapped(row int) bool { return g.lines(row+1).Join != md.JoinNone }

// cluster splits the first grapheme cluster off s.
func cluster(s string) (cl string, w int, rest string) {
	cl, w = ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
	if cl == "" {
		return s[:1], 0, s[1:]
	}

	return cl, w, s[len(cl):]
}

// textCopy is the text s selects over lines, read once per selected row.
func textCopy(lines func(row int) md.LineText, s selection, cols int) string {
	first, last := s.rowBounds()
	first = max(first, 0)

	if last < first {
		return ""
	}

	texts := make([]md.LineText, 0, last-first+1)

	for row := first; row <= last; row++ {
		texts = append(texts, lines(row))
	}

	return copyRows(texts, first, s, cols)
}

// copyRows is the text s selects over texts, where texts[i] is the copy text
// of row from+i.
func copyRows(texts []md.LineText, from int, s selection, cols int) string {
	picks := make([]md.CellRange, len(texts))

	for i := range texts {
		picks[i] = md.CellRange{From: 1, To: 0}

		if a, b, ok := s.rowSpan(from+i, cols); ok {
			picks[i] = md.CellRange{From: a, To: b}
		}
	}

	return md.CopyLines(texts, picks, s.mode == selBlock)
}

// textPaint is the cells to paint on a row of lt with cells from..to picked:
// the picked cells of its pieces, a whole piece in full. A picture's rows give
// none.
func textPaint(lt md.LineText, from, to int) [][2]int {
	if lt.Picture {
		return nil
	}

	var out [][2]int

	add := func(a, b int) {
		if n := len(out); n > 0 && out[n-1][1]+1 >= a {
			out[n-1][1] = max(out[n-1][1], b)

			return
		}

		out = append(out, [2]int{a, b})
	}

	for _, p := range lt.Pieces {
		end := p.Col + max(p.Cells, 1) - 1
		if from > to || from > end || to < p.Col {
			continue
		}

		if p.Whole {
			add(p.Col, end)

			continue
		}

		at := p.Col

		for s := p.Text; s != ""; {
			_, w, rest := cluster(s)

			if at <= to && at+max(w, 1)-1 >= from {
				add(at, at+max(w, 1)-1)
			}

			at += w
			s = rest
		}
	}

	return out
}

// textRev is what a text selection's text was read against.
type textRev struct {
	doc    mdSource
	gen, w int
	gw     int // the diff column's number width, which moves the code
	split  bool
}

// textSelection is the one selection when it is not a pane's: the view that
// holds it, in the tab whose drawer shows it, or "" for the plan or
// conversation overlay. Exactly one of view and rows is set.
type textSelection struct {
	tab  core.TabID
	view *mdView
	rows *reviewView
	lay  drawerLayout // the layout rows' columns are in
	side int          // the side of side by side rows the selection is on
	rev  textRev
	held heldText // the selected rows' copy text; a reload's compare reads it as the text before
}

// heldText is the copy text of rows from to from+len(lines)-1, read against rev
// on side at cols, and the clamped selection it was brought up to.
type heldText struct {
	rev        textRev
	side, cols int
	from       int
	lines      []md.LineText
	sel        selection
}

// text is what the held selection copies.
func (h heldText) text() string { return copyRows(h.lines, h.from, h.sel, h.cols) }

// grid is the holder's selection grid.
func (ts *textSelection) grid() textGrid {
	switch {
	case ts.view != nil:
		return ts.view.grid()
	case ts.rows != nil:
		return ts.rows.selGrid()
	}

	return textGrid{}
}

// tracked is ts.held brought up to the holder's selection. Rows held under the
// same rev, side and width are kept, and only the rows the selection gained are
// read. ts.held is left as it was: rows are appended only past its end.
func (a *App) tracked(ts *textSelection) heldText {
	g, sp := ts.grid(), ts.selection()
	if sp == nil || g.rows == 0 {
		return heldText{}
	}

	s := *sp
	s.anchor, s.head = clampPoint(g, s.anchor), clampPoint(g, s.head)
	first, last := s.rowBounds()
	rev := a.revOf(ts) // after the grid, which can lay a view's document out again
	h := ts.held
	end := h.from + len(h.lines)

	if h.rev != rev || h.side != ts.side || h.cols != g.cols || last < h.from || first >= end {
		h.lines, h.from, end = nil, first, first
	}

	if first > h.from {
		h.lines, h.from = h.lines[first-h.from:], first
	}

	if last < end-1 {
		h.lines, end = h.lines[:last-h.from+1], last+1
	}

	if first < h.from {
		above := make([]md.LineText, 0, end-first)
		for r := first; r < h.from; r++ {
			above = append(above, g.lines(r))
		}

		h.lines, h.from = append(above, h.lines...), first
	}

	for r := end; r <= last; r++ {
		h.lines = append(h.lines, g.lines(r))
	}

	h.rev, h.side, h.cols, h.sel = rev, ts.side, g.cols, s

	return h
}

// revOf is the state of ts's holder now.
func (a *App) revOf(ts *textSelection) textRev {
	if ts.rows != nil {
		r := textRev{gen: ts.rows.rowsRev, w: ts.rev.w, gw: ts.rev.gw, split: ts.rev.split}
		if lay, ok := a.rowsLayout(ts.tab, ts.rows); ok {
			r.w, r.gw, r.split = lay.diffW, lay.gw, lay.split
		}

		return r
	}

	if ts.view != nil {
		r := textRev{doc: ts.view.doc, w: ts.view.w}
		if ts.view.doc != nil {
			r.gen = ts.view.doc.gen()
		}

		return r
	}

	return textRev{}
}

// setSel makes s the holder's selection; lay is the layout of rows' columns.
func (ts *textSelection) setSel(s selection, lay drawerLayout) {
	switch {
	case ts.view != nil:
		ts.view.sel = &s
	case ts.rows != nil:
		ts.rows.sel, ts.rows.selLay, ts.rows.selSide = &s, lay, ts.side
	}
}

// drop clears the holder's selection.
func (ts *textSelection) drop() {
	switch {
	case ts.view != nil:
		ts.view.sel = nil
	case ts.rows != nil:
		ts.rows.sel = nil
	}
}

// selection is the holder's selection, nil when none.
func (ts *textSelection) selection() *selection {
	switch {
	case ts.view != nil:
		return ts.view.sel
	case ts.rows != nil:
		return ts.rows.sel
	}

	return nil
}

// has reports whether the holder still holds a selection.
func (ts *textSelection) has() bool {
	return (ts.view != nil && ts.view.sel != nil) || (ts.rows != nil && ts.rows.sel != nil)
}

// rowsLayout is the layout the drawer of tab draws rows in, ok only while that
// tab shows.
func (a *App) rowsLayout(tab core.TabID, v *reviewView) (drawerLayout, bool) {
	st := a.store.State()
	if st.Tab().ID != tab {
		return drawerLayout{}, false
	}

	box, ok := a.drawerBox(st)
	if !ok {
		return drawerLayout{}, false
	}

	return a.drawerLayoutOf(box, v, drawerTree(st)), true
}

// setTextSel makes s ts's holder's selection, the app's only one. The press
// that is making it stays held.
func (a *App) setTextSel(ts *textSelection, s selection) {
	held := a.mouse.text

	a.sel = nil
	a.clearTextSel()

	a.mouse.text = held

	ts.setSel(s, ts.lay)
	ts.held = a.tracked(ts)
	ts.rev = a.revOf(ts)
	a.text = ts
}

// clearTextSel drops the text selection and its holder's. A press still held
// on it ends, so a later motion or release cannot bring it back.
func (a *App) clearTextSel() {
	ts := a.text
	if ts == nil {
		return
	}

	ts.drop()

	a.text = nil
	a.mouse.text = nil
}

// clearSelections drops the pane's selection and the text one.
func (a *App) clearSelections() {
	a.sel = nil
	a.clearTextSel()
}

// textSelected is the text of the text selection, "" when none. It keeps none
// of what it reads, so a reload's compare still sees the text held before it.
func (a *App) textSelected() string {
	if a.text == nil {
		return ""
	}

	return a.tracked(a.text).text()
}

// textHolds reports whether ts's holder is still the view that shows.
func (a *App) textHolds(ts *textSelection) bool {
	switch {
	case ts.view != nil && ts.tab == "":
		if a.conv != nil && ts.view == &a.conv.view {
			return true
		}

		return a.plan != nil && a.plan.shown() == ts.view
	case ts.view != nil:
		v := a.drawer.views[ts.tab]

		return v != nil && v.page() != nil && &v.page().view == ts.view
	case ts.rows != nil:
		// A page pushed over the rows clears it, whichever way it was opened.
		return a.drawer.views[ts.tab] == ts.rows && ts.rows.page() == nil
	}

	return false
}

// textSelAfterUpdate drops the text selection when its holder is gone or its
// width changed, and when a new document or layout changed the selected text.
// It runs during a held drag too: a redraw that leaves the text alone lets the
// gesture go on, and one that changes it ends the gesture.
func (a *App) textSelAfterUpdate() {
	ts := a.text
	if ts == nil {
		return
	}

	if !a.textHolds(ts) || !ts.has() {
		a.clearTextSel()

		return
	}

	// A hidden tab's drawer reloads when it shows, and the text is compared then.
	if ts.rows != nil {
		if _, shown := a.rowsLayout(ts.tab, ts.rows); !shown {
			return
		}
	}

	cur := a.revOf(ts)
	if cur.w != ts.rev.w || cur.gw != ts.rev.gw || cur.split != ts.rev.split {
		a.clearTextSel()

		return
	}

	if cur.doc == ts.rev.doc && cur.gen == ts.rev.gen {
		return
	}

	held := a.tracked(ts)
	if held.text() != ts.held.text() {
		a.clearTextSel()

		return
	}

	ts.held, ts.rev = held, cur

	// A reload moves the selection's ends with their rows: the drag goes on
	// from where its anchor is now.
	if pr := a.mouse.text; pr != nil && pr.dragging {
		pr.anchor = ts.selection().anchor
	}
}

package ui

import (
	"image"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
)

// textPress is the left button held down on a text host. tab is "" for the
// plan overlay.
type textPress struct {
	tab                  core.TabID
	x, y                 int // screen cell of the press
	anchor               point
	alt, dragging, multi bool
	// line is the page line or diff row a drawer press landed on, -1 for none;
	// a click moves the cursor there on its release.
	line int
	// side is the side of a side by side row the press landed on.
	side int
	// px, py are the pointer's last cell; seq is the edge-scroll tick the press
	// waits for, 0 when none runs.
	px, py, seq int
}

// dragScrollEvery is the time between rows scrolled while a drag is held past
// the text.
const dragScrollEvery = 50 * time.Millisecond

// dragScrollMsg is an edge-scroll tick of the press it was started for.
type dragScrollMsg struct{ seq int }

// textHost is a text view on screen: the cells it draws in and what a cell
// of them reads. The overlay's host has view; the drawer's have rows.
type textHost struct {
	view *mdView
	rows *reviewView
	lay  drawerLayout // the layout of rows' cells
	page *mdPage
	tab  core.TabID
	rect image.Rectangle
	top  int
	// sticky is the row a pinned first line stands for, -1 for none.
	sticky int
	// minCol and maxCol bound the columns a selection reaches.
	minCol, maxCol int
	// side is the side of side by side rows the host reads, 0 when unified.
	side int
}

// at is the point under screen cell (x, y), clamped into the host's cells.
func (h textHost) at(x, y int) point {
	dx := min(max(x-h.rect.Min.X, 0), max(h.rect.Dx()-1, 0))
	dy := min(max(y-h.rect.Min.Y, 0), max(h.rect.Dy()-1, 0))
	row := h.top + dy

	if dy == 0 && h.sticky >= 0 && y >= h.rect.Min.Y {
		row = h.sticky
	}

	return point{row: row, col: min(max(dx, h.minCol), h.maxCol)}
}

// onSide is h limited to one side of side by side rows: the columns a selection
// reaches are that side's.
func (h textHost) onSide(side int) textHost {
	if h.rows == nil || !h.lay.split {
		return h
	}

	lo, hi := h.lay.splitCols(side)
	h.side, h.minCol, h.maxCol = side, max(h.minCol, lo), min(h.maxCol, hi)

	return h
}

// grid is the host's text as a selection grid.
func (h textHost) grid() grid {
	switch {
	case h.view != nil:
		return h.view.grid()
	case h.rows != nil:
		return h.rows.gridIn(h.lay, h.side)
	}

	return textGrid{}
}

// holder is the selection holder for the host.
func (h textHost) holder() *textSelection {
	return &textSelection{tab: h.tab, view: h.view, rows: h.rows, lay: h.lay, side: h.side}
}

// planHost is the plan overlay's host while the overlay takes the pointer.
func (a *App) planHost() (textHost, bool) {
	pv := a.plan
	if pv == nil || pv.chooser != nil || a.confirm != nil || a.prompt != nil || a.copy != nil ||
		a.overlay != nil || a.composeShown() || a.merge != nil {
		return textHost{}, false
	}

	a.sizePlan()

	rect, ok := planDocRect(a.width, a.height)
	if !ok {
		return textHost{}, false
	}

	v := pv.shown()

	return textHost{view: v, rect: rect, top: v.top, sticky: -1, maxCol: max(rect.Dx()-1, 0)}, true
}

// pageHost is the active tab's drawer page as a host while the drawer takes
// the pointer: the cells of the diff column's rows.
func (a *App) pageHost() (textHost, bool) {
	if !a.drawersOpen || a.modalOpen() || a.pending || a.drawer.closing {
		return textHost{}, false
	}

	st := a.store.State()

	r, shown := a.drawerRect(st)
	if !shown {
		return textHost{}, false
	}

	v, p := a.activePage()
	if p == nil {
		return textHost{}, false
	}

	box, _ := a.drawerFull(st)
	lay := a.drawerLayoutOf(box, v, false)
	w := min(lay.diffW, r.W-2)

	if w <= 0 || lay.diffH <= 0 {
		return textHost{}, false
	}

	rect := image.Rect(r.X+1, r.Y+1, r.X+1+w, r.Y+1+lay.diffH)

	return textHost{
		view: &p.view, page: p, tab: st.Tab().ID, rect: rect, top: p.view.top,
		sticky: -1, maxCol: w - 1,
	}, true
}

// rowsHost is the active tab's diff rows as a host while the drawer takes the
// pointer: the diff column under the pull request header. The tree, the header
// and the hint row are not text.
func (a *App) rowsHost() (textHost, bool) {
	if !a.drawersOpen || a.modalOpen() || a.pending || a.drawer.closing {
		return textHost{}, false
	}

	st := a.store.State()

	r, shown := a.drawerRect(st)
	if !shown {
		return textHost{}, false
	}

	tab := st.Tab().ID

	v := a.drawer.views[tab]
	if v == nil || v.page() != nil {
		return textHost{}, false
	}

	box, _ := a.drawerFull(st)
	lay := a.drawerLayoutOf(box, v, drawerTree(st))

	x := r.X + 1
	if lay.treeW > 0 {
		x += lay.treeW + 1
	}

	w := min(lay.diffW, r.X+r.W-1-x)

	if w <= 0 || lay.diffH <= 0 {
		return textHost{}, false
	}

	top := v.viewTop(lay.diffH)
	sticky := -1

	if i, ok := (&drawCtx{v: v, top: top}).stickyHeader(); ok {
		sticky = i
	}

	y := r.Y + 1 + lay.headH

	return textHost{
		rows: v, lay: lay, tab: tab, rect: image.Rect(x, y, x+w, y+lay.diffH), top: top,
		sticky: sticky, maxCol: w - 1,
	}, true
}

// drawerHost is the drawer's text host: its page, or its diff rows.
func (a *App) drawerHost() (textHost, bool) {
	if h, ok := a.pageHost(); ok {
		return h, true
	}

	return a.rowsHost()
}

// textHostAt is the host whose cells hold (x, y).
func (a *App) textHostAt(x, y int) (textHost, bool) {
	pt := image.Point{X: x, Y: y}

	if h, ok := a.planHost(); ok && pt.In(h.rect) {
		return h, true
	}

	if h, ok := a.drawerHost(); ok && pt.In(h.rect) {
		return h, true
	}

	return textHost{}, false
}

// textHostOf is the host a press began on, wherever the pointer is now.
func (a *App) textHostOf(pr *textPress) (textHost, bool) {
	if pr.tab == "" {
		return a.planHost()
	}

	if h, ok := a.drawerHost(); ok && h.tab == pr.tab {
		return h.onSide(pr.side), true
	}

	return textHost{}, false
}

// textClick gives a left press over a text host to it.
func (a *App) textClick(m tea.Mouse) bool {
	if m.Button != tea.MouseLeft {
		return false
	}

	h, ok := a.textHostAt(m.X, m.Y)
	if !ok {
		return false
	}

	a.releaseForwarded()
	a.endPress()
	a.textPressAt(h, m)

	if h.page != nil || h.rows != nil {
		a.drawerTake()
	}

	return true
}

// textPressAt starts a gesture on h: a double or triple click selects its word
// or line, and any other press clears the selection and waits for a drag.
func (a *App) textPressAt(h textHost, m tea.Mouse) {
	n := a.countClick(m.X, m.Y)
	pr := &textPress{tab: h.tab, x: m.X, y: m.Y, alt: m.Mod.Contains(tea.ModAlt), multi: n > 1, line: -1}

	if h.rows != nil && h.lay.split {
		pr.side = h.lay.splitSide(m.X - h.rect.Min.X)
		h = h.onSide(pr.side)
	}

	a.clearSelections()

	g := h.grid()

	if line := h.at(m.X, m.Y).row; (h.page != nil || h.rows != nil) && line < g.Rows() {
		pr.line = line
	}

	if g.Rows() > 0 {
		pr.anchor = clampPoint(g, h.at(m.X, m.Y))

		switch n {
		case 2:
			a.setTextSel(h.holder(), wordAt(g, pr.anchor))
		case 3:
			a.setTextSel(h.holder(), lineAt(g, pr.anchor))
		}
	}

	a.mouse.text = pr
}

// textDrag extends the press's selection to the pointer, clamped to the host's
// cells. The first move off the press cell starts the selection.
func (a *App) textDrag(pr *textPress, x, y int) tea.Cmd {
	pr.px, pr.py = x, y

	h, ok := a.textHostOf(pr)
	if !ok {
		a.mouse.text = nil

		return nil
	}

	if !pr.dragging && x == pr.x && y == pr.y {
		return nil
	}

	if !a.extendTextSel(pr, h, x, y) {
		return nil
	}

	if h.edge(y) == 0 {
		pr.seq = 0

		return nil
	}

	if pr.seq != 0 {
		return nil
	}

	a.mouse.seq++
	pr.seq = a.mouse.seq

	return dragScrollTick(pr.seq)
}

func dragScrollTick(seq int) tea.Cmd {
	return tea.Tick(dragScrollEvery, func(time.Time) tea.Msg { return dragScrollMsg{seq: seq} })
}

// edge is -1 when screen row y lies above the host's first text row, 1 when
// below its last and 0 over the text.
func (h textHost) edge(y int) int {
	switch {
	case y < h.rect.Min.Y:
		return -1
	case y >= h.rect.Max.Y:
		return 1
	}

	return 0
}

// dragScroll scrolls the held press's host a row toward the pointer, which is
// past its text, and puts the selection's head on the edge row.
func (a *App) dragScroll(msg dragScrollMsg) tea.Cmd {
	pr := a.mouse.text
	if pr == nil || pr.seq == 0 || pr.seq != msg.seq {
		return nil
	}

	h, ok := a.textHostOf(pr)
	if !ok {
		pr.seq = 0

		return nil
	}

	dir := h.edge(pr.py)
	if dir == 0 {
		pr.seq = 0

		return nil
	}

	before := h.top

	switch {
	case h.rows != nil:
		a.scrollRows(h.rows, dir)
	case h.page != nil:
		h.page.scroll(dir)
	case h.view != nil:
		h.view.scroll(dir)
	}

	h, ok = a.textHostOf(pr)
	if !ok {
		a.mouse.text = nil

		return nil
	}

	a.extendTextSel(pr, h, pr.px, pr.py)

	if h.top == before {
		pr.seq = 0

		return nil
	}

	return dragScrollTick(pr.seq)
}

// extendTextSel moves the press's selection head to the pointer's cell, clamped
// to h's cells, and reports whether the host has any text.
func (a *App) extendTextSel(pr *textPress, h textHost, x, y int) bool {
	g := h.grid()
	if g.Rows() == 0 {
		return false
	}

	pr.dragging = true

	mode := selStream
	if pr.alt {
		mode = selBlock
	}

	s := selection{mode: mode, anchor: pr.anchor, head: clampPoint(g, h.at(x, y))}

	ts := a.text
	if ts == nil || ts.view != h.view || ts.rows != h.rows || ts.tab != h.tab {
		a.setTextSel(h.holder(), s)

		return true
	}

	ts.side = h.side

	// The text read at the press stays the one the release compares with when
	// the doc changed under the drag.
	same := a.revOf(ts) == ts.rev

	ts.setSel(s, h.lay)

	if same {
		ts.shown = ts.text()
	}

	return true
}

// textRelease ends the press. A drag or multi-click then copies what the
// selection still covers, unless a redraw during the gesture changed it.
func (a *App) textRelease(pr *textPress, m tea.Mouse) tea.Cmd {
	a.mouse.text = nil

	if !pr.dragging && (m.X != pr.x || m.Y != pr.y) {
		a.textDrag(pr, m.X, m.Y)
	}

	if !pr.dragging && !pr.multi {
		a.pageClick(pr, m)

		return nil
	}

	a.textSelAfterUpdate()

	if a.text == nil {
		return nil
	}

	text := a.textSelected()

	if a.copyOnSelect {
		a.clearTextSel()

		return a.clip.copy(text)
	}

	return a.clip.copyPrimary(text)
}

// pageClick moves the cursor of a page or of the diff to the line a press
// landed on when the button came up on the cell it went down on.
func (a *App) pageClick(pr *textPress, m tea.Mouse) {
	if pr.line < 0 || m.X != pr.x || m.Y != pr.y {
		return
	}

	h, ok := a.textHostOf(pr)
	if !ok {
		return
	}

	switch {
	case h.rows != nil:
		a.reviewGo(h.rows, pr.line)
	case h.page != nil:
		h.view.cursor = pr.line
		h.view.clamp()
		h.page.rev++
	}
}

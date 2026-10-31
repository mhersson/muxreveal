package ui

import (
	"image"

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
	// line is the page line a drawer press landed on, -1 for none; a click
	// moves the cursor there on its release.
	line int
}

// textHost is a text view on screen: the cells it draws in and what a cell
// of them reads. The overlay's host has view; the drawer's have rows.
type textHost struct {
	view *mdView
	rows *reviewView
	page *mdPage
	tab  core.TabID
	rect image.Rectangle
	top  int
	// sticky is the row a pinned first line stands for, -1 for none.
	sticky int
	// minCol and maxCol bound the columns a selection reaches.
	minCol, maxCol int
}

// at is the point under screen cell (x, y), clamped into the host's cells.
func (h textHost) at(x, y int) point {
	dx := min(max(x-h.rect.Min.X, 0), max(h.rect.Dx()-1, 0))
	dy := min(max(y-h.rect.Min.Y, 0), max(h.rect.Dy()-1, 0))
	row := h.top + dy

	if dy == 0 && h.sticky >= 0 {
		row = h.sticky
	}

	return point{row: row, col: min(max(dx, h.minCol), h.maxCol)}
}

// grid is the host's text as a selection grid.
func (h textHost) grid() grid {
	if h.view != nil {
		return h.view.grid()
	}

	return textGrid{}
}

// holder is the selection holder for the host.
func (h textHost) holder() *textSelection {
	return &textSelection{tab: h.tab, view: h.view, rows: h.rows}
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

// textHostAt is the host whose cells hold (x, y).
func (a *App) textHostAt(x, y int) (textHost, bool) {
	pt := image.Point{X: x, Y: y}

	if h, ok := a.planHost(); ok && pt.In(h.rect) {
		return h, true
	}

	if h, ok := a.pageHost(); ok && pt.In(h.rect) {
		return h, true
	}

	return textHost{}, false
}

// textHostOf is the host a press began on, wherever the pointer is now.
func (a *App) textHostOf(pr *textPress) (textHost, bool) {
	if pr.tab == "" {
		return a.planHost()
	}

	if h, ok := a.pageHost(); ok && h.tab == pr.tab {
		return h, true
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

	if h.page != nil {
		a.drawerTake()
	}

	return true
}

// textPressAt starts a gesture on h: a double or triple click selects its word
// or line, and any other press clears the selection and waits for a drag.
func (a *App) textPressAt(h textHost, m tea.Mouse) {
	n := a.countClick(m.X, m.Y)
	pr := &textPress{tab: h.tab, x: m.X, y: m.Y, alt: m.Mod.Contains(tea.ModAlt), multi: n > 1, line: -1}

	a.clearSelections()

	g := h.grid()

	if line := h.top + m.Y - h.rect.Min.Y; h.page != nil && line < g.Rows() {
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
func (a *App) textDrag(pr *textPress, x, y int) {
	h, ok := a.textHostOf(pr)
	if !ok {
		a.mouse.text = nil

		return
	}

	if !pr.dragging && x == pr.x && y == pr.y {
		return
	}

	g := h.grid()
	if g.Rows() == 0 {
		return
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

		return
	}

	// The text read at the press stays the one the release compares with when
	// the doc changed under the drag.
	same := a.revOf(ts) == ts.rev

	ts.view.sel = &s

	if same {
		ts.shown = ts.text()
	}
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

// pageClick moves a page's cursor to the line a press landed on when the
// button came up on the cell it went down on.
func (a *App) pageClick(pr *textPress, m tea.Mouse) {
	if pr.line < 0 || m.X != pr.x || m.Y != pr.y {
		return
	}

	h, ok := a.textHostOf(pr)
	if !ok || h.page == nil {
		return
	}

	h.view.cursor = pr.line
	h.view.clamp()
	h.page.rev++
}

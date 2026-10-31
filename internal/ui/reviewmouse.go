package ui

// wheelRows is how many rows one wheel notch scrolls the drawer.
const wheelRows = 3

// inDrawer reports whether the cell lies on the part of the drawer on screen.
// With no drawer open in any tab there is none and no state to read.
func (a *App) inDrawer(x, y int) bool {
	if !a.drawersOpen {
		return false
	}

	r, ok := a.drawerRect(a.store.State())

	return ok && x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// drawerClick gives the drawer the keyboard; on a tree file it jumps to it. The
// text of a page or of the diff goes to textClick. The tree's scroll depends on
// who held the keyboard when it was drawn, so that is read before the diff takes
// it.
func (a *App) drawerClick(x, y int) {
	st := a.store.State()
	treeWas := a.drawer.focused && a.drawer.tree

	defer a.drawerTake()

	r, shown := a.drawerRect(st)
	box, _ := a.drawerBox(st)
	v := a.drawer.views[st.Tab().ID]

	if !shown || v == nil {
		return
	}

	lay := a.drawerLayoutOf(box, v, drawerTree(st))
	ix, iy := x-r.X-1, y-r.Y-1

	if iy < 0 || iy >= lay.bodyH || ix < 0 || ix >= lay.innerW {
		return
	}

	if v.page() != nil || lay.treeW == 0 || ix >= lay.treeW {
		return
	}

	top := v.viewTop(lay.diffH)
	d := &drawCtx{v: v, c: v.derived(), lay: lay, top: top, tree: treeWas}

	// The tree's rows start after the mode row, the totals row and a blank one.
	if i := d.treeTop(lay.bodyH-3) + iy - 3; iy >= 3 && i >= 0 && i < len(d.c.tree) && !d.c.tree[i].dir {
		a.reviewJumpFile(v, d.c.tree[i].file)
	}
}

// drawerWheel scrolls the drawer's diff 3 rows per notch (dir 1 is down); the
// cursor follows when it would leave the view.
func (a *App) drawerWheel(dir int) {
	v := a.drawer.views[a.store.State().Tab().ID]
	if v != nil && v.page() != nil {
		if _, p := a.activePage(); p != nil {
			p.scroll(dir * wheelRows)
		}

		return
	}

	if v == nil || len(v.rows) == 0 {
		return
	}

	a.scrollRows(v, dir*wheelRows)
}

// scrollRows scrolls the diff rows by n and keeps the cursor in view.
func (a *App) scrollRows(v *reviewView, n int) {
	if len(v.rows) == 0 {
		return
	}

	h := a.reviewBodyH(v)

	v.top = min(max(v.viewTop(h)+n, 0), max(len(v.rows)-h, 0))

	// The cursor stays while a row of its line shows. Otherwise it takes the
	// first line that starts in the view, or the last row's line when it was
	// below.
	v.cursor = min(max(v.cursor, 0), len(v.rows)-1)
	start, end := v.lineStart(v.cursor), v.lineEnd(v.cursor)
	bottom := min(v.top+h, len(v.rows)) - 1

	switch {
	case end > v.top && start <= bottom:
	case start > bottom:
		v.cursor = v.lineStart(max(bottom, 0))
	default:
		first := v.top
		if v.rows[first].part > 0 {
			first = v.lineEnd(first)
		}

		if first > bottom {
			first = v.lineStart(v.top)
		}

		v.cursor = first
	}
}

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

// drawerClick gives the drawer the keyboard; on a diff row it moves the cursor
// there and on a tree file it jumps to it. The tree's scroll depends on who held
// the keyboard when it was drawn, so that is read before the diff takes it.
func (a *App) drawerClick(x, y int) {
	st := a.store.State()
	treeWas := a.drawer.focused && a.drawer.tree

	defer a.drawerTake()

	r, shown := a.drawerRect(st)
	box, _ := a.drawerFull(st)
	v := a.drawer.views[st.Tab().ID]

	if !shown || v == nil {
		return
	}

	lay := a.drawerLayoutOf(box, v, drawerTree(st))
	ix, iy := x-r.X-1, y-r.Y-1

	if iy < 0 || iy >= lay.bodyH || ix < 0 || ix >= lay.innerW {
		return
	}

	top := v.viewTop(lay.bodyH)
	d := &drawCtx{v: v, c: v.derived(), lay: lay, top: top, tree: treeWas}

	switch {
	case lay.treeW > 0 && ix < lay.treeW:
		// The tree's rows start after the mode row, the totals row and a blank one.
		if i := d.treeTop(lay.bodyH-3) + iy - 3; iy >= 3 && i >= 0 && i < len(d.c.tree) && !d.c.tree[i].dir {
			a.reviewJumpFile(v, d.c.tree[i].file)
		}
	case lay.treeW == 0 || ix > lay.treeW:
		i := top + iy

		if sticky, ok := d.stickyHeader(); iy == 0 && ok {
			i = sticky
		}

		if i < len(v.rows) {
			a.reviewGo(v, i)
		}
	}
}

// drawerWheel scrolls the drawer's diff 3 rows per notch (dir 1 is down); the
// cursor follows when it would leave the view.
func (a *App) drawerWheel(dir int) {
	v := a.drawer.views[a.store.State().Tab().ID]
	if v == nil || len(v.rows) == 0 {
		return
	}

	h := a.reviewBodyH(v)

	v.top = min(max(v.viewTop(h)+dir*wheelRows, 0), max(len(v.rows)-h, 0))
	v.cursor = min(max(v.cursor, v.top), max(min(v.top+h-1, len(v.rows)-1), 0))
}

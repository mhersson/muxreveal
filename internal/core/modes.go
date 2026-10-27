package core

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/mhersson/muxreveal/internal/core/tiling"
)

// ErrUnknownMode is returned by SetLayoutMode for a mode that does not exist.
var ErrUnknownMode = errors.New("core: unknown layout mode")

// layoutModes is the cycle order.
var layoutModes = [...]LayoutMode{ModeBSP, ModeMaster, ModeScrolling}

// widthEpsilon absorbs float error when column widths are summed against 1.
const widthEpsilon = 1e-9

func validMode(m LayoutMode) bool { return slices.Contains(layoutModes[:], m) }

// SetLayoutMode rebuilds the active tab in a mode from its pane order: BSP
// inserts the panes one by one with the auto scheme, master-stack keeps the
// order with the configured ratio, scrolling makes one column per pane.
// Split ratios and column widths reset; focus stays, zoom and preselection
// clear. An empty tab only gets its mode, and the current mode changes
// nothing. Area is the body rect the UI lays out in.
type SetLayoutMode struct {
	Mode LayoutMode
	Area Rect
}

func (a SetLayoutMode) apply(s *State) error {
	if !validMode(a.Mode) {
		return fmt.Errorf("%w: %q", ErrUnknownMode, a.Mode)
	}

	return s.cur().setMode(a.Mode, a.Area, s.Layout)
}

// CycleLayoutMode switches the active tab to the next mode: bsp, master-stack,
// scrolling, bsp.
type CycleLayoutMode struct {
	Area Rect
}

func (a CycleLayoutMode) apply(s *State) error {
	t := s.cur()
	i := max(slices.Index(layoutModes[:], t.Mode), 0) // "" is bsp

	return t.setMode(layoutModes[(i+1)%len(layoutModes)], a.Area, s.Layout)
}

func (t *Tab) setMode(mode LayoutMode, area Rect, lc LayoutConfig) error {
	if t.Empty() {
		t.Mode = mode

		return nil
	}

	if t.Mode == mode || t.Mode == "" && mode == ModeBSP {
		return nil
	}

	order := slices.Clone(t.Panes)
	next := Tab{ID: t.ID, Name: t.Name, Focus: t.Focus}

	switch mode {
	case ModeMaster:
		next.Mode, next.Panes, next.Master = ModeMaster, order, lc.ratio()
	case ModeScrolling:
		next.Mode, next.Panes = ModeScrolling, order
		for _, id := range order {
			next.Columns = append(next.Columns, Column{Panes: []PaneID{id}, Width: lc.columnWidth()})
		}
	default:
		tree := tiling.NewBSPTree()
		tree.AutoScheme = lc.Scheme()
		prev := 0

		for _, id := range order {
			win, err := paneWindow(id)
			if err != nil {
				return err
			}

			tree.InsertWindow(win, prev, tiling.SplitNone, 0.5, tileArea(area), gap)
			prev = win
		}

		next.setTree(fromTiling(tree))
	}

	next.keepVisible()
	*t = next

	return nil
}

// DefaultLayout returns t with the configured default mode built from t.Panes,
// for a state file with no usable layout. It needs no screen size: BSP puts
// the panes side by side in equal columns, master-stack keeps the order at the
// configured ratio, scrolling makes one column per pane. Focus and zoom stay;
// preselection clears.
func DefaultLayout(t Tab, lc LayoutConfig) Tab {
	next := Tab{ID: t.ID, Name: t.Name, Focus: t.Focus, Zoomed: t.Zoomed, Mode: lc.Mode()}

	order := slices.Clone(t.Panes)
	if len(order) == 0 {
		return next
	}

	switch next.Mode {
	case ModeMaster:
		next.Panes, next.Master = order, lc.ratio()
	case ModeScrolling:
		next.Panes = order
		for _, id := range order {
			next.Columns = append(next.Columns, Column{Panes: []PaneID{id}, Width: lc.columnWidth()})
		}
	default:
		next.setTree(equalColumns(order))
	}

	return next
}

// equalColumns chains vertical splits where pane i takes 1/(n-i) of what is
// left. Ratios stay within [0.1, 0.9] on the 0.01 grid, so the columns are
// equal up to 10 panes.
func equalColumns(panes []PaneID) *Node {
	if len(panes) == 1 {
		return &Node{Pane: panes[0]}
	}

	r := math.Round(min(max(1/float64(len(panes)), minRatio), maxRatio)*100) / 100

	return &Node{Split: SplitVertical, Ratio: r, A: &Node{Pane: panes[0]}, B: equalColumns(panes[1:])}
}

// start makes id the only pane of an empty tab in mode.
func (t *Tab) start(id PaneID, mode LayoutMode, lc LayoutConfig) {
	*t = Tab{ID: t.ID, Name: t.Name, Mode: mode, Panes: []PaneID{id}}
	switch mode {
	case ModeMaster:
		t.Master = lc.ratio()
	case ModeScrolling:
		t.Columns = []Column{{Panes: []PaneID{id}, Width: lc.columnWidth()}}
	default:
		t.setTree(&Node{Pane: id})
	}
}

// masterRatio is the tab's ratio, or the configured one while it has none.
func (t Tab) masterRatio(lc LayoutConfig) float64 {
	if t.Master > 0 {
		return t.Master
	}

	return lc.ratio()
}

// colOf finds a pane's column and its index in it.
func (t Tab) colOf(id PaneID) (col, idx int, ok bool) {
	for c, column := range t.Columns {
		if i := slices.Index(column.Panes, id); i >= 0 {
			return c, i, true
		}
	}

	return 0, 0, false
}

// syncPanes keeps Panes in column order: left to right, top to bottom.
func (t *Tab) syncPanes() {
	t.Panes = t.Panes[:0:0]
	for _, c := range t.Columns {
		t.Panes = append(t.Panes, c.Panes...)
	}
}

// runEnd is the end (exclusive) of the columns shown from first: columns are
// taken while their widths sum to at most 1, and the first always is.
func (t Tab) runEnd(first int) int {
	sum, end := 0.0, first
	for end < len(t.Columns) {
		sum += t.Columns[end].Width
		if sum > 1+widthEpsilon && end > first {
			break
		}

		end++
	}

	return end
}

// keepVisible moves First only as far as needed to show the focused column,
// and into range. Every action that changes focus or columns calls it.
func (t *Tab) keepVisible() {
	if t.Mode != ModeScrolling {
		return
	}

	t.First = min(max(t.First, 0), max(len(t.Columns)-1, 0))

	f, _, ok := t.colOf(t.Focus)
	switch {
	case !ok:
	case f < t.First:
		t.First = f
	case f >= t.runEnd(t.First):
		sum := 0.0

		t.First = f
		for i := f; i >= 0; i-- {
			sum += t.Columns[i].Width
			if sum > 1+widthEpsilon && i < f {
				break
			}

			t.First = i
		}
	}
}

// KeepVisible scrolls a scrolling tab so its focused column is drawn; other
// modes are unchanged. A restored tab calls it once its focus is set.
func (t *Tab) KeepVisible() { t.keepVisible() }

// setFocus focuses a pane of the tab and keeps it in view.
func (t *Tab) setFocus(id PaneID) {
	t.Focus = id
	t.keepVisible()
}

func (t *Tab) placeMaster(id PaneID) { t.Panes = append(t.Panes, id) }

// placeScrolling adds a column after the focused one; a horizontal split adds
// the pane to the focused column right below the focused pane.
func (t *Tab) placeScrolling(id PaneID, split Split, lc LayoutConfig) error {
	c, i, ok := t.colOf(t.Focus)
	if !ok {
		return fmt.Errorf("core: focused pane %q is not in the layout", t.Focus)
	}

	if split == SplitHorizontal {
		t.Columns[c].Panes = slices.Insert(t.Columns[c].Panes, i+1, id)
	} else {
		t.Columns = slices.Insert(t.Columns, c+1, Column{Panes: []PaneID{id}, Width: lc.columnWidth()})
	}

	t.syncPanes()

	return nil
}

func (t *Tab) removeMaster(id PaneID) (PaneID, error) {
	i := slices.Index(t.Panes, id)
	if i < 0 {
		return "", fmt.Errorf("core: pane %q is not in the layout", id)
	}

	t.Panes = slices.Delete(t.Panes, i, i+1)
	switch {
	case id != t.Focus:
		return t.Focus, nil
	case len(t.Panes) == 0:
		return "", nil
	}

	return t.Panes[min(i, len(t.Panes)-1)], nil
}

func (t *Tab) removeScrolling(id PaneID) (PaneID, error) {
	c, i, ok := t.colOf(id)
	if !ok {
		return "", fmt.Errorf("core: pane %q is not in the layout", id)
	}

	col := slices.Delete(t.Columns[c].Panes, i, i+1)
	t.Columns[c].Panes = col

	next := t.Focus
	if len(col) == 0 {
		t.Columns = slices.Delete(t.Columns, c, c+1)
		if c < t.First {
			t.First--
		}
	}

	t.syncPanes()

	t.First = min(max(t.First, 0), max(len(t.Columns)-1, 0))
	if id != t.Focus {
		return next, nil
	}

	switch {
	case len(col) > 0:
		return col[min(i, len(col)-1)], nil
	case c > 0:
		return t.Columns[c-1].Panes[0], nil
	case len(t.Columns) > 0:
		return t.Columns[0].Panes[0], nil
	}

	return "", nil
}

func (t *Tab) swapMaster(a, b PaneID) error {
	i, j := slices.Index(t.Panes, a), slices.Index(t.Panes, b)
	if i < 0 || j < 0 {
		return fmt.Errorf("core: swap between %q and %q: pane not in the layout", a, b)
	}

	t.Panes[i], t.Panes[j] = t.Panes[j], t.Panes[i]

	return nil
}

func (t *Tab) swapScrolling(a, b PaneID) error {
	ca, ia, okA := t.colOf(a)

	cb, ib, okB := t.colOf(b)
	if !okA || !okB {
		return fmt.Errorf("core: swap between %q and %q: pane not in the layout", a, b)
	}

	pa, pb := &t.Columns[ca].Panes[ia], &t.Columns[cb].Panes[ib]
	*pa, *pb = *pb, *pa

	t.syncPanes()

	return nil
}

// scrollNeighbour is the pane a focus move reaches in scrolling: left and
// right go to the adjacent column's pane at the same index (clamped), up and
// down move within the column.
func scrollNeighbour(t Tab, d Dir) (PaneID, bool) {
	c, i, ok := t.colOf(t.Focus)
	if !ok {
		return "", false
	}

	switch d {
	case Left, Right:
		nc := c - 1
		if d == Right {
			nc = c + 1
		}

		if nc < 0 || nc >= len(t.Columns) {
			return "", false
		}

		col := t.Columns[nc].Panes

		return col[min(i, len(col)-1)], true
	case Up, Down:
		ni := i - 1
		if d == Down {
			ni = i + 1
		}

		if col := t.Columns[c].Panes; ni >= 0 && ni < len(col) {
			return col[ni], true
		}
	}

	return "", false
}

// stepShare moves v by delta, rounds to 0.01 and clamps to [lo, hi].
func stepShare(v, delta, lo, hi float64) float64 {
	return min(max(math.Round((v+delta)*100)/100, lo), hi)
}

// resizeMaster moves the divider between masters and stack. It acts on the
// axis that divides them (columns for left, right and center; rows for top and
// bottom). Right and Down grow the left or top region: with the masters on the
// right or bottom that is the stack, so the ratio shrinks. In center the ratio
// is the middle column's share and Right grows it.
func (t *Tab) resizeMaster(d Dir, lc LayoutConfig) {
	pos := lc.position()
	rows := pos == tiling.MasterPositionTop || pos == tiling.MasterPositionBottom
	delta := ratioStep

	switch {
	case d == Left && !rows, d == Up && rows:
		delta = -ratioStep
	case d == Right && !rows, d == Down && rows:
	default:
		return
	}

	if pos == tiling.MasterPositionRight || pos == tiling.MasterPositionBottom {
		delta = -delta
	}

	t.Master = stepShare(t.masterRatio(lc), delta, minRatio, maxRatio)
}

// resizeColumn grows (Right) or shrinks (Left) the focused column.
func (t *Tab) resizeColumn(d Dir) {
	c, _, ok := t.colOf(t.Focus)
	if !ok || (d != Left && d != Right) {
		return
	}

	delta := ratioStep
	if d == Left {
		delta = -ratioStep
	}

	t.Columns[c].Width = stepShare(t.Columns[c].Width, delta, minRatio, 1)
	t.keepVisible()
}

// arrangeMaster lays the tab's panes out with the master-stack tiler.
func arrangeMaster(t Tab, area tiling.Rect, lc LayoutConfig, a *Arrangement) {
	p := tiling.MasterParams{
		Position: lc.position(),
		Count:    lc.count(),
		Ratio:    t.masterRatio(lc),
		Grid:     lc.grid(),
		Gap:      gap,
	}

	k := keeper{area: area}
	for i, l := range tiling.CalculateMasterLayout(len(t.Panes), area.W, area.H, area.Y, p) {
		r := Rect{X: area.X + l.X, Y: l.Y, W: l.Width, H: l.Height}
		if !k.keep(r) {
			continue
		}

		a.Rects[t.Panes[i]], a.Sizes[t.Panes[i]] = r, r
	}
}

// keeper admits drawn pane rects in pane order: one is kept only if it is at
// least minPane square, lies in area and is disjoint from those already kept.
type keeper struct {
	area  tiling.Rect
	rects []Rect
}

func (k *keeper) keep(r Rect) bool {
	if r.W < minPane || r.H < minPane || !inside(r, k.area) || slices.ContainsFunc(k.rects, r.overlaps) {
		return false
	}

	k.rects = append(k.rects, r)

	return true
}

// arrangeScrolling draws the whole columns of the visible run, stretched over
// the body; every other column gets the size it would have shown alone.
func arrangeScrolling(t Tab, area tiling.Rect, a *Arrangement) {
	n := len(t.Columns)
	if n == 0 {
		return
	}

	first := min(max(t.First, 0), n-1)
	end := t.runEnd(first)
	a.HiddenLeft, a.HiddenRight = first, n-end

	widths := make([]float64, 0, end-first)
	for _, c := range t.Columns[first:end] {
		widths = append(widths, c.Width)
	}

	xs := divide(area.X, area.W, widths)

	k := keeper{area: area}
	for i, c := range t.Columns[first:end] {
		for j, y := range divide(area.Y, area.H, equalWeights(len(c.Panes))) {
			r := Rect{X: xs[i].pos, Y: y.pos, W: xs[i].size, H: y.size}
			if !k.keep(r) {
				continue
			}

			a.Rects[c.Panes[j]], a.Sizes[c.Panes[j]] = r, r
		}
	}

	for i, c := range t.Columns {
		if i >= first && i < end {
			continue
		}

		w := min(max(int(math.Round(c.Width*float64(area.W))), minPane), area.W)
		for j, y := range divide(area.Y, area.H, equalWeights(len(c.Panes))) {
			if y.size >= 1 {
				a.Sizes[c.Panes[j]] = Rect{X: area.X, Y: y.pos, W: w, H: y.size}
			}
		}
	}
}

// inside reports whether the pane rect r lies in the tile area.
func inside(r Rect, area tiling.Rect) bool {
	return r.X >= area.X && r.X+r.W <= area.X+area.W && r.Y >= area.Y && r.Y+r.H <= area.Y+area.H
}

func (r Rect) overlaps(o Rect) bool {
	return r.X < o.X+o.W && o.X < r.X+r.W && r.Y < o.Y+o.H && o.Y < r.Y+r.H
}

func equalWeights(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 1
	}

	return w
}

type extent struct{ pos, size int }

// divide splits total cells from origin between len(weights) adjacent
// neighbours in proportion to weights. Every neighbour that fits gets at least
// one cell; when total has fewer cells than neighbours the first total of them
// get one each and the rest get size 0.
func divide(origin, total int, weights []float64) []extent {
	n := len(weights)

	out := make([]extent, n)
	if n == 0 {
		return out
	}

	if total < n {
		for i := range out {
			out[i] = extent{pos: origin + min(i, total), size: min(max(total-i, 0), 1)}
		}

		return out
	}

	sum := 0.0
	for _, w := range weights {
		sum += w
	}

	pos, prev, acc := origin, 0, 0.0
	for i, w := range weights {
		acc += w

		edge := total
		if i < n-1 {
			edge = int(math.Round(float64(total) * acc / sum))
		}

		edge = min(max(edge, prev+1), total-(n-1-i))
		out[i] = extent{pos: pos, size: edge - prev}
		pos += edge - prev
		prev = edge
	}

	return out
}

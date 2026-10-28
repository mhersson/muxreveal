package core

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mhersson/muxreveal/internal/core/tiling"
)

// paneWindow maps a pane id "p<N>" to its tiling window id N.
func paneWindow(id PaneID) (int, error) {
	s, ok := strings.CutPrefix(string(id), "p")

	n, err := strconv.Atoi(s)
	if !ok || err != nil || n < 0 || s != strconv.Itoa(n) {
		return 0, fmt.Errorf("core: pane id %q is not p<N>", id)
	}

	return n, nil
}

func windowPane(w int) PaneID { return PaneID("p" + strconv.Itoa(w)) }

func splitType(s Split) tiling.SplitType {
	if s == SplitHorizontal {
		return tiling.SplitHorizontal
	}

	return tiling.SplitVertical
}

// toTiling builds a fresh tiling tree for n; a nil n gives an empty tree.
// Node ids are unique within one tree, so each edit starts from a new one.
func toTiling(n *Node) (*tiling.BSPTree, error) {
	tree := tiling.NewBSPTree()
	if n == nil {
		return tree, nil
	}

	var build func(*Node) (*tiling.TileNode, error)

	build = func(n *Node) (*tiling.TileNode, error) {
		if n.leaf() {
			w, err := paneWindow(n.Pane)
			if err != nil {
				return nil, err
			}

			if tree.HasWindow(w) {
				return nil, fmt.Errorf("core: pane %q appears twice in the layout", n.Pane)
			}

			node := tree.NewLeafNode(w)
			tree.WindowToNode[w] = node

			return node, nil
		}

		if n.A == nil || n.B == nil {
			return nil, fmt.Errorf("core: layout split without two children")
		}

		a, err := build(n.A)
		if err != nil {
			return nil, err
		}

		b, err := build(n.B)
		if err != nil {
			return nil, err
		}

		return tree.NewInternalNode(splitType(n.Split), n.Ratio, a, b), nil
	}

	root, err := build(n)
	if err != nil {
		return nil, err
	}

	tree.Root = root

	return tree, nil
}

// fromTiling is the Node tree of a tiling tree; nil for an empty tree.
func fromTiling(tree *tiling.BSPTree) *Node {
	var conv func(*tiling.TileNode) *Node

	conv = func(n *tiling.TileNode) *Node {
		if n == nil {
			return nil
		}

		if n.IsLeaf() {
			return &Node{Pane: windowPane(n.WindowID)}
		}

		split := SplitVertical
		if n.SplitType == tiling.SplitHorizontal {
			split = SplitHorizontal
		}

		return &Node{Split: split, Ratio: n.SplitRatio, A: conv(n.Left), B: conv(n.Right)}
	}

	return conv(tree.Root)
}

// leafOrder lists a tree's panes, first leaf first.
func leafOrder(n *Node) []PaneID {
	switch {
	case n == nil:
		return nil
	case n.leaf():
		return []PaneID{n.Pane}
	}

	return append(leafOrder(n.A), leafOrder(n.B)...)
}

// setTree installs a BSP tree and keeps Panes in its leaf order.
func (t *Tab) setTree(root *Node) {
	t.Mode = ModeBSP
	t.Layout = root
	t.Panes = leafOrder(root)
}

// place puts id into the tab per its mode, which an empty tab takes from lc.
// BSP splits the focused pane (split "" means the auto scheme decides the
// axis); master-stack appends; scrolling adds a column after the focused one,
// or a pane below the focused one for a horizontal split. It touches no pane
// metadata and not Focus; the caller sets focus, then calls keepVisible.
func (t *Tab) place(id PaneID, split Split, area Rect, lc LayoutConfig) error {
	if slices.Contains(t.Panes, id) {
		return fmt.Errorf("core: pane %q is already in the layout", id)
	}

	if t.Empty() {
		mode := t.Mode
		if mode == "" {
			mode = lc.Mode()
		}

		t.start(id, mode, lc)

		return nil
	}

	switch t.Mode {
	case ModeMaster:
		t.placeMaster(id)

		return nil
	case ModeScrolling:
		return t.placeScrolling(id, split, lc)
	}

	return t.placeBSP(id, split, area, lc)
}

// remove takes id out of the tab and returns the pane to focus next: the
// current focus unless id was focused. Removing the last pane empties the tab.
func (t *Tab) remove(id PaneID) (PaneID, error) {
	switch t.Mode {
	case ModeMaster:
		return t.removeMaster(id)
	case ModeScrolling:
		return t.removeScrolling(id)
	}

	return t.removeBSP(id)
}

func (t *Tab) placeBSP(id PaneID, split Split, area Rect, lc LayoutConfig) error {
	win, err := paneWindow(id)
	if err != nil {
		return err
	}

	tree, err := toTiling(t.Layout)
	if err != nil {
		return err
	}

	focus := 0
	if tree.Root != nil {
		if focus, err = paneWindow(t.Focus); err != nil || !tree.HasWindow(focus) {
			return fmt.Errorf("core: focused pane %q is not in the layout", t.Focus)
		}
	}

	tree.AutoScheme = lc.Scheme()

	dir := tiling.SplitNone
	if split != "" {
		dir = splitType(split)
	}

	if pre, ok := preselectionOf(t.Preselect); ok {
		tree.InsertWindowWithPreselection(win, focus, pre, tileArea(area), gap)
	} else {
		tree.InsertWindow(win, focus, dir, 0.5, tileArea(area), gap)
	}

	t.Preselect = NoDir

	if !tree.HasWindow(win) {
		return fmt.Errorf("core: pane %q is already in the layout", id)
	}

	t.setTree(fromTiling(tree))

	return nil
}

func preselectionOf(d Dir) (tiling.PreselectionDir, bool) {
	switch d {
	case Left:
		return tiling.PreselectionLeft, true
	case Down:
		return tiling.PreselectionDown, true
	case Up:
		return tiling.PreselectionUp, true
	case Right:
		return tiling.PreselectionRight, true
	}

	return tiling.PreselectionNone, false
}

// equalizeNode sets every split's ratio to the share of its first child in
// the cells that axis needs for all leaves to come out equal, and returns the
// subtree's column and row counts.
func equalizeNode(n *Node) (cols, rows int) {
	if n == nil {
		return 0, 0
	}

	if n.leaf() {
		return 1, 1
	}

	ac, ar := equalizeNode(n.A)

	bc, br := equalizeNode(n.B)
	if n.Split == SplitVertical {
		n.Ratio = equalShare(ac, bc)

		return ac + bc, max(ar, br)
	}

	n.Ratio = equalShare(ar, br)

	return max(ac, bc), ar + br
}

func equalShare(a, b int) float64 {
	return stepShare(float64(a)/float64(a+b), 0, minRatio, maxRatio)
}

func (t *Tab) removeBSP(id PaneID) (PaneID, error) {
	win, err := paneWindow(id)
	if err != nil {
		return "", err
	}

	tree, err := toTiling(t.Layout)
	if err != nil {
		return "", err
	}

	node := tree.FindNode(win)
	if node == nil {
		return "", fmt.Errorf("core: pane %q is not in the layout", id)
	}

	next := t.Focus
	if id == t.Focus {
		next = ""

		if sib := node.Sibling(); sib != nil {
			for !sib.IsLeaf() {
				sib = sib.Left
			}

			next = windowPane(sib.WindowID)
		}
	}

	tree.RemoveWindow(win)
	t.setTree(fromTiling(tree))

	return next, nil
}

// swap exchanges two panes' places in the tab.
func (t *Tab) swap(a, b PaneID) error {
	switch t.Mode {
	case ModeMaster:
		return t.swapMaster(a, b)
	case ModeScrolling:
		return t.swapScrolling(a, b)
	}

	wa, err := paneWindow(a)
	if err != nil {
		return err
	}

	wb, err := paneWindow(b)
	if err != nil {
		return err
	}

	tree, err := toTiling(t.Layout)
	if err != nil {
		return err
	}

	if !tree.HasWindow(wa) || !tree.HasWindow(wb) {
		return fmt.Errorf("core: swap between %q and %q: pane not in the layout", a, b)
	}

	tree.SwapWindows(wa, wb)
	t.setTree(fromTiling(tree))

	return nil
}

// sideOf maps a direction to the tiling side.
func sideOf(d Dir) (tiling.Side, bool) {
	switch d {
	case Left:
		return tiling.SideLeft, true
	case Down:
		return tiling.SideDown, true
	case Up:
		return tiling.SideUp, true
	case Right:
		return tiling.SideRight, true
	}

	return 0, false
}

// virtualBody is the body rect focus moves use when the caller gives none.
func virtualBody() Rect { return Rect{0, 0, 10000, 10000} }

// neighbour finds the pane the focused one steps to in direction d, by the
// rects Arrange lays out in area (the virtual rect when area is zero), or by
// the column rule in scrolling.
func neighbour(t Tab, d Dir, area Rect, lc LayoutConfig) (PaneID, bool) {
	return NeighbourSkipping(t, d, area, lc, nil)
}

// NeighbourSkipping is the pane a focus move reaches from the tab's focused
// pane in direction d, as FocusDir finds it, but passing over every pane skip
// reports as out of reach; a nil skip passes over none. skip gets the pane and
// the rect it is drawn at, which in scrolling is the rect after the focus has
// moved to it, since that scrolls the columns. A pane that is not drawn is
// never skipped. It reads the tab and changes nothing.
func NeighbourSkipping(t Tab, d Dir, area Rect, lc LayoutConfig, skip func(PaneID, Rect) bool) (PaneID, bool) {
	if t.Mode == ModeScrolling {
		return scrollNeighbourSkipping(t, d, area, lc, skip)
	}

	side, ok := sideOf(d)
	if !ok {
		return "", false
	}

	if area == (Rect{}) {
		area = virtualBody()
	}

	rects := Arrange(t, area, lc).Rects

	cur, ok := rects[t.Focus]
	if !ok {
		// A body too small to draw the focused pane: move as on the virtual rect.
		rects = Arrange(t, virtualBody(), lc).Rects
		if cur, ok = rects[t.Focus]; !ok {
			return "", false
		}
	}

	var (
		ids   []PaneID
		cands []tiling.Rect
	)

	for _, id := range t.Panes {
		if r, ok := rects[id]; ok && id != t.Focus && (skip == nil || !skip(id, r)) {
			ids = append(ids, id)
			cands = append(cands, tiling.Rect(r))
		}
	}

	i := tiling.Neighbour(tiling.Rect(cur), cands, side, false)
	if i < 0 {
		return "", false
	}

	return ids[i], true
}

// scrollNeighbourSkipping steps by column until it reaches a pane skip lets
// through. t is a copy, so moving its focus scrolls only the copy.
func scrollNeighbourSkipping(t Tab, d Dir, area Rect, lc LayoutConfig, skip func(PaneID, Rect) bool) (PaneID, bool) {
	for range len(t.Panes) {
		id, ok := scrollNeighbour(t, d)
		if !ok {
			return "", false
		}

		if skip == nil {
			return id, true
		}

		t.setFocus(id)

		if r, drawn := Arrange(t, area, lc).Rects[id]; !drawn || !skip(id, r) {
			return id, true
		}
	}

	return "", false
}

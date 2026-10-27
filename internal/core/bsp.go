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
	tree.InsertWindow(win, focus, dir, 0.5, contentArea(area), gap)
	if !tree.HasWindow(win) {
		return fmt.Errorf("core: pane %q is already in the layout", id)
	}
	t.setTree(fromTiling(tree))
	return nil
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

// virtualInner is the inner rect focus moves use when the caller gives none.
func virtualInner() Rect { return Rect{0, 0, 10000, 10000} }

// neighbour finds the pane the focused one steps to in direction d, by the
// tiles Arrange lays out in area (the virtual rect when area is zero), or by
// the column rule in scrolling.
func neighbour(t Tab, d Dir, area Rect, lc LayoutConfig) (PaneID, bool) {
	if t.Mode == ModeScrolling {
		return scrollNeighbour(t, d)
	}
	side, ok := sideOf(d)
	if !ok {
		return "", false
	}
	if area == (Rect{}) {
		area = virtualInner()
	}
	rects := Arrange(t, area, lc).Rects
	cur, ok := rects[t.Focus]
	if !ok {
		// A body too small to draw the focused pane: move as on the virtual rect.
		rects = Arrange(t, virtualInner(), lc).Rects
		if cur, ok = rects[t.Focus]; !ok {
			return "", false
		}
	}
	var ids []PaneID
	var cands []tiling.Rect
	for _, id := range t.Panes {
		if r, ok := rects[id]; ok && id != t.Focus {
			ids = append(ids, id)
			cands = append(cands, tileOf(r))
		}
	}
	i := tiling.Neighbour(tileOf(cur), cands, side, false)
	if i < 0 {
		return "", false
	}
	return ids[i], true
}

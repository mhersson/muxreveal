package core

import "github.com/mhersson/aim/internal/core/tiling"

// Rect is a cell rectangle.
type Rect struct {
	X, Y, W, H int
}

// minPane is the smallest pane rect, in cells on each axis: a frame around one
// content cell.
const minPane = 3

// Arrangement is a tab laid out in a body rect.
type Arrangement struct {
	Rects                   map[PaneID]Rect // drawn panes, each including its frame
	Sizes                   map[PaneID]Rect // every placed pane of the tab, hidden columns included: rects whose content is the PTY size
	HiddenLeft, HiddenRight int             // scrolling columns hidden on each side
}

// gap is the cells between two tiles: frames touch.
const gap = 0

// Arrange lays t out in body. Tiles fill the body edge to edge and each is a
// pane's rect, framed by its outline; the content is the rect inset by one cell.
// A pane is drawn only when its rect is at least minPane square, inside body and
// disjoint from the panes kept before it in pane order; others are in neither
// map. A body under minPane square stays blank. Zoom is the caller's.
func Arrange(t Tab, body Rect, lc LayoutConfig) Arrangement {
	a := Arrangement{Rects: map[PaneID]Rect{}, Sizes: map[PaneID]Rect{}}

	area := tileArea(body)
	if area.W < minPane || area.H < minPane {
		return a
	}

	switch t.Mode {
	case ModeMaster:
		arrangeMaster(t, area, lc, &a)
	case ModeScrolling:
		arrangeScrolling(t, area, &a)
	default:
		if t.Layout != nil {
			k := keeper{area: area}
			arrangeBSP(t.Layout, area, &k, &a)
		}
	}

	return a
}

// tileArea is where tiles go: the body itself. A zero body gives zero bounds.
func tileArea(body Rect) tiling.Rect {
	return tiling.Rect{X: body.X, Y: body.Y, W: body.W, H: body.H}
}

func arrangeBSP(n *Node, bounds tiling.Rect, k *keeper, a *Arrangement) {
	if bounds.W < 1 || bounds.H < 1 {
		return
	}

	if n.leaf() {
		r := Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: bounds.H}
		if k.keep(r) {
			a.Rects[n.Pane], a.Sizes[n.Pane] = r, r
		}

		return
	}

	near, far := tiling.SplitBounds(&tiling.TileNode{SplitType: splitType(n.Split), SplitRatio: n.Ratio}, bounds, gap)
	arrangeBSP(n.A, near, k, a)
	arrangeBSP(n.B, far, k, a)
}

package core

import "github.com/mhersson/muxreveal/internal/core/tiling"

// Rect is a cell rectangle.
type Rect struct {
	X, Y, W, H int
}

// LayoutConfig holds the layout settings Arrange and the placement helpers
// read.
type LayoutConfig struct{}

// Arrangement is a tab laid out in an inner rect.
type Arrangement struct {
	Rects                   map[PaneID]Rect // drawn panes, each including its title row
	Sizes                   map[PaneID]Rect // every placed pane of the tab, hidden columns included: PTY sizes
	HiddenLeft, HiddenRight int             // scrolling columns hidden on each side
}

// gap is the cells between two tiles, on both axes.
const gap = 1

// Arrange lays t out in inner, the rect inside the outer frame. Tiles fill the
// content area, which is inner without its first row, with a one-cell gap; a
// pane's rect is its tile grown one row up into the border row that holds its
// title. A pane whose tile would be empty is in neither map. Zoom is the
// caller's.
func Arrange(t Tab, inner Rect, lc LayoutConfig) Arrangement {
	a := Arrangement{Rects: map[PaneID]Rect{}, Sizes: map[PaneID]Rect{}}
	area := contentArea(inner)
	if area.W < 1 || area.H < 1 {
		return a
	}
	if t.Layout != nil {
		arrangeBSP(t.Layout, area, &a)
	}
	return a
}

// contentArea is where tiles go: inner without its first row, which is the top
// border of the topmost panes. A zero inner gives zero bounds.
func contentArea(inner Rect) tiling.Rect {
	if inner == (Rect{}) {
		return tiling.Rect{}
	}
	return tiling.Rect{X: inner.X, Y: inner.Y + 1, W: inner.W, H: max(inner.H-1, 0)}
}

// paneRect is a tile grown one row up.
func paneRect(tile tiling.Rect) Rect {
	return Rect{X: tile.X, Y: tile.Y - 1, W: tile.W, H: tile.H + 1}
}

// tileOf is a pane rect without its title row.
func tileOf(r Rect) tiling.Rect {
	return tiling.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}
}

func arrangeBSP(n *Node, bounds tiling.Rect, a *Arrangement) {
	if bounds.W < 1 || bounds.H < 1 {
		return
	}
	if n.leaf() {
		r := paneRect(bounds)
		a.Rects[n.Pane], a.Sizes[n.Pane] = r, r
		return
	}
	near, far := tiling.SplitBounds(&tiling.TileNode{SplitType: splitType(n.Split), SplitRatio: n.Ratio}, bounds, gap)
	arrangeBSP(n.A, near, a)
	arrangeBSP(n.B, far, a)
}

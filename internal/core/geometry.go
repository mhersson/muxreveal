package core

import "math"

// Rect is a cell rectangle.
type Rect struct {
	X, Y, W, H int
}

// Rects lays the tree out in area. A vertical split gives A floor((W-1)*Ratio)
// columns, one separator column and B the rest; a horizontal split gives A
// floor(H*Ratio) rows and B the rest. A side below 1 gets 0 and its panes are
// omitted, so no returned Rect is empty or negative.
func Rects(root *Node, area Rect) map[PaneID]Rect {
	rects, _ := layout(root, area)
	return rects
}

// Separators returns the one-column separators between side-by-side children
// that both have room.
func Separators(root *Node, area Rect) []Rect {
	_, seps := layout(root, area)
	return seps
}

// share is floor(n*ratio) computed in hundredths, so ratios such as 0.7 do not
// lose a cell to float rounding.
func share(n int, ratio float64) int {
	return n * int(math.Round(ratio*100)) / 100
}

func layout(root *Node, area Rect) (map[PaneID]Rect, []Rect) {
	rects := map[PaneID]Rect{}
	var seps []Rect
	place(root, area, rects, &seps)
	return rects, seps
}

func place(n *Node, area Rect, rects map[PaneID]Rect, seps *[]Rect) {
	if n == nil || area.W < 1 || area.H < 1 {
		return
	}
	if n.leaf() {
		rects[n.Pane] = area
		return
	}
	switch n.Split {
	case SplitVertical:
		a := max(share(area.W-1, n.Ratio), 0)
		b := area.W - 1 - a
		if a < 1 {
			a = 0
		}
		if b < 1 {
			b = 0
		}
		place(n.A, Rect{area.X, area.Y, a, area.H}, rects, seps)
		place(n.B, Rect{area.X + a + 1, area.Y, b, area.H}, rects, seps)
		if a > 0 && b > 0 {
			*seps = append(*seps, Rect{area.X + a, area.Y, 1, area.H})
		}
	case SplitHorizontal:
		a := max(share(area.H, n.Ratio), 0)
		b := area.H - a
		if a < 1 {
			a = 0
		}
		place(n.A, Rect{area.X, area.Y, area.W, a}, rects, seps)
		place(n.B, Rect{area.X, area.Y + a, area.W, b}, rects, seps)
	}
}

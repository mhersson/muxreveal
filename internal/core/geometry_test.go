package core

import (
	"fmt"
	"testing"
)

func TestRects(t *testing.T) {
	tests := []struct {
		name     string
		tree     *Node
		area     Rect
		want     map[PaneID]Rect
		wantSeps []Rect
	}{
		{
			"side by side",
			vsplit(leaf("p1"), leaf("p2")), Rect{0, 0, 81, 10},
			map[PaneID]Rect{"p1": {0, 0, 40, 10}, "p2": {41, 0, 40, 10}},
			[]Rect{{40, 0, 1, 10}},
		},
		{
			"stacked",
			hsplit(leaf("p1"), leaf("p2")), Rect{0, 0, 80, 11},
			map[PaneID]Rect{"p1": {0, 0, 80, 5}, "p2": {0, 5, 80, 6}},
			nil,
		},
		{
			"offset area",
			vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))), Rect{2, 3, 21, 10},
			map[PaneID]Rect{"p1": {2, 3, 10, 10}, "p2": {13, 3, 10, 5}, "p3": {13, 8, 10, 5}},
			[]Rect{{12, 3, 1, 10}},
		},
		{
			"single pane",
			leaf("p1"), Rect{0, 0, 5, 5},
			map[PaneID]Rect{"p1": {0, 0, 5, 5}},
			nil,
		},
		{
			"too narrow to split omits the second pane",
			vsplit(leaf("p1"), leaf("p2")), Rect{0, 0, 2, 4},
			map[PaneID]Rect{"p2": {1, 0, 1, 4}},
			nil,
		},
		{
			"ratio 0.7 of 90 rows is exactly 63",
			&Node{Split: SplitHorizontal, Ratio: 0.7, A: leaf("p1"), B: leaf("p2")}, Rect{0, 0, 10, 90},
			map[PaneID]Rect{"p1": {0, 0, 10, 63}, "p2": {0, 63, 10, 27}},
			nil,
		},
		{
			"ratio 0.35 of 180 columns is exactly 63",
			&Node{Split: SplitVertical, Ratio: 0.35, A: leaf("p1"), B: leaf("p2")}, Rect{0, 0, 181, 4},
			map[PaneID]Rect{"p1": {0, 0, 63, 4}, "p2": {64, 0, 117, 4}},
			[]Rect{{63, 0, 1, 4}},
		},
		{"empty tree", nil, Rect{0, 0, 10, 10}, map[PaneID]Rect{}, nil},
		{"empty area", vsplit(leaf("p1"), leaf("p2")), Rect{0, 0, 0, 0}, map[PaneID]Rect{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Rects(tt.tree, tt.area)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("Rects = %v, want %v", got, tt.want)
			}
			if seps := Separators(tt.tree, tt.area); fmt.Sprint(seps) != fmt.Sprint(tt.wantSeps) {
				t.Errorf("Separators = %v, want %v", seps, tt.wantSeps)
			}
		})
	}
}

// sixPanes is a deep tree that cannot fit in a tiny area.
func sixPanes() *Node {
	return vsplit(leaf("p1"), vsplit(leaf("p2"), vsplit(leaf("p3"),
		hsplit(leaf("p4"), vsplit(leaf("p5"), leaf("p6"))))))
}

func TestRectsTinyAreas(t *testing.T) {
	for _, area := range []Rect{{0, 0, 20, 5}, {0, 0, 1, 1}, {0, 0, 3, 2}, {0, 0, 0, 0}, {0, 0, 7, 1}, {5, 5, 2, 9}} {
		t.Run(fmt.Sprint(area), func(t *testing.T) {
			rects := Rects(sixPanes(), area)
			for id, r := range rects {
				if r.W < 1 || r.H < 1 || r.X < area.X || r.Y < area.Y ||
					r.X+r.W > area.X+area.W || r.Y+r.H > area.Y+area.H {
					t.Errorf("%s = %+v outside %+v", id, r, area)
				}
			}
			for _, r := range Separators(sixPanes(), area) {
				if r.W != 1 || r.H < 1 || r.X < area.X || r.X >= area.X+area.W {
					t.Errorf("separator %+v outside %+v", r, area)
				}
			}
		})
	}
	if got := len(Rects(sixPanes(), Rect{0, 0, 20, 5})); got >= 6 {
		t.Errorf("20x5 kept %d panes, want some omitted", got)
	}
}

func TestRectsDoNotOverlap(t *testing.T) {
	rects := Rects(sixPanes(), Rect{0, 0, 80, 24})
	if len(rects) != 6 {
		t.Fatalf("got %d panes", len(rects))
	}
	cells := map[[2]int]PaneID{}
	for id, r := range rects {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				if other, dup := cells[[2]int{x, y}]; dup {
					t.Fatalf("%s and %s overlap at %d,%d", id, other, x, y)
				}
				cells[[2]int{x, y}] = id
			}
		}
	}
}

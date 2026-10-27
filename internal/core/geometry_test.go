package core

import (
	"fmt"
	"testing"
)

// sixPanes is a deep tree that cannot fit in a tiny area.
func sixPanes() *Node {
	return vsplit(leaf("p1"), vsplit(leaf("p2"), vsplit(leaf("p3"),
		hsplit(leaf("p4"), vsplit(leaf("p5"), leaf("p6"))))))
}

func TestArrangeRects(t *testing.T) {
	tests := []struct {
		name  string
		tree  *Node
		inner Rect
		want  map[PaneID]Rect
	}{
		{
			"side by side",
			vsplit(leaf("p1"), leaf("p2")), Rect{0, 0, 81, 11},
			map[PaneID]Rect{"p1": {0, 0, 40, 11}, "p2": {41, 0, 40, 11}},
		},
		{
			"stacked",
			hsplit(leaf("p1"), leaf("p2")), Rect{0, 0, 80, 11},
			map[PaneID]Rect{"p1": {0, 0, 80, 6}, "p2": {0, 6, 80, 5}},
		},
		{
			"offset area",
			vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))), Rect{2, 3, 21, 11},
			map[PaneID]Rect{"p1": {2, 3, 10, 11}, "p2": {13, 3, 10, 6}, "p3": {13, 9, 10, 5}},
		},
		{
			"single pane",
			leaf("p1"), Rect{0, 0, 5, 5},
			map[PaneID]Rect{"p1": {0, 0, 5, 5}},
		},
		{
			"too narrow to split keeps the first pane only",
			vsplit(leaf("p1"), leaf("p2")), Rect{0, 0, 2, 4},
			map[PaneID]Rect{"p1": {0, 0, 1, 4}},
		},
		{"empty area", vsplit(leaf("p1"), leaf("p2")), Rect{0, 0, 0, 0}, map[PaneID]Rect{}},
		{"title row only", vsplit(leaf("p1"), leaf("p2")), Rect{0, 0, 10, 1}, map[PaneID]Rect{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Arrange(tabOfTree(tt.tree), tt.inner, LayoutConfig{})
			if fmt.Sprint(got.Rects) != fmt.Sprint(tt.want) || fmt.Sprint(got.Sizes) != fmt.Sprint(tt.want) {
				t.Errorf("Rects = %v, Sizes = %v, want %v", got.Rects, got.Sizes, tt.want)
			}
		})
	}
}

func TestArrangeTinyAreas(t *testing.T) {
	for _, area := range []Rect{{0, 0, 20, 5}, {0, 0, 1, 1}, {0, 0, 3, 2}, {0, 0, 0, 0}, {0, 0, 7, 1}, {5, 5, 2, 9}} {
		t.Run(fmt.Sprint(area), func(t *testing.T) {
			rects := Arrange(tabOfTree(sixPanes()), area, LayoutConfig{}).Rects
			for id, r := range rects {
				if r.W < 1 || r.H < 2 || r.X < area.X || r.Y < area.Y ||
					r.X+r.W > area.X+area.W || r.Y+r.H > area.Y+area.H {
					t.Errorf("%s = %+v outside %+v", id, r, area)
				}
			}
		})
	}
	if got := len(Arrange(tabOfTree(sixPanes()), Rect{0, 0, 20, 5}, LayoutConfig{}).Rects); got >= 6 {
		t.Errorf("20x5 kept %d panes, want some omitted", got)
	}
}

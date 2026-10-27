package tiling

import (
	"slices"
	"testing"
)

// stripOf builds a scrolling layout whose columns hold the given window ids,
// focused on column focus.
func stripOf(focus int, cols ...[]int) *ScrollingLayout {
	s := NewScrollingLayout()
	for _, ids := range cols {
		s.Columns = append(s.Columns, ScrollColumn{WindowIDs: slices.Clone(ids)})
	}
	s.FocusedCol = focus
	return s
}

func columnIDs(s *ScrollingLayout) [][]int {
	out := make([][]int, len(s.Columns))
	for i, c := range s.Columns {
		out[i] = c.WindowIDs
	}
	return out
}

func checkStrip(t *testing.T, s *ScrollingLayout, wantCols [][]int, wantFocus int) {
	t.Helper()
	if got := columnIDs(s); !slices.EqualFunc(got, wantCols, slices.Equal[[]int]) {
		t.Errorf("columns = %v, want %v", got, wantCols)
	}
	if s.FocusedCol != wantFocus {
		t.Errorf("FocusedCol = %d, want %d", s.FocusedCol, wantFocus)
	}
}

func TestScrollingAddColumn(t *testing.T) {
	tests := []struct {
		name      string
		start     *ScrollingLayout
		wantCols  [][]int
		wantFocus int
	}{
		{"empty strip", stripOf(0), [][]int{{9}}, 0},
		{"focused is the last column", stripOf(1, []int{1}, []int{2}), [][]int{{1}, {2}, {9}}, 2},
		{"focused is the first of three", stripOf(0, []int{1}, []int{2}, []int{3}), [][]int{{1}, {9}, {2}, {3}}, 1},
		{"focused is in the middle", stripOf(1, []int{1}, []int{2}, []int{3}), [][]int{{1}, {2}, {9}, {3}}, 2},
		{"focused column is stacked", stripOf(0, []int{1, 2}, []int{3}), [][]int{{1, 2}, {9}, {3}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.start.AddColumn(9)
			checkStrip(t, tt.start, tt.wantCols, tt.wantFocus)
			if got := tt.start.GetFocusedWindowID(); got != 9 {
				t.Errorf("focused window = %d, want 9", got)
			}
		})
	}
}

func TestScrollingRemoveWindow(t *testing.T) {
	tests := []struct {
		name      string
		start     *ScrollingLayout
		remove    int
		wantCols  [][]int
		wantFocus int
	}{
		{"only window of the focused middle column", stripOf(1, []int{1}, []int{2}, []int{3}), 2, [][]int{{1}, {3}}, 0},
		{"only window of the focused last column", stripOf(2, []int{1}, []int{2}, []int{3}), 3, [][]int{{1}, {2}}, 1},
		{"only window of the focused first column", stripOf(0, []int{1}, []int{2}, []int{3}), 1, [][]int{{2}, {3}}, 0},
		{"only window of a column left of focus", stripOf(2, []int{1}, []int{2}, []int{3}), 1, [][]int{{2}, {3}}, 1},
		{"only window of a column right of focus", stripOf(0, []int{1}, []int{2}, []int{3}), 3, [][]int{{1}, {2}}, 0},
		{"last window of the strip", stripOf(0, []int{1}), 1, [][]int{}, 0},
		{"one of two stacked windows keeps the column", stripOf(1, []int{1}, []int{2, 3}), 2, [][]int{{1}, {3}}, 1},
		{"unknown window changes nothing", stripOf(1, []int{1}, []int{2}), 7, [][]int{{1}, {2}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.start.RemoveWindow(tt.remove)
			checkStrip(t, tt.start, tt.wantCols, tt.wantFocus)
		})
	}
}

func TestScrollingMoveColumn(t *testing.T) {
	tests := []struct {
		name      string
		start     *ScrollingLayout
		move      func(*ScrollingLayout)
		wantCols  [][]int
		wantFocus int
	}{
		{"left from the middle", stripOf(1, []int{1}, []int{2}, []int{3}), (*ScrollingLayout).MoveColumnLeft, [][]int{{2}, {1}, {3}}, 0},
		{"left from the last", stripOf(2, []int{1}, []int{2}, []int{3}), (*ScrollingLayout).MoveColumnLeft, [][]int{{1}, {3}, {2}}, 1},
		{"left from the first does nothing", stripOf(0, []int{1}, []int{2}), (*ScrollingLayout).MoveColumnLeft, [][]int{{1}, {2}}, 0},
		{"right from the first", stripOf(0, []int{1}, []int{2}, []int{3}), (*ScrollingLayout).MoveColumnRight, [][]int{{2}, {1}, {3}}, 1},
		{"right from the middle", stripOf(1, []int{1}, []int{2}, []int{3}), (*ScrollingLayout).MoveColumnRight, [][]int{{1}, {3}, {2}}, 2},
		{"right from the last does nothing", stripOf(1, []int{1}, []int{2}), (*ScrollingLayout).MoveColumnRight, [][]int{{1}, {2}}, 1},
		{"a stacked column moves whole", stripOf(1, []int{1}, []int{2, 3}), (*ScrollingLayout).MoveColumnLeft, [][]int{{2, 3}, {1}}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.move(tt.start)
			checkStrip(t, tt.start, tt.wantCols, tt.wantFocus)
		})
	}
}

func TestScrollingFocusColumnContaining(t *testing.T) {
	tests := []struct {
		name       string
		window     int
		wantFound  bool
		wantFocus  int
		wantActive int
	}{
		{"top of a stacked column", 2, true, 1, 0},
		{"middle of a stacked column", 3, true, 1, 1},
		{"bottom of a stacked column", 4, true, 1, 2},
		{"single window column", 5, true, 2, 0},
		{"unknown window leaves focus", 9, false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stripOf(0, []int{1}, []int{2, 3, 4}, []int{5})
			if got := s.FocusColumnContaining(tt.window); got != tt.wantFound {
				t.Fatalf("found = %v, want %v", got, tt.wantFound)
			}
			if s.FocusedCol != tt.wantFocus {
				t.Errorf("FocusedCol = %d, want %d", s.FocusedCol, tt.wantFocus)
			}
			if got := s.Columns[tt.wantFocus].Active; got != tt.wantActive {
				t.Errorf("Active = %d, want %d", got, tt.wantActive)
			}
			if tt.wantFound {
				if got := s.GetFocusedWindowID(); got != tt.window {
					t.Errorf("focused window = %d, want %d", got, tt.window)
				}
			}
		})
	}
}

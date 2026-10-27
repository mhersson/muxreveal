// Ported from tuios internal/layout/tiling_test.go at commit
// aaaa128cd35b38ba8aa08a5f69e688f01385ec3e (github.com/Gaurav-Gosain/tuios).
// Copyright (c) 2025 Gaurav Gosain. MIT License; see THIRD_PARTY.md.

package tiling

import (
	"testing"
)

// outside reports the rectangles that leave the region, and overlapping reports
// the pairs that sit on top of each other. Between them they are the tiler's
// whole contract.
func outside(layouts []TileLayout, w, h, topMargin int) []TileLayout {
	var out []TileLayout
	for _, r := range layouts {
		if r.Width <= 0 || r.Height <= 0 ||
			r.X < 0 || r.X+r.Width > w ||
			r.Y < topMargin || r.Y+r.Height > topMargin+h {
			out = append(out, r)
		}
	}
	return out
}

func overlapping(layouts []TileLayout) [][2]TileLayout {
	var out [][2]TileLayout
	for a := range layouts {
		for b := a + 1; b < len(layouts); b++ {
			p, q := layouts[a], layouts[b]
			if p.X < q.X+q.Width && q.X < p.X+p.Width &&
				p.Y < q.Y+q.Height && q.Y < p.Y+p.Height {
				out = append(out, [2]TileLayout{p, q})
			}
		}
	}
	return out
}

// TestPanesShrinkRatherThanOverlap checks that tiles shrink below the default
// window size instead of overlapping or leaving the region, on sizes where
// every pane still gets a cell.
func TestPanesShrinkRatherThanOverlap(t *testing.T) {
	for _, c := range []struct{ n, w, h int }{{7, 51, 35}, {5, 45, 12}} {
		layouts := CalculateTilingLayout(c.n, c.w, c.h, 1, 0.5, 0)
		if len(layouts) != c.n {
			t.Fatalf("%d panes on %dx%d produced %d rectangles", c.n, c.w, c.h, len(layouts))
		}
		for _, pair := range overlapping(layouts) {
			t.Errorf("%d panes on %dx%d: %+v and %+v overlap", c.n, c.w, c.h, pair[0], pair[1])
		}
		for _, r := range outside(layouts, c.w, c.h, 1) {
			t.Errorf("%d panes on %dx%d: %+v is outside the region", c.n, c.w, c.h, r)
		}
		// At least one pane is below the default window size, so the case
		// still exercises shrinking.
		narrowest, shortest := layouts[0].Width, layouts[0].Height
		for _, r := range layouts {
			narrowest, shortest = min(narrowest, r.Width), min(shortest, r.Height)
		}
		if narrowest >= DefaultWindowWidth && shortest >= DefaultWindowHeight {
			t.Errorf("%d panes on %dx%d leave the smallest at %dx%d, which the default window size of %dx%d "+
				"could have satisfied; this size no longer exercises shrinking",
				c.n, c.w, c.h, narrowest, shortest, DefaultWindowWidth, DefaultWindowHeight)
		}
	}
}

// TestATightRegionGivesUpGroundBeforeItGivesUpTheRegion states the order of
// precedence when the asked-for gaps do not fit. Ground is spacing and a pane
// outside the region is not on screen, so the gaps shrink first.
//
// Without it, nine panes at a gap of two on a region six rows tall put the
// bottom row three rows past the end of it.
func TestATightRegionGivesUpGroundBeforeItGivesUpTheRegion(t *testing.T) {
	for _, c := range []struct{ n, w, h, gap int }{
		{9, 45, 6, 2}, {7, 30, 6, 2}, {9, 45, 8, 2}, {6, 24, 9, 3},
	} {
		layouts := CalculateTilingLayout(c.n, c.w, c.h, 0, 0.5, c.gap)
		for _, pair := range overlapping(layouts) {
			t.Errorf("n=%d %dx%d gap=%d: %+v and %+v overlap", c.n, c.w, c.h, c.gap, pair[0], pair[1])
		}
		for _, r := range outside(layouts, c.w, c.h, 0) {
			t.Errorf("n=%d %dx%d gap=%d: %+v is outside the region", c.n, c.w, c.h, c.gap, r)
		}
	}
}

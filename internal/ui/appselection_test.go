package ui

import (
	"testing"

	"github.com/mhersson/muxreveal/internal/core"
)

func TestPaneSelectionResolve(t *testing.T) {
	g := newFakeGrid(10, nil, "alpha", "beta", "gamma", "delta")
	sp := func(row uint64, col int) stablePoint { return stablePoint{row: row, col: col} }

	tests := []struct {
		name    string
		mode    selMode
		a, h    stablePoint
		trimmed uint64
		want    selection
		alive   bool
	}{
		{"untrimmed", selStream, sp(1, 2), sp(2, 3), 0, selection{selStream, point{1, 2}, point{2, 3}}, true},
		{"shifted by the trim count", selStream, sp(101, 2), sp(102, 3), 100, selection{selStream, point{1, 2}, point{2, 3}}, true},
		{"trimmed end takes column 0", selStream, sp(99, 3), sp(102, 3), 100, selection{selStream, point{0, 0}, point{2, 3}}, true},
		{"trimmed block end keeps its column", selBlock, sp(99, 3), sp(102, 5), 100, selection{selBlock, point{0, 3}, point{2, 5}}, true},
		{"both ends trimmed", selStream, sp(90, 3), sp(99, 1), 100, selection{selStream, point{0, 0}, point{0, 0}}, false},
		{"end past the grid is clamped", selStream, sp(100, 0), sp(900, 99), 100, selection{selStream, point{0, 0}, point{3, 9}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := paneSelection{pane: "p1", mode: tt.mode, anchor: tt.a, head: tt.h}

			got, alive := ps.resolve(g, tt.trimmed)
			if got != tt.want || alive != tt.alive {
				t.Errorf("resolve = %+v, %v; want %+v, %v", got, alive, tt.want, tt.alive)
			}
		})
	}
}

func TestDropFallenSelection(t *testing.T) {
	a, fp, _ := copyApp(t)
	p := a.panes["p1"]

	feedLines(t, p, fp, 0, 10200)

	var trimmed uint64

	p.withView(func(_ grid, _ int, tr uint64) { trimmed = tr })

	if trimmed < 100 {
		t.Fatalf("setup: only %d lines trimmed", trimmed)
	}

	tests := []struct {
		name string
		a, h uint64
		pane string
		kept bool
	}{
		{"both ends trimmed", trimmed - 50, trimmed - 10, "p1", false},
		{"one end left", trimmed - 10, trimmed + 20, "p1", true},
		{"both ends left", trimmed + 5, trimmed + 20, "p1", true},
		{"another pane's selection is left alone", trimmed - 50, trimmed - 10, "p2", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.sel = &paneSelection{
				pane: core.PaneID(tt.pane), mode: selStream,
				anchor: stablePoint{row: tt.a, col: 1}, head: stablePoint{row: tt.h, col: 2},
			}

			a.dropFallenSelection(p)

			if kept := a.sel != nil; kept != tt.kept {
				t.Errorf("selection kept = %v, want %v", kept, tt.kept)
			}
		})
	}
}

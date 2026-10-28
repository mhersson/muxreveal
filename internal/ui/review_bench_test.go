//go:build bench

package ui

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/git"
)

// Gate numbers for paging through a long diff in the drawer: the 99th
// percentile and the worst frame.
const (
	scrollGateP99 = 8 * time.Millisecond
	scrollGateMax = 16700 * time.Microsecond
)

// scrollHunks is how many hunks the long diff has; each is 40 lines of which 20
// are changed, so the file is 10,000 lines with half of them changed.
const scrollHunks = 250

// benchScrollResult is one file of scrollHunks hunks, each with 10 lines of
// context, 10 removed, 10 added and 10 of context.
func benchScrollResult() git.Result {
	hunks := make([]git.Hunk, scrollHunks)

	for k := range hunks {
		specs := make([]string, 0, 40)
		line := func(kind byte, tag string, i int) {
			specs = append(specs, fmt.Sprintf("%c\tv%d_%d := s.compute(ctx, %d, %q) // %s", kind, k, i, i*k, tag, tag))
		}

		for i := range 10 {
			line(' ', "before", i)
		}

		for i := range 10 {
			line('-', "old", i)
		}

		for i := range 10 {
			line('+', "new", i)
		}

		for i := range 10 {
			line(' ', "after", i)
		}

		start := 1 + k*50
		hunks[k] = hunkSpec(start, start, fmt.Sprintf("func (s *Server) step%d(ctx context.Context) error", k), specs...)
	}

	return git.Result{
		Old: "base", New: "tree", Live: true,
		Files: []git.FileDiff{fileSpec("internal/big/big.go", git.Modified, hunks...)},
	}
}

// TestBenchReviewScroll pages through a 10,000-line diff in the drawer with
// ctrl+d, one press per frame at 60 FPS on the virtual clock, and times update,
// view and present for each frame. Run it with:
//
//	CGO_ENABLED=1 go test -tags bench ./internal/ui -run TestBenchReviewScroll -v
func TestBenchReviewScroll(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	clk := &fakeClock{t: t0}

	app, _, err := newBenchApp(benchConfig{Panes: 1, Width: benchWidth, Height: benchHeight, Review: true}, clk)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = app.Close(context.Background()) }()

	// The long diff is a new source: it replaces the first result, at the top.
	app.Update(benchReviewMsg(app, benchScrollResult(), false))

	v := app.drawer.views[app.store.State().Tab().ID]
	if len(v.res.Files) != 1 || len(v.rows) < scrollHunks*40 || v.cursor != 0 {
		t.Fatalf("view holds %d files, %d rows, cursor %d; want the long diff at its top", len(v.res.Files), len(v.rows), v.cursor)
	}

	if !app.drawerHasKeys() {
		t.Fatal("the drawer does not have the keyboard")
	}

	pr := newPresenter(benchWidth, benchHeight)
	if err := pr.present(app.View().Content); err != nil {
		t.Fatalf("present: %v", err)
	}

	runtime.GC()

	var (
		frames, render, present frameTimes
		press                   = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
		fi                      = time.Second / 60
	)

	for v.cursor < len(v.rows)-1 {
		if len(frames.ds) > len(v.rows) {
			t.Fatal("ctrl+d does not reach the last row")
		}

		clk.Advance(fi)

		before := v.cursor
		start := time.Now()

		app.Update(press)

		frame := app.View().Content
		renderD := time.Since(start)
		err := pr.present(frame)

		frames.add(time.Since(start))
		render.add(renderD)
		present.add(time.Since(start) - renderD)

		if err != nil {
			t.Fatalf("present: %v", err)
		}

		if v.cursor <= before {
			t.Fatalf("ctrl+d moved the cursor from row %d to %d", before, v.cursor)
		}
	}

	s, r, p := frames.summary(), render.summary(), present.summary()
	t.Logf("paged %d rows in %d frames (%d rows a page): p50 %s, p95 %s, p99 %s (gate %s), max %s (gate %s)",
		len(v.rows), s.Count, app.reviewBodyH(v)/2, fmtMS(s.P50), fmtMS(s.P95), fmtMS(s.P99), fmtMS(scrollGateP99), fmtMS(s.Max), fmtMS(scrollGateMax))
	t.Logf("per-component p50/p99 (they do not sum to the frame's): update+view %s/%s, present %s/%s",
		fmtMS(r.P50), fmtMS(r.P99), fmtMS(p.P50), fmtMS(p.P99))

	if s.P99 > scrollGateP99 {
		t.Errorf("p99 = %v, gate is %v", s.P99, scrollGateP99)
	}

	if s.Max > scrollGateMax {
		t.Errorf("max = %v, gate is %v", s.Max, scrollGateMax)
	}
}

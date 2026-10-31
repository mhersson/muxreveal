//go:build bench

package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
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

	if app.drawer.fallbacks != 0 {
		t.Errorf("the compositor drew the drawer in %d frames, so the run did not measure the splice", app.drawer.fallbacks)
	}

	if s.P99 > scrollGateP99 {
		t.Errorf("p99 = %v, gate is %v", s.P99, scrollGateP99)
	}

	if s.Max > scrollGateMax {
		t.Errorf("max = %v, gate is %v", s.Max, scrollGateMax)
	}
}

func hunkHolds(v *reviewView, path, text string) bool {
	for _, f := range v.res.Files {
		if f.Path != path {
			continue
		}

		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Kind == git.Add && l.Text == text {
					return true
				}
			}
		}
	}

	return false
}

// saveToPaneCeiling bounds the median save-to-pane time the test asserts. The
// 300 ms gate is read from the logged median on a quiet machine.
const saveToPaneCeiling = time.Second

// A file saved in a 2,000-file repository reaches the open drawer. The test
// asserts a median ceiling of one second and logs the median and p95; the
// watcher has its default debounce, as in the app. Run it with:
//
//	CGO_ENABLED=1 go test -tags bench ./internal/ui -run TestReviewSaveToPane -v
func TestReviewSaveToPane(t *testing.T) {
	const (
		files = 2000
		saves = 20
	)

	r := newDrawerRigWatching(t, git.WatchOptions{})
	r.wait = 60 * time.Second

	dir := newRepoDir(t)
	for i := range files {
		write(t, filepath.Join(dir, fmt.Sprintf("d%02d/f%04d.txt", i%50, i)), numberedLines("line", 20))
	}

	commitEverything(t, dir, "base")
	gitIn(t, "git", dir, "checkout", "-q", "-b", "feature")

	// Files written in the same second as the index are racily clean: git must
	// hash them again until the index is written once the second has passed.
	time.Sleep(1500 * time.Millisecond)
	gitIn(t, "git", dir, "status", "--porcelain")

	root := rootOf(t, dir)
	id := r.shell(t, dir)

	v := r.open(t, id)
	r.watching(t, id, root)
	r.quiet(t, 400*time.Millisecond)

	if len(v.res.Files) != 0 {
		t.Fatalf("setup: %d changed files", len(v.res.Files))
	}

	took := make([]time.Duration, 0, saves)

	for i := range saves {
		n := (i*97 + 13) % files
		rel := fmt.Sprintf("d%02d/f%04d.txt", n%50, n)
		text := fmt.Sprintf("saved %d", i)

		r.quiet(t, 250*time.Millisecond)

		start := time.Now()

		write(t, filepath.Join(dir, rel), text+"\n")
		r.until(t, "save "+rel+" to reach the drawer", func() bool { return hunkHolds(v, rel, text) })

		took = append(took, time.Since(start))
	}

	slices.Sort(took)

	median, p95 := took[len(took)/2], took[len(took)*95/100]
	t.Logf("save to pane over %d saves: median %v, p95 %v, max %v", saves, median, p95, took[len(took)-1])

	if median > saveToPaneCeiling {
		t.Errorf("median save to pane is %v, want at most %v (p95 %v)", median, saveToPaneCeiling, p95)
	}
}

// stepRatio is how many times what a resize costs with 1 KiB lines a resize may
// cost with 1 MiB lines: what is drawn of a line must not depend on its length,
// which would show as a ratio in the hundreds. Both are timed in the same run,
// one after the other, so machine load moves them together.
const stepRatio = 10

// TestBenchDrawerResizeWithLongLines bounds what a drawer resize costs with
// 1 MiB lines against 1 KiB lines, summed over every width, for each icons
// setting and view variant. Run it with:
//
//	CGO_ENABLED=1 go test -tags bench ./internal/ui -run TestBenchDrawerResizeWithLongLines -v
func TestBenchDrawerResizeWithLongLines(t *testing.T) {
	for _, icons := range []bool{true, false} {
		for _, vr := range rowWidthVariants() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, vr.name), func(t *testing.T) {
				a, baseline := rowWidthApp(t, icons, vr, megabyteLines()), rowWidthApp(t, icons, vr, kilobyteLines())

				var slow, base time.Duration

				for _, w := range rowWidths(icons) {
					start := time.Now()
					_, _, _ = resizeDrawer(t, baseline, w)
					mid := time.Now()
					_, _, _ = resizeDrawer(t, a, w)

					base, slow = base+mid.Sub(start), slow+time.Since(mid)
				}

				t.Logf("resizes: %v with 1 MiB lines, %v with 1 KiB lines", slow, base)

				if slow > stepRatio*base {
					t.Errorf("the resizes took %v with 1 MiB lines and %v with 1 KiB lines, want at most %dx", slow, base, stepRatio)
				}
			})
		}
	}
}

//go:build bench

package ui

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/term/rec"
)

// Up to six panes on 240x64, each count run three times.
const (
	benchWidth, benchHeight = 240, 64
	benchRepeats            = 3
	defaultStagger          = 500 * time.Millisecond
)

// benchRecordings loads the recordings named by MUXREVEAL_BENCH_RECORDINGS: a
// list of files and directories (their *.rec files), separated like PATH.
func benchRecordings(t *testing.T) ([][]rec.Record, []string) {
	t.Helper()

	env := os.Getenv("MUXREVEAL_BENCH_RECORDINGS")
	if env == "" {
		t.Skip("MUXREVEAL_BENCH_RECORDINGS is not set")
	}

	var paths []string

	for _, p := range filepath.SplitList(env) {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}

		if !st.IsDir() {
			paths = append(paths, p)

			continue
		}

		m, err := filepath.Glob(filepath.Join(p, "*.rec"))
		if err != nil {
			t.Fatal(err)
		}

		paths = append(paths, m...)
	}

	if len(paths) == 0 {
		t.Fatalf("no recordings in %q", env)
	}

	var all [][]rec.Record

	for _, p := range paths {
		r, err := rec.Load(p)
		if errors.Is(err, rec.ErrTruncated) {
			t.Logf("%s: %v (using the complete records)", filepath.Base(p), err)
		} else if err != nil {
			t.Fatalf("%s: %v", p, err)
		}

		if len(r) == 0 {
			t.Fatalf("%s: no records", p)
		}

		all = append(all, r)
	}

	return all, paths
}

// busiestWindow returns the start (on a 1 s grid) of the length-long window
// with the most bytes across all recordings, covering the stagger of the
// largest run.
func busiestWindow(all [][]rec.Record, length, stagger time.Duration) (time.Duration, error) {
	span := length + (maxBenchPanes-1)*stagger

	var shortest time.Duration

	for i, r := range all {
		if d := rec.Duration(r); i == 0 || d < shortest {
			shortest = d
		}
	}

	if shortest < span {
		return 0, fmt.Errorf("the shortest recording is %v, need %v", shortest, span)
	}

	best, bestBytes := time.Duration(0), -1

	for start := time.Duration(0); start+span <= shortest; start += time.Second {
		n := 0

		for _, r := range all {
			for _, c := range r {
				if c.At >= start && c.At < start+span {
					n += len(c.Data)
				}
			}
		}

		if n > bestBytes {
			best, bestBytes = start, n
		}
	}

	return best, nil
}

func envDuration(t *testing.T, name string, def time.Duration) time.Duration {
	t.Helper()

	s := os.Getenv(name)
	if s == "" {
		return def
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	return d
}

// TestBench replays MUXREVEAL_BENCH_RECORDINGS in 2, 4 and 6 panes and fails
// when the six-pane median of the runs' worst frames is over the budget. The
// other knobs: _START, _LENGTH, _STAGGER, _MODE, _SELECTION, _RAIL, _REVIEW, and
// _PICTURES=1, which keeps a picture placed in the drawer beside the panes, so
// every frame pays for placing pictures, and fails a run whose frames lack it.
func TestBench(t *testing.T) {
	all, paths := benchRecordings(t)
	length := envDuration(t, "MUXREVEAL_BENCH_LENGTH", 10*time.Second)

	stagger := envDuration(t, "MUXREVEAL_BENCH_STAGGER", defaultStagger)

	if length <= 0 || stagger < 0 {
		t.Fatalf("MUXREVEAL_BENCH_LENGTH must be positive and MUXREVEAL_BENCH_STAGGER not negative (got %v, %v)", length, stagger)
	}

	var start time.Duration

	if os.Getenv("MUXREVEAL_BENCH_START") != "" {
		start = envDuration(t, "MUXREVEAL_BENCH_START", 0)
	} else {
		var err error

		if start, err = busiestWindow(all, length, stagger); err != nil {
			t.Fatal(err)
		}
	}

	selection := os.Getenv("MUXREVEAL_BENCH_SELECTION") != ""

	mode, err := parseBenchMode(os.Getenv("MUXREVEAL_BENCH_MODE"))
	if err != nil {
		t.Fatal(err)
	}

	rail, err := parseBenchRail(os.Getenv("MUXREVEAL_BENCH_RAIL"))
	if err != nil {
		t.Fatal(err)
	}

	review, err := parseBenchReview(os.Getenv("MUXREVEAL_BENCH_REVIEW"))
	if err != nil {
		t.Fatal(err)
	}

	pictures, err := parseBenchSwitch("MUXREVEAL_BENCH_PICTURES", os.Getenv("MUXREVEAL_BENCH_PICTURES"))
	if err != nil {
		t.Fatal(err)
	}

	var dir string
	if pictures {
		dir = t.TempDir()
	}

	var sb strings.Builder

	fmt.Fprintf(&sb, "recordings=%d window=%v+%v stagger=%v size=%dx%d selection=%v mode=%s rail=%v review=%v pictures=%v\n",
		len(paths), start, length, stagger, benchWidth, benchHeight, selection, mode, rail, review, pictures)
	sb.WriteString("Rows are the run whose p99 is the median of the repeats.\n")
	sb.WriteString("| panes | frames | p50 | p95 | p99 | max | CPU % | bytes fed |\n|---|---|---|---|---|---|---|---|\n")

	var gateP99, gateMax time.Duration

	for _, n := range []int{2, 4, 6} {
		var runs []benchResult

		for range benchRepeats {
			res, err := runBench(benchConfig{
				Panes: n, Recordings: all, Width: benchWidth, Height: benchHeight,
				Start: start, Length: length, Stagger: stagger, FPS: 60, Selection: selection, Agents: true, Mode: mode, Rail: rail, Review: review,
				Pictures: dir,
			})
			if err != nil {
				t.Fatal(err)
			}

			if res.Fallbacks != 0 {
				t.Errorf("%d panes: the compositor drew the drawer in %d frames, so the run did not measure the splice", n, res.Fallbacks)
			}

			if res.PictureFallbacks != 0 {
				t.Errorf("%d panes: the compositor placed the picture in %d frames, so the run did not measure the splice", n, res.PictureFallbacks)
			}

			if pictures && (res.Frames.Count == 0 || res.Pictured != res.Frames.Count) {
				t.Errorf("%d panes: the picture was placed in %d of %d frames, so the run did not measure placing it", n, res.Pictured, res.Frames.Count)
			}

			runs = append(runs, res)
		}

		r := medianRun(runs)

		fmt.Fprintf(&sb, "| %d | %d | %s | %s | %s | %s | %.1f | %d |\n", n, r.Frames.Count,
			fmtMS(r.Frames.P50), fmtMS(r.Frames.P95), fmtMS(r.Frames.P99), fmtMS(r.Frames.Max), r.CPUPercent,
			r.BytesFed)

		fmt.Fprintf(&sb, "  per-component p99 (they do not sum to the frame p99): feed %s, update+view %s, present %s; GCs %d, GC pause %s, highlight batches %d\n",
			fmtMS(r.Feed.P99), fmtMS(r.Render.P99), fmtMS(r.Present.P99), r.GCs, fmtMS(r.GCPause), r.Batches)

		worst, maxes := medianMax(runs), make([]string, len(runs))
		for i, run := range runs {
			maxes[i] = fmtMS(run.Frames.Max)
		}

		fmt.Fprintf(&sb, "  max per run: %s; median %s\n", strings.Join(maxes, ", "), fmtMS(worst))

		if pictures {
			fmt.Fprintf(&sb, "  picture %dx%d cells, placed in %d of %d frames\n", r.Picture[0], r.Picture[1], r.Pictured, r.Frames.Count)
		}

		fidelity(&sb, n, all, paths, r.PaneSizes, start, length, stagger)

		if n == maxBenchPanes {
			gateP99, gateMax = r.Frames.P99, worst
		}
	}

	t.Log("\n" + sb.String())

	gateFrames(t, "six-pane median", gateP99, gateMax)
}

// gateFrames holds a bench's frames to frameGate: it logs a p99 over the
// headroom and fails the test on a frame over the budget.
func gateFrames(t *testing.T, name string, p99, worst time.Duration) {
	t.Helper()

	fail, note := frameGate(name, p99, worst)
	if note != "" {
		t.Log(note)
	}

	if fail != "" {
		t.Error(fail)
	}
}

// medianRun returns the run with the median frame p99 (the upper middle for an even count).
func medianRun(runs []benchResult) benchResult {
	s := slices.Clone(runs)
	slices.SortFunc(s, func(a, b benchResult) int { return cmp.Compare(a.Frames.P99, b.Frames.P99) })

	return s[len(s)/2]
}

// fidelity reports how the replay differs from the recorded sessions: the
// recorded size against the pane size used, and the resizes skipped in the window.
func fidelity(sb *strings.Builder, panes int, all [][]rec.Record, paths []string, sizes [][2]int, start, length, stagger time.Duration) {
	for i := range panes {
		ri := i % len(all)
		r := all[ri]
		from := start + time.Duration(i)*stagger

		skipped := 0

		for _, c := range r[1:] {
			if c.Data == nil && c.At >= from && c.At < from+length {
				skipped++
			}
		}

		fmt.Fprintf(sb, "  %d panes, pane %d: %s recorded %dx%d, replayed at %dx%d, %d resizes skipped in window\n",
			panes, i+1, filepath.Base(paths[ri]), r[0].Cols, r[0].Rows, sizes[i][0], sizes[i][1], skipped)
	}
}

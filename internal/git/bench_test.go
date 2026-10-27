//go:build bench

package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	saveToDiffMedian = 300 * time.Millisecond
	snapshotBudget   = 1400 * time.Millisecond
)

func median(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)

	return s[len(s)/2]
}

func p95(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)

	return s[min(len(s)-1, (len(s)*95+99)/100-1)]
}

// timeIt returns how long f took.
func timeIt(t *testing.T, f func() error) time.Duration {
	t.Helper()

	start := time.Now()
	if err := f(); err != nil {
		t.Fatal(err)
	}

	return time.Since(start)
}

func TestBenchSaveToDiff(t *testing.T) {
	dir := bigRepo(t, 2000)
	src := BranchSource{Worktree: dir, Base: "main"}
	e := testEngine(t)

	prev, err := e.Diff(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}

	w := startWatch(t, dir, WatchOptions{})
	settle(t, w)

	var times []time.Duration

	for i := range 20 {
		rel := fmt.Sprintf("d%02d/f%04d.txt", i, i)

		var took time.Duration

		prev, took = saveToDiff(t, w, e, prev, src, rel, fmt.Sprintf("save %d\n", i))
		times = append(times, took)
	}

	t.Logf("save to diff over %d saves: median %v, p95 %v, all %v", len(times), median(times), p95(times), times)

	if m := median(times); m > saveToDiffMedian {
		t.Errorf("median %v exceeds %v", m, saveToDiffMedian)
	}

	// New files beside modified ones take the full-Diff path.
	for i := range 5 {
		writeFile(t, dir, fmt.Sprintf("d%02d/f%04d.txt", i, i+100), "modified\n")
	}

	prev, err = e.Diff(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}

	times = nil

	for i := range 20 {
		var took time.Duration

		prev, took = saveToDiff(t, w, e, prev, src, fmt.Sprintf("new%02d.txt", i), "a new file\n")
		times = append(times, took)
	}

	t.Logf("save of a new file beside 5 modified files over %d saves: median %v, p95 %v, all %v", len(times), median(times), p95(times), times)

	if m := median(times); m > saveToDiffMedian {
		t.Errorf("new-file median %v exceeds %v", m, saveToDiffMedian)
	}

	var snaps []time.Duration

	for range 5 {
		snaps = append(snaps, timeIt(t, func() error {
			_, err := Snapshot(context.Background(), dir, sessA, SnapshotOptions{TempDir: t.TempDir()})

			return err
		}))
	}

	t.Logf("snapshot of 2000 files: median %v, all %v", median(snaps), snaps)

	if m := median(snaps); m > snapshotBudget {
		t.Errorf("snapshot median %v exceeds %v", m, snapshotBudget)
	}
}

func isMuxRevealSource(top string) bool {
	mod, err := os.ReadFile(filepath.Join(top, "go.mod"))

	return err == nil && strings.Contains(string(mod), "module github.com/mhersson/muxreveal\n")
}

// TestBenchLargeRepo times a snapshot, a working tree and a branch diff of
// 20 edited files in the clone MUXREVEAL_BENCH_REPO names. It edits and
// snapshots that clone: never point it at a repository you care about.
func TestBenchLargeRepo(t *testing.T) {
	dir := os.Getenv("MUXREVEAL_BENCH_REPO")
	if dir == "" {
		t.Skip("MUXREVEAL_BENCH_REPO is not set")
	}

	isolateGit(t)

	ctx := context.Background()

	if top := gitOut(t, dir, "rev-parse", "--show-toplevel"); isMuxRevealSource(top) {
		t.Skipf("%s is the MuxReveal source tree; the bench edits files and writes snapshot refs in its target", top)
	}

	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	listed := gitOut(t, dir, "ls-files", "-z", "--", "*.go")
	files := strings.Split(strings.TrimRight(listed, "\x00"), "\x00")
	step := len(files) / 20

	for i := range 20 {
		p := filepath.Join(dir, files[i*step])

		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := f.WriteString("\n// bench edit\n"); err != nil {
			t.Fatal(err)
		}

		_ = f.Close()
	}

	t.Logf("tracked files: %d", len(strings.Split(gitOut(t, dir, "ls-files"), "\n")))

	measure := func(name string, op func() error) {
		cold := timeIt(t, op)

		var warm []time.Duration
		for range 5 {
			warm = append(warm, timeIt(t, op))
		}

		t.Logf("%s: cold %v, warm median %v, warm %v", name, cold, median(warm), warm)
	}

	measure("Snapshot", func() error {
		_, err := Snapshot(ctx, dir, sessA, SnapshotOptions{TempDir: t.TempDir()})

		return err
	})
	measure("WorkingTree", func() error {
		_, err := WorkingTree(ctx, r, t.TempDir(), 0)

		return err
	})
	// A fresh engine per call keeps the hunk cache out of the timing.
	measure("Diff against HEAD", func() error {
		res, err := NewEngine(Limits{}, t.TempDir()).Diff(ctx, BranchSource{Worktree: dir, Base: "HEAD"})
		if err == nil && len(res.Files) != 20 {
			err = fmt.Errorf("diff lists %d files, want 20", len(res.Files))
		}

		return err
	})
}

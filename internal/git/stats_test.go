package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func statsOf(t *testing.T, e *Engine, src Source) Stats {
	t.Helper()

	return statsOfContext(context.Background(), t, e, src)
}

// statsOfContext is statsOf with the context its git calls run under.
func statsOfContext(ctx context.Context, t *testing.T, e *Engine, src Source) Stats {
	t.Helper()

	got, err := e.Stats(ctx, src)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	return got
}

func totals(res Result) Stats {
	var s Stats

	for _, f := range res.Files {
		s.Files++
		s.Added += f.Added
		s.Removed += f.Removed
	}

	return s
}

func TestStatsMatchDiffTotals(t *testing.T) {
	t.Parallel()
	dir := branchRepo(t, func(dir string) {
		for _, n := range []string{"mod", "del", "ren"} {
			writeFile(t, dir, n+".txt", numbered(n, 20))
		}

		writeFile(t, dir, "bin.dat", "\x00\x01\x02\x03")
	}, func(dir string) {
		writeFile(t, dir, "mod.txt", strings.Replace(numbered("mod", 20), "mod 5\n", "mod five\nextra\n", 1))
		must(t, os.Remove(filepath.Join(dir, "del.txt")))
		must(t, os.Rename(filepath.Join(dir, "ren.txt"), filepath.Join(dir, "moved.txt")))
		writeFile(t, dir, "bin.dat", "\x00\x01\x02\x04")
		writeFile(t, dir, "untracked.txt", "u1\nu2\nu3\n")
		writeFile(t, dir, "staged.txt", "s1\n")
		gitOut(t, dir, "add", "staged.txt")
	})

	src := BranchSource{Worktree: dir, Base: "main"}
	want := totals(diffOK(t, testEngine(t), src))
	e := testEngine(t)
	got := statsOf(t, e, src)

	assertEmptyDir(t, e.tempDir)

	if got != want {
		t.Errorf("Stats = %+v, want %+v", got, want)
	}

	if want.Files != 6 || want.Added < 5 {
		t.Errorf("fixture totals look wrong: %+v", want)
	}
}

func TestStatsCountsEachKind(t *testing.T) {
	t.Parallel()
	dir := branchRepo(t, func(dir string) {
		writeFile(t, dir, "del.txt", numbered("del", 4))
		writeFile(t, dir, "bin.dat", "\x00a")
	}, func(dir string) {
		must(t, os.Remove(filepath.Join(dir, "del.txt")))
		writeFile(t, dir, "bin.dat", "\x00b")
		writeFile(t, dir, "untracked.txt", "u1\nu2\n")
	})

	got := statsOf(t, testEngine(t), BranchSource{Worktree: dir, Base: "main"})
	if want := (Stats{Files: 3, Added: 2, Removed: 4}); got != want {
		t.Errorf("Stats = %+v, want %+v", got, want)
	}
}

func TestStatsDefaultBaseAndClean(t *testing.T) {
	t.Parallel()
	dir := branchRepo(t, func(dir string) { writeFile(t, dir, "a.txt", "a\n") }, func(string) {})

	if got := statsOf(t, testEngine(t), BranchSource{Worktree: dir}); got != (Stats{}) {
		t.Errorf("Stats of a clean tree = %+v", got)
	}
}

func TestStatsRunsNoHunkPass(t *testing.T) {
	t.Parallel()

	dir := branchRepo(t, func(dir string) { writeFile(t, dir, "a.txt", numbered("a", 10)) }, func(dir string) {
		writeFile(t, dir, "a.txt", numbered("a", 10)+"more\n")
	})

	ctx, logPath := wrapGit(t, "")
	statsOfContext(ctx, t, testEngine(t), BranchSource{Worktree: dir, Base: "main"})

	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		t.Fatalf("fake git saw no calls: %v", err)
	}

	if got := hunkPasses(t, logPath); got != 0 {
		t.Errorf("hunk passes = %d, want none", got)
	}
}

func TestStatsErrors(t *testing.T) {
	t.Parallel()
	e := testEngine(t)

	if _, err := e.Stats(context.Background(), TurnSource{}); err == nil {
		t.Error("turn source: want an error")
	}

	dir := branchRepo(t, func(dir string) { writeFile(t, dir, "a.txt", "a\n") }, func(string) {})

	if _, err := e.Stats(context.Background(), BranchSource{Worktree: dir, Base: "--evil"}); err == nil {
		t.Error("option-like base: want an error")
	}

	if _, err := e.Stats(context.Background(), BranchSource{Worktree: t.TempDir()}); err == nil {
		t.Error("not a repo: want an error")
	}
}

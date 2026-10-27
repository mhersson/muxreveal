package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// updateRepo commits a.txt, b.txt, c.txt and dir/d.txt on main, then branches.
func updateRepo(t *testing.T) string {
	t.Helper()

	return branchRepo(t, func(dir string) {
		for _, n := range []string{"a.txt", "b.txt", "c.txt", "dir/d.txt", "dir/e.txt"} {
			writeFile(t, dir, n, numbered(n, 30))
		}
	}, func(dir string) {
		writeFile(t, dir, "a.txt", numbered("a.txt", 30)+"extra\n")
	})
}

func updateOK(t *testing.T, e *Engine, prev Result, src Source, b Batch) Result {
	t.Helper()

	res, err := e.Update(context.Background(), prev, src, b)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	assertEmptyDir(t, e.tempDir)
	checkInvariants(t, res)

	return res
}

// sameAsFull fails unless res equals a fresh Diff on a fresh engine.
func sameAsFull(t *testing.T, res Result, src Source) {
	t.Helper()

	want := diffOK(t, testEngine(t), src)
	if !reflect.DeepEqual(res, want) {
		t.Errorf("Update differs from a full Diff\n got: %s\nwant: %s", goldenJSON(t, res), goldenJSON(t, want))
	}
}

// listingCalls counts the listing passes in the fake git log, split by whether
// they were limited to paths.
func listingCalls(t *testing.T, logPath string) (restricted, full int) {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if err != nil {
		return 0, 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, " --raw ") {
			continue
		}

		if strings.Contains(line, ":(literal)") {
			restricted++
		} else {
			full++
		}
	}

	return restricted, full
}

func TestUpdateTouchedFileEqualsFullDiff(t *testing.T) {
	dir := updateRepo(t)
	src := BranchSource{Worktree: dir, Base: "main"}
	e := testEngine(t)
	prev := diffOK(t, e, src)

	logPath := fakeGit(t, "")

	writeFile(t, dir, "b.txt", numbered("b.txt", 30)+"new tail\n")

	before := hunkPasses(t, logPath)
	res := updateOK(t, e, prev, src, Batch{Paths: []string{"b.txt"}})

	got := hunkPasses(t, logPath) - before
	data, _ := os.ReadFile(logPath)

	sameAsFull(t, res, src)

	if got != 1 {
		t.Errorf("hunk passes = %d, want 1", got)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, " -U3 ") && (strings.Contains(line, "(literal)a.txt") || !strings.Contains(line, "(literal)b.txt")) {
			t.Errorf("hunk pass is not limited to b.txt: %s", line)
		}
	}
}

func TestUpdateShapesEqualFullDiff(t *testing.T) {
	tests := []struct {
		name    string
		change  func(dir string)
		touched []string
	}{
		{"modify", func(dir string) { writeFile(t, dir, "c.txt", "changed\n") }, []string{"c.txt"}},
		{"revert a changed file", func(dir string) { writeFile(t, dir, "a.txt", numbered("a.txt", 30)) }, []string{"a.txt"}},
		{"delete", func(dir string) { must(t, os.Remove(filepath.Join(dir, "b.txt"))) }, []string{"b.txt"}},
		{"untracked file", func(dir string) { writeFile(t, dir, "new.txt", "fresh\nfile\n") }, []string{"new.txt"}},
		{"removed directory", func(dir string) { must(t, os.RemoveAll(filepath.Join(dir, "dir"))) }, []string{"dir"}},
		{"touched path that never existed", func(dir string) {}, []string{"ghost.txt"}},
		{"two files", func(dir string) {
			writeFile(t, dir, "b.txt", "x\n")
			writeFile(t, dir, "c.txt", "y\n")
		}, []string{"b.txt", "c.txt"}},
		{"deleted file next to an untouched modification", func(dir string) { must(t, os.Remove(filepath.Join(dir, "dir/e.txt"))) }, []string{"dir/e.txt"}},
		{"identical content added while another file is deleted", func(dir string) {
			writeFile(t, dir, "moved.txt", numbered("c.txt", 30))
		}, []string{"moved.txt"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := updateRepo(t)
			src := BranchSource{Worktree: dir, Base: "main"}
			e := testEngine(t)

			if tt.name == "identical content added while another file is deleted" {
				must(t, os.Remove(filepath.Join(dir, "c.txt")))
			}

			prev := diffOK(t, e, src)

			tt.change(dir)

			res := updateOK(t, e, prev, src, Batch{Paths: tt.touched})
			sameAsFull(t, res, src)
		})
	}
}

func TestUpdateFallsBackToFullDiff(t *testing.T) {
	many := make([]string, 201)
	for i := range many {
		many[i] = fmt.Sprintf("many/f%03d.txt", i)
	}

	tests := []struct {
		name   string
		change func(dir string)
		batch  Batch
		// listed: the rename is only found by listing the touched paths first.
		listed bool
	}{
		{"rename", func(dir string) {
			must(t, os.Rename(filepath.Join(dir, "b.txt"), filepath.Join(dir, "b2.txt")))
		}, Batch{Paths: []string{"b.txt", "b2.txt"}}, true},
		{"git metadata", func(dir string) { writeFile(t, dir, "b.txt", "x\n") }, Batch{Paths: []string{"b.txt"}, Git: true}, false},
		{"overflow", func(dir string) { writeFile(t, dir, "b.txt", "x\n") }, Batch{Overflow: true}, false},
		{"ignore file", func(dir string) { writeFile(t, dir, "dir/.gitignore", "*.log\n") }, Batch{Paths: []string{"dir/.gitignore"}}, false},
		{"too many paths", func(dir string) { writeFile(t, dir, "b.txt", "x\n") }, Batch{Paths: many}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := updateRepo(t)
			src := BranchSource{Worktree: dir, Base: "main"}
			e := testEngine(t)
			prev := diffOK(t, e, src)

			tt.change(dir)

			logPath := fakeGit(t, "")
			res := updateOK(t, e, prev, src, tt.batch)
			restricted, full := listingCalls(t, logPath)

			sameAsFull(t, res, src)

			if full == 0 || (restricted != 0 && !tt.listed) {
				t.Errorf("listings: restricted=%d full=%d", restricted, full)
			}
		})
	}
}

func TestUpdateFallsBackWhenPrevHasRenames(t *testing.T) {
	dir := updateRepo(t)
	must(t, os.Rename(filepath.Join(dir, "b.txt"), filepath.Join(dir, "b2.txt")))

	src := BranchSource{Worktree: dir, Base: "main"}
	e := testEngine(t)
	prev := diffOK(t, e, src)

	if fileByPath(t, prev, "b2.txt").Status != Renamed {
		t.Fatal("setup: b2.txt is not a rename")
	}

	writeFile(t, dir, "c.txt", "x\n")

	logPath := fakeGit(t, "")
	res := updateOK(t, e, prev, src, Batch{Paths: []string{"c.txt"}})

	sameAsFull(t, res, src)

	if restricted, _ := listingCalls(t, logPath); restricted != 0 {
		t.Error("a restricted listing ran next to a rename")
	}
}

func TestUpdatePastTurnFallsBack(t *testing.T) {
	dir := turnRepo(t)
	writeFile(t, dir, "a.txt", "turn two\n")
	snap(t, dir, sessA, SnapshotOptions{})

	e := testEngine(t)
	src := TurnSource{Worktree: dir, Session: sessA, Turn: 1}
	prev := diffOK(t, e, src)

	if prev.Live {
		t.Fatal("setup: turn 1 is live")
	}

	logPath := fakeGit(t, "")
	res := updateOK(t, e, prev, src, Batch{Paths: []string{"a.txt"}})
	restricted, full := listingCalls(t, logPath)

	sameAsFull(t, res, src)

	if restricted != 0 || full == 0 {
		t.Errorf("listings: restricted=%d full=%d", restricted, full)
	}
}

func TestUpdateLiveTurn(t *testing.T) {
	dir := turnRepo(t)
	e := testEngine(t)
	src := TurnSource{Worktree: dir, Session: sessA}
	prev := diffOK(t, e, src)

	writeFile(t, dir, "b.txt", "edited after the prompt\n")

	logPath := fakeGit(t, "")
	res := updateOK(t, e, prev, src, Batch{Paths: []string{"b.txt"}})

	sameAsFull(t, res, src)

	if restricted, _ := listingCalls(t, logPath); restricted != 1 {
		t.Errorf("restricted listings = %d, want 1", restricted)
	}

	// A newer turn changes the old side: no incremental update.
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "c.txt", "later\n")

	logPath = fakeGit(t, "")
	res = updateOK(t, e, res, src, Batch{Paths: []string{"c.txt"}})

	sameAsFull(t, res, src)

	if restricted, _ := listingCalls(t, logPath); restricted != 0 {
		t.Error("a restricted listing ran after a new turn")
	}
}

func TestUpdateKeepsCaps(t *testing.T) {
	dir := branchRepo(t, func(dir string) {
		for i := range 6 {
			writeFile(t, dir, fmt.Sprintf("f%d.txt", i), numbered("x", 10))
		}
	}, func(dir string) {
		for i := range 4 {
			writeFile(t, dir, fmt.Sprintf("f%d.txt", i), numbered("y", 10))
		}
	})
	src := BranchSource{Worktree: dir, Base: "main"}
	e := NewEngine(Limits{Files: 5, Lines: 30}, t.TempDir())

	prev := diffOK(t, e, src)

	// The sixth changed file goes past the file cap, which a full Diff marks.
	writeFile(t, dir, "f4.txt", "z\n")
	writeFile(t, dir, "f5.txt", "z\n")

	res := updateOK(t, e, prev, src, Batch{Paths: []string{"f4.txt", "f5.txt"}})

	want := diffOK(t, NewEngine(Limits{Files: 5, Lines: 30}, t.TempDir()), src)
	if !reflect.DeepEqual(res, want) {
		t.Errorf("capped Update differs from a full Diff\n got: %s\nwant: %s", goldenJSON(t, res), goldenJSON(t, want))
	}

	// A touched file over the line cap is marked like a full Diff marks it.
	writeFile(t, dir, "f0.txt", numbered("w", 40))

	res = updateOK(t, e, res, src, Batch{Paths: []string{"f0.txt"}})
	want = diffOK(t, NewEngine(Limits{Files: 5, Lines: 30}, t.TempDir()), src)

	if !reflect.DeepEqual(res, want) || !res.Truncated {
		t.Errorf("line cap: got %s\nwant %s", goldenJSON(t, res), goldenJSON(t, want))
	}
}

func TestUpdateFallsBackPastTheByteCap(t *testing.T) {
	dir := branchRepo(t, func(dir string) {
		for i := range 4 {
			writeFile(t, dir, fmt.Sprintf("f%d.txt", i), numbered("x", 100))
		}
	}, func(dir string) {
		for i := range 3 {
			writeFile(t, dir, fmt.Sprintf("f%d.txt", i), numbered("y", 100))
		}
	})
	src := BranchSource{Worktree: dir, Base: "main"}
	e := NewEngine(Limits{Bytes: 5000}, t.TempDir())
	prev := diffOK(t, e, src)

	writeFile(t, dir, "f3.txt", numbered("y", 100))

	logPath := fakeGit(t, "")
	res := updateOK(t, e, prev, src, Batch{Paths: []string{"f3.txt"}})

	_, full := listingCalls(t, logPath)

	// Hunks cached from prev skip the byte budget, so compare on the same engine.
	if want := diffOK(t, e, src); !reflect.DeepEqual(res, want) {
		t.Errorf("Update differs from a full Diff\n got: %s\nwant: %s", goldenJSON(t, res), goldenJSON(t, want))
	}

	if full == 0 {
		t.Error("the merged files cross the byte cap but no full Diff ran")
	}
}

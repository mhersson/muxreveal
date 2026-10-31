package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// bigRepo commits n files in 50 directories on main and branches to feature.
func bigRepo(t *testing.T, n int) string {
	t.Helper()

	dir := newRepo(t)
	for i := range n {
		writeFile(t, dir, fmt.Sprintf("d%02d/f%04d.txt", i%50, i), numbered("line", 20))
	}

	commitAll(t, dir, "base")
	gitOut(t, dir, "checkout", "-q", "-b", "feature")

	return dir
}

// saveToDiff writes content to rel, waits for the watcher batch that names it
// and returns the Update of prev with that batch.
func saveToDiff(t *testing.T, w *Watcher, e *Engine, prev Result, src Source, rel, content string) Result {
	t.Helper()

	dir := src.(BranchSource).Worktree

	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var batch Batch

	for !slices.Contains(batch.Paths, rel) {
		select {
		case b, ok := <-w.Batches():
			if !ok {
				t.Fatal("batches closed")
			}

			batch = mergeBatches(batch, b)
		case err := <-w.Errors():
			t.Fatalf("watch error: %v", err)
		case <-time.After(watchWait):
			t.Fatalf("no batch named %s; saw %+v", rel, batch)
		}
	}

	res, err := e.Update(context.Background(), prev, src, batch)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	return res
}

func TestSavedFileReachesTheDiff(t *testing.T) {
	dir := bigRepo(t, 2000)
	src := BranchSource{Worktree: dir, Base: "main"}
	e := testEngine(t)
	ctx := context.Background()

	prev, err := e.Diff(ctx, src)
	if err != nil {
		t.Fatal(err)
	}

	w := startWatch(t, dir, WatchOptions{})
	settle(t, w)

	rel := "d07/f0007.txt"

	res := saveToDiff(t, w, e, prev, src, rel, "saved\n")

	if f := fileByPath(t, res, rel); f.Status != Modified || len(f.Hunks) == 0 {
		t.Errorf("%s: %+v", rel, f)
	}

	sameAsFull(t, res, src)
}

func TestTurnModeIsolatesTheLastTurn(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	writeFile(t, dir, "d.txt", "d\n")
	writeFile(t, dir, "e.txt", "e\n")
	commitAll(t, dir, "more")

	// Turn 1 edits a.txt.
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "a.txt", "turn one\n")

	// Turn 2 edits b.txt, deletes d.txt and adds an untracked file.
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "b.txt", "turn two\n")
	must(t, os.Remove(filepath.Join(dir, "d.txt")))
	writeFile(t, dir, "new.txt", "untracked\n")

	// Turn 3 edits dir/c.txt and commits mid-turn.
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "dir/c.txt", "turn three\n")
	commitAll(t, dir, "agent commit")

	e := testEngine(t)

	tests := []struct {
		turn int
		live bool
		want []string
	}{
		{0, true, []string{"dir/c.txt"}},
		{3, true, []string{"dir/c.txt"}},
		{2, false, []string{"b.txt", "d.txt", "new.txt"}},
		{1, false, []string{"a.txt"}},
	}

	for _, tt := range tests {
		res := diffOK(t, e, TurnSource{Worktree: dir, Session: sessA, Turn: tt.turn})

		got := pathsOf(res)
		slices.Sort(got)

		if !slices.Equal(got, tt.want) || res.Live != tt.live {
			t.Errorf("turn %d: files %v live=%v, want %v live=%v", tt.turn, got, res.Live, tt.want, tt.live)
		}
	}
}

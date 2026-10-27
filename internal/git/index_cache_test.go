package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// cacheRepo is a repository with two tracked files, a cache dir and a temp dir.
func cacheRepo(t *testing.T) (dir, cache, tmp string, r Repo) {
	t.Helper()

	dir = newRepo(t)
	writeFile(t, dir, "a.txt", "alpha\n")
	writeFile(t, dir, "b.txt", "beta\n")
	commitAll(t, dir, "init")

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	return dir, t.TempDir(), t.TempDir(), r
}

func treeOf(t *testing.T, r Repo, cache, tmp string) WorkTree {
	t.Helper()

	wt, err := WorkingTree(context.Background(), r, TreeOptions{TempDir: tmp, IndexCache: cache})
	if err != nil {
		t.Fatal(err)
	}

	return wt
}

func cacheFiles(t *testing.T, cache string) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(cache, "index", "*.idx"))
	if err != nil {
		t.Fatal(err)
	}

	return files
}

// wantCacheName is the cache file name for r's real index as it is now.
func wantCacheName(t *testing.T, r Repo) string {
	t.Helper()

	info, err := os.Stat(filepath.Join(r.GitDir, "index"))
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(r.GitDir))

	return fmt.Sprintf("%s-%d-%d-%d.idx", hex.EncodeToString(sum[:])[:16],
		info.Size(), info.ModTime().UnixNano(), info.Sys().(*syscall.Stat_t).Ino)
}

func TestWorkingTreeWritesTheIndexCache(t *testing.T) {
	dir, cache, tmp, r := cacheRepo(t)
	writeFile(t, dir, "a.txt", "changed\n")

	treeOf(t, r, cache, tmp)

	files := cacheFiles(t, cache)
	if len(files) != 1 || filepath.Base(files[0]) != wantCacheName(t, r) {
		t.Fatalf("cache files = %v, want one named %s", files, wantCacheName(t, r))
	}

	assertEmptyDir(t, tmp)
}

// A hit with nothing left to stage leaves the cache file untouched: same inode
// and mtime, where a miss would have written a new one.
func TestWorkingTreeReusesTheCache(t *testing.T) {
	dir, cache, tmp, r := cacheRepo(t)
	writeFile(t, dir, "a.txt", "changed\n")
	treeOf(t, r, cache, tmp)

	file := cacheFiles(t, cache)[0]

	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(20 * time.Millisecond)
	treeOf(t, r, cache, tmp)

	after, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("cache file was rewritten on a hit")
	}
}

func TestWorkingTreeIgnoresAStaleCache(t *testing.T) {
	dir, cache, tmp, r := cacheRepo(t)
	treeOf(t, r, cache, tmp)

	old := cacheFiles(t, cache)
	if len(old) != 1 {
		t.Fatalf("cache files = %v", old)
	}

	writeFile(t, dir, "c.txt", "gamma\n")
	gitOut(t, dir, "add", "c.txt")

	want := gitOut(t, dir, "write-tree") // may rewrite the index, so before the call under test

	if wt := treeOf(t, r, cache, tmp); wt.Tree != want {
		t.Fatalf("tree = %s, want %s", wt.Tree, want)
	}

	files := cacheFiles(t, cache)
	if len(files) != 1 || filepath.Base(files[0]) != wantCacheName(t, r) || files[0] == old[0] {
		t.Fatalf("cache files = %v, want only %s", files, wantCacheName(t, r))
	}
}

func TestWorkingTreeNeverWritesTheUserIndex(t *testing.T) {
	dir, cache, tmp, r := cacheRepo(t)
	writeFile(t, dir, "a.txt", "changed\n")
	writeFile(t, dir, "new.txt", "new\n")

	path := filepath.Join(r.GitDir, "index")
	data, _ := os.ReadFile(path)
	before, _ := os.Stat(path)

	for range 5 {
		treeOf(t, r, cache, tmp)
	}

	got, _ := os.ReadFile(path)
	after, _ := os.Stat(path)

	if !bytes.Equal(data, got) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("the user's index changed")
	}
}

func TestTwoWorktreesKeepTwoCaches(t *testing.T) {
	dir, cache, tmp, r := cacheRepo(t)
	other := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-wt")
	gitOut(t, dir, "worktree", "add", "-q", "-b", "side", other)

	r2, err := Open(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}

	treeOf(t, r, cache, tmp)
	treeOf(t, r2, cache, tmp)

	if files := cacheFiles(t, cache); len(files) != 2 {
		t.Fatalf("cache files = %v, want two", files)
	}

	if names := []string{wantCacheName(t, r), wantCacheName(t, r2)}; names[0] == names[1] {
		t.Fatal("two worktrees share a cache name")
	}
}

func TestCacheWriteIsAtomic(t *testing.T) {
	dir, cache, tmp, r := cacheRepo(t)
	for i := range 40 {
		writeFile(t, dir, fmt.Sprintf("f%02d.txt", i), "x\n")
	}

	treeOf(t, r, cache, tmp)

	file := cacheFiles(t, cache)[0]

	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	c := openIndexCache(cache, r.GitDir, filepath.Join(r.GitDir, "index"))
	next := filepath.Join(t.TempDir(), "next")

	body := append(bytes.Clone(want), make([]byte, 1<<20)...)

	//nolint:gosec // path under t.TempDir
	if err := os.WriteFile(next, body, 0o600); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	done := make(chan struct{})

	wg.Go(func() {
		for range 50 {
			c.store(next)
		}

		close(done)
	})

	for {
		got, err := os.ReadFile(file)
		if err == nil && !bytes.Equal(got, body) && !bytes.Equal(got, want) {
			t.Fatalf("partial cache file of %d bytes", len(got))
		}

		select {
		case <-done:
			wg.Wait()

			return
		default:
		}
	}
}

func TestWorkingTreeSeesAnEditAfterTheCache(t *testing.T) {
	dir, cache, tmp, r := cacheRepo(t)

	for i := range 10 {
		writeFile(t, dir, "a.txt", fmt.Sprintf("value %d\n", i%10))
		treeOf(t, r, cache, tmp)

		// Same size, written right after a cached call.
		writeFile(t, dir, "a.txt", fmt.Sprintf("value %d\n", (i+1)%10))

		wt := treeOf(t, r, cache, tmp)
		if got := gitOut(t, dir, "rev-parse", wt.Tree+":a.txt"); got != gitOut(t, dir, "hash-object", "a.txt") {
			t.Fatalf("round %d: tree has stale a.txt", i)
		}
	}
}

func TestWorkingTreeKeepsUntrackedOutOfTheCache(t *testing.T) {
	dir, cache, tmp, r := cacheRepo(t)
	writeFile(t, dir, "new.txt", "new\n")

	for range 2 {
		if wt := treeOf(t, r, cache, tmp); len(wt.Untracked) != 1 || wt.Untracked[0] != "new.txt" {
			t.Fatalf("untracked = %v", wt.Untracked)
		}
	}
}

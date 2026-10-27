package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

const watchWait = 5 * time.Second

var fastWatch = WatchOptions{Debounce: 30 * time.Millisecond, MaxDelay: 300 * time.Millisecond}

func startWatch(t *testing.T, dir string, opts WatchOptions) *Watcher {
	t.Helper()

	ctx := context.Background()

	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	w, err := Watch(ctx, r, opts)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = w.Close() })

	return w
}

// collect gathers batches until done accepts everything seen so far, failing at
// the deadline.
func collect(t *testing.T, w *Watcher, done func(Batch) bool) Batch {
	t.Helper()

	var all Batch

	deadline := time.After(watchWait)

	for {
		select {
		case b, ok := <-w.Batches():
			if !ok {
				t.Fatalf("batches closed; saw %+v", all)
			}

			all = mergeBatches(all, b)
			if done(all) {
				return all
			}
		case err := <-w.Errors():
			t.Fatalf("watch error: %v", err)
		case <-deadline:
			t.Fatalf("timed out; saw %+v", all)
		}
	}
}

func waitPaths(t *testing.T, w *Watcher, want ...string) Batch {
	t.Helper()

	return collect(t, w, func(b Batch) bool {
		for _, p := range want {
			if !slices.Contains(b.Paths, p) {
				return false
			}
		}

		return true
	})
}

// quiet fails when a batch satisfying bad arrives within d.
func quiet(t *testing.T, w *Watcher, d time.Duration, bad func(Batch) bool) {
	t.Helper()

	timer := time.After(d)

	for {
		select {
		case b, ok := <-w.Batches():
			if !ok {
				return
			}

			if bad(b) {
				t.Fatalf("unexpected batch %+v", b)
			}
		case <-timer:
			return
		}
	}
}

func anyPathWithPrefix(b Batch, prefix string) bool {
	return slices.ContainsFunc(b.Paths, func(p string) bool { return strings.HasPrefix(p, prefix) })
}

func watchedDirs(t *testing.T, w *Watcher) []string {
	t.Helper()

	return w.fs.WatchList()
}

func hasWatch(t *testing.T, w *Watcher, dir string) bool {
	t.Helper()

	return slices.Contains(watchedDirs(t, w), dir)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(watchWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// settle waits until nothing arrives for a debounce plus a margin.
func settle(t *testing.T, w *Watcher) {
	t.Helper()

	for {
		select {
		case <-w.Batches():
		case <-time.After(400 * time.Millisecond):
			return
		}
	}
}

func baseRepo(t *testing.T) string {
	t.Helper()

	dir := newRepo(t)
	writeFile(t, dir, "tracked.txt", "one\n")
	writeFile(t, dir, "src/a.go", "package a\n")
	commitAll(t, dir, "init")

	return dir
}

func TestWatchWriteTrackedFile(t *testing.T) {
	dir := baseRepo(t)
	w := startWatch(t, dir, fastWatch)

	writeFile(t, dir, "tracked.txt", "two\n")

	b := waitPaths(t, w, "tracked.txt")
	if b.Git || b.Overflow {
		t.Fatalf("plain write flagged %+v", b)
	}
}

func TestWatchNewNestedDirectory(t *testing.T) {
	dir := baseRepo(t)
	w := startWatch(t, dir, fastWatch)

	writeFile(t, dir, "x/y/z/new.txt", "n\n")
	waitPaths(t, w, "x/y/z/new.txt")

	if !hasWatch(t, w, filepath.Join(dir, "x", "y", "z")) {
		t.Fatalf("no watch on the new directory: %v", watchedDirs(t, w))
	}

	writeFile(t, dir, "x/y/z/second.txt", "n\n")
	waitPaths(t, w, "x/y/z/second.txt")
}

func TestWatchDeletedAndRecreatedDirectory(t *testing.T) {
	dir := baseRepo(t)
	w := startWatch(t, dir, fastWatch)

	if err := os.RemoveAll(filepath.Join(dir, "src")); err != nil {
		t.Fatal(err)
	}

	waitPaths(t, w, "src/a.go")
	settle(t, w)

	writeFile(t, dir, "src/b.go", "package a\n")
	waitPaths(t, w, "src/b.go")

	writeFile(t, dir, "src/c.go", "package a\n")
	waitPaths(t, w, "src/c.go")

	// Removed and recreated before the debounce fires.
	if err := os.RemoveAll(filepath.Join(dir, "src")); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "src/d.go", "package a\n")
	waitPaths(t, w, "src/d.go")
	settle(t, w)

	writeFile(t, dir, "src/e.go", "package a\n")
	waitPaths(t, w, "src/e.go")
}

func TestWatchIgnoredDirectoryGetsNoWatch(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "node_modules/\n*.log\n")
	writeFile(t, dir, "tracked.txt", "one\n")
	writeFile(t, dir, "node_modules/pkg/index.js", "x\n")
	commitAll(t, dir, "init")

	w := startWatch(t, dir, fastWatch)

	for _, d := range watchedDirs(t, w) {
		if strings.Contains(d, "node_modules") {
			t.Fatalf("watch on ignored directory %s", d)
		}
	}

	writeFile(t, dir, "node_modules/pkg/index.js", "y\n")
	writeFile(t, dir, "visible.txt", "v\n")

	b := waitPaths(t, w, "visible.txt")
	if anyPathWithPrefix(b, "node_modules") {
		t.Fatalf("ignored path in %+v", b)
	}
}

func TestWatchNewIgnoredDirectoryWithManyFiles(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "node_modules/\n")
	writeFile(t, dir, "tracked.txt", "one\n")
	commitAll(t, dir, "init")

	w := startWatch(t, dir, fastWatch)

	for i := range 1000 {
		writeFile(t, dir, fmt.Sprintf("node_modules/p%d/index.js", i%20), fmt.Sprint(i))
	}

	writeFile(t, dir, "visible.txt", "v\n")

	b := waitPaths(t, w, "visible.txt")
	if anyPathWithPrefix(b, "node_modules") {
		t.Fatalf("ignored path in %+v", b)
	}

	for _, d := range watchedDirs(t, w) {
		if strings.Contains(d, "node_modules") {
			t.Fatalf("watch on ignored directory %s", d)
		}
	}

	quiet(t, w, 300*time.Millisecond, func(b Batch) bool { return anyPathWithPrefix(b, "node_modules") })
}

func TestWatchIgnoredFileInWatchedDirectory(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "*.log\n")
	writeFile(t, dir, "src/a.go", "package a\n")
	commitAll(t, dir, "init")

	w := startWatch(t, dir, fastWatch)

	writeFile(t, dir, "src/debug.log", "noise\n")
	writeFile(t, dir, "src/a.go", "package b\n")

	b := waitPaths(t, w, "src/a.go")
	if slices.Contains(b.Paths, "src/debug.log") {
		t.Fatalf("ignored file listed: %+v", b)
	}

	// An ignored file created inside a new directory is dropped too.
	writeFile(t, dir, "fresh/keep.txt", "k\n")
	writeFile(t, dir, "fresh/skip.log", "s\n")

	b = waitPaths(t, w, "fresh/keep.txt")
	if slices.Contains(b.Paths, "fresh/skip.log") {
		t.Fatalf("ignored file listed: %+v", b)
	}
}

func TestWatchTrackedFileMatchingIgnorePatternIsReported(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "*.log\n")
	writeFile(t, dir, "kept.log", "one\n")
	commitAll(t, dir, "init")
	gitOut(t, dir, "add", "-f", "kept.log")

	w := startWatch(t, dir, fastWatch)

	writeFile(t, dir, "kept.log", "two\n")
	waitPaths(t, w, "kept.log")
}

func TestWatchMainCheckoutSkipsWorktreesDirectory(t *testing.T) {
	dir := baseRepo(t)
	writeFile(t, dir, ".worktrees/feat/file.txt", "x\n")

	w := startWatch(t, dir, fastWatch)

	for _, d := range watchedDirs(t, w) {
		if strings.Contains(d, ".worktrees") {
			t.Fatalf("watch inside .worktrees: %s", d)
		}
	}

	writeFile(t, dir, ".worktrees/feat/file.txt", "y\n")
	writeFile(t, dir, "tracked.txt", "two\n")

	b := waitPaths(t, w, "tracked.txt")
	if anyPathWithPrefix(b, ".worktrees") {
		t.Fatalf(".worktrees listed: %+v", b)
	}
}

func TestWatchCommitSetsGitWithoutGitPaths(t *testing.T) {
	dir := baseRepo(t)

	w := startWatch(t, dir, fastWatch)

	writeFile(t, dir, "tracked.txt", "two\n")
	waitPaths(t, w, "tracked.txt")
	settle(t, w)

	gitOut(t, dir, "commit", "-q", "-am", "second")

	b := collect(t, w, func(b Batch) bool { return b.Git })
	if anyPathWithPrefix(b, ".git") {
		t.Fatalf("git internals listed: %+v", b)
	}
}

func TestWatchRefChangesSetGit(t *testing.T) {
	dir := baseRepo(t)

	w := startWatch(t, dir, fastWatch)

	gitOut(t, dir, "update-ref", "refs/heads/other", "HEAD")
	collect(t, w, func(b Batch) bool { return b.Git })
	settle(t, w)

	gitOut(t, dir, "update-ref", "refs/heads/feat/one", "HEAD")
	collect(t, w, func(b Batch) bool { return b.Git })
	settle(t, w)

	// The new refs/heads/feat directory has a watch by now.
	gitOut(t, dir, "update-ref", "refs/heads/feat/two", "HEAD")
	collect(t, w, func(b Batch) bool { return b.Git })
	settle(t, w)

	gitOut(t, dir, "pack-refs", "--all")
	collect(t, w, func(b Batch) bool { return b.Git })
}

func TestWatchSnapshotRefsDoNotWake(t *testing.T) {
	t.Run("directory created after start", func(t *testing.T) {
		dir := baseRepo(t)
		w := startWatch(t, dir, fastWatch)

		gitOut(t, dir, "update-ref", "refs/muxreveal/x/1", "HEAD")
		quiet(t, w, 500*time.Millisecond, func(Batch) bool { return true })

		gitOut(t, dir, "update-ref", "refs/muxreveal/x/2", "HEAD")
		quiet(t, w, 500*time.Millisecond, func(Batch) bool { return true })
	})

	t.Run("directory exists at start", func(t *testing.T) {
		dir := baseRepo(t)
		gitOut(t, dir, "update-ref", "refs/muxreveal/x/1", "HEAD")

		w := startWatch(t, dir, fastWatch)

		for _, d := range watchedDirs(t, w) {
			if strings.Contains(d, "muxreveal") {
				t.Fatalf("watch on snapshot refs: %s", d)
			}
		}

		gitOut(t, dir, "update-ref", "refs/muxreveal/x/2", "HEAD")
		quiet(t, w, 500*time.Millisecond, func(Batch) bool { return true })
	})
}

func TestWatchBurstCoalesces(t *testing.T) {
	dir := baseRepo(t)
	w := startWatch(t, dir, fastWatch)

	want := make([]string, 0, 1000)

	for i := range 1000 {
		rel := fmt.Sprintf("burst/f%04d.txt", i)
		want = append(want, rel)
		writeFile(t, dir, rel, "x\n")
	}

	batches := 0

	seen := map[string]bool{}
	deadline := time.After(watchWait)

	for len(seen) < len(want) {
		select {
		case b := <-w.Batches():
			batches++

			for _, p := range b.Paths {
				seen[p] = true
			}
		case <-deadline:
			t.Fatalf("saw %d of %d paths", len(seen), len(want))
		}
	}

	for _, p := range want {
		if !seen[p] {
			t.Fatalf("missing %s", p)
		}
	}

	if batches > 20 {
		t.Fatalf("%d batches for one burst", batches)
	}
}

func TestWatchSteadyStreamIsSentByMaxDelay(t *testing.T) {
	dir := baseRepo(t)
	w := startWatch(t, dir, WatchOptions{Debounce: 400 * time.Millisecond})

	stop := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		defer close(finished)

		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				_ = os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte(fmt.Sprint(i)), 0o644)
			}
		}
	}()

	defer func() {
		close(stop)
		<-finished
	}()

	start := time.Now()

	select {
	case <-w.Batches():
		// Events every 50 ms keep resetting the 400 ms debounce, so only the
		// 1 s maximum delay can have sent this batch.
		if d := time.Since(start); d < 800*time.Millisecond || d > 2*time.Second {
			t.Fatalf("first batch after %v, want about 1s", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no batch while writes continue")
	}
}

func TestWatchBatchWaitsForTheDebounce(t *testing.T) {
	dir := baseRepo(t)
	w := startWatch(t, dir, WatchOptions{Debounce: 400 * time.Millisecond})

	start := time.Now()

	writeFile(t, dir, "tracked.txt", "two\n")

	select {
	case <-w.Batches():
		if d := time.Since(start); d < 400*time.Millisecond {
			t.Fatalf("batch after %v, before the debounce", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no batch")
	}
}

func TestWatchGitignoreChangeAddsDirectoryWatch(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "vendor/\n")
	writeFile(t, dir, "vendor/lib/lib.go", "package lib\n")
	writeFile(t, dir, "tracked.txt", "one\n")
	commitAll(t, dir, "init")

	w := startWatch(t, dir, fastWatch)

	if hasWatch(t, w, filepath.Join(dir, "vendor")) {
		t.Fatal("ignored directory watched")
	}

	writeFile(t, dir, ".gitignore", "")

	eventually(t, "watch on the un-ignored directory", func() bool {
		return hasWatch(t, w, filepath.Join(dir, "vendor", "lib"))
	})

	waitPaths(t, w, ".gitignore")
	writeFile(t, dir, "vendor/lib/more.go", "package lib\n")
	waitPaths(t, w, "vendor/lib/more.go")
}

func TestWatchLinkedWorktree(t *testing.T) {
	dir := baseRepo(t)
	linked := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-q", "-b", "feat", linked)

	linked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatal(err)
	}

	w := startWatch(t, linked, fastWatch)

	writeFile(t, linked, "tracked.txt", "linked\n")
	b := waitPaths(t, w, "tracked.txt")

	if anyPathWithPrefix(b, ".git") {
		t.Fatalf("git file listed: %+v", b)
	}

	settle(t, w)
	gitOut(t, linked, "commit", "-q", "-am", "in linked")
	collect(t, w, func(b Batch) bool { return b.Git })
	settle(t, w)

	// A branch moved from the main checkout shows up through the shared refs.
	gitOut(t, dir, "update-ref", "refs/heads/elsewhere", "HEAD")
	collect(t, w, func(b Batch) bool { return b.Git })
}

func TestWatchCheckoutOfManyFiles(t *testing.T) {
	dir := newRepo(t)

	for i := range 2000 {
		writeFile(t, dir, fmt.Sprintf("d%d/f%d.txt", i%40, i), "base\n")
	}

	commitAll(t, dir, "base")
	gitOut(t, dir, "checkout", "-q", "-b", "other")

	for i := range 2000 {
		writeFile(t, dir, fmt.Sprintf("d%d/f%d.txt", i%40, i), "other\n")
	}

	commitAll(t, dir, "other")
	gitOut(t, dir, "checkout", "-q", "main")

	w := startWatch(t, dir, fastWatch)

	gitOut(t, dir, "checkout", "-q", "other")

	// Either every changed path arrives, or the kernel queue overflowed and the
	// watcher says so.
	b := collect(t, w, func(b Batch) bool { return b.Overflow || (b.Git && len(b.Paths) >= 2000) })
	if anyPathWithPrefix(b, ".git") {
		t.Fatalf("git internals listed")
	}
}

func TestWatchCheckIgnoreCallsAreBatched(t *testing.T) {
	dir := baseRepo(t)
	w := startWatch(t, dir, WatchOptions{Debounce: 200 * time.Millisecond, MaxDelay: time.Second})
	log := fakeGit(t, "")

	for i := range 50 {
		writeFile(t, dir, fmt.Sprintf("many/f%d.txt", i), "x\n")
	}

	waitPaths(t, w, "many/f49.txt")

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	calls := strings.Count(string(data), "check-ignore")
	if calls == 0 || calls > 5 {
		t.Fatalf("%d check-ignore calls for 50 files:\n%s", calls, data)
	}
}

func TestWatchCloseStopsEverything(t *testing.T) {
	dir := baseRepo(t)

	before := runtime.NumGoroutine()

	ctx := context.Background()

	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	w, err := Watch(ctx, r, fastWatch)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "tracked.txt", "two\n")

	start := time.Now()

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if d := time.Since(start); d > time.Second {
		t.Fatalf("close took %v", d)
	}

	// Events after Close are dropped without a panic.
	writeFile(t, dir, "tracked.txt", "three\n")

	for range w.Batches() {
	}

	if err := w.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	eventually(t, "goroutines to settle", func() bool { return runtime.NumGoroutine() <= before })
}

func TestWatchContextEndClosesBatches(t *testing.T) {
	dir := baseRepo(t)

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	w, err := Watch(ctx, r, fastWatch)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = w.Close() })
	cancel()

	select {
	case _, ok := <-w.Batches():
		if ok {
			t.Fatal("batch after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("batches not closed after the context ended")
	}
}

func TestWatchSlowConsumerKeepsLatestChanges(t *testing.T) {
	dir := baseRepo(t)
	w := startWatch(t, dir, fastWatch)
	log := fakeGit(t, "")

	// Nobody reads, so the first batch waits for its consumer while the
	// second flush merges into it. The ignore check marks the first flush.
	writeFile(t, dir, "tracked.txt", "a\n")

	eventually(t, "the first batch to be flushed", func() bool {
		data, _ := os.ReadFile(log)

		return strings.Contains(string(data), "check-ignore")
	})

	writeFile(t, dir, "src/a.go", "package a // b\n")
	waitPaths(t, w, "tracked.txt", "src/a.go")
}

func TestWatchErrorNamesInotifyLimit(t *testing.T) {
	err := watchError("/repo/dir", syscall.ENOSPC)
	if !errors.Is(err, syscall.ENOSPC) || !strings.Contains(err.Error(), "fs.inotify.max_user_watches") {
		t.Fatalf("got %v", err)
	}

	err = watchError("/repo/dir", syscall.EINVAL)
	if strings.Contains(err.Error(), "max_user_watches") {
		t.Fatalf("unrelated error names the limit: %v", err)
	}
}

func TestMergeBatches(t *testing.T) {
	got := mergeBatches(Batch{Paths: []string{"b", "a"}, Git: true}, Batch{Paths: []string{"c", "a"}, Overflow: true})

	want := Batch{Paths: []string{"a", "b", "c"}, Git: true, Overflow: true}
	if !slices.Equal(got.Paths, want.Paths) || got.Git != want.Git || got.Overflow != want.Overflow {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestWatchSkipsSubmodulesAndNestedRepositories(t *testing.T) {
	sub := newRepo(t)
	writeFile(t, sub, "src/a.txt", "a\n")
	commitAll(t, sub, "sub")

	dir := baseRepo(t)
	gitOut(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "vend")
	gitOut(t, dir, "commit", "-q", "-m", "add submodule")

	nested := filepath.Join(dir, "nested")
	writeFile(t, nested, "inner/b.txt", "b\n")
	gitOut(t, nested, "init", "-q", "-b", "main")

	w := startWatch(t, dir, fastWatch)

	for _, d := range watchedDirs(t, w) {
		if strings.HasPrefix(d, filepath.Join(dir, "vend")) || strings.HasPrefix(d, nested) {
			t.Fatalf("watch inside a nested repository: %s", d)
		}
	}

	writeFile(t, dir, "vend/src/a.txt", "changed\n")
	writeFile(t, dir, "nested/inner/b.txt", "changed\n")
	writeFile(t, dir, "tracked.txt", "two\n")

	b := waitPaths(t, w, "tracked.txt")
	if anyPathWithPrefix(b, "vend/") || anyPathWithPrefix(b, "nested/") {
		t.Fatalf("nested repository path listed: %+v", b)
	}
}

func TestWatchErrorNamesInstanceLimit(t *testing.T) {
	err := instanceError("/repo", syscall.EMFILE)
	if !errors.Is(err, syscall.EMFILE) || !strings.Contains(err.Error(), "fs.inotify.max_user_instances") {
		t.Fatalf("got %v", err)
	}

	if err := instanceError("/repo", syscall.EINVAL); strings.Contains(err.Error(), "max_user_instances") {
		t.Fatalf("unrelated error names the limit: %v", err)
	}
}

func TestWatchOverflowRewalksUnwatchedDirectories(t *testing.T) {
	dir := baseRepo(t)

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	fsw, err := fsnotify.NewBufferedWatcher(eventBuffer)
	if err != nil {
		t.Fatal(err)
	}

	// No loop goroutine runs, so the Create events below are never handled.
	w := &Watcher{repo: r, fs: fsw, watched: map[string]struct{}{}}

	t.Cleanup(func() { _ = fsw.Close() })

	if err := w.start(context.Background()); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "fresh/deep/a.go", "package a\n")

	if hasWatch(t, w, filepath.Join(dir, "fresh")) {
		t.Fatal("fresh is watched before the flush")
	}

	p := newPending()
	p.overflow = true

	b, err := w.flush(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{"fresh", "fresh/deep"} {
		if !hasWatch(t, w, filepath.Join(dir, d)) {
			t.Errorf("%s has no watch after an overflow", d)
		}
	}

	if !b.Overflow || !slices.Contains(b.Paths, "fresh/deep/a.go") {
		t.Errorf("batch = %+v, want overflow and fresh/deep/a.go", b)
	}
}

// eventWatcher is a Watcher that was never started, for feeding handle
// synthetic events (what kqueue reports on macOS) on any platform.
func eventWatcher(t *testing.T) *Watcher {
	t.Helper()

	dir := baseRepo(t)

	r, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		repo:       r,
		refsDir:    filepath.Join(r.CommonDir, "refs"),
		ownRefsDir: filepath.Join(r.CommonDir, "refs", "muxreveal"),
		watched:    map[string]struct{}{},
	}

	for _, d := range []string{
		r.GitDir, w.refsDir, filepath.Join(w.refsDir, "heads"), w.ownRefsDir, filepath.Join(r.Root, "src"),
	} {
		w.watched[d] = struct{}{}
	}

	return w
}

func TestKqueueEvents(t *testing.T) {
	w := eventWatcher(t)
	gitDir, refs := w.repo.GitDir, w.refsDir

	tests := []struct {
		name     string
		ev       fsnotify.Event
		wantGit  bool
		wantPath string
		accepted bool
	}{
		{name: "dir write on refs", ev: fsnotify.Event{Name: refs, Op: fsnotify.Write}},
		{name: "dir write on refs/heads", ev: fsnotify.Event{Name: filepath.Join(refs, "heads"), Op: fsnotify.Write}},
		{name: "dir write on own refs", ev: fsnotify.Event{Name: w.ownRefsDir, Op: fsnotify.Write}},
		{name: "file write in own refs", ev: fsnotify.Event{Name: filepath.Join(w.ownRefsDir, "s1"), Op: fsnotify.Write}},
		{name: "chmod on index", ev: fsnotify.Event{Name: filepath.Join(gitDir, "index"), Op: fsnotify.Chmod}},
		{name: "chmod on a ref", ev: fsnotify.Event{Name: filepath.Join(refs, "heads", "main"), Op: fsnotify.Chmod}},
		{name: "write on HEAD", ev: fsnotify.Event{Name: filepath.Join(gitDir, "HEAD"), Op: fsnotify.Write}, wantGit: true, accepted: true},
		{name: "write on index", ev: fsnotify.Event{Name: filepath.Join(gitDir, "index"), Op: fsnotify.Write}, wantGit: true, accepted: true},
		{name: "write on a ref", ev: fsnotify.Event{Name: filepath.Join(refs, "heads", "main"), Op: fsnotify.Write}, wantGit: true, accepted: true},
		{name: "write plus chmod on a ref", ev: fsnotify.Event{Name: filepath.Join(refs, "heads", "main"), Op: fsnotify.Write | fsnotify.Chmod}, wantGit: true, accepted: true},
		{name: "dir write in worktree", ev: fsnotify.Event{Name: filepath.Join(w.repo.Root, "src"), Op: fsnotify.Write}},
		{name: "file write in worktree", ev: fsnotify.Event{Name: filepath.Join(w.repo.Root, "src", "a.go"), Op: fsnotify.Write}, wantPath: "src/a.go", accepted: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPending()
			before := w.Seq()

			got := w.handle(tt.ev, p)

			if got != tt.accepted || p.git != tt.wantGit {
				t.Errorf("handle = %v, git = %v; want %v, %v", got, p.git, tt.accepted, tt.wantGit)
			}

			if _, ok := p.paths[tt.wantPath]; tt.wantPath != "" && !ok {
				t.Errorf("paths = %v, want %q", p.paths, tt.wantPath)
			}

			if tt.wantPath == "" && len(p.paths) != 0 {
				t.Errorf("paths = %v, want none", p.paths)
			}

			if rose := w.Seq() - before; (rose == 1) != tt.accepted || rose > 1 {
				t.Errorf("Seq rose by %d, accepted = %v", rose, tt.accepted)
			}
		})
	}
}

func TestSeqCountsEventsBeforeTheBatch(t *testing.T) {
	dir := baseRepo(t)
	opts := WatchOptions{Debounce: 400 * time.Millisecond, MaxDelay: 2 * time.Second, SeqBase: 100}
	w := startWatch(t, dir, opts)

	if got := w.Seq(); got != 100 {
		t.Fatalf("Seq at start = %d, want the base 100", got)
	}

	writeFile(t, dir, "tracked.txt", "two\n")

	eventually(t, "Seq to rise", func() bool { return w.Seq() > 100 })

	select {
	case b := <-w.Batches():
		t.Fatalf("batch %+v arrived before the debounce", b)
	default:
	}

	waitPaths(t, w, "tracked.txt")
}

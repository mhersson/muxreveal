package git

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

func newWatchSet(t *testing.T) *WatchSet {
	t.Helper()

	s := NewWatchSet(context.Background(), fastWatch)

	t.Cleanup(func() { _ = s.Close() })

	return s
}

func nextRootBatch(t *testing.T, s *WatchSet) RootBatch {
	t.Helper()

	select {
	case b, ok := <-s.Batches():
		if !ok {
			t.Fatal("batches closed")
		}

		return b
	case <-time.After(watchWait):
		t.Fatal("no batch")
	}

	return RootBatch{}
}

func TestWatchSetSharesARoot(t *testing.T) {
	dir := newRepo(t)
	s := newWatchSet(t)
	r := openRepo(t, dir)

	rel1, err := s.Acquire(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	rel2, err := s.Acquire(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "a.txt", "x")

	b := nextRootBatch(t, s)
	if b.Root != r.Root || len(b.Batch.Paths) != 1 || b.Batch.Paths[0] != "a.txt" {
		t.Fatalf("batch = %+v", b)
	}

	select {
	case extra := <-s.Batches():
		t.Fatalf("second batch for one change: %+v", extra)
	case <-time.After(300 * time.Millisecond):
	}

	rel1()
	// One reference left: still watched.
	if _, ok := s.Seq(r.Root); !ok {
		t.Fatal("watcher stopped while a reference was held")
	}

	rel2()
	rel2() // idempotent
}

func TestWatchSetStopsAfterTheLastRelease(t *testing.T) {
	dir := newRepo(t)
	s := newWatchSet(t)
	r := openRepo(t, dir)

	rel, err := s.Acquire(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()

	rel()

	if time.Since(start) > time.Second {
		t.Fatalf("release took %v", time.Since(start))
	}

	if _, ok := s.Seq(r.Root); ok {
		t.Fatal("Seq still reports a watcher after the last release")
	}
}

func TestWatchSetReacquireNeverRepeatsSeq(t *testing.T) {
	dir := newRepo(t)
	s := newWatchSet(t)
	r := openRepo(t, dir)
	seen := map[uint64]bool{}

	for i := range 4 {
		rel, err := s.Acquire(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}

		n, ok := s.Seq(r.Root)
		if !ok {
			t.Fatal("not watched")
		}

		if seen[n] {
			t.Fatalf("round %d: seq %d repeated", i, n)
		}

		seen[n] = true

		if i%2 == 0 {
			writeFile(t, dir, "f.txt", string(rune('a'+i)))
			nextRootBatch(t, s)

			n, _ = s.Seq(r.Root)
			if seen[n] {
				t.Fatalf("round %d: seq %d repeated after a change", i, n)
			}

			seen[n] = true
		}

		rel()
	}
}

func TestWatchSetCloseStopsEverything(t *testing.T) {
	dir1, dir2 := newRepo(t), newRepo(t)
	base := runtime.NumGoroutine()
	s := NewWatchSet(context.Background(), fastWatch)

	for _, d := range []string{dir1, dir2} {
		if _, err := s.Acquire(context.Background(), openRepo(t, d)); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.Seq(openRepo(t, dir1).Root); ok {
		t.Fatal("Seq reports a watcher after Close")
	}

	if _, ok := <-s.Batches(); ok {
		t.Fatal("batches not closed")
	}

	if _, err := s.Acquire(context.Background(), openRepo(t, dir1)); err == nil {
		t.Fatal("Acquire after Close succeeded")
	}

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := runtime.NumGoroutine(); got > base {
		t.Fatalf("goroutines = %d, baseline %d", got, base)
	}
}

func TestWatchSetReleaseAfterCloseIsSafe(t *testing.T) {
	s := NewWatchSet(context.Background(), fastWatch)

	rel, err := s.Acquire(context.Background(), openRepo(t, newRepo(t)))
	if err != nil {
		t.Fatal(err)
	}

	_ = s.Close()

	rel()
	rel()
}

func TestWatchSetReportsAFailedWatch(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "sub/a.txt", "x")

	r := openRepo(t, dir)

	// Walking sub/ runs git check-ignore, which fails under a canceled context.
	ctx, cancel := context.WithCancel(context.Background())
	s := NewWatchSet(ctx, fastWatch)

	t.Cleanup(func() { _ = s.Close() })
	cancel()

	rel, err := s.Acquire(context.Background(), r)
	if err == nil {
		rel()
		t.Fatal("Acquire succeeded under a canceled context")
	}

	// Nobody was reading Batches while Acquire ran.
	select {
	case got := <-s.Batches():
		if got.Root != r.Root || got.Err == nil {
			t.Fatalf("batch = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no error batch")
	}

	if _, ok := s.Seq(r.Root); ok {
		t.Fatal("Seq reports a watcher that failed to start")
	}
}

func TestWatchSetFailedAcquireNeverBlocksWithoutAReader(t *testing.T) {
	r := openRepo(t, newRepo(t))
	ctx, cancel := context.WithCancel(context.Background())
	s := NewWatchSet(ctx, fastWatch)

	t.Cleanup(func() { _ = s.Close() })
	cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)

		for range 4 * rootBatchBuffer {
			_, _ = s.Acquire(context.Background(), r)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("failed Acquire blocked with nobody reading Batches")
	}
}

func TestWatchSetConcurrentAcquireRelease(t *testing.T) {
	dir := newRepo(t)
	s := newWatchSet(t)
	r := openRepo(t, dir)

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			for range 5 {
				rel, err := s.Acquire(context.Background(), r)
				if err != nil {
					t.Error(err)

					return
				}

				_, _ = s.Seq(r.Root)

				rel()
			}
		})
	}

	wg.Wait()

	if _, ok := s.Seq(r.Root); ok {
		t.Fatal("still watched after every release")
	}
}

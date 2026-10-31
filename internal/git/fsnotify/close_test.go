package fsnotify

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestCloseWhileWatchingNewFiles closes watchers while their reader adds the
// watch of a file just created. Close must release every descriptor the
// watcher opened before it returns, and close none twice: by the second close
// another file may hold the number. It counts the process's descriptors, so it
// does not run in parallel.
func TestCloseWhileWatchingNewFiles(t *testing.T) {
	const watchers = 100

	dir := t.TempDir()

	for i := range 20 {
		writeFile(t, filepath.Join(dir, fmt.Sprint(i)))
	}

	// A first watcher sets up what the runtime keeps open for good, such as its poller.
	first, _ := watchDir(t, dir)
	closeWatcher(t, first)

	before := openFDs(t)

	stop := make(chan struct{})
	strays := make(chan string, 1)

	var (
		wg  sync.WaitGroup
		ids atomic.Uint64
	)

	halt := sync.OnceFunc(func() {
		close(stop)
		wg.Wait()
	})
	t.Cleanup(halt)

	wg.Go(func() { churn(dir, stop) })

	for range 4 {
		wg.Go(func() { holdPipes(stop, &ids, strays) })
	}

	guard := time.After(time.Minute)

	for range watchers {
		w, created := watchDir(t, dir)

		select {
		case <-created:
		case <-guard:
			t.Fatal("no create event")
		}

		closeWatcher(t, w)
	}

	halt()

	select {
	case s := <-strays:
		t.Error(s)
	default:
	}

	if left := openFDs(t) - before; left > 0 {
		t.Errorf("%d descriptors still open after %d watchers closed while watching new files", left, watchers)
	}
}

// watchDir watches dir, retrying while the churn removes a file the watcher was
// about to watch. created closes at the first Create. Events and errors are
// drained until the watcher closes, so its reader keeps up with the churn and
// is still busy when the caller closes it.
func watchDir(t *testing.T, dir string) (w *Watcher, created <-chan struct{}) {
	t.Helper()

	w, err := NewWatcher()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = w.Close() })

	seen := make(chan struct{})
	once := sync.OnceFunc(func() { close(seen) })

	go func() {
		for ev := range w.Events {
			if ev.Has(Create) {
				once()
			}
		}
	}()

	go func() {
		for range w.Errors {
		}
	}()

	for tries := 1; ; tries++ {
		err := w.Add(dir)
		if err == nil {
			return w, seen
		}

		if tries == 100 {
			t.Fatalf("watch %s: %v", dir, err)
		}
	}
}

func closeWatcher(t *testing.T, w *Watcher) {
	t.Helper()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// churn creates files in dir and removes each five files later, so a reader
// keeps finding new files that are still there to watch.
func churn(dir string, stop <-chan struct{}) {
	const live = 5

	for n := 0; ; n++ {
		select {
		case <-stop:
			return
		default:
		}

		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("new%d", n)), nil, 0o600)

		if n >= live {
			_ = os.Remove(filepath.Join(dir, fmt.Sprintf("new%d", n-live)))
		}
	}
}

// holdPipes opens pipes and checks that each carries its own token, so a
// descriptor closed twice shows when the second close lands on one of them. On
// the first stray it reports and stops without closing that pipe: its numbers
// may belong to another file by then.
func holdPipes(stop <-chan struct{}, ids *atomic.Uint64, strays chan<- string) {
	for {
		select {
		case <-stop:
			return
		default:
		}

		var p [2]int
		if err := unix.Pipe(p[:]); err != nil {
			continue
		}

		if err := carriesOwnToken(p, ids.Add(1)); err != nil {
			select {
			case strays <- err.Error():
			default:
			}

			return
		}

		_ = unix.Close(p[0])
		_ = unix.Close(p[1])
	}
}

func carriesOwnToken(p [2]int, id uint64) error {
	if err := unix.SetNonblock(p[0], true); err != nil {
		return fmt.Errorf("pipe %v closed under its owner: %w", p, err)
	}

	var token, got [8]byte

	binary.NativeEndian.PutUint64(token[:], id)

	if _, err := unix.Write(p[1], token[:]); err != nil {
		return fmt.Errorf("pipe %v closed under its owner: write: %w", p, err)
	}

	n, err := unix.Read(p[0], got[:])
	if err != nil {
		return fmt.Errorf("pipe %v closed under its owner: read: %w", p, err)
	}

	if n != len(got) || got != token {
		return fmt.Errorf("pipe %v read %x, want its own token %x", p, got[:n], token)
	}

	return nil
}

// openFDs counts the process's open descriptors.
func openFDs(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}

	return len(entries)
}

func writeFile(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

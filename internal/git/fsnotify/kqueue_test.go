//go:build darwin

package fsnotify

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// TestFailedRewatchKeepsTheDescriptor: a watched file whose registration fails
// on a later add keeps its descriptor open. The watch set still holds it and
// closes it once; closing it here as well shut whatever file had taken the
// number by the time the watch set closed it.
func TestFailedRewatchKeepsTheDescriptor(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "f")
	writeFile(t, path)

	fd, err := unix.Open(path, openMode, 0)
	if err != nil {
		t.Fatal(err)
	}

	var want unix.Stat_t
	if err := unix.Fstat(fd, &want); err != nil {
		t.Fatal(err)
	}

	stillOpen := func() bool {
		var st unix.Stat_t

		return unix.Fstat(fd, &st) == nil && st.Dev == want.Dev && st.Ino == want.Ino
	}

	// Once closed, the number may be another test's file.
	t.Cleanup(func() {
		if stillOpen() {
			_ = unix.Close(fd)
		}
	})

	// With no kqueue every registration fails.
	w := &kqueue{shared: newShared(make(chan Event), make(chan error)), kq: -1, watches: newWatches()}
	w.watches.add(path, "", fd, false)

	if _, err := w.addWatch(path, noteAllEvents, false); err == nil {
		t.Fatal("addWatch registered without a kqueue")
	}

	if !stillOpen() {
		t.Errorf("the failed rewatch closed the watched descriptor %d", fd)
	}
}

package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := Lock(dir)
	noErr(t, err)
	info, err := os.Stat(filepath.Join(dir, "state.lock"))
	noErr(t, err)
	eq(t, info.Mode().Perm(), os.FileMode(0o600))

	_, err = Lock(dir)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Lock err = %v, want ErrLocked", err)
	}

	noErr(t, unlock())
	unlock2, err := Lock(dir)
	noErr(t, err)
	noErr(t, unlock2())
	noErr(t, unlock2())
}

func TestLockMissingDir(t *testing.T) {
	_, err := Lock(filepath.Join(t.TempDir(), "missing"))
	wantErr(t, err)
	if errors.Is(err, ErrLocked) {
		t.Fatal("a missing dir is not a held lock")
	}
}

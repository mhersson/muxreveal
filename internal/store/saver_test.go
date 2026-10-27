package store

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func closeSaver(t *testing.T, s *Saver) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	noErr(t, s.Close(ctx))
}

func named(name string) core.State {
	st := fixedState()
	st.Tabs[0].Name = name

	return st
}

func TestSaverLatestWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	s := NewSaver(path, quietLog())
	for i := range 10 {
		s.Save(named(string(rune('a' + i))))
	}

	closeSaver(t, s)

	st, _, err := Load(path, core.LayoutConfig{})
	noErr(t, err)
	eq(t, st.Tabs[0].Name, "j")
}

func TestSaverSkipsEqualBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewSaver(path, quietLog())
	s.Save(fixedState())

	deadline := time.Now().Add(5 * time.Second)

	for {
		if _, err := os.Stat(path); err == nil {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("state file not written")
		}

		time.Sleep(time.Millisecond)
	}

	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	noErr(t, os.Chtimes(path, old, old))

	s.Save(fixedState())
	closeSaver(t, s)

	info, err := os.Stat(path)
	noErr(t, err)

	if !info.ModTime().Equal(old) {
		t.Fatalf("identical state rewrote the file: mtime %v, want %v", info.ModTime(), old)
	}
}

func TestSaverCloseFlushes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewSaver(path, quietLog())
	s.Save(fixedState())
	closeSaver(t, s)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pending state not flushed: %v", err)
	}
}

func TestSaverDropsSavesAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewSaver(path, quietLog())
	closeSaver(t, s)
	s.Save(fixedState())
	closeSaver(t, s) // a second Close is harmless

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Save after Close wrote the file: %v", err)
	}
}

func TestSaverKeepsFileOnEncodeError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	data, err := Encode(fixedState())
	noErr(t, err)
	noErr(t, os.WriteFile(path, data, 0o600))
	s := NewSaver(path, quietLog())
	bad := fixedState()
	bad.Active = 9
	s.Save(bad)
	closeSaver(t, s)

	got, err := os.ReadFile(path)
	noErr(t, err)

	if string(got) != string(data) {
		t.Fatal("an unencodable state replaced the file")
	}
}

func TestSaverSurvivesWriteFailure(t *testing.T) {
	// A directory in place of the file makes every write fail but never hangs.
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	noErr(t, os.Mkdir(path, 0o700))
	s := NewSaver(path, quietLog())
	s.Save(fixedState())
	closeSaver(t, s)

	ents, err := os.ReadDir(dir)
	noErr(t, err)
	eq(t, len(ents), 1)
}

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	noErr(t, WriteAtomic(path, []byte("one")))
	noErr(t, WriteAtomic(path, []byte("two")))
	got, err := os.ReadFile(path)
	noErr(t, err)
	eq(t, string(got), "two")

	info, err := os.Stat(path)
	noErr(t, err)
	eq(t, info.Mode().Perm(), os.FileMode(0o600))

	ents, err := os.ReadDir(dir)
	noErr(t, err)
	eq(t, len(ents), 1)
}

func TestWriteAtomicFailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	noErr(t, os.Mkdir(target, 0o700)) // rename onto a directory fails
	wantErr(t, WriteAtomic(target, []byte("x")))

	ents, err := os.ReadDir(dir)
	noErr(t, err)
	eq(t, len(ents), 1)

	wantErr(t, WriteAtomic(filepath.Join(dir, "missing", "state.json"), []byte("x")))
}

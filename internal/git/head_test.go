package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHead(t *testing.T) {
	t.Parallel()
	t.Run("branch", func(t *testing.T) {
		t.Parallel()
		dir := newRepo(t)
		writeFile(t, dir, "a.txt", "a\n")
		commitAll(t, dir, "one")
		gitOut(t, dir, "checkout", "-q", "-b", "feat/x")

		got := headOf(t, dir)
		want := HeadInfo{Branch: "feat/x", OID: gitOut(t, dir, "rev-parse", "HEAD")}

		if got != want {
			t.Errorf("Head = %+v, want %+v", got, want)
		}
	})

	t.Run("detached", func(t *testing.T) {
		t.Parallel()
		dir := newRepo(t)
		writeFile(t, dir, "a.txt", "a\n")
		commitAll(t, dir, "one")
		gitOut(t, dir, "checkout", "-q", "--detach")

		got := headOf(t, dir)
		want := HeadInfo{Detached: true, OID: gitOut(t, dir, "rev-parse", "HEAD")}

		if got != want {
			t.Errorf("Head = %+v, want %+v", got, want)
		}

		if len(got.OID) < 40 {
			t.Errorf("OID %q is not a full id", got.OID)
		}
	})

	t.Run("unborn", func(t *testing.T) {
		t.Parallel()
		dir := newRepo(t)

		got := headOf(t, dir)
		if want := (HeadInfo{Branch: "main"}); got != want {
			t.Errorf("Head = %+v, want %+v", got, want)
		}
	})
}

func headOf(t *testing.T, dir string) HeadInfo {
	t.Helper()

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	h, err := Head(context.Background(), r)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}

	return h
}

func TestHeadReportsFailures(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "one")

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if h, err := Head(ctx, r); err == nil {
		t.Errorf("cancelled context: Head = %+v, want an error", h)
	}

	if err := os.WriteFile(filepath.Join(dir, ".git", "refs", "heads", "main"), []byte(strings.Repeat("1", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if h, err := Head(context.Background(), r); err == nil {
		t.Errorf("ref to a missing object: Head = %+v, want an error", h)
	}
}

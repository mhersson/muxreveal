package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHead(t *testing.T) {
	t.Run("branch", func(t *testing.T) {
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

func TestHeadCommit(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, dir string)
		wantSubject string
	}{
		{
			name: "root commit",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.txt", "a\n")
				commitAll(t, dir, "first")
			},
			wantSubject: "first",
		},
		{
			name: "newest of several",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.txt", "a\n")
				commitAll(t, dir, "first")
				writeFile(t, dir, "a.txt", "b\n")
				commitAll(t, dir, "second")
			},
			wantSubject: "second",
		},
		{
			name: "detached head",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.txt", "a\n")
				commitAll(t, dir, "first")
				gitOut(t, dir, "checkout", "-q", "--detach")
			},
			wantSubject: "first",
		},
		{
			name: "subject without its body",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.txt", "a\n")
				commitAll(t, dir, "subject\n\nthe body\nsecond line")
			},
			wantSubject: "subject",
		},
		{
			name: "wrapped subject is one line",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.txt", "a\n")
				commitAll(t, dir, "line one\nline two\n\nbody")
			},
			wantSubject: "line one line two",
		},
		{
			name: "control and invisible characters stay as git has them",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.txt", "a\n")
				commitAll(t, dir, "a\x1b[31mb\u200bc\td")
			},
			wantSubject: "a\x1b[31mb\u200bc\td",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newRepo(t)
			tt.setup(t, dir)

			got, err := HeadCommit(context.Background(), openRepo(t, dir))
			if err != nil {
				t.Fatal(err)
			}

			want := Commit{OID: gitOut(t, dir, "rev-parse", "HEAD"), Subject: tt.wantSubject}
			if got != want {
				t.Errorf("HeadCommit = %+v, want %+v", got, want)
			}
		})
	}
}

func TestHeadCommitUnbornBranchIsAnError(t *testing.T) {
	dir := newRepo(t)

	if c, err := HeadCommit(context.Background(), openRepo(t, dir)); err == nil {
		t.Errorf("HeadCommit = %+v on an unborn branch, want an error", c)
	}
}

func TestHeadCommitIsARead(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "first")

	r := openRepo(t, dir)
	log := fakeGit(t, `echo "locks=$GIT_OPTIONAL_LOCKS" >> "$LOG.locks"`)

	if _, err := HeadCommit(context.Background(), r); err != nil {
		t.Fatal(err)
	}

	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	if n := strings.Count(string(calls), "\n"); n != 1 {
		t.Errorf("git ran %d times, want 1:\n%s", n, calls)
	}

	locks, err := os.ReadFile(log + ".locks")
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(string(locks)); got != "locks=0" {
		t.Errorf("GIT_OPTIONAL_LOCKS = %q, want 0", got)
	}
}

package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAhead(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, f pushFixture)
		want   int
		wantOK bool
	}{
		{
			name:  "no upstream",
			setup: func(*testing.T, pushFixture) {},
		},
		{
			name: "up to date",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")
			},
			wantOK: true,
		},
		{
			name: "two local commits",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")
				writeFile(t, f.root, "f", "2\n")
				commitAll(t, f.root, "two")
				writeFile(t, f.root, "f", "3\n")
				commitAll(t, f.root, "three")
			},
			want:   2,
			wantOK: true,
		},
		{
			name: "behind only",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")
				writeFile(t, f.root, "f", "2\n")
				commitAll(t, f.root, "two")
				gitOut(t, f.root, "push", "-q", "origin", "feat")
				gitOut(t, f.root, "reset", "-q", "--hard", "HEAD~1")
			},
			wantOK: true,
		},
		{
			name: "upstream on a local branch",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "branch", "--set-upstream-to=main")
				writeFile(t, f.root, "f", "2\n")
				commitAll(t, f.root, "two")
			},
			want:   1,
			wantOK: true,
		},
		{
			name: "detached",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")
				gitOut(t, f.root, "checkout", "-q", "--detach")
			},
		},
		{
			name: "unborn branch",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "checkout", "-q", "--orphan", "fresh")
			},
		},
		{
			name: "upstream ref gone",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")
				gitOut(t, f.root, "update-ref", "-d", "refs/remotes/origin/feat")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPushFixture(t)
			tt.setup(t, f)

			n, ok, err := Ahead(context.Background(), f.repo)
			if err != nil {
				t.Fatalf("Ahead: %v", err)
			}

			if n != tt.want || ok != tt.wantOK {
				t.Errorf("Ahead = (%d, %v), want (%d, %v)", n, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestAheadReportsFailures(t *testing.T) {
	f := newPushFixture(t)
	gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if n, ok, err := Ahead(ctx, f.repo); err == nil {
		t.Errorf("cancelled context: Ahead = (%d, %v), want an error", n, ok)
	}

	// An upstream that resolves but a history that cannot be walked is an error, not "no upstream".
	if err := os.WriteFile(filepath.Join(f.root, ".git", "refs", "remotes", "origin", "feat"), []byte(strings.Repeat("1", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if n, ok, err := Ahead(context.Background(), f.repo); err == nil {
		t.Errorf("upstream ref to a missing object: Ahead = (%d, %v), want an error", n, ok)
	}
}

func TestUnpushed(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, f pushFixture)
		want  int
	}{
		{
			name:  "before a first push counts every commit",
			setup: func(*testing.T, pushFixture) {},
			want:  1,
		},
		{
			name: "two commits and no push",
			setup: func(t *testing.T, f pushFixture) {
				writeFile(t, f.root, "f", "2\n")
				commitAll(t, f.root, "two")
			},
			want: 2,
		},
		{
			name: "pushed",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")
			},
		},
		{
			name: "one commit after a push",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "origin", "feat")
				writeFile(t, f.root, "f", "2\n")
				commitAll(t, f.root, "two")
			},
			want: 1,
		},
		{
			name: "another remote does not count",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "remote", "add", "backup", newBare(t))
				gitOut(t, f.root, "push", "-q", "backup", "feat")
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPushFixture(t)
			tt.setup(t, f)

			got, err := Unpushed(context.Background(), f.repo, "origin")
			if err != nil {
				t.Fatalf("Unpushed: %v", err)
			}

			if got != tt.want {
				t.Errorf("Unpushed = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestUnpushedRefusesOptionNames(t *testing.T) {
	f := newPushFixture(t)
	log := fakeGit(t, "")

	for _, remote := range []string{"--all", "-x", ""} {
		if _, err := Unpushed(context.Background(), f.repo, remote); !errors.Is(err, ErrNoRemote) {
			t.Errorf("remote %q: err = %v, want ErrNoRemote", remote, err)
		}
	}

	if calls, err := os.ReadFile(log); err == nil && len(calls) > 0 {
		t.Errorf("a refused remote ran git:\n%s", calls)
	}
}

func TestAheadAndUnpushedAreReads(t *testing.T) {
	f := newPushFixture(t)
	gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")

	log := fakeGit(t, `echo "locks=$GIT_OPTIONAL_LOCKS" >> "$LOG.locks"`)

	if _, ok, err := Ahead(context.Background(), f.repo); err != nil || !ok {
		t.Fatalf("Ahead = ok %v, err %v", ok, err)
	}

	if _, err := Unpushed(context.Background(), f.repo, "origin"); err != nil {
		t.Fatalf("Unpushed: %v", err)
	}

	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"rev-list --count @{upstream}..HEAD", "rev-list --count HEAD --not --remotes=origin"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("git calls lack %q:\n%s", want, calls)
		}
	}

	// One call each: an upstream that resolves needs no second look.
	if n := strings.Count(string(calls), "\n"); n != 2 {
		t.Errorf("git ran %d times, want 2:\n%s", n, calls)
	}

	locks, err := os.ReadFile(log + ".locks")
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(string(locks)); got != "locks=0\nlocks=0" {
		t.Errorf("GIT_OPTIONAL_LOCKS per call = %q, want 0 for both", got)
	}
}

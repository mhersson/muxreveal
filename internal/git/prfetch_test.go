package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prFixture is a bare "github" repository whose pull request 7 head holds one
// commit beyond main, a main that moved after, and a clone with the bare as origin.
type prFixture struct {
	bare, author, work string
	repo               Repo
}

func newPRFixture(t *testing.T) prFixture {
	t.Helper()

	bare := newBare(t)

	author := newRepo(t)
	writeFile(t, author, "a.txt", "a\n")
	writeFile(t, author, "b.txt", "b\n")
	commitAll(t, author, "init")
	gitOut(t, author, "remote", "add", "origin", bare)
	gitOut(t, author, "push", "-q", "origin", "main")

	gitOut(t, author, "switch", "-q", "-c", "feat")
	writeFile(t, author, "a.txt", "a changed\n")
	writeFile(t, author, "new.txt", "new\n")
	commitAll(t, author, "feat work")
	gitOut(t, author, "push", "-q", "origin", "feat:refs/pull/7/head")

	gitOut(t, author, "switch", "-q", "main")
	writeFile(t, author, "later.txt", "later\n")
	commitAll(t, author, "main moves on")

	work := t.TempDir()
	gitOut(t, work, "clone", "-q", bare, ".")

	gitOut(t, author, "push", "-q", "origin", "main")

	// Tagged after the clone, so only a fetch that follows tags brings them in.
	gitOut(t, author, "tag", "v-main", "main")
	gitOut(t, author, "tag", "v-pr", "feat")
	gitOut(t, author, "push", "-q", "origin", "v-main", "v-pr")

	return prFixture{bare: bare, author: author, work: work, repo: openRepo(t, work)}
}

func TestFetchPRWritesOnlyItsRefs(t *testing.T) {
	f := newPRFixture(t)
	before := refs(t, f.work)
	remoteMain := gitOut(t, f.work, "rev-parse", "refs/remotes/origin/main")

	got, err := FetchPR(context.Background(), f.repo, "origin", 7, "main")
	if err != nil {
		t.Fatal(err)
	}

	after := refs(t, f.work)

	was := map[string]bool{}
	for _, line := range strings.Split(before, "\n") {
		was[line] = true
	}

	var added []string

	for _, line := range strings.Split(after, "\n") {
		if !was[line] {
			added = append(added, strings.Fields(line)[0])
		}
	}

	want := []string{"refs/muxreveal/pr/7/base", "refs/muxreveal/pr/7/head"}
	if strings.Join(added, " ") != strings.Join(want, " ") {
		t.Errorf("new refs = %v, want %v", added, want)
	}

	if now := gitOut(t, f.work, "rev-parse", "refs/remotes/origin/main"); now != remoteMain {
		t.Errorf("origin/main moved to %s, was %s", now, remoteMain)
	}

	if _, err := os.Stat(filepath.Join(f.repo.GitDir, "FETCH_HEAD")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("FETCH_HEAD exists: %v", err)
	}

	if got.Head != gitOut(t, f.bare, "rev-parse", "refs/pull/7/head") {
		t.Errorf("Head = %s", got.Head)
	}

	if got.Base != gitOut(t, f.bare, "rev-parse", "refs/heads/main") {
		t.Errorf("Base = %s, want the moved main", got.Base)
	}
}

func TestFetchPRForcePushedHead(t *testing.T) {
	f := newPRFixture(t)

	first, err := FetchPR(context.Background(), f.repo, "origin", 7, "main")
	if err != nil {
		t.Fatal(err)
	}

	gitOut(t, f.author, "switch", "-q", "feat")
	writeFile(t, f.author, "a.txt", "rewritten\n")
	gitOut(t, f.author, "commit", "-q", "--amend", "-a", "-m", "rewritten")
	gitOut(t, f.author, "push", "-q", "-f", "origin", "feat:refs/pull/7/head")

	second, err := FetchPR(context.Background(), f.repo, "origin", 7, "main")
	if err != nil {
		t.Fatal(err)
	}

	if second.Head == first.Head {
		t.Fatal("head did not move")
	}

	if got := gitOut(t, f.work, "rev-parse", "refs/muxreveal/pr/7/head"); got != second.Head {
		t.Errorf("ref = %s, want %s", got, second.Head)
	}
}

func TestFetchPRMissing(t *testing.T) {
	f := newPRFixture(t)

	_, err := FetchPR(context.Background(), f.repo, "origin", 99, "main")
	if !errors.Is(err, ErrNoPR) {
		t.Errorf("err = %v, want ErrNoPR", err)
	}
}

func TestFetchPRRefusesBadInput(t *testing.T) {
	f := newPRFixture(t)
	before := refs(t, f.work)
	calls := fakeGit(t, "")
	credURL := "https://user:secret@example.invalid/o/r.git" //nolint:gosec // G101: fixture URL, no real credential

	tests := []struct {
		name, remote, base string
		number             int
		want               error // nil: any error
	}{
		{"zero", "origin", "main", 0, nil},
		{"negative", "origin", "main", -1, nil},
		{"base option", "origin", "-main", 7, nil},
		{"remote option", "--upload-pack=x", "main", 7, ErrNoRemote},
		{"empty base", "origin", "", 7, ErrBadBranch},
		{"empty remote", "", "main", 7, ErrNoRemote},
		{"unknown remote", "nope", "main", 7, ErrNoRemote},
		{"url as remote", credURL, "main", 7, ErrNoRemote},
		{"base refspec", "origin", "main:refs/heads/x", 7, ErrBadBranch},
		{"base glob", "origin", "ma*", 7, ErrBadBranch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FetchPR(context.Background(), f.repo, tt.remote, tt.number, tt.base)
			if err == nil {
				t.Fatal("err = nil")
			}

			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}

			if strings.Contains(err.Error(), "secret") {
				t.Errorf("error carries the URL: %v", err)
			}
		})
	}

	if after := refs(t, f.work); after != before {
		t.Errorf("refs changed:\n%s\n%s", before, after)
	}

	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Split(string(log), "\n") {
		if strings.Contains(line, "fetch") {
			t.Errorf("a fetch ran: %s", line)
		}
	}
}

func TestFetchPRErrorHidesTheURL(t *testing.T) {
	f := newPRFixture(t)
	clearSSHEnv(t)
	gitOut(t, f.work, "remote", "add", "secret", "ssh://example.invalid/o/r.git")
	gitOut(t, f.work, "config", "core.sshCommand", writeScript(t,
		`echo "fatal: unable to access 'https://user:secret@example.invalid/o/r.git/'" >&2; exit 1`))

	_, err := FetchPR(context.Background(), f.repo, "secret", 7, "main")
	if err == nil {
		t.Fatal("err = nil")
	}

	for _, leak := range []string{"user:secret", "example.invalid/o/r"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q contains %q", err, leak)
		}
	}
}

func TestDiffPRSourceShowsOnlyThePR(t *testing.T) {
	f := newPRFixture(t)

	pr, err := FetchPR(context.Background(), f.repo, "origin", 7, "main")
	if err != nil {
		t.Fatal(err)
	}

	res := diffOK(t, testEngine(t), PRSource{Worktree: f.work, Number: 7})

	var paths []string
	for _, file := range res.Files {
		paths = append(paths, file.Path)
	}

	if strings.Join(paths, " ") != "a.txt new.txt" {
		t.Errorf("files = %v, want a.txt new.txt", paths)
	}

	if res.Head != pr.Head {
		t.Errorf("Head = %s, want %s", res.Head, pr.Head)
	}

	if res.Live {
		t.Error("Live = true")
	}
}

func TestDiffPRSourceNotFetched(t *testing.T) {
	f := newPRFixture(t)

	_, err := testEngine(t).Diff(context.Background(), PRSource{Worktree: f.work, Number: 7})
	if !errors.Is(err, ErrNotFetched) {
		t.Errorf("err = %v, want ErrNotFetched", err)
	}
}

func TestDeletePRRefs(t *testing.T) {
	f := newPRFixture(t)

	if _, err := FetchPR(context.Background(), f.repo, "origin", 7, "main"); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := DeletePRRefs(context.Background(), f.repo, 7); err != nil {
			t.Fatal(err)
		}

		if got := gitOut(t, f.work, "for-each-ref", "refs/muxreveal/"); got != "" {
			t.Errorf("refs left: %s", got)
		}
	}
}

func TestEngineUpdateOnPRSourceRunsFullDiff(t *testing.T) {
	f := newPRFixture(t)

	if _, err := FetchPR(context.Background(), f.repo, "origin", 7, "main"); err != nil {
		t.Fatal(err)
	}

	e := testEngine(t)
	src := PRSource{Worktree: f.work, Number: 7}
	prev := diffOK(t, e, src)

	got, err := e.Update(context.Background(), prev, src, Batch{Paths: []string{"a.txt"}})
	if err != nil {
		t.Fatal(err)
	}

	if got.Head != prev.Head || len(got.Files) != len(prev.Files) {
		t.Errorf("Update = %d files, head %s; want %d files, head %s", len(got.Files), got.Head, len(prev.Files), prev.Head)
	}
}

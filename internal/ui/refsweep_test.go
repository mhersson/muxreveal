package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

var sweepNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

const (
	sweepKeyA = "0123456789abcdef"
	sweepKeyB = "fedcba9876543210"

	sessH = "aaaaaaaa-0000-4000-8000-00000000000a" // held by a pane, old
	sessU = "bbbbbbbb-0000-4000-8000-00000000000b" // unheld, old
	sessR = "cccccccc-0000-4000-8000-00000000000c" // unheld, recent
)

func turnRefName(session, key string, at time.Time) string {
	return fmt.Sprintf("refs/aim/turns/%s/%s/%d/start", session, key, at.UnixMilli())
}

// sweepRepo is a repository with two commits and a clock set to sweepNow.
type sweepRepo struct {
	*drawerRig
	dir          string
	first, other string
}

func newSweepRig(t *testing.T) *sweepRepo {
	t.Helper()

	dir := newRepoDir(t)
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	commitEverything(t, dir, "one")

	first := gitRead(t, "git", dir, "rev-parse", "HEAD")

	write(t, filepath.Join(dir, "a.txt"), "b\n")
	commitEverything(t, dir, "two")

	other := gitRead(t, "git", dir, "rev-parse", "HEAD")

	r := newKeysRig(t, dir)
	r.clock = &fakeClock{t: sweepNow}

	return &sweepRepo{drawerRig: r, dir: dir, first: first, other: other}
}

func (s *sweepRepo) ref(t *testing.T, name, oid string) {
	t.Helper()
	gitIn(t, "git", s.dir, "update-ref", name, oid)
}

func (s *sweepRepo) has(t *testing.T, name string) bool {
	t.Helper()

	return gitRead(t, "git", s.dir, "for-each-ref", "--format=%(refname)", name) != ""
}

func (s *sweepRepo) repo(t *testing.T) git.Repo {
	t.Helper()

	r, err := git.Open(context.Background(), s.dir)
	if err != nil {
		t.Fatal(err)
	}

	return r
}

func (s *sweepRepo) seed(t *testing.T) {
	t.Helper()

	old := sweepNow.Add(-30 * 24 * time.Hour)
	recent := sweepNow.Add(-24 * time.Hour)

	s.ref(t, turnRefName(sessH, sweepKeyA, old), s.first)
	s.ref(t, turnRefName(sessU, sweepKeyA, old), s.first)
	s.ref(t, turnRefName(sessU, sweepKeyB, old), s.first)
	s.ref(t, turnRefName(sessR, sweepKeyA, recent), s.first)

	for _, n := range []int{5, 6, 8} {
		s.ref(t, fmt.Sprintf("refs/aim/pr/%d/head", n), s.first)
		s.ref(t, fmt.Sprintf("refs/aim/pr/%d/base", n), s.first)
	}

	s.ref(t, "refs/tags/keep", s.first)
}

func (s *sweepRepo) turnsOf(t *testing.T, session string) bool {
	t.Helper()

	return s.has(t, "refs/aim/turns/"+session+"/")
}

func (s *sweepRepo) prRefs(t *testing.T, n int) bool {
	t.Helper()

	return s.has(t, fmt.Sprintf("refs/aim/pr/%d/", n))
}

func (s *sweepRepo) list(t *testing.T) refsListedMsg {
	t.Helper()

	if len(s.swept) != 0 {
		t.Fatalf("swept = %v before the direct call, want none", s.swept)
	}

	s.sweepRefs = true

	cmd := s.refSweep(s.repo(t))
	if cmd == nil {
		t.Fatal("refSweep returned no Cmd")
	}

	msg, ok := cmd().(refsListedMsg)
	if !ok {
		t.Fatal("refSweep did not answer with refsListedMsg")
	}

	return msg
}

func TestRefSweepKeepsWhatPanesAndDrawersHold(t *testing.T) {
	t.Parallel()

	s := newSweepRig(t)
	id := s.agent(t, s.dir, core.SessionID(strings.ToUpper(sessH)))
	openIn(t, s.App, core.OpenReview{Pane: id, Mode: core.ReviewTurn},
		core.ShowReviewPR{Repo: "github.com/o/r", Number: 5, Root: s.dir})

	s.drawer.fetching = map[int]int{6: 1}
	s.seed(t)

	msg := s.list(t)
	flatten(s.onRefsListed(msg))

	for _, c := range []struct {
		name string
		got  bool
		want bool
	}{
		{"held session", s.turnsOf(t, sessH), true},
		{"recent session", s.turnsOf(t, sessR), true},
		{"unheld old session", s.turnsOf(t, sessU), false},
		{"shown PR", s.prRefs(t, 5), true},
		{"fetching PR", s.prRefs(t, 6), true},
		{"other PR", s.prRefs(t, 8), false},
		{"branch", s.has(t, "refs/heads/main"), true},
		{"tag", s.has(t, "refs/tags/keep"), true},
	} {
		if c.got != c.want {
			t.Errorf("%s present = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestRefSweepFiltersAtTheAnswer(t *testing.T) {
	t.Parallel()

	s := newSweepRig(t)
	s.seed(t)

	msg := s.list(t)

	s.agent(t, s.dir, core.SessionID(sessU))
	s.drawer.fetching = map[int]int{8: 1}

	flatten(s.onRefsListed(msg))

	if !s.turnsOf(t, sessU) {
		t.Error("a session taken after the listing lost its refs")
	}

	if !s.prRefs(t, 8) {
		t.Error("a pull request fetched after the listing lost its refs")
	}

	if s.prRefs(t, 5) {
		t.Error("an unshown pull request kept its refs")
	}
}

func TestRefSweepTurnsGoWhenAPRRefMoved(t *testing.T) {
	t.Parallel()

	s := newSweepRig(t)
	s.seed(t)

	msg := s.list(t)

	s.ref(t, "refs/aim/pr/8/head", s.other)
	flatten(s.onRefsListed(msg))

	if got := gitRead(t, "git", s.dir, "rev-parse", "refs/aim/pr/8/head"); got != s.other {
		t.Errorf("moved head = %s, want it kept at %s", got, s.other)
	}

	if !s.has(t, "refs/aim/pr/8/base") {
		t.Error("base of the moved pull request is gone, want it kept with its head")
	}

	if s.turnsOf(t, sessU) {
		t.Error("unheld turn refs stayed behind a moved PR ref")
	}
}

func TestRefSweepOncePerRepository(t *testing.T) {
	t.Parallel()

	mainDir := newRepoDir(t)
	write(t, filepath.Join(mainDir, "a.txt"), "a\n")
	commitEverything(t, mainDir, "one")

	linked := filepath.Join(t.TempDir(), "wt")
	gitIn(t, "git", mainDir, "worktree", "add", "-q", "-b", "side", linked)

	linked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatal(err)
	}

	elsewhere := newRepoDir(t)
	write(t, filepath.Join(elsewhere, "a.txt"), "a\n")
	commitEverything(t, elsewhere, "one")

	open := func(dir string) git.Repo {
		r, err := git.Open(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}

		return r
	}

	t.Run("refSweep", func(t *testing.T) {
		t.Parallel()

		r := newKeysRig(t, mainDir)
		r.sweepRefs = true

		if r.refSweep(open(mainDir)) == nil {
			t.Error("the first resolution of a repository started no sweep")
		}

		if r.refSweep(open(linked)) != nil {
			t.Error("a linked worktree of a swept repository started another sweep")
		}

		if r.refSweep(open(elsewhere)) == nil {
			t.Error("another repository started no sweep")
		}

		off := newKeysRig(t, mainDir)
		if off.refSweep(open(mainDir)) != nil {
			t.Error("a sweep started with SweepRefs off")
		}
	})

	t.Run("onPaneRepo", func(t *testing.T) {
		t.Parallel()

		r := newKeysRig(t, mainDir)
		first := r.agent(t, mainDir, "")
		second := r.agent(t, linked, "")
		r.watches.panes[first] = &paneWatch{cwd: mainDir}
		r.watches.panes[second] = &paneWatch{cwd: linked}
		r.sweepRefs = true

		cmd := r.onPaneRepo(paneRepoMsg{id: first, cwd: mainDir, repo: open(mainDir)})
		if n := countOf[refsListedMsg](flatten(cmd)); n != 1 {
			t.Errorf("first agent started %d listings, want 1", n)
		}

		cmd = r.onPaneRepo(paneRepoMsg{id: second, cwd: linked, repo: open(linked)})
		if n := countOf[refsListedMsg](flatten(cmd)); n != 0 {
			t.Errorf("second agent in a linked worktree started %d listings, want 0", n)
		}
	})
}

func TestRefSweepAnswersThatDeleteNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  func(*testing.T, *sweepRepo) refsListedMsg
		kept string
	}{
		{
			name: "a listing error",
			msg: func(t *testing.T, s *sweepRepo) refsListedMsg {
				t.Helper()
				s.seed(t)

				msg := s.list(t)
				msg.err = errors.New("listing failed")

				return msg
			},
			kept: "refs/aim/pr/8/",
		},
		{
			name: "nothing stale",
			msg: func(t *testing.T, s *sweepRepo) refsListedMsg {
				t.Helper()
				s.ref(t, turnRefName(sessR, sweepKeyA, sweepNow.Add(-24*time.Hour)), s.first)

				return s.list(t)
			},
			kept: "refs/aim/turns/" + sessR + "/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSweepRig(t)
			msg := tc.msg(t, s)

			before := gitRead(t, "git", s.dir, "for-each-ref", "--format=%(refname)", "refs/aim/")

			if cmd := s.onRefsListed(msg); cmd != nil {
				t.Error("onRefsListed returned a Cmd, want nil")
			}

			if after := gitRead(t, "git", s.dir, "for-each-ref", "--format=%(refname)", "refs/aim/"); after != before {
				t.Errorf("refs changed:\n%s\nwant\n%s", after, before)
			}

			if !s.has(t, tc.kept) {
				t.Errorf("%s is gone", tc.kept)
			}
		})
	}
}

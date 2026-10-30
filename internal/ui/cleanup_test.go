package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/github"
)

const mergedToast = "merged #7 (merge)"

// cleanupRig is the PR drawer on .worktrees/feat-x, a linked worktree of a
// repository whose main checkout is on main. The worktree holds a shell and an
// idle agent; a third shell works in the main checkout.
type cleanupRig struct {
	*prDrawer
	f       *fakeActions
	main    string
	wt      string
	agentID core.PaneID
	shellID core.PaneID
	other   core.PaneID
}

func newCleanupRig(t *testing.T) *cleanupRig {
	t.Helper()

	main := prFeatureRepo(t)
	wt := filepath.Join(main, ".worktrees", "feat-x")

	// The branch is made inside the worktree, so the main checkout stays on main.
	gitIn(t, "git", main, "worktree", "add", "-q", "--detach", wt)

	wt = rootOf(t, wt)
	r := &cleanupRig{prDrawer: newPRDrawerOn(t, wt), main: main, wt: wt, f: &fakeActions{}}
	r.actions = r.f

	// The branch sits at the pull request's head, as it does once the work is pushed and merged.
	gitIn(t, "git", wt, "reset", "-q", "--hard", r.prHead)

	r.shellID = r.store.State().Tab().Focus
	r.agentID = r.agentIn(t, wt)
	r.other = r.shell(t, main)

	openIn(t, r.App, core.FocusPane{ID: r.shellID})

	info := github.RepoInfo{DefaultBranch: "main", Methods: allMethods, DeleteBranchOnMerge: false}
	r.run(r.step(prSnapshotMsg{Snapshot: github.Snapshot{Repo: prRepo, Info: info, PRs: []github.PR{prSeven()}}}))
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	return r
}

// agentIn opens an idle agent pane in dir.
func (r *cleanupRig) agentIn(t *testing.T, dir string) core.PaneID {
	t.Helper()

	id := r.agent(t, dir, "")
	openIn(t, r.App, core.SetAgentState{ID: id, State: core.AgentIdle})

	return id
}

// mergeToBox presses M and enter and waits for the cleanup box.
func (r *cleanupRig) mergeToBox(t *testing.T) {
	t.Helper()

	r.typed("M")
	r.keys(keyEnter)
	r.until(t, "the cleanup box", func() bool { return r.confirm != nil && r.confirm.cleanup != nil })
}

// check runs the cleanup check for pull request 7 as a merge leaves it and
// returns its answer without feeding it to the app.
func (r *cleanupRig) check(head string) cleanupFoundMsg {
	pr, _ := prInfoByNumber(r.store.State(), prRepoKey, 7)
	pr.State = prMergedState

	r.toast(levelSuccess, mergedToast)

	return r.cleanupCheck(fromMerge, prRepo, pr, r.root, head)().(cleanupFoundMsg)
}

// offer is check followed by feeding the answer to the app.
func (r *cleanupRig) offer(head string) {
	r.run(r.step(r.check(head)))
}

func (r *cleanupRig) name() string { return r.cardName(r.store.State(), r.agentID) }

func (r *cleanupRig) text() string { return ansi.Strip(r.View().Content) }

func (r *cleanupRig) branchExists(t *testing.T) bool {
	t.Helper()

	return gitRead(t, "git", r.main, "branch", "--list", "feat/x") != ""
}

func (r *cleanupRig) worktreeExists() bool {
	_, err := os.Stat(r.wt)

	return err == nil
}

func (r *cleanupRig) panesLeft() int {
	n := 0

	for _, id := range []core.PaneID{r.shellID, r.agentID} {
		if _, ok := r.store.State().Panes[id]; ok {
			n++
		}
	}

	return n
}

// done waits for the toast a run ends with.
func (r *cleanupRig) done(t *testing.T) {
	t.Helper()

	r.until(t, "the answer", func() bool { return r.notice != "" })
}

func TestCleanupOffer(t *testing.T) {
	t.Parallel()

	r := newCleanupRig(t)
	r.mergeToBox(t)

	if r.notice != mergedToast {
		t.Errorf("toast = %q, want %q", r.notice, mergedToast)
	}

	sha := r.prHead[:7]
	got := r.text()

	for _, want := range []string{
		" " + iconCleanup + " Clean up after #7 ",
		"#7 Add x",
		"is merged at " + sha + ". Clean up its worktree?",
		"[x] remove the worktree  .worktrees/feat-x",
		"clean; closes " + r.name() + " (idle) and 1 shell",
		"[x] delete the local branch  feat/x",
		"at " + sha + ", the merged head",
		"enter clean up",
		"w worktree",
		"b branch",
		"esc keep",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the box lacks %q:\n%s", want, got)
		}
	}
}

func TestCleanupRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		keys    []string
		toast   string
		level   toastLevel
		removed bool
		branch  bool
	}{
		{"both", nil, "removed .worktrees/feat-x and closed 2 panes; deleted feat/x", levelSuccess, true, false},
		{"branch off", []string{"b"}, "removed .worktrees/feat-x and closed 2 panes", levelSuccess, true, true},
		{"worktree off", []string{"w"}, "nothing to clean up", levelNotice, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newCleanupRig(t)
			r.mergeToBox(t)

			r.typed(strings.Join(tc.keys, ""))
			r.notice = ""
			r.keys(keyEnter)
			r.done(t)

			if r.notice != tc.toast || r.noticeLevel() != tc.level {
				t.Errorf("toast = %q at level %v, want %q at level %v", r.notice, r.noticeLevel(), tc.toast, tc.level)
			}

			if r.confirm != nil {
				t.Error("the box is still open")
			}

			if r.worktreeExists() == tc.removed {
				t.Errorf("worktree exists = %v, want %v", r.worktreeExists(), !tc.removed)
			}

			if got := r.branchExists(t); got != tc.branch {
				t.Errorf("branch exists = %v, want %v", got, tc.branch)
			}

			wantPanes := 2
			if tc.removed {
				wantPanes = 0
			}

			if got := r.panesLeft(); got != wantPanes {
				t.Errorf("%d panes of the worktree are open, want %d", got, wantPanes)
			}

			if _, ok := r.store.State().Panes[r.other]; !ok {
				t.Error("the pane outside the worktree was closed")
			}
		})
	}

	t.Run("esc keeps everything", func(t *testing.T) {
		t.Parallel()

		r := newCleanupRig(t)
		r.mergeToBox(t)
		r.keys(keyEsc)

		if r.confirm != nil || !r.worktreeExists() || !r.branchExists(t) || r.panesLeft() != 2 {
			t.Errorf("esc: box %v, worktree %v, branch %v, panes %d; want it closed and all kept",
				r.confirm, r.worktreeExists(), r.branchExists(t), r.panesLeft())
		}
	})

	t.Run("worktree off reads the branch as unavailable", func(t *testing.T) {
		t.Parallel()

		r := newCleanupRig(t)
		r.mergeToBox(t)
		r.typed("w")

		got := r.text()
		for _, want := range []string{"[ ] remove the worktree", "[ ] delete the local branch", "needs the worktree removed"} {
			if !strings.Contains(got, want) {
				t.Errorf("with w off the box lacks %q:\n%s", want, got)
			}
		}

		before := r.confirm.cleanup.del
		r.typed("b")

		if r.confirm.cleanup.del != before || r.confirm.cleanup.remove {
			t.Errorf("b changed the target with w off: %+v", *r.confirm.cleanup)
		}
	})

	t.Run("other keys are dropped", func(t *testing.T) {
		t.Parallel()

		r := newCleanupRig(t)
		r.mergeToBox(t)

		r.typed("yn q")

		if r.confirm == nil || r.confirm.cleanup == nil || !r.confirm.cleanup.remove || !r.confirm.cleanup.del {
			t.Errorf("a stray key changed the box: %+v", r.confirm)
		}
	})
}

func TestCleanupGuards(t *testing.T) {
	t.Parallel()

	const unknownHead = "1234567890abcdef1234567890abcdef12345678"

	moveOut := func(t *testing.T, r *cleanupRig) {
		t.Helper()
		gitIn(t, "git", r.wt, "switch", "-q", "--detach")
	}

	commit := func(t *testing.T, r *cleanupRig, n int) {
		t.Helper()

		for i := range n {
			gitIn(t, "git", r.wt, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("extra %d", i))
		}
	}

	tests := []struct {
		name    string
		setup   func(t *testing.T, r *cleanupRig)
		head    string
		toast   func(r *cleanupRig) string // the toast after the check
		box     []string                   // text the box holds; none means no box
		after   func(t *testing.T, r *cleanupRig)
		final   func(r *cleanupRig) string // the notice after enter
		level   toastLevel
		removed bool // the worktree is gone at the end
		branch  bool // the branch is left at the end
		panes   int  // panes of the worktree left at the end
	}{
		{
			name: "modified file",
			setup: func(t *testing.T, r *cleanupRig) {
				write(t, filepath.Join(r.wt, "a.txt"), "changed\n")
			},
			toast: func(*cleanupRig) string {
				return mergedToast + "; .worktrees/feat-x has 1 uncommitted change, so it stays"
			},
			branch: true, panes: 2,
		},
		{
			name: "untracked files",
			setup: func(t *testing.T, r *cleanupRig) {
				write(t, filepath.Join(r.wt, "u1.txt"), "1\n")
				write(t, filepath.Join(r.wt, "u2.txt"), "2\n")
			},
			toast: func(*cleanupRig) string {
				return mergedToast + "; .worktrees/feat-x has 2 uncommitted changes, so it stays"
			},
			branch: true, panes: 2,
		},
		{
			name: "working agent",
			setup: func(t *testing.T, r *cleanupRig) {
				openIn(t, r.App, core.SetAgentState{ID: r.agentID, State: core.AgentWorking})
			},
			toast: func(r *cleanupRig) string {
				return mergedToast + "; " + r.name() + " is still working in .worktrees/feat-x"
			},
			branch: true, panes: 2,
		},
		{
			name:  "branch two commits past the head",
			setup: func(t *testing.T, r *cleanupRig) { commit(t, r, 2) },
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"[-] delete the local branch  feat/x", "kept feat/x: it has 2 commits the PR did not merge"},
			final: func(*cleanupRig) string {
				return "removed .worktrees/feat-x and closed 2 panes; kept feat/x: it has 2 commits the PR did not merge"
			},
			level:   levelSuccess,
			removed: true, branch: true,
		},
		{
			name:  "head unknown here",
			head:  unknownHead,
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"[-] delete the local branch  feat/x", "kept feat/x: #7's head 1234567 is not in this repository"},
			final: func(*cleanupRig) string {
				return "removed .worktrees/feat-x and closed 2 panes; kept feat/x: #7's head 1234567 is not in this repository"
			},
			level:   levelSuccess,
			removed: true, branch: true,
		},
		{
			name: "main checkout on the branch",
			setup: func(t *testing.T, r *cleanupRig) {
				moveOut(t, r)
				gitIn(t, "git", r.main, "switch", "-q", "feat/x")
			},
			toast: func(r *cleanupRig) string {
				return mergedToast + "; " + r.main + " is outside MuxReveal's worktrees, so it stays"
			},
			branch: true, panes: 2,
		},
		{
			name: "worktree outside both roots",
			setup: func(t *testing.T, r *cleanupRig) {
				moveOut(t, r)
				gitIn(t, "git", r.main, "worktree", "add", "-q", filepath.Join(filepath.Dir(r.main), "elsewhere"), "feat/x")
			},
			toast: func(r *cleanupRig) string {
				return mergedToast + "; " + filepath.Join(filepath.Dir(r.main), "elsewhere") + " is outside MuxReveal's worktrees, so it stays"
			},
			branch: true, panes: 2,
		},
		{
			name:   "no worktree on the branch",
			setup:  moveOut,
			toast:  func(*cleanupRig) string { return mergedToast },
			branch: true, panes: 2,
		},
		{
			name:  "agent starts working after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"[x] remove the worktree  .worktrees/feat-x"},
			after: func(t *testing.T, r *cleanupRig) {
				openIn(t, r.App, core.SetAgentState{ID: r.agentID, State: core.AgentWorking})
			},
			final:  func(r *cleanupRig) string { return r.name() + " is still working in .worktrees/feat-x" },
			branch: true, panes: 2,
		},
		{
			name:  "branch moved after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"at "},
			after: func(t *testing.T, r *cleanupRig) { commit(t, r, 1) },
			final: func(*cleanupRig) string {
				return "removed .worktrees/feat-x and closed 2 panes; kept feat/x: it moved since you asked"
			},
			level:   levelSuccess,
			removed: true, branch: true,
		},
		{
			name:  "worktree switched to another branch after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"at "},
			after: func(t *testing.T, r *cleanupRig) {
				gitIn(t, "git", r.wt, "switch", "-q", "-c", "other")
				commit(t, r, 1)
			},
			final: func(*cleanupRig) string { return "kept .worktrees/feat-x: it is no longer on feat/x" },
			level: levelError,
			// The panes were closed before the check; they stay closed.
			branch: true, panes: 0,
		},
		{
			name:  "worktree detached with a new commit after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"at "},
			after: func(t *testing.T, r *cleanupRig) {
				moveOut(t, r)
				commit(t, r, 1)
			},
			final:  func(*cleanupRig) string { return "kept .worktrees/feat-x: it is no longer on feat/x" },
			level:  levelError,
			branch: true, panes: 0,
		},
		{
			name:  "listed agent moved out and started working after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"at "},
			after: func(t *testing.T, r *cleanupRig) {
				openIn(t, r.App,
					core.SetPaneCwd{ID: r.agentID, Cwd: r.main},
					core.SetAgentCwd{ID: r.agentID, Cwd: r.main},
					core.SetAgentState{ID: r.agentID, State: core.AgentWorking})
			},
			final:  func(r *cleanupRig) string { return r.name() + " is still working in .worktrees/feat-x" },
			branch: true, panes: 2,
		},
		{
			name:  "file modified after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"at "},
			after: func(t *testing.T, r *cleanupRig) { write(t, filepath.Join(r.wt, "a.txt"), "changed\n") },
			final: func(*cleanupRig) string { return "kept .worktrees/feat-x: it has uncommitted changes" },
			level: levelError,
			// The panes were closed before git refused; they stay closed.
			branch: true, panes: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newCleanupRig(t)
			if tc.setup != nil {
				tc.setup(t, r)
			}

			head := tc.head
			if head == "" {
				head = r.prHead
			}

			r.offer(head)

			if want := tc.toast(r); r.notice != want {
				t.Errorf("toast = %q, want %q", r.notice, want)
			}

			if len(tc.box) == 0 {
				if r.confirm != nil {
					t.Fatalf("a box opened:\n%s", r.text())
				}

				r.assertEnd(t, tc.removed, tc.branch, tc.panes)

				return
			}

			if r.confirm == nil || r.confirm.cleanup == nil {
				t.Fatalf("no cleanup box opened; toast %q", r.notice)
			}

			got := r.text()
			for _, want := range tc.box {
				if !strings.Contains(got, want) {
					t.Errorf("the box lacks %q:\n%s", want, got)
				}
			}

			if tc.after != nil {
				tc.after(t, r)
			}

			r.notice = ""
			r.keys(keyEnter)
			r.done(t)

			if want := tc.final(r); r.notice != want || (tc.level != levelNotice && r.noticeLevel() != tc.level) {
				t.Errorf("after enter: toast %q at level %v, want %q at level %v", r.notice, r.noticeLevel(), want, tc.level)
			}

			r.assertEnd(t, tc.removed, tc.branch, tc.panes)
		})
	}
}

// assertEnd checks what is left of the worktree, the branch and the worktree's panes.
func (r *cleanupRig) assertEnd(t *testing.T, removed, branch bool, panes int) {
	t.Helper()

	// A worktree outside the roots is never removed; only the rig's own is looked at.
	if got := !r.worktreeExists(); got != removed {
		t.Errorf("worktree removed = %v, want %v", got, removed)
	}

	if got := r.branchExists(t); got != branch {
		t.Errorf("branch left = %v, want %v", got, branch)
	}

	if got := r.panesLeft(); got != panes {
		t.Errorf("%d panes of the worktree are open, want %d", got, panes)
	}
}

func TestCleanupOfferLate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		run   func(t *testing.T, r *cleanupRig)
		box   bool
		toast func(r *cleanupRig) string
	}{
		{
			name: "palette opened",
			run: func(t *testing.T, r *cleanupRig) {
				held := r.check(r.prHead)

				r.openCommandPalette()
				r.run(r.step(held))
			},
			toast: func(*cleanupRig) string { return mergedToast },
		},
		{
			name: "a key cleared the toast",
			run: func(t *testing.T, r *cleanupRig) {
				held := r.check(r.prHead)

				r.notice = ""
				r.run(r.step(held))
			},
			box:   true,
			toast: func(*cleanupRig) string { return "" },
		},
		{
			name: "the agent started working",
			run: func(t *testing.T, r *cleanupRig) {
				held := r.check(r.prHead)

				openIn(t, r.App, core.SetAgentState{ID: r.agentID, State: core.AgentWorking})
				r.run(r.step(held))
			},
			toast: func(r *cleanupRig) string {
				return mergedToast + "; " + r.name() + " is still working in .worktrees/feat-x"
			},
		},
		{
			name: "a second merge started",
			run: func(t *testing.T, r *cleanupRig) {
				first := r.check(r.prHead)
				second := r.check(r.prHead)

				r.run(r.step(first))

				if r.confirm != nil {
					t.Fatal("the older check opened a box")
				}

				r.run(r.step(second))
			},
			box:   true,
			toast: func(*cleanupRig) string { return mergedToast },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newCleanupRig(t)
			tc.run(t, r)

			if got := r.confirm != nil && r.confirm.cleanup != nil; got != tc.box {
				t.Errorf("cleanup box open = %v, want %v", got, tc.box)
			}

			if want := tc.toast(r); r.notice != want {
				t.Errorf("toast = %q, want %q", r.notice, want)
			}
		})
	}
}

// cleanupScene draws the cleanup box over the review scene.
func cleanupScene(t *testing.T, icons bool, w, h int, tune func(*cleanupTarget)) *App {
	t.Helper()

	target := cleanupTarget{
		repo: prRepo, number: 7, title: "Show the pull request over its diff", head: prHeadOID, home: "/home/u",
		found: cleanupFound{
			main: "/w/repo", path: "/w/repo/.worktrees/feat-review-pane", branch: "feat/review-pane", tip: prHeadOID, managed: true,
		},
		panes:  []cleanupPane{{id: "p1", name: "muxr-worker-2", agent: true, state: core.AgentIdle}, {id: "p2"}},
		remove: true, del: true,
	}
	if tune != nil {
		tune(&target)
	}

	s := reviewScene{
		theme: "catppuccin-mocha", icons: icons, w: w, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: func(t *testing.T, a *App, v *reviewView) {
			t.Helper()

			prSceneWith(nil, 0)(t, a, v)
			a.confirm = &confirm{cleanup: &target}
		},
	}

	return s.build(t)
}

func cleanupScenes() []struct {
	name string
	w, h int
	tune func(*cleanupTarget)
} {
	return []struct {
		name string
		w, h int
		tune func(*cleanupTarget)
	}{
		{"offer", 120, 40, nil},
		{"branch-past-head", 120, 40, func(t *cleanupTarget) {
			t.found.ahead, t.del = 2, false
		}},
		{"head-missing", 120, 40, func(t *cleanupTarget) {
			t.found.headErr, t.found.tip, t.del = git.ErrNoCommit, "", false
		}},
		{"worktree-off", 120, 40, func(t *cleanupTarget) { t.remove = false }},
		{"branch-off-no-panes", 120, 40, func(t *cleanupTarget) { t.del, t.panes = false, nil }},
		{"branch-behind-head", 120, 40, func(t *cleanupTarget) { t.found.tip = "0123456789abcdef0123456789abcdef01234567" }},
	}
}

// TestCleanupGolden draws the box once in colour; it compares highlighted
// colours, so it runs serially.
func TestCleanupGolden(t *testing.T) {
	for _, c := range cleanupScenes() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(cleanupScene(t, true, c.w, c.h, c.tune).View().Content))
		})
	}
}

// TestCleanupText is the same screens without styling, so the layout reads.
func TestCleanupText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, c := range cleanupScenes() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, c.name), func(t *testing.T) {
				t.Parallel()

				got := ansi.Strip(cleanupScene(t, icons, c.w, c.h, c.tune).View().Content)
				golden.RequireEqual(t, []byte(got))
			})
		}
	}
}

func TestCleanupBoxFits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		w, h         int
		blank, hints bool // the blank rows and the hint row the box keeps
	}{
		{"80x24", 80, 24, true, true},
		{"60x16", 60, 16, false, true},
		{"60x14", 60, 14, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := ansi.Strip(cleanupScene(t, true, tc.w, tc.h, nil).View().Content)
			lines := strings.Split(got, "\n")

			for i, l := range lines {
				if w := ansi.StringWidth(l); w != tc.w {
					t.Errorf("line %d is %d cells wide, want %d: %q", i, w, tc.w, l)
				}
			}

			if has := strings.Contains(got, "enter clean up"); has != tc.hints {
				t.Errorf("hint row shown = %v, want %v:\n%s", has, tc.hints, got)
			}

			pw := min(maxCleanupWidth, tc.w-2)
			x := (tc.w - pw) / 2
			blanks, in := 0, false

			for _, l := range lines {
				switch {
				case strings.Contains(l, "Clean up after"):
					in = true

					continue
				case strings.Contains(l, "╰"):
					in = false
				}

				if in && strings.TrimSpace(ansi.Cut(l, x+1, x+pw-1)) == "" {
					blanks++
				}
			}

			if has := blanks > 1; has != tc.blank {
				t.Errorf("box keeps %d blank rows, want blank rows kept = %v:\n%s", blanks, tc.blank, got)
			}

			if !strings.Contains(got, "[x] remove the worktree") {
				t.Errorf("the worktree row is gone:\n%s", got)
			}
		})
	}
}

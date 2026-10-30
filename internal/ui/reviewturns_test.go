package ui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/harness"
)

func (r *drawerRig) coreMode() core.ReviewMode { return r.store.State().Tab().Review.Mode }

func isBranchSource(v *reviewView) bool {
	_, ok := v.src.(git.BranchSource)

	return ok
}

// A session that ends and another that starts, as /clear sends them, leave the
// drawer's turn mode alone: it shows the branch until the new session has a
// turn, then that turn.
func TestSessionEndAndStartKeepTurnMode(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t, turnFirst)
	dir := threeTurnRepo(t)
	root := rootOf(t, dir)
	id := r.agent(t, dir, testSession)

	v := r.open(t, id)
	r.watching(t, id, root)

	// The mode row is the tree's first row.
	r.showTree(t)

	if _, ok := v.src.(git.TurnSource); !ok {
		t.Fatalf("setup: source = %T, want the turn source", v.src)
	}

	branchShown := func(what string) {
		t.Helper()

		r.until(t, what, r.viewHolds(func(v *reviewView) bool { return isBranchSource(v) && v.loaded && !v.loading }))

		v := r.view()

		if r.coreMode() != core.ReviewTurn || v.err != nil {
			t.Errorf("%s: core mode %q err %v, want turn mode kept and no error", what, r.coreMode(), v.err)
		}

		if got := r.drawn(t); !strings.Contains(got, "branch vs main") {
			t.Errorf("%s: the mode row does not read branch:\n%s", what, got)
		}
	}

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	branchShown("the branch after the session ended")

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))
	r.until(t, "the new session", r.viewHolds(func(v *reviewView) bool { return v.session == string(otherSession) && v.loaded && !v.loading }))
	branchShown("the branch while the new session has no turns")

	// The new session's first prompt reaches the app before its snapshot exists.
	snapshot := r.turnStartFirst(t, id)

	snapshotTurn(t, dir, otherSession)
	write(t, filepath.Join(dir, "x.txt"), "after the first prompt\n")
	snapshot()
	r.until(t, "the new session's turn", r.viewHolds(func(v *reviewView) bool {
		ts, ok := v.src.(git.TurnSource)

		return ok && ts.Session == string(otherSession) && v.loaded && !v.loading
	}))

	v = r.view()
	if got := viewPaths(v); !slices.Equal(got, []string{"x.txt"}) || v.turns != 1 || r.coreMode() != core.ReviewTurn {
		t.Errorf("shows %v of %d turns in %q mode, want x.txt of 1 in turn mode", got, v.turns, r.coreMode())
	}
}

// A turn drawer whose session has no turns yet shows the branch, and the turn
// keys say why they do nothing.
func TestNoTurnsYetNotices(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	dir := editedRepo(t)
	id := r.agent(t, dir, testSession)

	// A restored drawer in turn mode, on a session with no turns.
	openIn(t, r.App, core.OpenReview{Pane: id, Mode: core.ReviewTurn})
	r.settle()

	v := r.idle(t)

	if !isBranchSource(v) || v.err != nil || r.coreMode() != core.ReviewTurn {
		t.Fatalf("source %T err %v core mode %q, want the branch shown and no error in turn mode", v.src, v.err, r.coreMode())
	}

	keys := map[string]func() tea.Cmd{
		"t": func() tea.Cmd { return r.setReviewMode(core.ReviewTurn) },
		",": func() tea.Cmd { return r.stepReviewTurn(-1) },
		".": func() tea.Cmd { return r.stepReviewTurn(1) },
	}

	for key, press := range keys {
		r.notice = ""
		r.run(press())

		if want := "no turns yet"; r.notice != want {
			t.Errorf("%s: notice = %q, want %q", key, r.notice, want)
		}

		if v.loading {
			t.Errorf("%s started a load", key)
		}
	}

	// b still works, and a pane with no session keeps its own notice.
	r.run(r.setReviewMode(core.ReviewBranch))
	r.idle(t)

	if r.coreMode() != core.ReviewBranch {
		t.Errorf("b left core mode %q", r.coreMode())
	}

	openIn(t, r.App, core.SetAgentSession{ID: id})
	r.settle()

	r.notice = ""
	r.run(r.setReviewMode(core.ReviewTurn))

	if want := "no turns: this pane has no agent session"; r.notice != want {
		t.Errorf("t without a session: notice = %q, want %q", r.notice, want)
	}
}

// turnStartFirst sends a turn start as the gate orders it in production: before
// the turn's snapshot exists. The drawer must not load until the returned
// function has closed the event's Snapped channel.
func (r *drawerRig) turnStartFirst(t *testing.T, id core.PaneID) (snapshotDone func()) {
	t.Helper()

	snapped := make(chan struct{})
	v := r.view()
	epoch := v.epoch

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "UserPromptSubmit", TurnStart: true, State: core.AgentWorking, Snapped: snapped}}))

	if v.loading || v.epoch != epoch {
		t.Fatalf("loading = %v, epoch %d -> %d: the drawer loaded before the turn's snapshot existed", v.loading, epoch, v.epoch)
	}

	return func() { close(snapped) }
}

// A turn's snapshot ref never wakes the watcher, so the turn's start event
// recomputes the drawer, once the turn's snapshot exists. A turn that has
// changed files moves the count and the mode row; a past turn only learns the
// new count.
func TestTurnStartRefreshesTheDrawer(t *testing.T) {
	t.Parallel()
	t.Run("a turn that changes files", func(t *testing.T) {
		r := newDrawerRig(t, turnFirst)
		dir := threeTurnRepo(t)
		id := r.agent(t, dir, testSession)

		r.open(t, id)
		r.watching(t, id, rootOf(t, dir))

		// The mode row is the tree's first row.
		r.showTree(t)

		snapshotDone := r.turnStartFirst(t, id)

		snapshotTurn(t, dir, testSession)
		write(t, filepath.Join(dir, "four.txt"), "turn four\n")
		snapshotDone()
		r.until(t, "turn 4", r.viewHolds(func(v *reviewView) bool { return v.turns == 4 && v.res.Turn == 4 && !v.loading }))

		got := r.drawn(t)
		for _, want := range []string{"turn 4 of 4, live", "four.txt"} {
			if !strings.Contains(got, want) {
				t.Errorf("the drawer lacks %q:\n%s", want, got)
			}
		}
	})

	t.Run("a turn that changes nothing yet", func(t *testing.T) {
		t.Parallel()
		r := newDrawerRig(t, turnFirst)
		dir := threeTurnRepo(t)
		id := r.agent(t, dir, testSession)

		r.open(t, id)
		r.watching(t, id, rootOf(t, dir))

		snapshotDone := r.turnStartFirst(t, id)

		snapshotTurn(t, dir, testSession)
		snapshotDone()
		r.until(t, "turn 3 closed", r.viewHolds(func(v *reviewView) bool { return v.turns == 3 && v.res.Turn == 3 && !v.res.Live && !v.loading }))
	})

	t.Run("a past turn", func(t *testing.T) {
		r := newDrawerRig(t, turnFirst)
		dir := threeTurnRepo(t)
		id := r.agent(t, dir, testSession)

		r.open(t, id)
		r.watching(t, id, rootOf(t, dir))
		r.showTree(t)
		r.run(r.stepReviewTurn(-1))

		v := r.idle(t)
		gen := v.gen

		snapshotDone := r.turnStartFirst(t, id)

		snapshotTurn(t, dir, testSession)
		write(t, filepath.Join(dir, "four.txt"), "turn four\n")
		snapshotDone()
		r.until(t, "the new count", func() bool { return v.turns == 4 && !v.loading })

		if v.res.Turn != 2 || v.gen != gen {
			t.Errorf("a past turn reloaded: turn %d, generation %d -> %d", v.res.Turn, gen, v.gen)
		}

		if got := r.drawn(t); !strings.Contains(got, "turn 2 of 4") {
			t.Errorf("the mode row does not read turn 2 of 4:\n%s", got)
		}
	})

	// The wait for the snapshot ends with the app, so no command outlives it.
	t.Run("the app ending ends the wait", func(t *testing.T) {
		r := newDrawerRig(t)
		id := r.agent(t, threeTurnRepo(t), testSession)
		r.open(t, id)

		cmd := r.reviewHook(id, true, make(chan struct{}))
		if cmd == nil {
			t.Fatal("a turn start with a snapshot to wait for returned no command")
		}

		answered := make(chan tea.Msg, 1)

		go func() { answered <- cmd() }()

		r.cancel()

		select {
		case m := <-answered:
			if m != nil {
				t.Errorf("the wait answered %T after the app ended, want nothing", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the wait outlived the app")
		}
	})
}

// A drawer on a pane that moved to another repository whose watch cannot start
// holds nothing for the old repository, and hook events refresh it as they do
// any drawer without a watch.
func TestFailedWatchOfANewRepositoryFallsBackToHooks(t *testing.T) {
	t.Parallel()
	t.Run("a failed acquire releases the old watch", func(t *testing.T) {
		r := newDrawerRig(t)
		first, second := editedRepo(t), editedRepo(t)
		id := r.agent(t, first, "")

		r.open(t, id)
		r.watching(t, id, rootOf(t, first))

		// A closed set refuses every Acquire.
		if err := r.set.Close(); err != nil {
			t.Fatal(err)
		}

		openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: second})
		r.settle()
		r.until(t, "the old reference to be released", func() bool { return r.held(id) == "" && r.holdCount() == 0 })

		if w := r.watches.panes[id]; w.want != rootOf(t, second) {
			t.Errorf("wants %q, want the new repository %q", w.want, rootOf(t, second))
		}

		v := r.idle(t)

		r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "Stop", State: core.AgentIdle}}))

		if !v.loading {
			t.Error("a hook event did not refresh the drawer of the repository with no watch")
		}
	})

	t.Run("the old root's watch does not count while another root is wanted", func(t *testing.T) {
		r := newDrawerRig(t)
		dir := editedRepo(t)
		id := r.agent(t, dir, "")

		v := r.open(t, id)
		r.watching(t, id, rootOf(t, dir))

		// The pane moved on, so the drawer is on the new root, whose watch has not answered yet.
		r.watches.panes[id].want = "/another/root"
		v.root = "/another/root"

		r.run(r.reviewHook(id, false, nil))

		if !v.loading {
			t.Error("a hook event did not refresh while the held watch is for another root")
		}
	})
}

// A hook event for a watch-less drawer in a hidden tab marks it stale, as a
// batch would, so it recomputes when its tab shows.
func TestHookEventMarksAHiddenDrawerStale(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t, func(o *Options) { o.Watches = nil })
	dir := editedRepo(t)
	id := r.agent(t, dir, "")

	v := r.open(t, id)
	gen := v.gen

	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: t.TempDir()})
	r.settle()

	write(t, filepath.Join(dir, "f.txt"), editedF(2))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "Stop", State: core.AgentIdle}}))

	if v.loading || v.queued == nil || v.gen != gen {
		t.Fatalf("hidden drawer: loading=%v queued=%v gen %d -> %d, want it marked stale and left alone", v.loading, v.queued, gen, v.gen)
	}

	openIn(t, r.App, core.SelectTab{Index: 0})
	r.settle()
	r.until(t, "the drawer to recompute", func() bool { return v.gen > gen && !v.loading })

	if got := v.res.Files[0].Hunks[0].NewStart; got != 1 {
		t.Errorf("the first hunk starts at line %d, want 1 (the saved file)", got)
	}
}

// A new session makes the turn count the ended session's: the turn keys wait
// for the new session's count instead of trusting it. With no turns they say
// so, and core stays in branch mode.
func TestTurnKeysDoNotTrustTheEndedSessionsCount(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	dir := threeTurnRepo(t)
	root := rootOf(t, dir)
	id := r.agent(t, dir, testSession)

	r.open(t, id)
	r.watching(t, id, root)
	r.run(r.setReviewMode(core.ReviewBranch))

	if v := r.idle(t); v.turns != 3 {
		t.Fatalf("setup: %d turns, want the ended session's 3", v.turns)
	}

	// The /clear order, with the key pressed before anything refreshed the count.
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))

	r.notice = ""
	r.run(r.setReviewMode(core.ReviewTurn))
	r.until(t, "the notice", func() bool { return r.notice != "" })

	if want := "no turns yet"; r.notice != want {
		t.Errorf("notice = %q, want %q", r.notice, want)
	}

	if r.coreMode() != core.ReviewBranch {
		t.Errorf("core mode = %q, want branch kept", r.coreMode())
	}
}

// The same wait ends in the switch when the new session has turns.
func TestTurnKeysSwitchToANewSessionThatHasTurns(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	dir := threeTurnRepo(t)
	root := rootOf(t, dir)
	snapshotTurn(t, dir, otherSession)
	write(t, filepath.Join(dir, "other.txt"), "the other session's change\n")
	id := r.agent(t, dir, testSession)

	r.open(t, id)
	r.watching(t, id, root)
	r.run(r.setReviewMode(core.ReviewBranch))
	r.idle(t)

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))

	r.notice = ""
	r.run(r.setReviewMode(core.ReviewTurn))
	r.until(t, "the new session's turn", r.viewHolds(func(v *reviewView) bool {
		ts, ok := v.src.(git.TurnSource)

		return ok && ts.Session == string(otherSession) && v.loaded && !v.loading
	}))

	if r.notice != "" || r.coreMode() != core.ReviewTurn || r.view().turns != 1 {
		t.Errorf("notice %q core mode %q turns %d, want no notice, turn mode and 1 turn", r.notice, r.coreMode(), r.view().turns)
	}
}

// An answer whose probe ran for another session does not set the count.
func TestAnswerForAnotherSessionDoesNotSetTheCount(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	id := r.agent(t, threeTurnRepo(t), testSession)

	v := r.open(t, id)
	tab := r.store.State().Tab().ID

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))
	r.step(reviewLoadedMsg{tab: tab, gen: v.epoch, session: string(testSession), info: reviewInfo{turns: 7}})

	if v.turns == 7 {
		t.Error("the count of the ended session was adopted for the new one")
	}
}

// A turn key held for the turn count gives way to the user's next key: t then
// b, inside the one probe, ends in branch mode.
func TestLaterKeyReplacesTheHeldTurnKey(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	dir := threeTurnRepo(t)
	root := rootOf(t, dir)
	snapshotTurn(t, dir, otherSession)
	id := r.agent(t, dir, testSession)

	r.open(t, id)
	r.watching(t, id, root)
	r.run(r.setReviewMode(core.ReviewBranch))
	r.idle(t)

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))

	// Both keys come before any answer is delivered.
	r.run(r.setReviewMode(core.ReviewTurn))
	r.run(r.setReviewMode(core.ReviewBranch))
	r.until(t, "the probe and the diff to settle", r.viewHolds(func(v *reviewView) bool { return !v.loading && v.turnsFor == string(otherSession) }))

	if v := r.view(); r.coreMode() != core.ReviewBranch || !isBranchSource(v) || v.after != nil {
		t.Errorf("core mode %q source %T held key %v, want branch with no key held", r.coreMode(), v.src, v.after != nil)
	}
}

// A turn key is held only while a probe can answer it. Outside a repository
// there is none, so the key gets its notice at once.
func TestTurnKeyOutsideARepositoryAnswersAtOnce(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	id := r.agent(t, t.TempDir(), testSession)

	openIn(t, r.App, core.OpenReview{Pane: id, Mode: core.ReviewTurn})
	r.settle()
	r.until(t, "the error", r.viewHolds(func(v *reviewView) bool { return v.err != nil }))

	r.notice = ""

	cmd := r.setReviewMode(core.ReviewTurn)

	if want := "no turns yet"; r.notice != want || cmd != nil || r.view().after != nil {
		t.Errorf("notice %q command %v held key %v, want the notice at once and nothing held", r.notice, cmd != nil, r.view().after != nil)
	}
}

// A new session drops a held turn key: it was pressed for the old one.
func TestNewSessionDropsAHeldTurnKey(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	dir := threeTurnRepo(t)
	root := rootOf(t, dir)
	snapshotTurn(t, dir, otherSession)
	id := r.agent(t, dir, testSession)

	r.open(t, id)
	r.watching(t, id, root)
	r.run(r.setReviewMode(core.ReviewBranch))
	r.idle(t)

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))
	r.run(r.setReviewMode(core.ReviewTurn))

	if r.view().after == nil {
		t.Fatal("setup: the key was not held")
	}

	// The session ends before the probe answers.
	r.notice = ""
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))

	if r.view().after != nil {
		t.Error("the held key outlived the session it was pressed for")
	}

	r.until(t, "the loads to settle", r.viewHolds(func(v *reviewView) bool { return !v.loading }))

	if r.notice != "" || r.coreMode() != core.ReviewBranch {
		t.Errorf("notice %q core mode %q, want the dropped key to do nothing", r.notice, r.coreMode())
	}
}

// newTurnRig is a follow rig whose agent has one turn of its session, taken in
// A's main checkout (the agent's own directory) and followed there by a new
// file, and five uncommitted lines in b that only the branch shows.
func newTurnRig(t *testing.T, opts ...func(*Options)) *followRig {
	t.Helper()

	r := newFollowRig(t, testSession, opts...)
	snapshotTurn(t, r.dirA, testSession)
	write(t, filepath.Join(r.dirA, "x.txt"), "after the turn\n")
	r.addLines(t, filepath.Join(r.dirB, "a.txt"))

	return r
}

// settledOn is a condition: the drawer shows a loaded turn, or the branch, of root.
func (r *followRig) settledOn(root string, turn bool) func() bool {
	return r.viewHolds(func(v *reviewView) bool {
		_, isTurn := v.src.(git.TurnSource)

		return v.root == root && v.loaded && !v.loading && isTurn == turn
	})
}

// followB opens the drawer in turn mode on A, then edits in b and waits for the
// drawer to show b.
func (r *followRig) followB(t *testing.T) *reviewView {
	t.Helper()

	if v := r.open(t, r.id); v.root != r.rootA || v.turns != 1 || r.coreMode() != core.ReviewTurn {
		t.Fatalf("setup: root %q with %d turns in %q mode, want A's turn mode", v.root, v.turns, r.coreMode())
	}

	r.edit(filepath.Join(r.dirB, "a.txt"))
	r.until(t, "the drawer to show b", r.settledOn(r.rootB, false))

	return r.view()
}

func TestTurnModeInAnotherWorktreeShowsBranch(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t, turnFirst)
	v := r.followB(t)

	if want := (git.BranchSource{Worktree: r.rootB}); v.src != want {
		t.Errorf("source = %+v, want %+v", v.src, want)
	}

	// A turn of A's session would show x.txt, the file A changed after it.
	if got := viewPaths(v); !slices.Equal(got, []string{"a.txt"}) || v.res.Turn != 0 || v.turns != 0 || !v.foreign {
		t.Errorf("shows %v as turn %d of %d turns, foreign %v, want a.txt of b's branch with no turns", got, v.res.Turn, v.turns, v.foreign)
	}

	if r.coreMode() != core.ReviewTurn {
		t.Errorf("core mode = %q, want turn mode kept", r.coreMode())
	}

	if got := r.drawn(t); !strings.Contains(got, "branch") || strings.Contains(got, "turn 1/1") {
		t.Errorf("the tag does not read branch:\n%s", got)
	}
}

func TestTurnKeysInAnotherWorktree(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t, turnFirst)
	v := r.followB(t)
	want := "no turns in " + filepath.Base(r.dirA) + " · b yet"

	keys := []struct {
		name  string
		press func() tea.Cmd
	}{
		{"t", func() tea.Cmd { return r.setReviewMode(core.ReviewTurn) }},
		{",", func() tea.Cmd { return r.stepReviewTurn(-1) }},
		{".", func() tea.Cmd { return r.stepReviewTurn(1) }},
	}

	for _, k := range keys {
		r.notice = ""
		epoch, gen := v.epoch, v.gen

		cmd := k.press()

		if r.notice != want {
			t.Errorf("%s: notice = %q, want %q", k.name, r.notice, want)
		}

		if cmd != nil || v.loading || v.epoch != epoch || v.gen != gen || r.coreMode() != core.ReviewTurn || !isBranchSource(v) {
			t.Errorf("%s changed something: command %v, loading %v, epoch %d -> %d, core mode %q, source %T", k.name, cmd != nil, v.loading, epoch, v.epoch, r.coreMode(), v.src)
		}
	}
}

func TestNoTurnsNoticeCleansNames(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	tab := r.store.State().Tab().ID
	v := &reviewView{loaded: true, foreign: true, project: "my\x07repo", branch: "feat\u200b/x"}

	if ok, cmd := r.turnsGate(tab, v, string(testSession), nil); ok || cmd != nil {
		t.Fatalf("turnsGate() = %v, %v, want the key stopped", ok, cmd != nil)
	}

	if want := "no turns in myrepo · feat/x yet"; r.notice != want {
		t.Errorf("notice = %q, want %q", r.notice, want)
	}

	// A pane with no session keeps its own notice.
	r.notice = ""
	r.turnsGate(tab, v, "", nil)

	if want := "no turns: this pane has no agent session"; r.notice != want {
		t.Errorf("notice = %q, want %q", r.notice, want)
	}
}

func TestTurnModeBackInOwnWorktree(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t, turnFirst)
	r.followB(t)

	r.edit(filepath.Join(r.dirA, "x.txt"))
	r.until(t, "A's turn", r.settledOn(r.rootA, true))

	v := r.view()
	if got := viewPaths(v); !slices.Equal(got, []string{"x.txt"}) || v.res.Turn != 1 || !v.res.Live || v.turns != 1 || v.foreign {
		t.Errorf("shows %v as turn %d (live %v) of %d turns, foreign %v, want x.txt in A's latest turn", got, v.res.Turn, v.res.Live, v.turns, v.foreign)
	}

	// The turn keys work again.
	r.notice = ""
	r.run(r.stepReviewTurn(1))

	if want := "turn 1 is the latest turn"; r.notice != want {
		t.Errorf("notice = %q, want %q", r.notice, want)
	}
}

func TestOpenDrawerOnAnotherWorktreeStartsInBranch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		edit   bool
		want   core.ReviewMode
		source func(r *followRig) git.Source
	}{
		{"agent in its own worktree", false, core.ReviewTurn, func(r *followRig) git.Source {
			return git.TurnSource{Worktree: r.rootA, Session: string(testSession)}
		}},
		{"agent following another worktree", true, core.ReviewBranch, func(r *followRig) git.Source {
			return git.BranchSource{Worktree: r.rootB}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTurnRig(t, turnFirst)

			if tt.edit {
				r.edit(filepath.Join(r.dirB, "a.txt"))
			}

			v := r.open(t, r.id)

			if got := r.coreMode(); got != tt.want {
				t.Errorf("opened in %q mode, want %q", got, tt.want)
			}

			if want := tt.source(r); v.src != want {
				t.Errorf("source = %+v, want %+v", v.src, want)
			}
		})
	}
}

// The agent's own directory moving into or out of the shown worktree switches a
// turn drawer between the worktree's turn and the branch: b has no turn of the
// session, so it shows the branch either way.
func TestOwnDirectoryMovingFlipsTurnMode(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t, turnFirst)
	r.followB(t)

	r.hookNow(cwdHook(r.id, r.dirB))
	r.until(t, "the branch of b as the agent's own", r.viewHolds(func(v *reviewView) bool { return v.root == r.rootB && !v.foreign && v.loaded && !v.loading }))

	if v := r.view(); v.turns != 0 || isTurnSource(v) || r.pane().FollowDir() != r.dirB {
		t.Errorf("%d turns, source %T following %q, want b's branch with no turns, followed", v.turns, v.src, r.pane().FollowDir())
	}

	r.hookNow(cwdHook(r.id, r.dirA))
	r.until(t, "the branch of b", r.settledOn(r.rootB, false))

	if v := r.view(); !v.foreign || v.turns != 0 || r.coreMode() != core.ReviewTurn {
		t.Errorf("foreign %v with %d turns in %q mode, want b's branch in turn mode", v.foreign, v.turns, r.coreMode())
	}
}

// A move of the own directory inside the shown repository neither reloads the
// diff nor changes what it shows.
func TestOwnDirectoryMovingInsideTheRepositoryKeepsTheDiff(t *testing.T) {
	r := newTurnRig(t, turnFirst)
	sub := filepath.Join(r.dirA, "pkg")
	write(t, filepath.Join(sub, "p.txt"), "p\n")

	// The watcher reports these writes and the rig's x.txt after its debounce.
	// A drawer that opens first, as it does when diffs are fast, would reload
	// for them after the move and mask what the test checks.
	r.until(t, "the watcher to report the files written", r.reportedAll(
		filepath.Join(r.rootA, "x.txt"), filepath.Join(r.rootA, "pkg", "p.txt")))

	v := r.open(t, r.id)
	epoch, gen := v.epoch, v.gen

	r.hookNow(cwdHook(r.id, sub))
	r.until(t, "the own directory to be seen", func() bool { return v.own == sub && !v.loading })
	r.quiet()

	if v != r.view() || v.epoch != epoch || v.gen != gen || v.foreign || !isTurnSource(v) {
		t.Errorf("the drawer reloaded: epoch %d -> %d, generation %d -> %d, foreign %v, source %T", epoch, v.epoch, gen, v.gen, v.foreign, v.src)
	}
}

func isTurnSource(v *reviewView) bool {
	_, ok := v.src.(git.TurnSource)

	return ok
}

// A live load that starts after the own directory moved away from the shown
// root swaps the turn for the branch, and computes the branch afresh rather
// than updating the turn's result.
func TestLiveLoadAfterOwnDirectoryMovedComputesTheBranch(t *testing.T) {
	r := newFollowRig(t, testSession, turnFirst)

	// The turn's snapshot holds m.txt changed. Since then m.txt is back to its
	// committed text and n.txt is changed, so the turn shows both files and the
	// branch only n.txt.
	write(t, filepath.Join(r.dirA, "m.txt"), "m\n")
	write(t, filepath.Join(r.dirA, "n.txt"), "n\n")
	commitEverything(t, r.dirA, "more files")
	write(t, filepath.Join(r.dirA, "m.txt"), "m in the turn\n")
	snapshotTurn(t, r.dirA, testSession)
	write(t, filepath.Join(r.dirA, "m.txt"), "m\n")
	write(t, filepath.Join(r.dirA, "n.txt"), "n changed\n")

	v := r.open(t, r.id)
	if !isTurnSource(v) || !slices.Equal(viewPaths(v), []string{"m.txt", "n.txt"}) {
		t.Fatalf("setup: source %T shows %v, want the turn with m.txt and n.txt", v.src, viewPaths(v))
	}

	// An edit keeps the followed place on A whatever the own directory does.
	r.edit(filepath.Join(r.dirA, "n.txt"))
	r.quiet()

	// The own directory moves while a load is in flight and a second batch
	// waits behind it, so the load that follows starts for the moved directory.
	r.run(r.batch(r.rootA, git.Batch{Paths: []string{"n.txt"}}))

	if !v.loading {
		t.Fatal("setup: no load in flight")
	}

	r.hookNow(cwdHook(r.id, r.dirB))
	r.run(r.batch(r.rootA, git.Batch{Paths: []string{"n.txt"}}))
	r.until(t, "the queued load", func() bool {
		v := r.view()

		return v.own == r.dirB && !v.loading && v.queued == nil
	})

	fresh, err := r.engine.Diff(context.Background(), git.BranchSource{Worktree: r.rootA})
	if err != nil {
		t.Fatal(err)
	}

	v = r.view()
	if want := (git.BranchSource{Worktree: r.rootA}); v.src != want || !v.foreign {
		t.Errorf("source %+v foreign %v, want %+v and foreign", v.src, v.foreign, want)
	}

	if got := viewPaths(v); !slices.Equal(got, []string{"n.txt"}) || v.res.Turn != 0 || v.res.Old != fresh.Old {
		t.Errorf("shows %v as turn %d against %q, want n.txt of the branch against %q", got, v.res.Turn, v.res.Old, fresh.Old)
	}
}

// An own directory that is gone, or in no repository, is not the shown root:
// turn mode shows the branch and the turn keys say there are no turns.
func TestTurnModeWithoutAnOwnRepository(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		own  func(t *testing.T) string
	}{
		{"own directory in no repository", func(t *testing.T) string { return t.TempDir() }},
		{"own directory gone", func(t *testing.T) string {
			dir := filepath.Join(t.TempDir(), "gone")
			if err := os.Mkdir(dir, 0o750); err != nil {
				t.Fatal(err)
			}

			return dir
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTurnRig(t)
			own := tt.own(t)

			r.edit(filepath.Join(r.dirB, "a.txt"))
			r.hookNow(cwdHook(r.id, own))

			if tt.name == "own directory gone" {
				if err := os.Remove(own); err != nil {
					t.Fatal(err)
				}
			}

			openIn(t, r.App, core.OpenReview{Pane: r.id, Mode: core.ReviewTurn})
			r.settle()
			r.until(t, "b's branch", r.settledOn(r.rootB, false))

			v := r.view()
			if want := (git.BranchSource{Worktree: r.rootB}); v.src != want || !v.foreign || v.turns != 0 || r.coreMode() != core.ReviewTurn {
				t.Errorf("source %+v foreign %v with %d turns in %q mode, want b's branch in turn mode with no turns", v.src, v.foreign, v.turns, r.coreMode())
			}

			r.notice = ""
			r.run(r.setReviewMode(core.ReviewTurn))

			if want := "no turns in " + filepath.Base(r.dirA) + " · b yet"; r.notice != want {
				t.Errorf("t: notice = %q, want %q", r.notice, want)
			}
		})
	}
}

// A turn is listed in the worktree it was taken in: after the agent's own
// directory moved into b, A's turn is not a turn of b.
func TestOwnDirectoryMovedListsOnlyItsWorktreesTurns(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t, turnFirst)
	r.open(t, r.id)

	r.hookNow(cwdHook(r.id, r.dirB))
	r.until(t, "the drawer to show b", r.viewHolds(func(v *reviewView) bool { return v.root == r.rootB && v.loaded && !v.loading }))

	if v := r.view(); v.turns != 0 || isTurnSource(v) || r.coreMode() != core.ReviewTurn {
		t.Errorf("b has %d turns, source %T, core mode %q, want none, the branch and turn mode kept", v.turns, v.src, r.coreMode())
	}

	r.notice = ""
	r.run(r.stepReviewTurn(-1))
	r.idle(t)

	if r.notice != "no turns yet" {
		t.Errorf("notice %q, want %q", r.notice, "no turns yet")
	}
}

// A session that moved worktrees keeps each turn with the worktree it began in.
func TestPastTurnOfAMovedSessionStaysInItsWorktree(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, testSession, turnFirst)

	const lineOfA = "only in worktree A"

	snapshotTurn(t, r.dirA, testSession)
	write(t, filepath.Join(r.dirA, "a.txt"), "one\n"+lineOfA+"\n")
	r.open(t, r.id)

	r.hookNow(cwdHook(r.id, r.dirB))
	r.until(t, "the drawer to show b", r.viewHolds(func(v *reviewView) bool { return v.root == r.rootB && v.loaded && !v.loading }))

	write(t, filepath.Join(r.dirB, "b.txt"), "changed in b\n")

	done := r.turnStartFirst(t, r.id)

	snapshotTurn(t, r.dirB, testSession)
	done()

	write(t, filepath.Join(r.dirB, "after.txt"), "after the turn\n")
	r.until(t, "b's turn", r.viewHolds(func(v *reviewView) bool { return v.turns > 0 && isTurnSource(v) && !v.loading }))

	got := r.drawn(t)
	if !strings.Contains(got, "turn 1/1") || strings.Contains(got, lineOfA) {
		t.Errorf("the drawer should read turn 1/1 without A's line:\n%s", got)
	}

	r.notice = ""
	r.run(r.stepReviewTurn(-1))
	r.idle(t)

	if want := "turn 1 is the first turn"; r.notice != want {
		t.Errorf("notice %q, want %q", r.notice, want)
	}

	if got := r.drawn(t); strings.Contains(got, lineOfA) {
		t.Errorf("a row holds A's line:\n%s", got)
	}
}

// A turn that changed nothing is not one of the worktree's turns: the drawer
// shows the one before it until a file changes.
func TestQuestionTurnIsNotListed(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t, turnFirst)
	dir := newRepoDir(t)
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	commitEverything(t, dir, "init")

	snapshotTurn(t, dir, testSession)
	write(t, filepath.Join(dir, "a.txt"), "turn one\n")
	snapshotTurn(t, dir, testSession)

	root := rootOf(t, dir)
	id := r.agent(t, dir, testSession)

	v := r.open(t, id)
	r.watching(t, id, root)

	if v.turns != 1 || v.res.Turn != 1 || v.res.Live {
		t.Fatalf("turns %d shown %d live %v, want turn 1 of 1, not live", v.turns, v.res.Turn, v.res.Live)
	}

	if got := r.drawn(t); !strings.Contains(got, "turn 1/1") {
		t.Errorf("the tag does not read turn 1/1:\n%s", got)
	}

	write(t, filepath.Join(dir, "b.txt"), "turn two\n")
	r.run(r.batch(root, git.Batch{Paths: []string{"b.txt"}}))
	r.until(t, "turn 2", r.viewHolds(func(v *reviewView) bool { return v.turns == 2 && v.res.Turn == 2 && v.res.Live && !v.loading }))

	if got := r.drawn(t); !strings.Contains(got, "turn 2/2") {
		t.Errorf("the tag does not read turn 2/2:\n%s", got)
	}
}

// A drawer on a past turn recounts on a batch of plain paths: the open turn may
// have started to differ from the working tree. The turn shown stays.
func TestPastTurnViewRecountsOnPathBatches(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t, turnFirst)
	dir := threeTurnRepo(t)
	root := rootOf(t, dir)
	id := r.agent(t, dir, testSession)

	r.open(t, id)
	r.watching(t, id, root)
	r.run(r.stepReviewTurn(-1))

	v := r.idle(t)
	gen := v.gen

	snapshotTurn(t, dir, testSession)
	write(t, filepath.Join(dir, "four.txt"), "turn four\n")
	r.run(r.batch(root, git.Batch{Paths: []string{"four.txt"}}))
	r.until(t, "the new count", func() bool { return v.turns == 4 && !v.loading })

	if v.res.Turn != 2 || v.gen != gen {
		t.Errorf("the shown turn moved: turn %d, generation %d -> %d", v.res.Turn, gen, v.gen)
	}

	if got := r.drawn(t); !strings.Contains(got, "turn 2/4") {
		t.Errorf("the tag does not read turn 2/4:\n%s", got)
	}
}

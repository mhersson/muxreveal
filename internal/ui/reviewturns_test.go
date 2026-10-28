package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	r := newDrawerRig(t)
	dir := threeTurnRepo(t)
	root := rootOf(t, dir)
	id := r.agent(t, dir, testSession)

	v := r.open(t, id)
	r.watching(t, id, root)

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

	// The new session's first prompt takes its snapshot before the hook arrives.
	snapshotTurn(t, dir, otherSession)
	write(t, filepath.Join(dir, "x.txt"), "after the first prompt\n")
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "UserPromptSubmit", TurnStart: true, State: core.AgentWorking}}))
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

// A turn's snapshot ref never wakes the watcher, so the turn's start event
// recomputes the drawer. A turn that changes no files moves the count and the
// mode row; a past turn only learns the new count.
func TestTurnStartRefreshesTheDrawer(t *testing.T) {
	turnStart := func(id core.PaneID) tea.Msg {
		return hookMsg{ev: harness.Event{Pane: id, Name: "UserPromptSubmit", TurnStart: true, State: core.AgentWorking}}
	}

	t.Run("a turn that changes no files", func(t *testing.T) {
		r := newDrawerRig(t)
		dir := threeTurnRepo(t)
		id := r.agent(t, dir, testSession)

		r.open(t, id)
		r.watching(t, id, rootOf(t, dir))

		snapshotTurn(t, dir, testSession)
		r.run(r.step(turnStart(id)))
		r.until(t, "turn 4", r.viewHolds(func(v *reviewView) bool { return v.turns == 4 && v.res.Turn == 4 && !v.loading }))

		got := r.drawn(t)
		for _, want := range []string{"turn 4 of 4, live", "turn 4 changed nothing"} {
			if !strings.Contains(got, want) {
				t.Errorf("the drawer lacks %q:\n%s", want, got)
			}
		}
	})

	t.Run("a past turn", func(t *testing.T) {
		r := newDrawerRig(t)
		dir := threeTurnRepo(t)
		id := r.agent(t, dir, testSession)

		r.open(t, id)
		r.watching(t, id, rootOf(t, dir))
		r.run(r.stepReviewTurn(-1))

		v := r.idle(t)
		gen := v.gen

		snapshotTurn(t, dir, testSession)
		r.run(r.step(turnStart(id)))
		r.until(t, "the new count", func() bool { return v.turns == 4 && !v.loading })

		if v.res.Turn != 2 || v.gen != gen {
			t.Errorf("a past turn reloaded: turn %d, generation %d -> %d", v.res.Turn, gen, v.gen)
		}

		if got := r.drawn(t); !strings.Contains(got, "turn 2 of 4") {
			t.Errorf("the mode row does not read turn 2 of 4:\n%s", got)
		}
	})
}

// A hook event for a watch-less drawer in a hidden tab marks it stale, as a
// batch would, so it recomputes when its tab shows.
func TestHookEventMarksAHiddenDrawerStale(t *testing.T) {
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

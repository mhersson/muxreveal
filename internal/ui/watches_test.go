package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/harness"
)

const watchWait = 5 * time.Second

// watchApp is a rail app with a real watch set whose commands run on goroutines.
type watchApp struct {
	*railApp
	pm  *pump
	set *git.WatchSet
}

func newWatchApp(t *testing.T, stats bool) *watchApp {
	t.Helper()

	r := newRailApp(t, stats)
	set := git.NewWatchSet(context.Background(), git.WatchOptions{Debounce: 20 * time.Millisecond})

	t.Cleanup(func() { _ = set.Close() })

	r.watches.set = set
	w := &watchApp{railApp: r, pm: newPump(t), set: set}
	w.pm.run(r.waitWatch())
	w.pm.run(r.settle())

	return w
}

// until feeds queued messages to Update until cond holds; seen sees each one first.
func (w *watchApp) until(t *testing.T, what string, seen func(tea.Msg), cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(watchWait)

	for !cond() {
		select {
		case m := <-w.pm.msgs:
			if seen != nil {
				seen(m)
			}

			_, cmd := w.Update(m)
			w.pm.run(cmd)
		case <-time.After(10 * time.Millisecond):
			// A state change outside Update needs a pass of its own.
			w.pm.run(w.settle())
		}

		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (w *watchApp) rootOf(t *testing.T, dir string) string {
	t.Helper()

	r, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	return r.Root
}

func (w *watchApp) watched(root string) bool {
	_, ok := w.set.Seq(root)

	return ok
}

func (w *watchApp) held(id core.PaneID) string {
	if pw := w.watches.panes[id]; pw != nil {
		return pw.root
	}

	return ""
}

func TestAgentPaneAcquiresItsWorktree(t *testing.T) {
	w := newWatchApp(t, false)
	dir := w.repo(t, "proj")
	root := w.rootOf(t, dir)
	id := w.addAgent(t, false, dir)

	w.pm.run(w.settle())
	w.until(t, "the worktree to be watched", nil, func() bool { return w.watched(root) && w.held(id) == root })

	if got, ok := w.paneRoot(id); !ok || got != root {
		t.Errorf("paneRoot() = %q, %v, want %q", got, ok, root)
	}
}

func TestThreeAgentsShareOneWatch(t *testing.T) {
	w := newWatchApp(t, false)
	dir := w.repo(t, "proj")
	root := w.rootOf(t, dir)

	ids := []core.PaneID{w.addAgent(t, false, dir), w.addAgent(t, false, dir), w.addAgent(t, false, dir)}

	w.pm.run(w.settle())
	w.until(t, "all three to hold the watch", nil, func() bool {
		return w.held(ids[0]) == root && w.held(ids[1]) == root && w.held(ids[2]) == root
	})

	for _, id := range ids[:2] {
		w.pm.run(w.closePane(id))
	}

	w.until(t, "two panes to let go", nil, func() bool { return len(w.watches.panes) == 1 })
	w.closing.Wait()

	if !w.watched(root) {
		t.Fatal("closing two of three agents stopped the watch")
	}

	w.pm.run(w.closePane(ids[2]))
	w.until(t, "the last pane to let go", nil, func() bool { return len(w.watches.panes) == 0 })
	w.closing.Wait()

	if w.watched(root) {
		t.Error("the watch outlived its last agent")
	}
}

func TestShellDoesNotAcquire(t *testing.T) {
	w := newWatchApp(t, false)
	dir := w.repo(t, "proj")
	root := w.rootOf(t, dir)

	openIn(t, w.App, core.OpenPane{Kind: core.KindShell, Cwd: dir, Split: core.SplitVertical, Area: w.bodyArea()})

	if cmd := w.settle(); cmd != nil {
		t.Errorf("a shell raised %d message(s)", len(flatten(cmd)))
	}

	if len(w.watches.panes) != 0 || w.watched(root) {
		t.Error("a shell pane holds a watch")
	}
}

func TestCwdToAnotherRepoMovesTheWatch(t *testing.T) {
	w := newWatchApp(t, false)
	a, b := w.repo(t, "a"), w.repo(t, "b")
	rootA, rootB := w.rootOf(t, a), w.rootOf(t, b)
	id := w.addAgent(t, false, a)

	w.pm.run(w.settle())
	w.until(t, "a to be watched", nil, func() bool { return w.held(id) == rootA })

	openIn(t, w.App, core.SetPaneCwd{ID: id, Cwd: b})
	w.pm.run(w.settle())
	w.until(t, "b to be watched", nil, func() bool { return w.held(id) == rootB })
	w.until(t, "a to be released", nil, func() bool { return !w.watched(rootA) })

	// A cwd outside any repository drops the watch.
	openIn(t, w.App, core.SetPaneCwd{ID: id, Cwd: t.TempDir()})
	w.pm.run(w.settle())
	w.until(t, "b to be released", nil, func() bool { return !w.watched(rootB) })

	if _, ok := w.paneRoot(id); ok {
		t.Error("paneRoot() resolved a cwd outside a repository")
	}
}

func TestStaleResolutionIsDropped(t *testing.T) {
	w := newWatchApp(t, false)
	a, b := w.repo(t, "a"), w.repo(t, "b")
	id := w.addAgent(t, false, b)

	w.pm.run(w.settle())
	w.until(t, "b to be held", nil, func() bool { return w.held(id) != "" })

	// An answer for a cwd the pane left must not start a watch.
	w.step(paneRepoMsg{id: id, cwd: a, repo: git.Repo{Root: w.rootOf(t, a)}})

	if w.watched(w.rootOf(t, a)) {
		t.Error("a stale resolution started a watch")
	}
}

func TestStaleAcquisitionIsReleased(t *testing.T) {
	w := newWatchApp(t, false)
	dir := w.repo(t, "proj")
	root := w.rootOf(t, dir)

	repo, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	rel, err := w.set.Acquire(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}

	// The pane is gone by the time the reference arrives.
	w.step(watchAcquiredMsg{id: "p99", repo: repo, release: w.watches.holds.add(rel)})
	w.closing.Wait()

	if w.watched(root) {
		t.Error("a reference for a closed pane was kept")
	}
}

func TestHoldsReleaseOnceAndAfterClose(t *testing.T) {
	var h holds

	n := 0
	rel := h.add(func() { n++ })
	rel()
	rel()
	h.closeAll()

	if n != 1 {
		t.Errorf("released %d times, want 1", n)
	}

	m := 0

	h.add(func() { m++ })()

	if m != 1 {
		t.Errorf("a reference taken after closeAll was released %d times, want 1", m)
	}

	var g holds

	k := 0

	g.add(func() { k++ })
	g.closeAll()
	g.closeAll()

	if k != 1 {
		t.Errorf("closeAll released %d times, want 1", k)
	}
}

func TestRailSeesABranchSwitch(t *testing.T) {
	w := newWatchApp(t, true)
	dir := w.repo(t, "proj")
	// A branch with one more file, so switching to it changes the totals.
	w.gitIn(t, dir, "switch", "-q", "-c", "other")
	write(t, dir+"/b.txt", "b\n")
	w.gitIn(t, dir, "add", "b.txt")
	w.gitIn(t, dir, "commit", "-q", "-m", "b")
	w.gitIn(t, dir, "switch", "-q", "main")

	root := w.rootOf(t, dir)
	id := w.addAgent(t, false, dir)

	w.pm.run(w.settle())
	w.until(t, "the worktree to be watched", nil, func() bool { return w.held(id) == root })

	w.pm.run(w.railOpened())
	w.until(t, "the first probes", nil, func() bool {
		c, ok := w.card(id)

		return ok && c.branch == "main" && c.statsKnown
	})

	c, _ := w.card(id)
	files := c.stats.Files

	// No clock jump: the switch lands inside both intervals.
	w.gitIn(t, dir, "switch", "-q", "other")

	start := time.Now()

	w.until(t, "the card to show other with its totals", nil, func() bool {
		c, _ := w.card(id)

		return c.branch == "other" && c.stats.Files != files
	})

	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the card took %v to follow the switch", d)
	}
}

func TestIndexOnlyChangeCostsNoStatsProbe(t *testing.T) {
	w := newWatchApp(t, true)
	dir := w.repo(t, "proj")
	root := w.rootOf(t, dir)
	id := w.addAgent(t, false, dir)

	w.pm.run(w.settle())
	w.until(t, "the worktree to be watched", nil, func() bool { return w.held(id) == root })

	w.pm.run(w.railOpened())
	w.until(t, "the first probes", nil, func() bool {
		c, ok := w.card(id)

		return ok && c.branch == "main" && c.statsKnown
	})

	heads, stats := 0, 0
	seen := func(m tea.Msg) {
		switch m.(type) {
		case railHeadMsg:
			heads++
		case railStatsMsg:
			stats++
		}
	}

	// Staging rewrites the index and moves no head.
	w.gitIn(t, dir, "add", "a.txt")
	w.until(t, "the head probe the index change caused", seen, func() bool { return heads >= 1 })

	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case m := <-w.pm.msgs:
			seen(m)

			_, cmd := w.Update(m)
			w.pm.run(cmd)
		case <-time.After(20 * time.Millisecond):
		}
	}

	if stats != 0 {
		t.Errorf("an index-only change ran %d stats probes inside the interval", stats)
	}
}

func TestFailedResolveNotifiesOnceAndRetriesOnAHook(t *testing.T) {
	w := newWatchApp(t, false)
	dir := w.repo(t, "proj")
	root := w.rootOf(t, dir)

	w.watches.open = func(context.Context, string) (git.Repo, error) { return git.Repo{}, errors.New("too slow") }

	first := w.addAgent(t, false, dir)
	w.addAgent(t, false, dir)

	w.pm.run(w.settle())

	failures := 0
	seen := func(m tea.Msg) {
		if r, ok := m.(paneRepoMsg); ok && r.err != nil {
			failures++
		}
	}

	w.until(t, "the first failure", seen, func() bool { return failures >= 1 && w.notice != "" })

	if want := "watching proj failed: too slow"; w.notice != want {
		t.Errorf("notice = %q, want %q", w.notice, want)
	}

	w.notice = ""

	w.until(t, "the second failure", seen, func() bool { return failures >= 2 })

	if w.notice != "" {
		t.Errorf("the same project's second failure showed %q", w.notice)
	}

	if w.held(first) != "" || w.watched(root) {
		t.Fatal("a failed resolve left a watch held")
	}

	// A hook event retries, with no timer.
	w.watches.open = git.Open

	w.pm.run(hook(w.App, harness.Event{Pane: first, Name: "Stop"}))
	w.until(t, "the retry to acquire", nil, func() bool { return w.held(first) == root })
}

func TestRailStatsFollowAFileWrite(t *testing.T) {
	w := newWatchApp(t, true)
	dir := w.repo(t, "proj")
	root := w.rootOf(t, dir)
	id := w.addAgent(t, false, dir)

	w.pm.run(w.settle())
	w.until(t, "the worktree to be watched", nil, func() bool { return w.held(id) == root })

	w.pm.run(w.railOpened())
	w.until(t, "the first stats", nil, func() bool {
		c, ok := w.card(id)

		return ok && c.statsKnown && c.stats.Added == 1
	})

	w.clk.Advance(statsEvery + time.Second)
	write(t, dir+"/a.txt", "one\ntwo\nthree\n")

	w.until(t, "the stats to follow the write", nil, func() bool {
		c, _ := w.card(id)

		return c.stats.Added == 2
	})
}

func TestFailedWatchNotifiesOnce(t *testing.T) {
	w := newWatchApp(t, false)
	dir := w.repo(t, "proj")

	// A closed set refuses every Acquire.
	if err := w.set.Close(); err != nil {
		t.Fatal(err)
	}

	w.addAgent(t, false, dir)
	w.addAgent(t, false, dir)

	w.pm.run(w.settle())

	failures := 0
	seen := func(m tea.Msg) {
		if a, ok := m.(watchAcquiredMsg); ok && errors.Is(a.err, git.ErrWatchSetClosed) {
			failures++
		}
	}

	w.until(t, "the first failure", seen, func() bool { return failures >= 1 })
	// Update ran after seen, so wait one more step for the notice.
	w.until(t, "the notice", seen, func() bool { return w.notice != "" })

	if want := "watching proj failed: watch set closed"; w.notice != want {
		t.Errorf("notice = %q, want %q", w.notice, want)
	}

	w.notice = ""

	w.until(t, "the second failure", seen, func() bool { return failures >= 2 })

	if w.notice != "" {
		t.Errorf("the same root's second failure showed %q", w.notice)
	}
}

func TestWatchBatchErrorShowsNothing(t *testing.T) {
	w := newWatchApp(t, false)
	w.step(watchMsg{RootBatch: git.RootBatch{Root: "/x", Err: errors.New("boom")}})

	if w.notice != "" {
		t.Errorf("notice = %q", w.notice)
	}
}

func TestIdleAppDoesNoProbes(t *testing.T) {
	w := newWatchApp(t, true)
	dir := w.repo(t, "proj")
	root := w.rootOf(t, dir)
	id := w.addAgent(t, false, dir)

	w.pm.run(w.settle())
	w.until(t, "the worktree to be watched", nil, func() bool { return w.held(id) == root })
	w.pm.run(w.railOpened())
	w.until(t, "the card", nil, func() bool {
		c, ok := w.card(id)

		return ok && c.statsKnown
	})

	calls := w.gitCalls()

	w.clk.Advance(time.Minute)

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case m := <-w.pm.msgs:
			_, cmd := w.Update(m)
			w.pm.run(cmd)
		case <-time.After(20 * time.Millisecond):
		}
	}

	if got := w.gitCalls(); got != calls {
		t.Errorf("an idle app ran %d git calls", got-calls)
	}
}

func TestAppCloseNoLeaksWithWatches(t *testing.T) {
	base := runtime.NumGoroutine()
	set := git.NewWatchSet(context.Background(), git.WatchOptions{Debounce: 20 * time.Millisecond})
	r := newRealApp(t, "sleep 30; :")
	r.watches.set = set

	cmd := exec.Command("git", "init", "-q", r.cwd)

	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}

	pm := newPump(t)
	_, c := r.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pm.run(c)
	pm.run(r.waitWatch())
	pm.run(r.openPane(core.KindAgent, core.SplitVertical))
	_, c = r.Update(struct{}{})
	pm.run(c)

	deadline := time.Now().Add(watchWait)
	root, _ := git.Open(context.Background(), r.cwd)

	for {
		if _, ok := set.Seq(root.Root); ok {
			break
		}

		if !pm.step(r.App, deadline) {
			t.Fatal("the agent's worktree was never watched")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := set.Close(); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(r.notice, "failed") {
		t.Errorf("notice = %q", r.notice)
	}

	eventually2(t, 2*time.Second, "goroutines to return to baseline", func() bool { return runtime.NumGoroutine() <= base })
}

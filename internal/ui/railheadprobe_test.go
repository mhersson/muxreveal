package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/harness"
)

// repoAhead is a repository on main with an upstream and n commits past it.
func (r *railApp) repoAhead(t *testing.T, name string, n int) string {
	t.Helper()

	dir := r.repo(t, name)
	bare := filepath.Join(t.TempDir(), "remote.git")

	gitIn(t, r.real, filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	gitIn(t, r.real, dir, "remote", "add", "origin", bare)
	gitIn(t, r.real, dir, "push", "-q", "-u", "origin", "main")

	for i := range n {
		gitIn(t, r.real, dir, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("c%d", i))
	}

	_ = os.Remove(r.log)

	return dir
}

// openRail opens the rail as the app does and answers what follows: the head
// probes an update raises for the agents, then the rail's own probes.
func (r *railApp) openRail() []tea.Msg { return r.deliver(tea.Batch(r.settle(), r.railOpened())) }

// gitBatch hands the app a watcher batch for the pane's worktree, as the watch
// set would, and answers the probes it raises. An update that follows drops
// the pane's watch, since no watch set holds one.
func (r *railApp) gitBatch(t *testing.T, id core.PaneID, dir string, b git.Batch) []tea.Msg {
	t.Helper()

	root := rootOf(t, dir)
	r.watches.panes[id] = &paneWatch{want: root, root: root}

	return r.deliver(r.onWatch(watchMsg{Root: root, Batch: b}))
}

func TestHeadProbeRunsWithTheRailClosed(t *testing.T) {
	r := newRailApp(t, true)
	dir := r.repoAhead(t, "proj", 2)
	id := r.addAgent(t, false, dir)

	if r.railIsOpen() {
		t.Fatal("setup: the rail is open")
	}

	// A pane that becomes an agent is probed with no rail to show it.
	msgs := r.deliver(r.settle())
	if heads, _ := probedCwds(msgs); len(heads) != 1 || heads[0] != dir {
		t.Fatalf("settle probed %v, want the agent's cwd", heads)
	}

	c, ok := r.card(id)
	if !ok || c.branch != "main" || c.ahead != 2 {
		t.Fatalf("card = %+v, want branch main, 2 ahead", c)
	}

	// A watcher batch with a git change probes again at once, inside the interval.
	gitIn(t, r.real, dir, "commit", "-q", "--allow-empty", "-m", "another")
	r.clk.Advance(time.Second)

	msgs = r.gitBatch(t, id, dir, git.Batch{Git: true})
	if heads, _ := probedCwds(msgs); len(heads) != 1 || heads[0] != dir {
		t.Fatalf("a git batch probed %v, want the agent's cwd", heads)
	}

	if c, _ := r.card(id); c.ahead != 3 {
		t.Errorf("ahead = %d after a commit, want 3", c.ahead)
	}

	// The branch a checkout lands on shows too.
	gitIn(t, r.real, dir, "checkout", "-q", "-b", "side")
	r.clk.Advance(time.Second)
	r.gitBatch(t, id, dir, git.Batch{Git: true})

	if c, _ := r.card(id); c.branch != "side" || c.ahead != 0 {
		t.Errorf("card = branch %q ahead %d, want side with no upstream", c.branch, c.ahead)
	}
}

func TestHeadProbeOutlivesTheRail(t *testing.T) {
	r := newRailApp(t, true)
	dir := r.repoAhead(t, "proj", 1)
	id := r.addAgent(t, false, dir)
	r.openRail()

	if c, _ := r.card(id); c.ahead != 1 {
		t.Fatalf("ahead = %d, want 1", c.ahead)
	}

	// A head probe running when the rail closes is not cancelled by it.
	r.clk.Advance(time.Minute)
	gitIn(t, r.real, dir, "commit", "-q", "--allow-empty", "-m", "another")

	running := r.railFire(r.store.State(), id, probeHead)
	r.railClosed()

	hm, ok := running().(railHeadMsg)
	if !ok || hm.err != nil {
		t.Fatalf("the running probe ended with %+v, want an answer", hm)
	}

	r.step(hm)

	if c, _ := r.card(id); c.ahead != 2 {
		t.Errorf("ahead = %d after the rail closed, want 2", c.ahead)
	}

	// A trigger inside the interval waits for it, as it does with the rail open.
	r.ticks.delays = nil
	r.clk.Advance(time.Second)

	if msgs := flatten(hook(r.App, idleHook(id))); countOf[railDueMsg](msgs) != 1 || countOf[railHeadMsg](msgs) != 0 {
		t.Fatalf("an idle hook inside the interval ran %v", msgs)
	}

	if len(r.ticks.delays) != 1 || r.ticks.delays[0] != 4*time.Second {
		t.Errorf("delays = %v, want one of 4s", r.ticks.delays)
	}

	r.clk.Advance(4 * time.Second)

	if msgs := flatten(r.step(railDueMsg{pane: id, kind: probeHead})); countOf[railHeadMsg](msgs) != 1 {
		t.Errorf("the due message ran %v, want one head probe", msgs)
	}
}

func TestHeadProbeEndsWithTheApp(t *testing.T) {
	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj"))

	run := r.railFire(r.store.State(), id, probeHead)
	r.cancel()

	hm, ok := run().(railHeadMsg)
	if !ok || !errors.Is(hm.err, context.Canceled) {
		t.Errorf("a probe of a stopped app ended with %+v, want a cancel", hm)
	}
}

func TestStatsAndNameProbesStayOffWithTheRailClosed(t *testing.T) {
	r := newRailApp(t, true)
	dir := r.repoAhead(t, "proj", 1)
	id := r.addAgent(t, false, dir)
	transcript := filepath.Join(dir, "t.jsonl")
	openIn(t, r.App, core.SetAgentSession{ID: id, Session: "s1", Transcript: transcript})

	var msgs []tea.Msg

	msgs = append(msgs, r.deliver(r.settle())...)
	msgs = append(msgs, flatten(hook(r.App, harness.Event{Pane: id, Name: "SessionStart", Start: true, State: core.AgentIdle, Session: "s1", Transcript: transcript}))...)
	msgs = append(msgs, r.gitBatch(t, id, dir, git.Batch{Git: true, Paths: []string{"a.txt"}, Overflow: true})...)
	r.clk.Advance(time.Minute)
	msgs = append(msgs, r.deliver(hook(r.App, idleHook(id)))...)

	if n := countOf[railStatsMsg](msgs) + countOf[railNameMsg](msgs); n != 0 {
		t.Errorf("%d stats or name probes ran with the rail closed: %v", n, msgs)
	}

	for _, m := range msgs {
		if due, ok := m.(railDueMsg); ok && due.kind != probeHead {
			t.Errorf("a delayed %v probe was scheduled with the rail closed", due.kind)
		}
	}

	rp := r.rail.panes[id]
	if rp.stats.target != "" || rp.nameSlot.target != "" || rp.stats.busy || rp.nameSlot.busy {
		t.Errorf("stats slot %+v, name slot %+v: want both untouched", rp.stats, rp.nameSlot)
	}

	// Opening the rail starts them.
	opened := r.deliver(r.railOpened())
	if countOf[railStatsMsg](opened) != 1 || countOf[railNameMsg](opened) != 1 || countOf[railHeadMsg](opened) != 0 {
		t.Errorf("open ran %v, want one stats and one name probe", opened)
	}
}

func TestFocusedShellHeadIsProbed(t *testing.T) {
	r := newRailApp(t, true)
	first, second := r.repoAhead(t, "first", 1), r.repoAhead(t, "second", 2)

	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: first})

	firstID := r.store.State().Tab().Focus

	// A shell that takes focus is probed, with the rail closed and with no agent.
	msgs := r.deliver(r.settle())
	if heads, stats := probedCwds(msgs); len(heads) != 1 || heads[0] != first || len(stats) != 0 {
		t.Fatalf("focusing the shell probed heads %v stats %v, want its head alone", heads, stats)
	}

	if p := r.rail.panes[firstID].probe; p.branch != "main" || p.ahead != 1 || !p.repo {
		t.Errorf("probe = %+v, want branch main, 1 ahead", p)
	}

	// A shell that is not focused is not probed.
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: second})

	secondID := r.store.State().Tab().Focus

	openIn(t, r.App, core.SelectTab{Index: 1})

	if msgs := flatten(r.settle()); len(msgs) != 0 {
		t.Errorf("settling with the first shell focused again ran %v", msgs)
	}

	if _, ok := r.rail.panes[secondID]; ok {
		t.Error("a shell that never had focus has probe memory")
	}

	// It is probed when it takes focus.
	r.clk.Advance(6 * time.Second)
	openIn(t, r.App, core.SelectTab{Index: 2})

	msgs = r.deliver(r.settle())
	if heads, _ := probedCwds(msgs); len(heads) != 1 || heads[0] != second {
		t.Fatalf("focusing the second shell probed %v, want %s", heads, second)
	}

	if p := r.rail.panes[secondID].probe; p.branch != "main" || p.ahead != 2 {
		t.Errorf("probe = %+v, want branch main, 2 ahead", p)
	}

	// Taking focus again probes again, once the interval has passed.
	r.clk.Advance(6 * time.Second)
	openIn(t, r.App, core.SelectTab{Index: 1})

	if heads, _ := probedCwds(r.deliver(r.settle())); len(heads) != 1 || heads[0] != first {
		t.Errorf("refocusing the first shell probed %v, want %s", heads, first)
	}

	// Inside the interval a focus change waits for it.
	openIn(t, r.App, core.SelectTab{Index: 2})
	r.deliver(r.settle())
	openIn(t, r.App, core.SelectTab{Index: 1})

	if msgs := flatten(r.settle()); countOf[railHeadMsg](msgs) != 0 || countOf[railDueMsg](msgs) != 1 {
		t.Errorf("a focus inside the interval ran %v, want one delayed probe", msgs)
	}

	// A change of working directory probes the new one.
	r.clk.Advance(6 * time.Second)
	openIn(t, r.App, core.SetPaneCwd{ID: firstID, Cwd: second})

	if heads, _ := probedCwds(flatten(r.settle())); len(heads) != 1 || heads[0] != second {
		t.Errorf("a cwd change probed %v, want %s", heads, second)
	}
}

// brokenUpstream points the upstream ref at a missing commit: it still resolves,
// but rev-list cannot walk it.
func brokenUpstream(t *testing.T, dir string) {
	t.Helper()
	write(t, filepath.Join(dir, ".git", "refs", "remotes", "origin", "main"), strings.Repeat("1", 40)+"\n")
}

func TestHeadProbeKeepsTheBranchWhenTheCountFails(t *testing.T) {
	t.Run("same cwd", func(t *testing.T) {
		r := newRailApp(t, false)
		dir := r.repoAhead(t, "proj", 2)
		id := r.addAgent(t, false, dir)
		r.deliver(r.settle())

		if c, _ := r.card(id); c.branch != "main" || c.ahead != 2 {
			t.Fatalf("card = branch %q ahead %d, want main, 2", c.branch, c.ahead)
		}

		// A checkout onto a branch whose upstream cannot be counted still shows.
		gitIn(t, r.real, dir, "checkout", "-q", "-b", "feat2", "--track", "origin/main")
		brokenUpstream(t, dir)
		r.clk.Advance(time.Second)
		r.gitBatch(t, id, dir, git.Batch{Git: true})

		if c, _ := r.card(id); c.branch != "feat2" || c.ahead != 0 {
			t.Errorf("card = branch %q ahead %d, want feat2 with no count", c.branch, c.ahead)
		}
	})

	t.Run("new cwd", func(t *testing.T) {
		r := newRailApp(t, false)
		first, second := r.repoAhead(t, "first", 2), r.repoAhead(t, "second", 1)
		brokenUpstream(t, second)

		id := r.addAgent(t, false, first)
		r.deliver(r.settle())

		openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: second})
		r.clk.Advance(6 * time.Second)
		r.deliver(r.settle())

		c, _ := r.card(id)
		if c.project != "second" || c.branch != "main" || c.ahead != 0 {
			t.Errorf("card = project %q branch %q ahead %d, want second on main with no count", c.project, c.branch, c.ahead)
		}
	})
}

func TestHeadProbeCountTimeoutIsNoAnswer(t *testing.T) {
	r := newRailApp(t, false)
	dir := r.repoAhead(t, "proj", 1)
	id := r.addAgent(t, false, dir)
	r.deliver(r.settle())

	// Only rev-list hangs: the probe's own budget runs out inside the count.
	script := "#!/bin/sh\ncase \"$*\" in *rev-list*) exec sleep 30;; esac\nexec '" + r.real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(r.log), "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	r.clk.Advance(time.Minute)

	msgs := flatten(hook(r.App, idleHook(id)))
	if len(msgs) != 1 {
		t.Fatalf("msgs = %v", msgs)
	}

	hm, ok := msgs[0].(railHeadMsg)
	if !ok || !errors.Is(hm.err, context.DeadlineExceeded) {
		t.Fatalf("msg = %+v, want a deadline error", msgs[0])
	}

	r.step(hm)

	if c, _ := r.card(id); c.branch != "main" || c.ahead != 1 {
		t.Errorf("card = branch %q ahead %d, want the previous result", c.branch, c.ahead)
	}
}

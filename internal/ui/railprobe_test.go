package ui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/harness"
)

// gitIn runs the git at bin in dir, with a config that needs nothing from the host.
func gitIn(t *testing.T, bin, dir string, args ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repo makes a repository named name on branch main with one commit and one edit.
func (r *railApp) repo(t *testing.T, name string) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	gitIn(t, r.real, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	gitIn(t, r.real, dir, "add", ".")
	gitIn(t, r.real, dir, "commit", "-q", "-m", "init")
	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\n")

	// The setup's own git calls are not the probes' business.
	_ = os.Remove(r.log)

	return dir
}

func write(t *testing.T, path, s string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func idleHook(id core.PaneID) harness.Event {
	return harness.Event{Pane: id, Name: "Stop", State: core.AgentIdle}
}

func TestRailProbesOnOpen(t *testing.T) {
	r := newRailApp(t, true)
	dir := r.repo(t, "proj")
	id := r.addAgent(t, false, dir)

	if cmd := r.settle(); cmd != nil || r.gitCalls() != 0 {
		t.Fatal("a probe ran while the rail was closed")
	}

	msgs := r.deliver(r.railOpened())
	if countOf[railHeadMsg](msgs) != 1 || countOf[railStatsMsg](msgs) != 1 {
		t.Fatalf("open ran %v", msgs)
	}

	c, ok := r.card(id)
	if !ok || c.project != "proj" || c.branch != "main" || c.detached {
		t.Errorf("card = %+v", c)
	}

	if !c.statsKnown || c.stats.Files != 1 || c.stats.Added != 1 || c.stats.Removed != 0 {
		t.Errorf("stats = %+v known %v", c.stats, c.statsKnown)
	}

	// Opening again changes nothing.
	if r.railOpened() != nil {
		t.Error("a second open probed again")
	}
}

func TestRailDetachedHead(t *testing.T) {
	r := newRailApp(t, false)
	dir := r.repo(t, "proj")
	gitIn(t, r.real, dir, "checkout", "-q", "--detach")
	id := r.addAgent(t, false, dir)
	r.deliver(r.railOpened())

	c, _ := r.card(id)
	if !c.detached || len(c.branch) != 7 {
		t.Errorf("card = %+v, want a detached 7-character id", c)
	}
}

func TestRailProbeIntervals(t *testing.T) {
	r := newRailApp(t, true)
	id := r.addAgent(t, false, r.repo(t, "proj"))
	r.deliver(r.railOpened())

	r.clk.Advance(time.Second)

	// Both triggers fall inside their intervals: one delayed probe each.
	msgs := flatten(hook(r.App, idleHook(id)))
	if countOf[railDueMsg](msgs) != 2 || countOf[railHeadMsg](msgs)+countOf[railStatsMsg](msgs) != 0 {
		t.Fatalf("idle hook inside the intervals ran %v", msgs)
	}

	if want := []time.Duration{4 * time.Second, 9 * time.Second}; len(r.ticks.delays) != 2 || r.ticks.delays[0] != want[0] || r.ticks.delays[1] != want[1] {
		t.Errorf("delays = %v, want %v", r.ticks.delays, want)
	}

	// More triggers while the delayed probes wait add nothing.
	r.ticks.delays = nil

	if msgs := flatten(hook(r.App, idleHook(id))); len(msgs) != 0 || len(r.ticks.delays) != 0 {
		t.Errorf("a second trigger scheduled %v %v", msgs, r.ticks.delays)
	}

	r.clk.Set(t0.Add(5 * time.Second))

	head := railDueMsg{pane: id, kind: probeHead, gen: r.rail.gen}
	if msgs := flatten(r.step(head)); countOf[railHeadMsg](msgs) != 1 {
		t.Errorf("the head due message ran %v", msgs)
	}

	r.clk.Set(t0.Add(10 * time.Second))

	stats := railDueMsg{pane: id, kind: probeStats, gen: r.rail.gen}
	if msgs := flatten(r.step(stats)); countOf[railStatsMsg](msgs) != 1 {
		t.Errorf("the stats due message ran %v", msgs)
	}

	// Nothing pending: a late due message does nothing.
	if msgs := flatten(r.step(head)); len(msgs) != 0 {
		t.Errorf("a due message with nothing pending ran %v", msgs)
	}
}

func TestRailTriggerDuringProbeRunsOneAfter(t *testing.T) {
	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj"))
	cmd := r.railOpened()
	r.clk.Advance(6 * time.Second)

	if msgs := flatten(hook(r.App, harness.Event{Pane: id, Name: "SessionStart", Start: true, State: core.AgentIdle})); len(msgs) != 0 {
		t.Fatalf("a trigger during a probe ran %v", msgs)
	}

	// The first probe's answer starts the one that waited.
	if n := countOf[railHeadMsg](flatten(r.step(flatten(cmd)[0]))); n != 1 {
		t.Errorf("follow-up head probes = %d, want 1", n)
	}
}

func TestRailDropsStaleResults(t *testing.T) {
	t.Run("pane closed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj"))
		msgs := flatten(r.railOpened())
		openIn(t, r.App, core.ClosePane{ID: id})
		r.settle()
		r.step(msgs[0])

		if _, ok := r.rail.panes[id]; ok {
			t.Error("a late answer revived the closed pane's entry")
		}
	})

	t.Run("cwd changed", func(t *testing.T) {
		r := newRailApp(t, false)
		first, second := r.repo(t, "first"), r.repo(t, "second")
		id := r.addAgent(t, false, first)
		msgs := flatten(r.railOpened())

		openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: second})
		r.clk.Advance(6 * time.Second)

		next := r.step(msgs[0])
		if c, _ := r.card(id); c.project == "first" {
			t.Error("the answer for the old cwd was applied")
		}

		// The new cwd gets its own probe.
		follow := flatten(next)
		if len(follow) != 1 {
			t.Fatalf("follow-up = %v", follow)
		}

		if hm, ok := follow[0].(railHeadMsg); !ok || hm.cwd != second {
			t.Fatalf("follow-up = %+v", follow[0])
		}

		r.step(follow[0])

		if c, _ := r.card(id); c.project != "second" {
			t.Errorf("project = %q, want second", c.project)
		}
	})

	t.Run("rail closed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj"))
		cmd := r.railOpened()
		r.railClosed()

		for _, m := range flatten(cmd) {
			if hm, ok := m.(railHeadMsg); ok && !errors.Is(hm.err, context.Canceled) {
				t.Errorf("a probe of a closed rail ended with %v, want a cancel", hm.err)
			}

			r.step(m)
		}

		if rp := r.rail.panes[id]; rp == nil || rp.probe.known {
			t.Error("an answer after the rail closed was applied")
		}

		if r.rail.ctx != nil {
			t.Error("rail context kept")
		}
	})
}

func TestRailTriggersNeedAnOpenRail(t *testing.T) {
	r := newRailApp(t, true)
	id := r.addAgent(t, false, r.repo(t, "proj"))

	if msgs := flatten(hook(r.App, idleHook(id))); len(msgs) != 0 || r.gitCalls() != 0 {
		t.Errorf("a hook probed with the rail closed: %v", msgs)
	}

	r.deliver(r.railOpened())
	r.railClosed()
	r.clk.Advance(time.Minute)
	r.ticks.delays = nil

	if msgs := flatten(hook(r.App, idleHook(id))); len(msgs) != 0 || len(r.ticks.delays) != 0 {
		t.Errorf("a hook probed after the rail closed: %v", msgs)
	}
}

func TestRailSlowGitIsCancelledAndKeepsTheLastResult(t *testing.T) {
	r := newRailApp(t, false)
	dir := r.repo(t, "proj")
	id := r.addAgent(t, false, dir)
	r.deliver(r.railOpened())
	write(t, r.slow, "")
	r.clk.Advance(6 * time.Second)

	start := time.Now()
	msgs := flatten(hook(r.App, idleHook(id)))

	if took := time.Since(start); took < railProbeTimeout || took > railProbeTimeout+3*time.Second {
		t.Errorf("slow probe took %v, want about %v", took, railProbeTimeout)
	}

	if len(msgs) != 1 {
		t.Fatalf("msgs = %v", msgs)
	}

	hm, ok := msgs[0].(railHeadMsg)
	if !ok || !errors.Is(hm.err, context.DeadlineExceeded) {
		t.Fatalf("msg = %+v, want a deadline error", msgs[0])
	}

	r.step(hm)

	if c, _ := r.card(id); c.project != "proj" || c.branch != "main" {
		t.Errorf("card = %+v, want the previous result", c)
	}
}

func TestRailProjectNames(t *testing.T) {
	r := newRailApp(t, true)

	plain := t.TempDir()
	gone := filepath.Join(t.TempDir(), "vanished")

	src := r.repo(t, "src")
	bare := filepath.Join(t.TempDir(), "b.git")
	gitIn(t, r.real, filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	gitIn(t, r.real, src, "push", "-q", bare, "main")
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, r.real, bare, "worktree", "add", "-q", wt, "main")

	wtsrc := filepath.Join(t.TempDir(), "linked")
	gitIn(t, r.real, src, "worktree", "add", "-q", "-b", "side", wtsrc)

	cases := []struct {
		name, dir, project, branch string
		stats                      bool
	}{
		{"not a repo", plain, filepath.Base(plain), "", false},
		{"deleted cwd", gone, "vanished", "", false},
		{"bare repo worktree", wt, "b", "main", true},
		{"linked worktree", wtsrc, "src", "side", true},
	}

	for _, c := range cases {
		id := r.addAgent(t, true, c.dir)
		r.deliver(r.railOpened())
		r.railClosed()

		got, _ := r.card(id)
		if got.project != c.project || got.branch != c.branch || got.statsKnown != c.stats {
			t.Errorf("%s: card = project %q branch %q stats %v; want %q %q %v", c.name, got.project, got.branch, got.statsKnown, c.project, c.branch, c.stats)
		}
	}
}

func TestRailIdleShellsCostNothing(t *testing.T) {
	r := newRailApp(t, true)

	if cmd := r.railOpened(); cmd != nil {
		t.Error("opening over shells probed")
	}

	if msgs := flatten(r.settle()); len(msgs) != 0 || len(r.ticks.delays) != 0 || r.gitCalls() != 0 {
		t.Errorf("work with nothing to do: %v %v %d", msgs, r.ticks.delays, r.gitCalls())
	}
}

func TestRailStatsOffWithoutTempDir(t *testing.T) {
	r := newRailApp(t, false)
	r.addAgent(t, false, r.repo(t, "proj"))

	if n := countOf[railStatsMsg](r.deliver(r.railOpened())); n != 0 {
		t.Errorf("stats probes = %d, want 0", n)
	}
}

func TestRailReopenProbesAgain(t *testing.T) {
	r := newRailApp(t, true)
	id := r.addAgent(t, false, r.repo(t, "proj"))
	r.deliver(r.railOpened())
	r.railClosed()

	// Inside the intervals the reopen schedules one delayed probe of each kind.
	r.ticks.delays = nil

	if msgs := flatten(r.railOpened()); countOf[railDueMsg](msgs) != 2 {
		t.Fatalf("reopen inside the intervals ran %v", msgs)
	}

	r.railClosed()
	r.clk.Advance(time.Minute)

	msgs := r.deliver(r.railOpened())
	if countOf[railHeadMsg](msgs) != 1 || countOf[railStatsMsg](msgs) != 1 {
		t.Fatalf("reopen with the same cwd ran %v", msgs)
	}

	if c, _ := r.card(id); !c.statsKnown {
		t.Error("no stats after the reopen")
	}
}

func TestRailAnswerOfAnEarlierOpenIsDropped(t *testing.T) {
	r := newRailApp(t, false)
	dir := r.repo(t, "proj")
	id := r.addAgent(t, false, dir)

	r.railOpened()
	oldGen := r.rail.gen
	r.railClosed()
	r.clk.Advance(time.Minute)

	newCmd := r.railOpened()
	if r.rail.gen == oldGen || !r.rail.panes[id].head.busy {
		t.Fatal("the reopen did not start a probe")
	}

	stale := railHeadMsg{pane: id, cwd: dir, gen: oldGen, res: railProbe{project: "stale", known: true, headCwd: dir, repo: true}}
	r.step(stale)

	if rp := r.rail.panes[id]; rp.probe.known {
		t.Error("a successful answer of an earlier open was applied")
	}

	if !r.rail.panes[id].head.busy {
		t.Error("the stale answer freed the new open's probe")
	}

	r.deliver(newCmd)

	if c, _ := r.card(id); c.branch != "main" {
		t.Errorf("card = %+v after the new answer", c)
	}

	// With the rail closed and no reopen, a successful answer is dropped too.
	r.railClosed()
	r.step(railHeadMsg{pane: id, cwd: dir, gen: r.rail.gen, res: railProbe{project: "late", known: true, headCwd: dir}})

	if c, _ := r.card(id); c.project == "late" {
		t.Error("an answer after the close was applied")
	}
}

func TestRailCardKeepsItsResultUntilTheNewCwdAnswers(t *testing.T) {
	r := newRailApp(t, true)
	first, second := r.repo(t, "first"), r.repo(t, "second")
	id := r.addAgent(t, false, first)
	r.deliver(r.railOpened())

	openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: second})
	r.clk.Advance(time.Minute)
	r.settle()

	if c, _ := r.card(id); c.project != "first" || c.branch != "main" || !c.statsKnown {
		t.Fatalf("card = %+v, want the previous result until the new cwd answers", c)
	}

	// A failed probe of the new cwd clears it.
	r.step(railHeadMsg{pane: id, cwd: second, gen: r.rail.gen, err: context.DeadlineExceeded})
	r.step(railStatsMsg{pane: id, cwd: second, gen: r.rail.gen, err: context.DeadlineExceeded})

	c, _ := r.card(id)
	if c.project != "second" || c.branch != "" || c.statsKnown {
		t.Errorf("card = %+v, want the cwd's base name with no branch and no stats", c)
	}
}

func TestRailFailedProbeOfTheSameCwdKeepsTheResult(t *testing.T) {
	r := newRailApp(t, true)
	id := r.addAgent(t, false, r.repo(t, "proj"))
	r.deliver(r.railOpened())

	cwd := r.store.State().Panes[id].Cwd
	r.step(railHeadMsg{pane: id, cwd: cwd, gen: r.rail.gen, err: context.DeadlineExceeded})
	r.step(railStatsMsg{pane: id, cwd: cwd, gen: r.rail.gen, err: context.DeadlineExceeded})

	if c, _ := r.card(id); c.branch != "main" || !c.statsKnown {
		t.Errorf("card = %+v, want the previous result", c)
	}
}

// gate replaces the fake git with one that logs its start, holds a marker file
// while it waits and blocks until the block file is removed.
type gate struct {
	block, running string
}

func (r *railApp) gate(t *testing.T) gate {
	t.Helper()

	dir := t.TempDir()
	g := gate{block: filepath.Join(dir, "block"), running: filepath.Join(dir, "running")}

	if err := os.Mkdir(g.running, 0o755); err != nil {
		t.Fatal(err)
	}

	write(t, g.block, "")

	script := "#!/bin/sh\necho \"$PWD $*\" >> '" + r.log + "'\ntouch '" + g.running + "'/$$\n" +
		"while [ -e '" + g.block + "' ]; do sleep 0.02; done\nrm -f '" + g.running + "'/$$\nexec '" + r.real + "' \"$@\"\n"

	if err := os.WriteFile(filepath.Join(filepath.Dir(r.log), "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	return g
}

func (g gate) alive() int {
	ents, _ := os.ReadDir(g.running)

	return len(ents)
}

// runAll runs the commands of a batch at once and sends each message on the channel.
func runAll(cmd tea.Cmd) <-chan tea.Msg {
	out := make(chan tea.Msg, 64)

	b, ok := cmd().(tea.BatchMsg)
	if !ok {
		close(out)

		return out
	}

	var wg sync.WaitGroup

	for _, c := range b {
		wg.Add(1)

		go func() {
			defer wg.Done()

			out <- c()
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

func agentsInOneRepo(t *testing.T, r *railApp, n int) {
	t.Helper()

	dir := r.repo(t, "proj")
	for i := range n {
		r.addAgent(t, i%2 == 0, dir)
	}

	_ = os.Remove(r.log)
}

func TestRailProbesRunFourAtATime(t *testing.T) {
	r := newRailApp(t, true)
	agentsInOneRepo(t, r, 10)
	g := r.gate(t)

	msgs := runAll(r.railOpened())

	deadline := time.Now().Add(5 * time.Second)
	for g.alive() < railProbeMax && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if g.alive() != railProbeMax {
		t.Fatalf("running probes = %d, want %d", g.alive(), railProbeMax)
	}

	peak := 0

	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		peak = max(peak, g.alive())
	}

	if peak != railProbeMax {
		t.Errorf("peak concurrent probes = %d while blocked, want %d", peak, railProbeMax)
	}

	if err := os.Remove(g.block); err != nil {
		t.Fatal(err)
	}

	got := 0

	for {
		select {
		case <-msgs:
			got++
		case <-time.After(20 * time.Second):
			t.Fatalf("only %d of 20 probes finished after the release", got)
		}

		if got == 20 {
			break
		}
	}

	if peak = max(peak, g.alive()); peak > railProbeMax {
		t.Errorf("peak = %d, want at most %d", peak, railProbeMax)
	}
}

func TestRailClosingFreesWaitingProbes(t *testing.T) {
	r := newRailApp(t, true)
	agentsInOneRepo(t, r, 10)
	g := r.gate(t)

	msgs := runAll(r.railOpened())

	deadline := time.Now().Add(5 * time.Second)
	for g.alive() < railProbeMax && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	r.railClosed()

	got := 0

	for got < 20 {
		select {
		case <-msgs:
			got++
		case <-time.After(5 * time.Second):
			t.Fatalf("%d of 20 probes returned after the rail closed; waiting probes were not cancelled", got)
		}
	}

	if n := r.gitCalls(); n > railProbeMax {
		t.Errorf("git started %d times, want the waiting probes never to start it (at most %d)", n, railProbeMax)
	}
}

func TestRailNonRepoAgentDoesNotRefireItsStatsProbe(t *testing.T) {
	r := newRailApp(t, true)
	first, second := t.TempDir(), t.TempDir()
	id := r.addAgent(t, false, first)
	r.deliver(r.railOpened())

	if rp := r.rail.panes[id]; !rp.probe.known || rp.probe.repo {
		t.Fatalf("setup: probe = %+v, want a known non-repo", rp.probe)
	}

	// Past the head interval but inside the stats one: the head probe answers
	// before the delayed stats probe comes due.
	r.clk.Advance(6 * time.Second)
	openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: second})
	r.deliver(r.settle())

	// The head probe of the new cwd answered; the stats probe has nothing to count.
	if got := r.rail.panes[id].stats.target; got != second {
		t.Errorf("stats slot cwd = %q, want %q so that no update asks again", got, second)
	}
}

func TestRailStatsProbeRunsWhenAnAgentMovesIntoARepo(t *testing.T) {
	r := newRailApp(t, true)
	plain := t.TempDir()
	repo := r.repo(t, "proj")
	id := r.addAgent(t, false, plain)
	r.deliver(r.railOpened())
	r.clk.Advance(11 * time.Second)

	openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: repo})

	msgs := r.deliver(r.settle())
	if countOf[railStatsMsg](msgs) != 1 {
		t.Fatalf("moving into a repo ran %v, want one stats probe", msgs)
	}

	if c, _ := r.card(id); !c.statsKnown || c.stats.Files != 1 {
		t.Errorf("card = %+v, want the repo's stats", c)
	}
}

// rewriteTranscript writes a synthetic transcript whose only custom-title entry
// names the session name, among other lines.
func rewriteTranscript(t *testing.T, path, name string) {
	t.Helper()

	title, err := json.Marshal(name)
	if err != nil {
		t.Fatal(err)
	}

	write(t, path, `{"type":"user","message":"hi"}`+"\n"+
		`{"type":"custom-title","customTitle":`+string(title)+`,"sessionId":"s"}`+"\n"+
		`{"type":"assistant","message":"hello"}`+"\n")
}

// transcriptNamed is a new transcript file for a session named name.
func transcriptNamed(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	rewriteTranscript(t, path, name)

	return path
}

// session gives an agent a session and its transcript, as a session start does.
func (r *railApp) session(t *testing.T, id core.PaneID, transcript string) {
	t.Helper()
	openIn(t, r.App, core.SetAgentSession{ID: id, Session: core.SessionID("s-" + string(id)), Transcript: transcript})
}

func (r *railApp) name(t *testing.T, id core.PaneID) string {
	t.Helper()

	c, ok := r.card(id)
	if !ok {
		t.Fatalf("no card for %s", id)
	}

	return c.name
}

func TestRailNameProbeOnOpen(t *testing.T) {
	r := newRailApp(t, false)
	dir := r.repo(t, "proj")
	named := r.addAgent(t, false, dir)
	bare := r.addAgent(t, true, dir)
	r.session(t, named, transcriptNamed(t, "muxr-orchestrator"))

	if cmd := r.settle(); cmd != nil {
		t.Fatal("a probe ran while the rail was closed")
	}

	msgs := r.deliver(r.railOpened())

	// Only the agent that has a transcript is asked.
	if n := countOf[railNameMsg](msgs); n != 1 {
		t.Fatalf("open ran %d name probes, want 1: %v", n, msgs)
	}

	if got := r.name(t, named); got != "muxr-orchestrator" {
		t.Errorf("name = %q, want muxr-orchestrator", got)
	}

	if got := r.name(t, bare); got != "" {
		t.Errorf("an agent with no session has the name %q", got)
	}
}

func TestRailNameProbeTriggers(t *testing.T) {
	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj"))
	r.deliver(r.railOpened())

	transcript := transcriptNamed(t, "first")

	// A session start reports the transcript; the name is read at once.
	r.clk.Advance(6 * time.Second)

	start := harness.Event{Pane: id, Name: "SessionStart", Start: true, Session: "s-1", Transcript: transcript, State: core.AgentIdle}
	cmd := hook(r.App, start)

	if got := r.name(t, id); got != "" {
		t.Errorf("the hook read the file on the update loop: name = %q", got)
	}

	if n := countOf[railNameMsg](r.deliver(cmd)); n != 1 {
		t.Fatalf("a session start ran %d name probes, want 1", n)
	}

	if got := r.name(t, id); got != "first" {
		t.Errorf("name after the session start = %q, want first", got)
	}

	// A rename happens between prompts: the next idle hook sees it.
	rewriteTranscript(t, transcript, "second")
	r.clk.Advance(6 * time.Second)

	if n := countOf[railNameMsg](r.deliver(hook(r.App, idleHook(id)))); n != 1 {
		t.Fatalf("an idle hook ran %d name probes, want 1", n)
	}

	if got := r.name(t, id); got != "second" {
		t.Errorf("name after the idle hook = %q, want second", got)
	}

	// A hook that keeps the agent working reads nothing.
	rewriteTranscript(t, transcript, "third")
	r.clk.Advance(6 * time.Second)

	working := harness.Event{Pane: id, Name: "PostToolUse", State: core.AgentWorking}
	if n := countOf[railNameMsg](r.deliver(hook(r.App, working))); n != 0 {
		t.Errorf("a working hook ran %d name probes", n)
	}

	if got := r.name(t, id); got != "second" {
		t.Errorf("name after a working hook = %q, want second", got)
	}
}

// A session start replaces the session: the probe it raises reads the
// transcript the hook reports, not the earlier session's.
func TestRailNameProbeAfterASessionStartReadsTheNewTranscript(t *testing.T) {
	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj"))
	r.session(t, id, transcriptNamed(t, "old"))
	r.deliver(r.railOpened())

	if got := r.name(t, id); got != "old" {
		t.Fatalf("setup name = %q", got)
	}

	r.clk.Advance(6 * time.Second)

	next := transcriptNamed(t, "new")
	start := harness.Event{Pane: id, Name: "SessionStart", Start: true, Session: "s-2", Transcript: next, State: core.AgentIdle}

	var read []string

	for _, m := range flatten(hook(r.App, start)) {
		if nm, ok := m.(railNameMsg); ok {
			read = append(read, nm.transcript)
		}
	}

	if len(read) != 1 || read[0] != next {
		t.Errorf("the session start read %q, want only %q", read, next)
	}
}

func TestRailNameProbeForAPaneThatBecomesAnAgent(t *testing.T) {
	r := newRailApp(t, false)
	r.deliver(r.railOpened())

	id := r.addAgent(t, false, r.repo(t, "proj"))
	r.session(t, id, transcriptNamed(t, "newcomer"))

	if n := countOf[railNameMsg](r.deliver(r.settle())); n != 1 {
		t.Fatalf("a new agent with a transcript ran %d name probes, want 1", n)
	}

	if got := r.name(t, id); got != "newcomer" {
		t.Errorf("name = %q, want newcomer", got)
	}

	// Nothing changed since: the next update asks nothing.
	if msgs := r.deliver(r.settle()); countOf[railNameMsg](msgs) != 0 {
		t.Errorf("an update with nothing new ran %v", msgs)
	}
}

func TestRailNameProbeInterval(t *testing.T) {
	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj"))
	transcript := transcriptNamed(t, "first")
	r.session(t, id, transcript)
	r.deliver(r.railOpened())

	r.clk.Advance(time.Second)
	rewriteTranscript(t, transcript, "second")

	// Inside the interval one delayed probe is scheduled, and nothing runs yet.
	r.ticks.delays = nil
	msgs := flatten(r.railFire(r.store.State(), id, probeName))

	if len(msgs) != 1 || countOf[railDueMsg](msgs) != 1 {
		t.Fatalf("a trigger inside the interval ran %v, want one delayed probe", msgs)
	}

	if len(r.ticks.delays) != 1 || r.ticks.delays[0] != 4*time.Second {
		t.Errorf("delays = %v, want [4s]", r.ticks.delays)
	}

	// More triggers while it waits add nothing.
	if cmd := r.railFire(r.store.State(), id, probeName); cmd != nil {
		t.Errorf("a second trigger scheduled %v", flatten(cmd))
	}

	if got := r.name(t, id); got != "first" {
		t.Errorf("name before the interval ended = %q, want first", got)
	}

	r.clk.Set(t0.Add(5 * time.Second))

	due, ok := msgs[0].(railDueMsg)
	if !ok || due.kind != probeName {
		t.Fatalf("delayed message = %+v, want a name due message", msgs[0])
	}

	if n := countOf[railNameMsg](r.deliver(r.step(due))); n != 1 {
		t.Fatalf("the due message ran %d name probes, want 1", n)
	}

	if got := r.name(t, id); got != "second" {
		t.Errorf("name after the interval = %q, want second", got)
	}

	// Nothing pending: a late due message does nothing.
	if msgs := flatten(r.step(due)); len(msgs) != 0 {
		t.Errorf("a due message with nothing pending ran %v", msgs)
	}
}

func TestRailNameTriggerDuringProbeRunsOneAfter(t *testing.T) {
	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj"))
	r.session(t, id, transcriptNamed(t, "first"))

	cmd := r.railOpened()

	r.clk.Advance(6 * time.Second)

	// The first probe has not answered: a trigger waits for it.
	if cmd := r.railFire(r.store.State(), id, probeName); cmd != nil {
		t.Fatalf("a trigger during a probe ran %v", flatten(cmd))
	}

	var follow []tea.Msg

	for _, m := range flatten(cmd) {
		if _, ok := m.(railNameMsg); ok {
			follow = flatten(r.step(m))
		} else {
			r.step(m)
		}
	}

	if n := countOf[railNameMsg](follow); n != 1 {
		t.Errorf("follow-up name probes = %d, want 1", n)
	}
}

func TestRailNameProbeDropsStaleResults(t *testing.T) {
	t.Run("pane closed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj"))
		r.session(t, id, transcriptNamed(t, "gone"))
		msgs := flatten(r.railOpened())

		openIn(t, r.App, core.ClosePane{ID: id})
		r.settle()

		for _, m := range msgs {
			r.step(m)
		}

		if _, ok := r.rail.panes[id]; ok {
			t.Error("a late answer revived the closed pane's entry")
		}
	})

	t.Run("session changed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj"))
		r.session(t, id, transcriptNamed(t, "old"))
		msgs := flatten(r.railOpened())

		next := transcriptNamed(t, "new")
		r.session(t, id, next)

		var follow []tea.Msg

		for _, m := range msgs {
			if _, ok := m.(railNameMsg); ok {
				follow = flatten(r.step(m))
			} else {
				r.step(m)
			}
		}

		if got := r.name(t, id); got != "" {
			t.Errorf("the answer for the old transcript was applied: name = %q", got)
		}

		// The new transcript gets its own probe once the interval ends.
		r.clk.Advance(6 * time.Second)

		for _, m := range follow {
			r.deliver(r.step(m))
		}

		if got := r.name(t, id); got != "new" {
			t.Errorf("name = %q, want new", got)
		}
	})

	t.Run("rail closed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj"))
		r.session(t, id, transcriptNamed(t, "late"))
		cmd := r.railOpened()
		r.railClosed()

		for _, m := range flatten(cmd) {
			if nm, ok := m.(railNameMsg); ok && !errors.Is(nm.err, context.Canceled) {
				t.Errorf("a probe of a closed rail ended with %v, want a cancel", nm.err)
			}

			r.step(m)
		}

		if rp := r.rail.panes[id]; rp == nil || rp.name.text != "" {
			t.Error("an answer after the rail closed was applied")
		}
	})

	t.Run("a hook with the rail closed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj"))
		r.session(t, id, transcriptNamed(t, "x"))

		if msgs := flatten(hook(r.App, idleHook(id))); countOf[railNameMsg](msgs) != 0 {
			t.Errorf("a hook read a transcript with the rail closed: %v", msgs)
		}
	})
}

func TestRailNameProbeFailures(t *testing.T) {
	setup := func(t *testing.T) (*railApp, core.PaneID, string) {
		t.Helper()

		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj"))
		transcript := transcriptNamed(t, "kept")
		r.session(t, id, transcript)
		r.deliver(r.railOpened())

		if got := r.name(t, id); got != "kept" {
			t.Fatalf("setup name = %q", got)
		}

		r.clk.Advance(6 * time.Second)

		return r, id, transcript
	}

	t.Run("a read that fails keeps the last name", func(t *testing.T) {
		r, id, transcript := setup(t)

		// A directory where the file was cannot be read.
		if err := os.Remove(transcript); err != nil {
			t.Fatal(err)
		}

		if err := os.Mkdir(transcript, 0o755); err != nil {
			t.Fatal(err)
		}

		r.deliver(hook(r.App, idleHook(id)))

		if got := r.name(t, id); got != "kept" {
			t.Errorf("name after a failed read = %q, want kept", got)
		}
	})

	t.Run("a file that is gone means no name", func(t *testing.T) {
		r, id, transcript := setup(t)

		if err := os.Remove(transcript); err != nil {
			t.Fatal(err)
		}

		r.deliver(hook(r.App, idleHook(id)))

		if got := r.name(t, id); got != "" {
			t.Errorf("name after the file went = %q, want none", got)
		}
	})

	t.Run("a failed read of another session does not keep this name", func(t *testing.T) {
		r, id, _ := setup(t)
		r.session(t, id, t.TempDir()) // the new transcript is unreadable

		r.deliver(r.settle())
		r.deliver(hook(r.App, idleHook(id)))

		if got := r.name(t, id); got != "" {
			t.Errorf("name = %q, want none: it belongs to the earlier session", got)
		}
	})
}

func TestRailNameProbeSharesTheProbeSlots(t *testing.T) {
	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj"))
	r.deliver(r.railOpened())
	r.session(t, id, transcriptNamed(t, "waited"))

	cmd := r.railFire(r.store.State(), id, probeName)
	if cmd == nil {
		t.Fatal("no name probe started")
	}

	for range railProbeMax {
		r.probeSem <- struct{}{}
	}

	done := make(chan tea.Msg, 1)

	go func() { done <- cmd() }()

	select {
	case <-done:
		t.Fatal("a name probe ran with every probe slot taken")
	case <-time.After(150 * time.Millisecond):
	}

	<-r.probeSem

	select {
	case msg := <-done:
		if nm, ok := msg.(railNameMsg); !ok || nm.name != "waited" {
			t.Errorf("answer = %+v, want the name", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the name probe did not run once a slot was free")
	}

	for range railProbeMax - 1 {
		<-r.probeSem
	}
}

func TestRailNameProbeCleansWhatItReads(t *testing.T) {
	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj"))
	r.session(t, id, transcriptNamed(t, "ok\x1b]0;pwn\x07\u202eed\u009b2J"))
	r.deliver(r.railOpened())

	if got := r.name(t, id); got != "ok]0;pwned2J" {
		t.Errorf("name = %q, want the hostile characters dropped", got)
	}

	view := r.View().Content
	for _, bad := range []string{"\x1b]", "\u202e", "\u009b", "\x07"} {
		if strings.Contains(view, bad) {
			t.Errorf("the frame holds %q", bad)
		}
	}
}

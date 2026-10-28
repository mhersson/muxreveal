package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/harness"
)

const gateSession = "11111111-2222-4333-8444-555555555555"

// gateRig runs a gate over fake snapshot functions.
type gateRig struct {
	in   chan harness.Event
	out  chan harness.Event
	done chan struct{}
}

func noSeq(context.Context, string) (string, uint64, bool) { return "", 0, false }

func startGate(t *testing.T, snap, mark snapshotFunc) *gateRig {
	t.Helper()

	return startSeqGate(t, snap, mark, noSeq)
}

func startSeqGate(t *testing.T, snap, mark snapshotFunc, seq seqFunc) *gateRig {
	t.Helper()

	r := &gateRig{in: make(chan harness.Event, 16), out: make(chan harness.Event, 16), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer close(r.done)

		runTurnGate(ctx, r.in, r.out, snap, mark, seq, discardLogger())
	}()

	t.Cleanup(func() {
		cancel()
		<-r.done
	})

	return r
}

// turnEvent returns a turn start whose Done closes the returned channel.
func turnEvent(name, session, cwd string) (harness.Event, <-chan struct{}) {
	released := make(chan struct{})
	ev := harness.Event{
		Name: name, Session: core.SessionID(session), Cwd: cwd, TurnStart: true,
		Done: sync.OnceFunc(func() { close(released) }),
	}

	return ev, released
}

func within(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("%s did not happen within %v", what, d)
	}
}

func notWithin(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()

	select {
	case <-ch:
		t.Fatalf("%s happened too early", what)
	case <-time.After(d):
	}
}

func TestGateForwardsEventsInOrderWithoutWaiting(t *testing.T) {
	release := make(chan struct{})
	snap := func(ctx context.Context, _, _ string, _ bool) error {
		select {
		case <-release:
		case <-ctx.Done():
		}

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })
	turn, released := turnEvent("UserPromptSubmit", gateSession, "/repo")

	r.in <- turn

	r.in <- harness.Event{Name: "PreToolUse"}

	r.in <- harness.Event{Name: "Stop"}

	for _, want := range []string{"UserPromptSubmit", "PreToolUse", "Stop"} {
		select {
		case ev := <-r.out:
			if ev.Name != want {
				t.Fatalf("event = %q, want %q", ev.Name, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("event %q was held behind the snapshot", want)
		}
	}

	notWithin(t, released, 100*time.Millisecond, "Done before the snapshot returned")
	close(release)
	within(t, released, time.Second, "Done after the snapshot")
}

// forwarded reads the next event the gate forwards.
func forwarded(t *testing.T, r *gateRig) harness.Event {
	t.Helper()

	select {
	case ev := <-r.out:
		return ev
	case <-time.After(time.Second):
		t.Fatal("the gate forwarded nothing")

		return harness.Event{}
	}
}

// A forwarded turn start carries a channel that closes when its snapshot is
// done, so the app can wait for the turn to exist before it counts turns.
func TestGateSnappedClosesWhenTheSnapshotIsDone(t *testing.T) {
	release := make(chan struct{})
	snap := func(ctx context.Context, _, _ string, _ bool) error {
		select {
		case <-release:
		case <-ctx.Done():
		}

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })
	turn, _ := turnEvent("UserPromptSubmit", gateSession, "/repo")

	r.in <- turn

	ev := forwarded(t, r)
	if ev.Snapped == nil {
		t.Fatal("the forwarded turn start has no Snapped channel")
	}

	notWithin(t, ev.Snapped, 100*time.Millisecond, "Snapped before the snapshot returned")
	close(release)
	within(t, ev.Snapped, time.Second, "Snapped after the snapshot")
}

// A turn whose snapshot failed gets a marker that counts as a turn, so Snapped
// closes after the marker is written, not before.
func TestGateSnappedWaitsForTheMarkerOfAFailedSnapshot(t *testing.T) {
	releaseMark := make(chan struct{})
	snap := func(context.Context, string, string, bool) error { return errors.New("no tree") }
	mark := func(ctx context.Context, _, _ string, _ bool) error {
		select {
		case <-releaseMark:
		case <-ctx.Done():
		}

		return nil
	}

	r := startGate(t, snap, mark)
	turn, _ := turnEvent("UserPromptSubmit", gateSession, "/repo")

	r.in <- turn

	ev := forwarded(t, r)
	if ev.Snapped == nil {
		t.Fatal("the forwarded turn start has no Snapped channel")
	}

	notWithin(t, ev.Snapped, 100*time.Millisecond, "Snapped before the marker was written")
	close(releaseMark)
	within(t, ev.Snapped, time.Second, "Snapped after the marker")
}

// Only a turn that gets a snapshot carries Snapped: other events and turn
// starts the gate cannot snapshot leave it nil.
func TestGateSnappedIsNilWithoutASnapshot(t *testing.T) {
	fn := func(context.Context, string, string, bool) error { return nil }
	r := startGate(t, fn, fn)

	r.in <- harness.Event{Name: "PreToolUse"}

	for _, ev := range []struct{ session, cwd string }{{gateSession, ""}, {"not-a-uuid", "/repo"}} {
		turn, _ := turnEvent("UserPromptSubmit", ev.session, ev.cwd)
		r.in <- turn
	}

	for range 3 {
		if got := forwarded(t, r); got.Snapped != nil {
			t.Errorf("%s (session %q, cwd %q) carries Snapped, want nil", got.Name, got.Session, got.Cwd)
		}
	}
}

// startUnreadGate runs a gate whose out channel is unbuffered and never read.
func startUnreadGate(t *testing.T, snap snapshotFunc) chan<- harness.Event {
	t.Helper()

	in := make(chan harness.Event, 4)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer close(done)

		runTurnGate(ctx, in, make(chan harness.Event), snap, func(context.Context, string, string, bool) error { return nil }, noSeq, discardLogger())
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	return in
}

func TestGateUsesTheEventDeadline(t *testing.T) {
	got := make(chan time.Time, 1)
	snap := func(ctx context.Context, _, _ string, _ bool) error {
		dl, _ := ctx.Deadline()
		got <- dl

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })
	turn, _ := turnEvent("UserPromptSubmit", gateSession, "/repo")
	turn.Deadline = time.Now().Add(300 * time.Millisecond)

	r.in <- turn

	select {
	case dl := <-got:
		if !dl.Equal(turn.Deadline) {
			t.Errorf("snapshot deadline = %v, want the event's %v", dl, turn.Deadline)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot")
	}
}

func TestGateWithoutDeadlineUsesArrival(t *testing.T) {
	got := make(chan time.Time, 1)
	snap := func(ctx context.Context, _, _ string, _ bool) error {
		dl, _ := ctx.Deadline()
		got <- dl

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })
	turn, _ := turnEvent("UserPromptSubmit", gateSession, "/repo")

	before := time.Now()

	r.in <- turn

	select {
	case dl := <-got:
		if dl.Before(before.Add(turnBudget)) || dl.After(time.Now().Add(turnBudget)) {
			t.Errorf("snapshot deadline = %v, want arrival + %v", dl, turnBudget)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot")
	}
}

func TestGateSnapshotsBeforeTheAppReadsTheEvent(t *testing.T) {
	snapped := make(chan struct{})
	snap := func(context.Context, string, string, bool) error {
		close(snapped)

		return nil
	}

	in := startUnreadGate(t, snap)
	turn, released := turnEvent("UserPromptSubmit", gateSession, "/repo")

	in <- turn

	within(t, snapped, time.Second, "the snapshot with the app channel blocked")
	within(t, released, time.Second, "the hold release with the app channel blocked")
}

func TestGateSnapshotsOfOneSessionStayInArrivalOrder(t *testing.T) {
	var (
		mu       sync.Mutex
		order    []string
		inFlight atomic.Int32
		maxSeen  atomic.Int32
	)

	snap := func(_ context.Context, cwd, _ string, _ bool) error {
		n := inFlight.Add(1)

		for {
			m := maxSeen.Load()
			if n <= m || maxSeen.CompareAndSwap(m, n) {
				break
			}
		}

		time.Sleep(5 * time.Millisecond)

		mu.Lock()

		order = append(order, cwd)

		mu.Unlock()
		inFlight.Add(-1)

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })

	var releases []<-chan struct{}

	want := make([]string, 0, 12)

	for i := range 12 {
		cwd := "/repo" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		turn, released := turnEvent("UserPromptSubmit", gateSession, cwd)
		want = append(want, cwd)
		releases = append(releases, released)

		r.in <- turn
	}

	for _, released := range releases {
		within(t, released, 2*time.Second, "a snapshot")
	}

	mu.Lock()
	defer mu.Unlock()

	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("snapshot order = %v, want %v", order, want)
	}

	if m := maxSeen.Load(); m != 1 {
		t.Errorf("snapshots in flight at once = %d, want 1", m)
	}
}

func TestGateSlowSnapshotIsCancelledAtTheBudget(t *testing.T) {
	var (
		order      []string
		mu         sync.Mutex
		snapErr    error
		markedAt   time.Time
		snapCancel = make(chan struct{})
	)

	record := func(s string) {
		mu.Lock()

		order = append(order, s)
		mu.Unlock()
	}

	snap := func(ctx context.Context, _, _ string, _ bool) error {
		<-ctx.Done()
		snapErr = ctx.Err()

		close(snapCancel)
		record("snap-cancelled")

		return ctx.Err()
	}
	marked := make(chan struct{})
	mark := func(context.Context, string, string, bool) error {
		markedAt = time.Now()

		record("mark")
		close(marked)

		return nil
	}

	r := startGate(t, snap, mark)
	turn, released := turnEvent("UserPromptSubmit", gateSession, "/repo")
	start := time.Now()

	r.in <- turn

	within(t, released, turnBudget+500*time.Millisecond, "Done at the budget")

	if d := time.Since(start); d < turnBudget-50*time.Millisecond {
		t.Errorf("Done after %v, want about %v", d, turnBudget)
	}

	within(t, marked, time.Second, "marker")
	within(t, snapCancel, time.Second, "snapshot cancellation")

	if !errors.Is(snapErr, context.DeadlineExceeded) {
		t.Errorf("snapshot context error = %v, want deadline exceeded", snapErr)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(order) != 2 || order[0] != "snap-cancelled" || order[1] != "mark" || markedAt.Before(start) {
		t.Errorf("order = %v, want snapshot cancelled then marker", order)
	}
}

func TestGateSerializesTurnsOfOneSession(t *testing.T) {
	var (
		mu      sync.Mutex
		running int
		trace   []string
		maxRun  int
	)

	snap := func(_ context.Context, _, _ string, _ bool) error {
		mu.Lock()
		running++
		maxRun = max(maxRun, running)

		trace = append(trace, "snap-start")
		mu.Unlock()

		time.Sleep(100 * time.Millisecond)

		mu.Lock()
		running--

		trace = append(trace, "snap-end")
		mu.Unlock()

		return errors.New("boom")
	}
	mark := func(context.Context, string, string, bool) error {
		mu.Lock()

		trace = append(trace, "mark")
		mu.Unlock()

		return nil
	}

	r := startGate(t, snap, mark)
	a, relA := turnEvent("UserPromptSubmit", gateSession, "/repo")
	b, relB := turnEvent("UserPromptSubmit", strings.ToUpper(gateSession), "/repo")

	r.in <- a

	r.in <- b

	within(t, relA, time.Second, "first Done")
	within(t, relB, time.Second, "second Done")

	deadline := time.Now().Add(2 * time.Second)

	for {
		mu.Lock()
		n := len(trace)
		mu.Unlock()

		if n == 6 || time.Now().After(deadline) {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	want := []string{"snap-start", "snap-end", "mark", "snap-start", "snap-end", "mark"}
	if strings.Join(trace, ",") != strings.Join(want, ",") || maxRun != 1 {
		t.Errorf("trace = %v (max concurrent %d), want %v one at a time", trace, maxRun, want)
	}
}

func TestGateBudgetSpentWaitingForTheLockSkipsTheSnapshot(t *testing.T) {
	var snaps, marks atomic.Int32

	snap := func(ctx context.Context, _, _ string, _ bool) error {
		snaps.Add(1)
		<-ctx.Done()

		return ctx.Err()
	}
	mark := func(context.Context, string, string, bool) error {
		marks.Add(1)
		time.Sleep(300 * time.Millisecond)

		return nil
	}

	r := startGate(t, snap, mark)
	a, relA := turnEvent("UserPromptSubmit", gateSession, "/repo")
	b, relB := turnEvent("UserPromptSubmit", gateSession, "/repo")

	r.in <- a

	r.in <- b

	within(t, relA, turnBudget+500*time.Millisecond, "first Done")
	within(t, relB, turnBudget+500*time.Millisecond, "second Done")

	deadline := time.Now().Add(3 * time.Second)
	for marks.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if snaps.Load() != 1 || marks.Load() != 2 {
		t.Errorf("snapshots = %d, markers = %d, want 1 and 2", snaps.Load(), marks.Load())
	}
}

func TestGateSkipsEventsItCannotSnapshot(t *testing.T) {
	var calls atomic.Int32

	fn := func(context.Context, string, string, bool) error {
		calls.Add(1)

		return nil
	}
	r := startGate(t, fn, fn)

	for _, ev := range []struct{ session, cwd string }{
		{gateSession, ""},
		{"not-a-uuid", "/repo"},
		{"", "/repo"},
	} {
		turn, released := turnEvent("UserPromptSubmit", ev.session, ev.cwd)
		r.in <- turn

		within(t, released, 200*time.Millisecond, "Done for "+ev.session+"|"+ev.cwd)
	}

	if calls.Load() != 0 {
		t.Errorf("snapshot functions ran %d times, want none", calls.Load())
	}
}

func TestGateNoMarkerOutsideARepository(t *testing.T) {
	var marks atomic.Int32

	for _, sentinel := range []error{git.ErrNotRepo, git.ErrBadSession} {
		snap := func(context.Context, string, string, bool) error { return sentinel }
		mark := func(context.Context, string, string, bool) error {
			marks.Add(1)

			return nil
		}
		r := startGate(t, snap, mark)

		turn, released := turnEvent("UserPromptSubmit", gateSession, "/repo")
		r.in <- turn

		within(t, released, time.Second, "Done")
		// Closing the input ends the gate after its snapshot goroutines.
		close(r.in)
		within(t, r.done, time.Second, "gate return")
	}

	if marks.Load() != 0 {
		t.Errorf("markers = %d, want none", marks.Load())
	}
}

func TestGateCancelWaitsForSnapshotsAndWritesNoMarker(t *testing.T) {
	var (
		marks  atomic.Int32
		ended  atomic.Bool
		inSnap = make(chan struct{})
	)

	snap := func(ctx context.Context, _, _ string, _ bool) error {
		close(inSnap)
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond)
		ended.Store(true)

		return ctx.Err()
	}
	mark := func(context.Context, string, string, bool) error {
		marks.Add(1)

		return nil
	}

	in := make(chan harness.Event, 1)
	out := make(chan harness.Event, 1)
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})

	go func() {
		defer close(returned)

		runTurnGate(ctx, in, out, snap, mark, noSeq, discardLogger())
	}()

	turn, _ := turnEvent("UserPromptSubmit", gateSession, "/repo")
	in <- turn

	within(t, inSnap, time.Second, "snapshot start")
	cancel()
	within(t, returned, 2*time.Second, "gate return")

	if !ended.Load() {
		t.Error("runTurnGate returned before its snapshot goroutine ended")
	}

	if marks.Load() != 0 {
		t.Errorf("markers = %d after cancel, want none", marks.Load())
	}
}

// --- real git ---

func isolateGit(t *testing.T) {
	t.Helper()

	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	t.Setenv("HOME", shortDir(t))
	t.Setenv("XDG_STATE_HOME", shortDir(t))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

// seededRepo returns a repository with one commit and an untracked file, so a
// snapshot has something to stage.
func seededRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	gitOut(t, dir, "init", "-q", "-b", "main")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "init")

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

// slowStaging puts a git wrapper first on PATH that sleeps before staging
// (update-index) and runs the real git otherwise.
func slowStaging(t *testing.T) (started string) {
	t.Helper()

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}

	dir := t.TempDir()
	started = filepath.Join(dir, "started")
	script := "#!/bin/sh\ncase \"$*\" in *update-index*) : > '" + started + "'; sleep 10;; esac\nexec '" + gitPath + "' \"$@\"\n"

	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return started
}

func gitTurns(t *testing.T, dir string) []git.Turn {
	t.Helper()

	r, err := git.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}

	turns, err := git.Turns(t.Context(), r, gateSession)
	if err != nil {
		t.Fatal(err)
	}

	return turns
}

func sendTurn(t *testing.T, sock, cwd, session string) time.Duration {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()

	start := time.Now()
	ev := harness.Event{Name: "UserPromptSubmit", Session: core.SessionID(session), Cwd: cwd, TurnStart: true, Pane: "p"}

	if err := harness.Send(ctx, sock, ev); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	return time.Since(start)
}

// settles waits for the goroutine count to fall back to at most base.
func settles(t *testing.T, base int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if n := runtime.NumGoroutine(); n > base {
		buf := make([]byte, 1<<16)
		t.Errorf("goroutines = %d, want at most %d\n%s", n, base, buf[:runtime.Stack(buf, true)])
	}
}

func TestHookAnswersAfterTheSnapshotExists(t *testing.T) {
	repo := seededRepo(t)
	base := runtime.NumGoroutine()

	h, err := setupHooks(discardLogger(), shortDir(t), "", testWatches(t))
	if err != nil {
		t.Fatal(err)
	}

	sendTurn(t, h.Sock, repo, gateSession)

	turns := gitTurns(t, repo)
	if len(turns) != 1 || turns[0].Missing {
		t.Fatalf("turns = %+v, want one snapshot by the time the hook was answered", turns)
	}

	select {
	case ev := <-h.Events:
		if ev.Name != "UserPromptSubmit" || !ev.TurnStart {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("the app never got the event")
	}

	h.Stop()
	settles(t, base)
}

func TestHookBudgetSlowSnapshot(t *testing.T) {
	repo := seededRepo(t)
	_ = slowStaging(t)

	base := runtime.NumGoroutine()

	h, err := setupHooks(discardLogger(), shortDir(t), "", testWatches(t))
	if err != nil {
		t.Fatal(err)
	}

	took := sendTurn(t, h.Sock, repo, gateSession)
	if took > turnHold+500*time.Millisecond {
		t.Errorf("hook answered after %v, want within %v plus a margin", took, turnHold)
	}

	deadline := time.Now().Add(5 * time.Second)

	var turns []git.Turn

	for time.Now().Before(deadline) {
		if turns = gitTurns(t, repo); len(turns) > 0 {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	if len(turns) != 1 || !turns[0].Missing || turns[0].N != 1 {
		t.Fatalf("turns = %+v, want only a marker for turn 1", turns)
	}

	if out := gitOut(t, repo, "for-each-ref", "refs/muxreveal/"); strings.Count(out, "\n") != 0 {
		t.Errorf("refs = %q, want exactly the marker", out)
	}

	h.Stop()
	settles(t, base)
}

func TestHookBudgetServerClosingMidSnapshot(t *testing.T) {
	repo := seededRepo(t)
	started := slowStaging(t)

	base := runtime.NumGoroutine()

	h, err := setupHooks(discardLogger(), shortDir(t), "", testWatches(t))
	if err != nil {
		t.Fatal(err)
	}

	sent := make(chan error, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		sent <- harness.Send(ctx, h.Sock, harness.Event{
			Name: "UserPromptSubmit", Session: gateSession, Cwd: repo, TurnStart: true, Pane: "p",
		})
	}()

	select {
	case <-h.Events:
	case <-time.After(2 * time.Second):
		t.Fatal("event never arrived")
	}

	for deadline := time.Now().Add(3 * time.Second); !exists(started) && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}

	if !exists(started) {
		t.Fatal("the snapshot never reached staging")
	}

	start := time.Now()

	h.Stop()

	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Stop took %v mid-snapshot", d)
	}

	if err := <-sent; err == nil {
		t.Error("Send() succeeded though the server closed mid-snapshot")
	}

	if turns := gitTurns(t, repo); len(turns) != 0 {
		t.Errorf("turns = %+v after Stop, want none (no ref, no marker)", turns)
	}

	settles(t, base)
}

func TestHookBudgetHeldIndexLockDoesNotBlockTheSnapshot(t *testing.T) {
	repo := seededRepo(t)
	base := runtime.NumGoroutine()

	lock := filepath.Join(repo, ".git", "index.lock")
	if err := os.WriteFile(lock, []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}

	h, err := setupHooks(discardLogger(), shortDir(t), "", testWatches(t))
	if err != nil {
		t.Fatal(err)
	}

	if took := sendTurn(t, h.Sock, repo, gateSession); took > turnHold+500*time.Millisecond {
		t.Errorf("hook answered after %v, want within %v plus a margin", took, turnHold)
	}

	if turns := gitTurns(t, repo); len(turns) != 1 || turns[0].Missing {
		t.Errorf("turns = %+v, want one snapshot despite the lock", turns)
	}

	if data, err := os.ReadFile(lock); err != nil || string(data) != "held" {
		t.Errorf("index.lock = %q, %v, want it untouched", data, err)
	}

	h.Stop()
	settles(t, base)
}

func TestHookBudgetUnsnapshottableTurnsAnswerAtOnce(t *testing.T) {
	repo := seededRepo(t)
	notRepo := t.TempDir()
	base := runtime.NumGoroutine()

	h, err := setupHooks(discardLogger(), shortDir(t), "", testWatches(t))
	if err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct{ cwd, session string }{
		"no cwd":          {"", gateSession},
		"unknown session": {repo, "not-a-session"},
		"not a repo":      {notRepo, gateSession},
	} {
		if took := sendTurn(t, h.Sock, c.cwd, c.session); took > 500*time.Millisecond {
			t.Errorf("%s: hook answered after %v, want at once", name, took)
		}
	}

	if turns := gitTurns(t, repo); len(turns) != 0 {
		t.Errorf("turns = %+v, want none", turns)
	}

	h.Stop()
	settles(t, base)
}

// seqRig is a gate whose snapshots record their unchanged flag and whose
// watcher counter the test controls.
type seqRig struct {
	*gateRig

	n         atomic.Uint64
	watched   atomic.Bool
	flags     chan bool
	beforeSnp func()
}

func newSeqRig(t *testing.T) *seqRig {
	t.Helper()

	s := &seqRig{flags: make(chan bool, 16)}
	s.watched.Store(true)

	snap := func(_ context.Context, _, _ string, unchanged bool) error {
		if s.beforeSnp != nil {
			s.beforeSnp()
		}

		s.flags <- unchanged

		return nil
	}
	seq := func(context.Context, string) (string, uint64, bool) {
		return "/root", s.n.Load(), s.watched.Load()
	}
	s.gateRig = startSeqGate(t, snap, func(context.Context, string, string, bool) error { return nil }, seq)

	return s
}

// turn sends a turn start and returns the unchanged flag its snapshot saw.
func (s *seqRig) turn(t *testing.T) bool {
	t.Helper()

	ev, released := turnEvent("UserPromptSubmit", gateSession, "/repo")
	s.in <- ev

	within(t, released, time.Second, "turn release")

	select {
	case f := <-s.flags:
		return f
	case <-time.After(time.Second):
		t.Fatal("no snapshot")
	}

	return false
}

func TestGateSkipsAnUnchangedTurn(t *testing.T) {
	s := newSeqRig(t)

	if s.turn(t) {
		t.Fatal("first turn skipped")
	}

	if !s.turn(t) {
		t.Fatal("second turn with an unchanged counter was not skipped")
	}

	if !s.turn(t) {
		t.Fatal("third turn was not skipped")
	}
}

func TestGateDoesNotSkipAfterAChange(t *testing.T) {
	s := newSeqRig(t)

	s.turn(t)
	s.n.Add(1)

	if s.turn(t) {
		t.Fatal("turn after a change was skipped")
	}

	if !s.turn(t) {
		t.Fatal("turn after the changed one was not skipped")
	}
}

// The counter rises before the debounce, so a change whose batch has not been
// sent yet already defeats the skip.
func TestGateDoesNotSkipAChangeInsideTheDebounce(t *testing.T) {
	s := newSeqRig(t)

	s.turn(t)
	s.n.Add(1) // seq rose; no batch was sent

	if s.turn(t) {
		t.Fatal("a change inside the debounce was skipped")
	}
}

func TestGateDoesNotSkipWithoutAWatcher(t *testing.T) {
	s := newSeqRig(t)

	s.turn(t)
	s.watched.Store(false)

	if s.turn(t) {
		t.Fatal("turn without a watcher was skipped")
	}

	s.watched.Store(true)

	if s.turn(t) {
		t.Fatal("turn right after an unwatched one was skipped")
	}
}

func TestGateDoesNotSkipAfterAChangeDuringTheSnapshot(t *testing.T) {
	s := newSeqRig(t)

	s.beforeSnp = func() { s.n.Add(1) } // after the counter was read, before the snapshot ends
	s.turn(t)

	s.beforeSnp = nil

	if s.turn(t) {
		t.Fatal("turn after a change during the previous snapshot was skipped")
	}
}

func TestGateDoesNotSkipAfterAFailedSnapshot(t *testing.T) {
	var fail atomic.Bool

	flags := make(chan bool, 4)
	snap := func(_ context.Context, _, _ string, unchanged bool) error {
		flags <- unchanged

		if fail.Load() {
			return errors.New("boom")
		}

		return nil
	}
	seq := func(context.Context, string) (string, uint64, bool) { return "/root", 1, true }
	r := startSeqGate(t, snap, func(context.Context, string, string, bool) error { return nil }, seq)

	send := func() bool {
		ev, released := turnEvent("UserPromptSubmit", gateSession, "/repo")
		r.in <- ev

		within(t, released, time.Second, "turn release")

		return <-flags
	}

	send()
	fail.Store(true)
	send()
	fail.Store(false)

	if send() {
		t.Fatal("turn after a failed snapshot was skipped")
	}
}

func TestGateKeepsSeqPerSession(t *testing.T) {
	s := newSeqRig(t)
	other := "99999999-2222-4333-8444-555555555555"

	s.turn(t)

	ev, released := turnEvent("UserPromptSubmit", other, "/repo")
	s.in <- ev

	within(t, released, time.Second, "turn release")

	if <-s.flags {
		t.Fatal("another session's first turn was skipped")
	}
}

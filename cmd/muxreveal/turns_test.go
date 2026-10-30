package main

import (
	"cmp"
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

// snapshotFunc is a fake snapshot or marker; funcWriter adapts a pair of them to
// the gate's turnWriter. A marker ignores unchanged.
type snapshotFunc func(ctx context.Context, dir, session string, unchanged bool) error

type funcWriter struct{ snap, mark snapshotFunc }

func (w funcWriter) Start(ctx context.Context, dir, session string, _, _ int, unchanged bool) error {
	return w.snap(ctx, dir, session, unchanged)
}

func (w funcWriter) Mark(ctx context.Context, dir, session string, _ int, _ bool) error {
	return w.mark(ctx, dir, session, false)
}

func (funcWriter) End(context.Context, string, string, int, bool) error { return nil }

func (funcWriter) OpenTurns(context.Context, string, string) ([]git.OpenTurn, error) {
	return nil, nil
}

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

	return startWriterGate(t, funcWriter{snap, mark}, seq)
}

func startWriterGate(t *testing.T, w turnWriter, seq seqFunc) *gateRig {
	t.Helper()

	r := &gateRig{in: make(chan harness.Event, 16), out: make(chan harness.Event, 16), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer close(r.done)

		runTurnGate(ctx, r.in, r.out, w, seq, discardLogger())
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

		runTurnGate(ctx, in, make(chan harness.Event), funcWriter{snap, func(context.Context, string, string, bool) error { return nil }}, noSeq, discardLogger())
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

		runTurnGate(ctx, in, out, funcWriter{snap, mark}, noSeq, discardLogger())
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

	if len(turns) != 1 || !turns[0].Missing || turns[0].N == 0 {
		t.Fatalf("turns = %+v, want only a marker for the turn", turns)
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

func TestNextTurnID(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_500)

	tests := []struct {
		name string
		prev int
		want int
	}{
		{"a later time", 1_700_000_000_000, 1_700_000_000_500},
		{"the same millisecond", 1_700_000_000_500, 1_700_000_000_501},
		{"a clock set back", 1_700_000_009_000, 1_700_000_009_001},
		{"the pane's first turn", 0, 1_700_000_000_500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := nextTurnID(tt.prev, at); got != tt.want {
				t.Errorf("nextTurnID(%d) = %d, want %d", tt.prev, got, tt.want)
			}
		})
	}
}

// recordedStart is one call of a recording writer, or with a pane, dir and
// session one turn start to send.
type recordedStart struct {
	pane       core.PaneID
	dir        string
	session    string
	turn, ends int
	mark, end  bool
	unchanged  bool
}

// recordingWriter records what the gate writes, in the order the calls finish
// (a start is recorded when it begins). failing starts return an error.
type recordingWriter struct {
	mu    sync.Mutex
	calls []recordedStart
	fail  func(dir string) bool
	// failEnd makes the End of the root fail with the error.
	failEnd func(root string) error
	// open is what OpenTurns reports.
	open []git.OpenTurn
	// markHold, when set, makes Mark announce itself on markStarted and wait.
	markHold    chan struct{}
	markStarted chan recordedStart
}

func (w *recordingWriter) record(c recordedStart) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.calls = append(w.calls, c)
}

func (w *recordingWriter) Start(_ context.Context, dir, session string, turn, ends int, unchanged bool) error {
	w.record(recordedStart{dir: dir, session: session, turn: turn, ends: ends, unchanged: unchanged})

	if w.fail != nil && w.fail(dir) {
		return errors.New("boom")
	}

	return nil
}

func (w *recordingWriter) End(_ context.Context, root, session string, turn int, unchanged bool) error {
	w.record(recordedStart{dir: root, session: session, turn: turn, end: true, unchanged: unchanged})

	if w.failEnd != nil {
		return w.failEnd(root)
	}

	return nil
}

func (w *recordingWriter) Mark(ctx context.Context, dir, session string, turn int, end bool) error {
	c := recordedStart{dir: dir, session: session, turn: turn, mark: true, end: end}

	if w.markHold != nil {
		w.markStarted <- c

		select {
		case <-w.markHold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	w.record(c)

	return nil
}

func (w *recordingWriter) OpenTurns(context.Context, string, string) ([]git.OpenTurn, error) {
	return w.open, nil
}

func (w *recordingWriter) filter(keep func(recordedStart) bool) []recordedStart {
	w.mu.Lock()
	defer w.mu.Unlock()

	var out []recordedStart

	for _, c := range w.calls {
		if keep(c) {
			out = append(out, c)
		}
	}

	return out
}

func (w *recordingWriter) starts() []recordedStart {
	return w.filter(func(c recordedStart) bool { return !c.mark && !c.end })
}

func (w *recordingWriter) endings() []recordedStart {
	return w.filter(func(c recordedStart) bool { return c.end && !c.mark })
}

func (w *recordingWriter) all() []recordedStart {
	return w.filter(func(recordedStart) bool { return true })
}

// dirSeq resolves a directory to the worktree root named like it.
func dirSeq(_ context.Context, dir string) (string, uint64, bool) { return "root" + dir, 1, true }

// sendTurns sends one turn start per (pane, cwd) pair and waits for each to be
// answered, so the starts run in order.
func sendTurns(t *testing.T, w turnWriter, seq seqFunc, sends ...recordedStart) {
	t.Helper()

	in, out := make(chan harness.Event, 16), make(chan harness.Event, 16)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		runTurnGate(ctx, in, out, w, seq, discardLogger())
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	for _, s := range sends {
		session := cmp.Or(s.session, gateSession)
		ev, released := turnEvent("UserPromptSubmit", session, s.dir)

		ev.Pane = s.pane
		in <- ev

		within(t, released, time.Second, "turn release")
	}
}

func TestGateEndsThePreviousTurnInTheSameWorktree(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}

	sendTurns(t, w, dirSeq, recordedStart{dir: "/a"}, recordedStart{dir: "/a"}, recordedStart{dir: "/a"})

	got := w.starts()
	if len(got) != 3 {
		t.Fatalf("starts = %+v, want 3", got)
	}

	if got[0].ends != 0 || got[1].ends != got[0].turn || got[2].ends != got[1].turn {
		t.Errorf("ends = %d, %d, %d, want 0 then each previous number %d, %d", got[0].ends, got[1].ends, got[2].ends, got[0].turn, got[1].turn)
	}

	if got[0].turn <= 0 || got[1].turn <= got[0].turn || got[2].turn <= got[1].turn {
		t.Errorf("numbers %d, %d, %d do not increase", got[0].turn, got[1].turn, got[2].turn)
	}
}

func TestGateEndsNothingAcrossWorktrees(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sends []recordedStart
		fail  func(dir string) bool
	}{
		{"a start in another worktree", []recordedStart{{dir: "/a"}, {dir: "/b"}}, nil},
		{"after a failed start", []recordedStart{{dir: "/a"}, {dir: "/a"}}, func(string) bool { return true }},
		{"another pane", []recordedStart{{dir: "/a", pane: "p1"}, {dir: "/a", pane: "p2"}}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &recordingWriter{fail: tt.fail}

			sendTurns(t, w, dirSeq, tt.sends...)

			got := w.starts()
			if len(got) != 2 {
				t.Fatalf("starts = %+v, want 2", got)
			}

			if got[0].ends != 0 || got[1].ends != 0 {
				t.Errorf("ends = %d, %d, want 0 and 0", got[0].ends, got[1].ends)
			}
		})
	}
}

// An edit's before-event is answered when the gate reads it, with no snapshot.
func TestGateReleasesAnEditAtOnce(t *testing.T) {
	t.Parallel()

	snapped := make(chan struct{}, 1)
	snap := func(context.Context, string, string, bool) error {
		snapped <- struct{}{}

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })
	released := make(chan struct{})

	r.in <- harness.Event{
		Name: "PreToolUse", Pane: "p", Session: gateSession, Cwd: "/repo", Edit: "/repo/a.go", Before: true,
		Done: sync.OnceFunc(func() { close(released) }),
	}

	within(t, released, time.Second, "Done of the edit")

	if ev := forwarded(t, r); ev.Snapped != nil {
		t.Error("edit has Snapped, want no snapshot")
	}

	select {
	case <-snapped:
		t.Error("the gate snapshotted for an edit")
	default:
	}
}

func TestGateEndsThePreviousTurnAtTheNextPrompt(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}

	sendTurns(t, w, dirSeq, recordedStart{dir: "/a"}, recordedStart{dir: "/a"})

	got := w.starts()
	if len(got) != 2 || got[1].ends != got[0].turn {
		t.Fatalf("starts = %+v, want the second to end the first", got)
	}

	if e := w.endings(); len(e) != 0 {
		t.Errorf("End calls = %+v, want none for the own root", e)
	}
}

func TestGateEndsTheOldRootWhenTheOwnDirectoryMoved(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}

	sendTurns(t, w, dirSeq, recordedStart{dir: "/a"}, recordedStart{dir: "/b"})

	calls := w.all()
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want start, end, start", calls)
	}

	first, end, second := calls[0], calls[1], calls[2]

	if !end.end || end.dir != "root/a" || end.turn != first.turn {
		t.Errorf("second call = %+v, want End of root/a at turn %d", end, first.turn)
	}

	if second.end || second.dir != "/b" || second.ends != 0 {
		t.Errorf("third call = %+v, want the start in /b with ends 0, after the End", second)
	}
}

func TestGateStopEndsNothing(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}
	r := startWriterGate(t, w, dirSeq)

	first, released := turnEvent("UserPromptSubmit", gateSession, "/a")
	r.in <- first

	within(t, released, time.Second, "first turn release")
	forwarded(t, r)

	for _, name := range []string{"Stop", "StopFailure", "SessionEnd"} {
		r.in <- harness.Event{Name: name, Session: gateSession, Cwd: "/a"}

		if ev := forwarded(t, r); ev.Name != name || ev.Snapped != nil {
			t.Errorf("%s forwarded as %+v, want no Snapped", name, ev)
		}
	}

	if got := w.all(); len(got) != 1 {
		t.Fatalf("calls after Stop, StopFailure and SessionEnd = %+v, want only the first start", got)
	}

	second, released := turnEvent("UserPromptSubmit", gateSession, "/a")
	r.in <- second

	within(t, released, time.Second, "second turn release")

	got := w.starts()
	if len(got) != 2 || got[1].ends != got[0].turn {
		t.Errorf("starts = %+v, want the next prompt to end the first turn", got)
	}
}

func TestGateNewSessionEndsTheOldTurn(t *testing.T) {
	t.Parallel()

	const other = "99999999-2222-4333-8444-555555555555"

	w := &recordingWriter{}

	sendTurns(t, w, dirSeq, recordedStart{dir: "/a"}, recordedStart{dir: "/a", session: other})

	calls := w.all()
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want start, end, start", calls)
	}

	if end := calls[1]; !end.end || end.session != gateSession || end.dir != "root/a" || end.turn != calls[0].turn {
		t.Errorf("second call = %+v, want End of the old session's turn %d", end, calls[0].turn)
	}

	if second := calls[2]; second.end || second.session != other || second.ends != 0 {
		t.Errorf("third call = %+v, want the new session's start with ends 0, after the End", second)
	}
}

func TestGateEndsTheLastTurnAfterARestart(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{open: []git.OpenTurn{{Root: "root/a", N: 7}, {Root: "root/b", N: 7}}}

	sendTurns(t, w, dirSeq, recordedStart{dir: "/a"})

	starts, ends := w.starts(), w.endings()
	if len(starts) != 1 || starts[0].ends != 7 {
		t.Errorf("starts = %+v, want one that ends the own root's turn 7", starts)
	}

	if len(ends) != 1 || ends[0].dir != "root/b" || ends[0].turn != 7 {
		t.Errorf("End calls = %+v, want only root/b at turn 7", ends)
	}
}

func TestGateFailedEndWritesAnEndMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantMark bool
	}{
		{"a failed end", errors.New("boom"), true},
		{"a turn that is not open", git.ErrTurnNotOpen, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &recordingWriter{
				failEnd:     func(string) error { return tt.err },
				markHold:    make(chan struct{}),
				markStarted: make(chan recordedStart, 4),
			}

			sendTurns(t, w, dirSeq, recordedStart{dir: "/a"}, recordedStart{dir: "/b"})

			// The second start was answered while the marker is still unwritten.
			select {
			case m := <-w.markStarted:
				if !tt.wantMark {
					t.Fatalf("marker %+v written, want none", m)
				}

				if !m.end || m.dir != "root/a" || m.session != gateSession || m.turn != w.starts()[0].turn {
					t.Errorf("marker = %+v, want the end of the first turn in root/a", m)
				}

				close(w.markHold)
			case <-time.After(300 * time.Millisecond):
				if tt.wantMark {
					t.Fatal("no end marker")
				}
			}
		})
	}
}

func TestGateUnchangedEndReusesTheStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		seq  func() seqFunc
		want bool
	}{
		{
			name: "a counter that did not move",
			seq:  func() seqFunc { return dirSeq },
			want: true,
		},
		{
			name: "a counter that moved",
			seq: func() seqFunc {
				var n atomic.Uint64

				return func(_ context.Context, dir string) (string, uint64, bool) {
					return "root" + dir, n.Add(1), true
				}
			},
		},
		{
			name: "a root that is not watched",
			seq: func() seqFunc {
				return func(_ context.Context, dir string) (string, uint64, bool) { return "root" + dir, 1, false }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &recordingWriter{}

			sendTurns(t, w, tt.seq(), recordedStart{dir: "/a"}, recordedStart{dir: "/b"})

			ends := w.endings()
			if len(ends) != 1 || ends[0].unchanged != tt.want {
				t.Errorf("End calls = %+v, want one with unchanged %v", ends, tt.want)
			}
		})
	}
}

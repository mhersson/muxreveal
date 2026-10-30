package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
)

// shortDir returns a fresh directory under /tmp: macOS's temp dir is too deep
// for a 104-byte socket path.
func shortDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "mr")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return dir
}

// socketPath returns a short socket path.
func socketPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(shortDir(t), "s.sock")
}

type served struct {
	srv  *Server
	out  chan Event
	path string
	done chan error
}

func startServer(t *testing.T, buf int, log *slog.Logger) *served {
	t.Helper()

	path := socketPath(t)

	srv, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	srv.Log = log
	s := &served{srv: srv, out: make(chan Event, buf), path: path, done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())

	go func() { s.done <- srv.Serve(ctx, s.out) }()

	t.Cleanup(func() {
		cancel()

		_ = srv.Close()

		if err := <-s.done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	})

	return s
}

func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()

	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event received")

		return Event{}
	}
}

func TestSendRoundTrip(t *testing.T) {
	s := startServer(t, 1, nil)
	want := Event{Pane: "p1", Name: "SessionStart", Session: "abc", Transcript: "/t.jsonl", State: core.AgentIdle, Start: true}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Send(ctx, s.path, want); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if got := recv(t, s.out); !reflect.DeepEqual(got, want) {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
}

func TestServerDeliversEditAndSubagent(t *testing.T) {
	t.Parallel()

	s := startServer(t, 1, nil)
	want := Event{Pane: "p1", Name: "PreToolUse", Cwd: "/r", Edit: "/r/a.go", Subagent: true}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Send(ctx, s.path, want); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	got := recv(t, s.out)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
}

func TestServerDeliversChanged(t *testing.T) {
	t.Parallel()

	s := startServer(t, 1, nil)
	want := Event{Pane: "p1", Name: "PostToolUse", Cwd: "/r"}

	for i := range maxChanged {
		dir := fmt.Sprintf("/r/d%02d/", i)
		want.Changed = append(want.Changed, dir+strings.Repeat("a", maxChangedPath-len(dir)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Send(ctx, s.path, want); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	got := recv(t, s.out)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
}

func TestServerDeliversHostileChanged(t *testing.T) {
	t.Parallel()

	// Sixteen paths of control characters: JSON writes each as 6 bytes, which
	// would take the line past maxLine, and the whole event would be dropped.
	var files []string

	for i := range maxChanged {
		dir := fmt.Sprintf("/h%02d/", i)
		files = append(files, dir+strings.Repeat("\x01", maxChangedPath-len(dir)))
	}

	list, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}

	ev, err := Claude{}.ParseEvent(payload("PostToolUse",
		`"session_id":"s","cwd":"/r","tool_name":"Bash","tool_input":{"command":"x"},`+
			`"tool_response":{"bashEditDiff":{"changedFiles":`+string(list)+`}}`))
	if err != nil {
		t.Fatal(err)
	}

	if len(ev.Changed) == 0 {
		t.Fatal("ParseEvent kept no path")
	}

	ev.Pane = "p1"

	s := startServer(t, 1, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Send(ctx, s.path, ev); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	got := recv(t, s.out)
	if !reflect.DeepEqual(got, ev) || got.State != core.AgentWorking || got.Action == "" || got.Cwd != "/r" {
		t.Fatalf("event = %+v, want %+v with its state, action and cwd", got, ev)
	}
}

func TestSocketMode(t *testing.T) {
	s := startServer(t, 1, nil)

	fi, err := os.Stat(s.path)
	if err != nil {
		t.Fatal(err)
	}

	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %o, want 600", perm)
	}
}

func TestSendParallel(t *testing.T) {
	s := startServer(t, 8, nil)

	var wg sync.WaitGroup

	errs := make(chan error, 2)

	for _, name := range []string{"a", "b"} {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			errs <- Send(ctx, s.path, Event{Pane: "p", Name: name})
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}

	got := map[string]bool{recv(t, s.out).Name: true, recv(t, s.out).Name: true}
	if !got["a"] || !got["b"] {
		t.Fatalf("events = %v, want a and b", got)
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	path := socketPath(t)

	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}

	// Leave the file behind as a crashed process would.
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket file missing: %v", err)
	}

	srv, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOversizedLineDropped(t *testing.T) {
	var logs bytes.Buffer

	s := startServer(t, 1, slog.New(slog.NewTextHandler(&logs, nil)))

	conn, err := net.Dial("unix", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	big := append(bytes.Repeat([]byte("x"), maxLine+10), '\n')
	_, _ = conn.Write(big)

	// A dropped event gets no answer: the server closes the connection.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	if reply, _ := io.ReadAll(conn); len(reply) != 0 {
		t.Fatalf("reply = %q, want none", reply)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Send(ctx, s.path, Event{Pane: "p", Name: "after"}); err != nil {
		t.Fatalf("Send() after oversized line error = %v", err)
	}

	if got := recv(t, s.out); got.Name != "after" {
		t.Fatalf("event = %+v, want the one sent after", got)
	}

	if !strings.Contains(logs.String(), "dropped") {
		t.Fatalf("log = %q, want a drop entry", logs.String())
	}
}

func TestBadJSONDropped(t *testing.T) {
	s := startServer(t, 1, nil)

	conn, err := net.Dial("unix", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	_, _ = conn.Write([]byte("not json\n"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	if reply, _ := io.ReadAll(conn); len(reply) != 0 {
		t.Fatalf("reply = %q, want none", reply)
	}
}

func TestFullChannelDropsAfterOneSecond(t *testing.T) {
	s := startServer(t, 0, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()

	if err := Send(ctx, s.path, Event{Pane: "p", Name: "x"}); err == nil {
		t.Fatal("Send() error = nil, want one for a dropped event")
	}

	if d := time.Since(start); d < 900*time.Millisecond || d > 2*time.Second {
		t.Fatalf("drop took %v, want about 1s", d)
	}
}

func TestCloseEndsServeAndRemovesFile(t *testing.T) {
	path := socketPath(t)

	srv, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() { done <- srv.Serve(context.Background(), make(chan Event)) }()

	if err := srv.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after Close")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket file after Close: err = %v, want not exist", err)
	}
}

func TestServeEndsWithContext(t *testing.T) {
	srv, err := Listen(socketPath(t))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Serve(ctx, make(chan Event)) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}

	_ = srv.Close()
}

func TestSendMissingPathFailsFast(t *testing.T) {
	start := time.Now()

	err := Send(context.Background(), filepath.Join(shortDir(t), "none.sock"), Event{})
	if err == nil {
		t.Fatal("Send() error = nil, want one")
	}

	if d := time.Since(start); d > time.Second {
		t.Fatalf("Send took %v, want under 1s", d)
	}
}

func TestSendSilentServerHonoursDeadline(t *testing.T) {
	path := socketPath(t)

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}

	var accepted []net.Conn

	var mu sync.Mutex

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			mu.Lock()

			accepted = append(accepted, c)
			mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()

		mu.Lock()
		defer mu.Unlock()

		for _, c := range accepted {
			_ = c.Close()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()

	if err := Send(ctx, path, Event{Pane: "p"}); err == nil {
		t.Fatal("Send() error = nil, want one")
	}

	if d := time.Since(start); d > time.Second {
		t.Fatalf("Send took %v, want about 300ms", d)
	}
}

func TestListenKeepsNonSocketFile(t *testing.T) {
	path := socketPath(t)

	if err := os.WriteFile(path, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Listen(path); err == nil {
		t.Fatal("Listen() error = nil, want one for a regular file")
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "precious" {
		t.Fatalf("file = %q, %v; want it left untouched", got, err)
	}
}

func startHeldServer(t *testing.T, hold time.Duration) *served {
	t.Helper()

	return startBudgetServer(t, hold, 0)
}

func startBudgetServer(t *testing.T, hold, budget time.Duration) *served {
	t.Helper()

	path := socketPath(t)

	srv, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	srv.Hold = hold
	srv.Budget = budget
	s := &served{srv: srv, out: make(chan Event, 4), path: path, done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())

	go func() { s.done <- srv.Serve(ctx, s.out) }()

	t.Cleanup(func() {
		cancel()

		_ = srv.Close()

		if err := <-s.done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	})

	return s
}

// sendAsync sends ev and reports the Send error and how long it took.
func sendAsync(path string, ev Event) <-chan sendResult {
	res := make(chan sendResult, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		start := time.Now()

		err := Send(ctx, path, ev)
		res <- sendResult{err: err, took: time.Since(start)}
	}()

	return res
}

type sendResult struct {
	err  error
	took time.Duration
}

func TestTurnStartAnsweredAfterDone(t *testing.T) {
	s := startHeldServer(t, 300*time.Millisecond)
	res := sendAsync(s.path, Event{Pane: "p", Name: "UserPromptSubmit", TurnStart: true})

	ev := recv(t, s.out)
	if ev.Done == nil {
		t.Fatal("held event has no Done")
	}

	select {
	case r := <-res:
		t.Fatalf("answered before Done: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}

	ev.Done()
	ev.Done() // more than once is fine

	r := <-res
	if r.err != nil || r.took > 250*time.Millisecond {
		t.Errorf("Send() = %v after %v, want ok well under the hold", r.err, r.took)
	}
}

func TestTurnStartAnsweredAfterHold(t *testing.T) {
	s := startHeldServer(t, 300*time.Millisecond)
	res := sendAsync(s.path, Event{Pane: "p", Name: "UserPromptSubmit", TurnStart: true})
	_ = recv(t, s.out)

	r := <-res
	if r.err != nil || r.took < 250*time.Millisecond || r.took > time.Second {
		t.Errorf("Send() = %v after %v, want ok at about 300ms", r.err, r.took)
	}
}

func TestNonTurnEventAnsweredAtOnce(t *testing.T) {
	s := startHeldServer(t, 300*time.Millisecond)
	res := sendAsync(s.path, Event{Pane: "p", Name: "Stop"})

	if ev := recv(t, s.out); ev.Done != nil {
		t.Error("non-turn event has Done")
	}

	if r := <-res; r.err != nil || r.took > 200*time.Millisecond {
		t.Errorf("Send() = %v after %v, want ok at once", r.err, r.took)
	}
}

func TestZeroHoldAnswersAtOnce(t *testing.T) {
	s := startHeldServer(t, 0)
	res := sendAsync(s.path, Event{Pane: "p", Name: "UserPromptSubmit", TurnStart: true})

	if ev := recv(t, s.out); ev.Done != nil {
		t.Error("event has Done without a hold")
	}

	if r := <-res; r.err != nil || r.took > 200*time.Millisecond {
		t.Errorf("Send() = %v after %v, want ok at once", r.err, r.took)
	}
}

func TestCloseDuringHoldDoesNotWait(t *testing.T) {
	s := startHeldServer(t, 5*time.Second)
	res := sendAsync(s.path, Event{Pane: "p", Name: "UserPromptSubmit", TurnStart: true})
	_ = recv(t, s.out)

	start := time.Now()

	if err := s.srv.Close(); err != nil {
		t.Fatal(err)
	}

	if d := time.Since(start); d > time.Second {
		t.Errorf("Close() took %v during a hold", d)
	}

	if r := <-res; r.err == nil {
		t.Error("Send() succeeded though the server closed mid-hold")
	}
}

func TestServerStampsTheTurnDeadline(t *testing.T) {
	const budget = 1400 * time.Millisecond

	s := startBudgetServer(t, 300*time.Millisecond, budget)

	before := time.Now()
	res := sendAsync(s.path, Event{Pane: "p", Name: "UserPromptSubmit", TurnStart: true})
	turn := recv(t, s.out)
	after := time.Now()

	turn.Done()
	<-res

	if turn.Deadline.Before(before.Add(budget)) || turn.Deadline.After(after.Add(budget+50*time.Millisecond)) {
		t.Errorf("turn Deadline = %v, want read time + %v (between %v and %v)", turn.Deadline, budget, before.Add(budget), after.Add(budget))
	}

	res = sendAsync(s.path, Event{Pane: "p", Name: "Stop"})
	if stop := recv(t, s.out); !stop.Deadline.IsZero() {
		t.Errorf("non-turn Deadline = %v, want zero", stop.Deadline)
	}

	<-res
}

func TestServerHoldsAnEditBeforeItsTool(t *testing.T) {
	const budget = 1400 * time.Millisecond

	s := startBudgetServer(t, 300*time.Millisecond, budget)

	res := sendAsync(s.path, Event{Pane: "p", Name: "PreToolUse", Edit: "/r/a.go", Before: true})
	ev := recv(t, s.out)

	if ev.Done == nil || ev.Deadline.IsZero() {
		t.Fatalf("held edit has Done %v and Deadline %v", ev.Done != nil, ev.Deadline)
	}

	select {
	case r := <-res:
		t.Fatalf("answered before Done: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}

	ev.Done()

	if r := <-res; r.err != nil || r.took > 250*time.Millisecond {
		t.Errorf("Send() = %v after %v, want ok well under the hold", r.err, r.took)
	}

	res = sendAsync(s.path, Event{Pane: "p", Name: "PostToolUse", Edit: "/r/a.go"})
	if post := recv(t, s.out); post.Done != nil {
		t.Error("PostToolUse edit has Done")
	}

	if r := <-res; r.err != nil || r.took > 200*time.Millisecond {
		t.Errorf("Send() = %v after %v, want ok at once", r.err, r.took)
	}
}

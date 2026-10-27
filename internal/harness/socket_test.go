package harness

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
)

// socketPath returns a short socket path under t.TempDir().
func socketPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "s.sock")
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

	if got := recv(t, s.out); got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
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

	err := Send(context.Background(), filepath.Join(t.TempDir(), "none.sock"), Event{})
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

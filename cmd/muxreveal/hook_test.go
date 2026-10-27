package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/harness"
)

const sessionStart = `{"hook_event_name":"SessionStart","session_id":"s1","transcript_path":"/t.jsonl","source":"startup"}`

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func shortSock(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "s.sock")
}

// captureStdio fails the test if f writes to the process stdout or stderr.
func captureStdio(t *testing.T, f func()) {
	t.Helper()

	stdout, stderr := os.Stdout, os.Stderr

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout, os.Stderr = w, w

	f()

	os.Stdout, os.Stderr = stdout, stderr
	_ = w.Close()

	got, _ := io.ReadAll(r)
	_ = r.Close()

	if len(got) != 0 {
		t.Fatalf("runHook wrote %q to stdout/stderr", got)
	}
}

func TestRunHookDelivers(t *testing.T) {
	path := shortSock(t)

	srv, err := harness.Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan harness.Event, 1)
	done := make(chan error, 1)

	go func() { done <- srv.Serve(context.Background(), out) }()

	t.Cleanup(func() {
		_ = srv.Close()

		if err := <-done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	})

	runHook(context.Background(), "claude-code", strings.NewReader(sessionStart),
		env(map[string]string{"MUXREVEAL_SOCK": path, "MUXREVEAL_PANE": "p7"}))

	select {
	case ev := <-out:
		if ev.Pane != core.PaneID("p7") || ev.Session != "s1" || !ev.Start {
			t.Fatalf("event = %+v, want pane p7 and a started session s1", ev)
		}
	default:
		t.Fatal("no event delivered")
	}
}

func TestRunHookIsSilentAndQuick(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.sock")

	// A listener that accepts and never answers.
	silent := shortSock(t)

	ln, err := net.Listen("unix", silent)
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu       sync.Mutex
		conns    []net.Conn
		accepted = make(chan struct{})
	)

	go func() {
		defer close(accepted)

		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			mu.Lock()

			conns = append(conns, c)
			mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()

		<-accepted

		mu.Lock()
		defer mu.Unlock()

		for _, c := range conns {
			_ = c.Close()
		}
	})

	big := `{"hook_event_name":"PostToolUse","pad":"` + strings.Repeat("x", 10<<20) + `"}`

	tests := []struct {
		name  string
		id    string
		stdin string
		env   map[string]string
	}{
		{"no socket", "claude-code", sessionStart, map[string]string{"MUXREVEAL_PANE": "p"}},
		{"no pane", "claude-code", sessionStart, map[string]string{"MUXREVEAL_SOCK": missing}},
		{"unknown harness", "nope", sessionStart, map[string]string{"MUXREVEAL_SOCK": missing, "MUXREVEAL_PANE": "p"}},
		{"invalid json", "claude-code", "{not json", map[string]string{"MUXREVEAL_SOCK": missing, "MUXREVEAL_PANE": "p"}},
		{"truncated", "claude-code", sessionStart[:30], map[string]string{"MUXREVEAL_SOCK": missing, "MUXREVEAL_PANE": "p"}},
		{"10 MB payload", "claude-code", big, map[string]string{"MUXREVEAL_SOCK": silent, "MUXREVEAL_PANE": "p"}},
		{"missing socket", "claude-code", sessionStart, map[string]string{"MUXREVEAL_SOCK": missing, "MUXREVEAL_PANE": "p"}},
		{"never answers", "claude-code", sessionStart, map[string]string{"MUXREVEAL_SOCK": silent, "MUXREVEAL_PANE": "p"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()

			captureStdio(t, func() {
				ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
				defer cancel()

				runHook(ctx, tt.id, strings.NewReader(tt.stdin), env(tt.env))
			})

			if d := time.Since(start); d > 2500*time.Millisecond {
				t.Fatalf("runHook took %v, want within 2.5s", d)
			}
		})
	}
}

func TestRunHookStdinNeverClosesStaysInBudget(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()

	runHook(ctx, "claude-code", pr, env(map[string]string{"MUXREVEAL_SOCK": "/x", "MUXREVEAL_PANE": "p"}))

	if d := time.Since(start); d > time.Second {
		t.Fatalf("runHook took %v, want about 200ms", d)
	}

	_ = pr.Close()
}

func TestHookCommandHiddenAndQuiet(t *testing.T) {
	cmd := newRootCmd()

	var help bytes.Buffer

	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(help.String(), "hook") {
		t.Fatalf("help lists the hidden hook command:\n%s", help.String())
	}

	hook, _, err := newRootCmd().Find([]string{"hook", "claude-code"})
	if err != nil || hook.Name() != "hook" || !hook.Hidden {
		t.Fatalf("Find(hook) = %v, %v; want the hidden hook command", hook, err)
	}
}

func TestHookCommandReturnsNilWithoutEnvironment(t *testing.T) {
	t.Setenv("MUXREVEAL_SOCK", "")
	t.Setenv("MUXREVEAL_PANE", "")

	cmd := newRootCmd()

	var buf bytes.Buffer

	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader(sessionStart))
	cmd.SetArgs([]string{"hook", "claude-code"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}

	if buf.Len() != 0 {
		t.Fatalf("output = %q, want none", buf.String())
	}
}

func TestRunHookDrainsStdinWithoutEnvironment(t *testing.T) {
	pr, pw := io.Pipe()
	payload := bytes.Repeat([]byte("x"), 1<<20)
	written := make(chan error, 1)

	go func() {
		_, err := pw.Write(payload)
		_ = pw.Close()

		written <- err
	}()

	start := time.Now()

	captureStdio(t, func() {
		ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
		defer cancel()

		runHook(ctx, "claude-code", pr, env(nil))
	})

	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Fatalf("runHook took %v, want within 2.5s", d)
	}

	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("writer error = %v, want every byte consumed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer still blocked after runHook returned")
	}
}

func TestRunHookDrainsPayloadOverLimit(t *testing.T) {
	pr, pw := io.Pipe()
	total := maxHookPayload + 1<<20
	written := make(chan error, 1)

	go func() {
		_, err := io.Copy(pw, io.LimitReader(zeroReader{}, int64(total)))
		_ = pw.Close()

		written <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
	defer cancel()

	runHook(ctx, "claude-code", pr, env(map[string]string{"MUXREVEAL_SOCK": "/x", "MUXREVEAL_PANE": "p"}))

	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("writer error = %v, want every byte consumed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer still blocked after runHook returned")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)

	return len(p), nil
}

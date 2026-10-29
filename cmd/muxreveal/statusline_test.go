package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/harness"
)

const statusPayload = `{"hook_event_name":"Status","session_id":"s1","model":{"id":"claude-opus-4-5","display_name":"Opus 4.5"},` +
	`"context_window":{"total_input_tokens":84000,"context_window_size":200000,"used_percentage":42.7}}`

// statusServer serves a hook socket and returns its path and the events it
// receives.
func statusServer(t *testing.T) (string, <-chan harness.Event) {
	t.Helper()

	path := shortSock(t)

	srv, err := harness.Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan harness.Event, 4)
	done := make(chan error, 1)

	go func() { done <- srv.Serve(context.Background(), out) }()

	t.Cleanup(func() {
		_ = srv.Close()

		if err := <-done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	})

	return path, out
}

func TestRunStatuslineDelivers(t *testing.T) {
	path, out := statusServer(t)

	runStatusline(context.Background(), strings.NewReader(statusPayload),
		env(map[string]string{"MUXREVEAL_SOCK": path, "MUXREVEAL_PANE": "p7"}))

	select {
	case ev := <-out:
		if ev.Pane != core.PaneID("p7") || ev.Session != "s1" || !ev.Status || ev.Model != "Opus 4.5" {
			t.Fatalf("event = %+v, want a status event for pane p7, session s1 and model Opus 4.5", ev)
		}

		if !ev.ContextKnown || ev.Context != 42.7 || ev.Window != 200000 || ev.InputTokens != 84000 {
			t.Fatalf("event = %+v, want 42.7%% of a 200000 window with 84000 input tokens", ev)
		}
	default:
		t.Fatal("no event delivered")
	}
}

func TestRunStatuslineIsSilentAndQuick(t *testing.T) {
	missing := filepath.Join(shortDir(t), "none.sock")
	silent := silentSock(t)

	oversized := `{"session_id":"s1","pad":"` + strings.Repeat("x", maxHookPayload) + `"}`

	tests := []struct {
		name  string
		stdin string
		env   map[string]string
	}{
		{"no socket", statusPayload, map[string]string{"MUXREVEAL_PANE": "p"}},
		{"no pane", statusPayload, map[string]string{"MUXREVEAL_SOCK": missing}},
		{"invalid json", "{not json", map[string]string{"MUXREVEAL_SOCK": missing, "MUXREVEAL_PANE": "p"}},
		{"truncated", statusPayload[:40], map[string]string{"MUXREVEAL_SOCK": silent, "MUXREVEAL_PANE": "p"}},
		{"oversized", oversized, map[string]string{"MUXREVEAL_SOCK": silent, "MUXREVEAL_PANE": "p"}},
		{"never answers", statusPayload, map[string]string{"MUXREVEAL_SOCK": silent, "MUXREVEAL_PANE": "p"}},
		{"missing socket", statusPayload, map[string]string{"MUXREVEAL_SOCK": missing, "MUXREVEAL_PANE": "p"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()

			captureStdio(t, func() {
				ctx, cancel := context.WithTimeout(context.Background(), statuslineBudget)
				defer cancel()

				runStatusline(ctx, strings.NewReader(tt.stdin), env(tt.env))
			})

			if d := time.Since(start); d > 1500*time.Millisecond {
				t.Fatalf("runStatusline took %v, want within 1.5s", d)
			}
		})
	}
}

func TestRunStatuslineStdinNeverClosesStaysInBudget(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()

	captureStdio(t, func() {
		runStatusline(ctx, pr, env(map[string]string{"MUXREVEAL_SOCK": "/x", "MUXREVEAL_PANE": "p"}))
	})

	if d := time.Since(start); d > time.Second {
		t.Fatalf("runStatusline took %v, want about 200ms", d)
	}

	_ = pr.Close()
}

func TestStatuslineCommandHiddenAndQuiet(t *testing.T) {
	cmd := newRootCmd()

	var help bytes.Buffer

	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(help.String(), "statusline") {
		t.Fatalf("help lists the hidden statusline command:\n%s", help.String())
	}

	found, _, err := newRootCmd().Find([]string{"statusline"})
	if err != nil || found.Name() != "statusline" || !found.Hidden {
		t.Fatalf("Find(statusline) = %v, %v; want the hidden statusline command", found, err)
	}

	t.Run("without the environment", func(t *testing.T) {
		t.Setenv("MUXREVEAL_SOCK", "")
		t.Setenv("MUXREVEAL_PANE", "")

		var buf bytes.Buffer

		cmd := newRootCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetIn(strings.NewReader(statusPayload))
		cmd.SetArgs([]string{"statusline"})

		captureStdio(t, func() {
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Errorf("Execute() error = %v, want nil", err)
			}
		})

		if buf.Len() != 0 {
			t.Fatalf("output = %q, want none", buf.String())
		}
	})

	t.Run("with the environment", func(t *testing.T) {
		path, out := statusServer(t)

		t.Setenv("MUXREVEAL_SOCK", path)
		t.Setenv("MUXREVEAL_PANE", "p3")

		var buf bytes.Buffer

		cmd := newRootCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetIn(strings.NewReader(statusPayload))
		cmd.SetArgs([]string{"statusline"})

		captureStdio(t, func() {
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Errorf("Execute() error = %v, want nil", err)
			}
		})

		if buf.Len() != 0 {
			t.Fatalf("output = %q, want none", buf.String())
		}

		select {
		case ev := <-out:
			if ev.Pane != core.PaneID("p3") || !ev.Status || ev.Model != "Opus 4.5" {
				t.Fatalf("event = %+v, want a status event for pane p3", ev)
			}
		default:
			t.Fatal("no event delivered")
		}
	})
}

func TestRunStatuslineDrainsStdin(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		payload io.Reader
	}{
		{"without the environment", nil, bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20))},
		{
			"with the environment",
			map[string]string{"MUXREVEAL_SOCK": "/x", "MUXREVEAL_PANE": "p"},
			io.LimitReader(zeroReader{}, maxHookPayload+1<<20),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, pw := io.Pipe()
			written := make(chan error, 1)

			go func() {
				_, err := io.Copy(pw, tt.payload)
				_ = pw.Close()

				written <- err
			}()

			captureStdio(t, func() {
				ctx, cancel := context.WithTimeout(context.Background(), statuslineBudget)
				defer cancel()

				runStatusline(ctx, pr, env(tt.env))
			})

			select {
			case err := <-written:
				if err != nil {
					t.Fatalf("writer error = %v, want every byte consumed", err)
				}
			case <-time.After(time.Second):
				t.Fatal("writer still blocked after runStatusline returned")
			}
		})
	}
}

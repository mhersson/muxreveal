package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name    string
		content string
		missing bool
	}{
		{name: "unknown key", content: "them = 1\n"},
		{name: "explicit path missing", missing: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			stateHome := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateHome)

			path := filepath.Join(t.TempDir(), "config.toml")
			if !tt.missing {
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			cmd := newRootCmd()
			cmd.SetArgs([]string{"--config", path})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			// Cancelled up front, so a run that wrongly gets as far as the TUI
			// returns at once instead of taking over the test's terminal.
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err := cmd.ExecuteContext(ctx)

			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("Execute() error = %v, want one naming %s", err, path)
			}
			// The log is opened right before the TUI starts, so an empty state
			// dir means the TUI never started.
			if entries, _ := os.ReadDir(stateHome); len(entries) != 0 {
				t.Errorf("state dir has %d entries, want none", len(entries))
			}
		})
	}
}

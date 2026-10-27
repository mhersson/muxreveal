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

func TestRootRejectsBadLayoutConfig(t *testing.T) {
	for _, content := range []string{
		"[layout]\nmaster_ratio = 95\n",
		"[layout]\nmaster_count = 0\n",
		"[layout]\ndefault_mode = \"tiled\"\n",
		"[layout]\ncolumn_width = 5\n",
	} {
		t.Run(strings.TrimSpace(content), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			stateHome := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateHome)

			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			cmd := newRootCmd()
			cmd.SetArgs([]string{"--config", path})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err := cmd.ExecuteContext(ctx)

			if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "layout.") {
				t.Fatalf("Execute() error = %v, want one naming %s and the layout key", err, path)
			}

			if entries, _ := os.ReadDir(stateHome); len(entries) != 0 {
				t.Errorf("state dir has %d entries, want none", len(entries))
			}
		})
	}
}

func TestRootRejectsUnknownTheme(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("theme = \"nope\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--config", path})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := cmd.ExecuteContext(ctx)

	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "catppuccin-mocha") {
		t.Fatalf("Execute() error = %v, want one naming %s and the known themes", err, path)
	}

	if entries, _ := os.ReadDir(stateHome); len(entries) != 0 {
		t.Errorf("state dir has %d entries, want none", len(entries))
	}
}

func TestRecordFlagIsHidden(t *testing.T) {
	cmd := newRootCmd()

	f := cmd.Flags().Lookup("record")
	if f == nil || !f.Hidden {
		t.Fatalf("record flag = %+v, want a hidden flag", f)
	}

	var help strings.Builder

	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(help.String(), "--record") {
		t.Fatalf("help lists the hidden flag:\n%s", help.String())
	}
}

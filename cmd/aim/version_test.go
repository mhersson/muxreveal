package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

const testRev = "667c9237aaaabbbbccccddddeeeeffff00001111"

func vcsSettings(modified string) []debug.BuildSetting {
	return []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: testRev},
		{Key: "vcs.time", Value: "2026-11-01T10:06:00Z"},
		{Key: "vcs.modified", Value: modified},
	}
}

func infoWith(version string, settings []debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Version: version}, Settings: settings}
}

func TestVersionString(t *testing.T) {
	t.Parallel()

	release := stamp{version: "v1.0.0", commit: "667c923", date: "2026-11-01T12:00:00Z"}

	const branch = "v0.0.0-20261101100600-667c92370000"

	tests := []struct {
		name string
		ld   stamp
		info *debug.BuildInfo
		want string
	}{
		{"release", release, infoWith("(devel)", vcsSettings("false")), "v1.0.0 (commit 667c923, built 2026-11-01T12:00:00Z)"},
		{"release from a modified tree", release, infoWith("(devel)", vcsSettings("true")), "v1.0.0 (commit 667c923, modified, built 2026-11-01T12:00:00Z)"},
		{"formula", stamp{version: "1.0.0"}, infoWith("(devel)", nil), "v1.0.0"},
		{"formula head", stamp{version: "HEAD"}, infoWith("(devel)", nil), "HEAD"},
		{"go install of a tag", stamp{}, infoWith("v1.0.0", nil), "v1.0.0"},
		{"go install of a branch", stamp{}, infoWith(branch, nil), branch},
		{"checkout", stamp{}, infoWith(branch, vcsSettings("false")), "dev (commit 667c923, committed 2026-11-01T10:06:00Z)"},
		{"checkout, modified", stamp{}, infoWith(branch+"+dirty", vcsSettings("true")), "dev (commit 667c923, modified, committed 2026-11-01T10:06:00Z)"},
		{"checkout at a tag", stamp{}, infoWith("v1.0.0", vcsSettings("false")), "dev (commit 667c923, committed 2026-11-01T10:06:00Z)"},
		{"no build info", stamp{}, nil, "dev"},
		{"devel without vcs", stamp{}, infoWith("(devel)", nil), "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := versionString(tt.ld, tt.info); got != tt.want {
				t.Errorf("versionString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRootVersion(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, configHome string) []string
	}{
		{"broken default config", func(t *testing.T, configHome string) []string {
			dir := filepath.Join(configHome, "aim")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("them = 1\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			return []string{"--version"}
		}},
		{"missing named config", func(t *testing.T, _ string) []string {
			return []string{"--config", filepath.Join(t.TempDir(), "missing.toml"), "--version"}
		}},
		{"no HOME", func(t *testing.T, _ string) []string {
			t.Setenv("HOME", "")

			return []string{"-v"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configHome := t.TempDir()
			stateHome := shortDir(t)

			t.Setenv("HOME", shortDir(t))
			t.Setenv("XDG_CONFIG_HOME", configHome)
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("XDG_STATE_HOME", stateHome)

			args := tt.setup(t, configHome)

			var stdout, stderr bytes.Buffer

			cmd := newRootCmd()
			cmd.SetArgs(args)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := cmd.ExecuteContext(ctx); err != nil {
				t.Fatalf("Execute() error = %v, want nil", err)
			}

			want := "aim " + versionString(stamp{version, commit, date}, buildInfo()) + "\n"
			if stdout.String() != want {
				t.Errorf("stdout = %q, want %q", stdout.String(), want)
			}

			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}

			if entries, _ := os.ReadDir(stateHome); len(entries) != 0 {
				t.Errorf("state dir has %d entries, want none", len(entries))
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRunServesHooks(t *testing.T) {
	t.Setenv("HOME", shortDir(t))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stateHome := shortDir(t)
	t.Setenv("XDG_STATE_HOME", stateHome)

	env, err := core.DefaultEnv()
	if err != nil {
		t.Fatal(err)
	}

	// No native clipboard tool may run from a test.
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[clipboard]\nnative = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	in, inW := io.Pipe()

	t.Cleanup(func() { _ = inW.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- run(ctx, env, cfg, "", false, noClients, tea.WithInput(in), tea.WithOutput(io.Discard))
	}()

	runDir := filepath.Join(core.StateDir(env), "run")
	pid := strconv.Itoa(os.Getpid())
	sock := filepath.Join(runDir, pid+".sock")
	settings := filepath.Join(runDir, pid+"-claude.json")

	waitFor(t, func() bool { return exists(sock) && exists(settings) })

	if fi, err := os.Stat(runDir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("run dir = %v, %v, want mode 0700", fi, err)
	}

	for _, p := range []string{sock, settings} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s = %v, %v, want mode 0600", p, fi, err)
		}
	}

	data, err := os.ReadFile(settings)
	if err != nil || !strings.Contains(string(data), `"hook"`) {
		t.Errorf("settings = %q, %v, want the hook command", data, err)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after its context ended")
	}

	if exists(sock) || exists(settings) {
		t.Errorf("socket exists = %v, settings exists = %v, want both removed", exists(sock), exists(settings))
	}
}

func TestStartHooksFailureTurnsHooksOff(t *testing.T) {
	stateDir := t.TempDir()
	// A file where the run dir belongs makes the setup fail without relying on permissions.
	if err := os.WriteFile(filepath.Join(stateDir, "run"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := startHooks(discardLogger(), stateDir, "", testWatches(t))
	defer h.Stop()

	if h.Settings != "" || h.Sock != "" || h.Events != nil {
		t.Errorf("runtime = %+v, want no settings, socket or events", h)
	}

	if !strings.HasPrefix(h.Notice, "agent hooks are off: ") {
		t.Errorf("notice = %q, want the hooks-off notice", h.Notice)
	}
}

func TestStartHooksNamesAnOverlongSocketPath(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), strings.Repeat("d", maxSockPath))

	h := startHooks(discardLogger(), stateDir, "", testWatches(t))
	defer h.Stop()

	if h.Settings != "" || h.Sock != "" || h.Events != nil {
		t.Errorf("runtime = %+v, want no settings, socket or events", h)
	}

	sock := filepath.Join(stateDir, "run", strconv.Itoa(os.Getpid())+".sock")
	want := fmt.Sprintf("agent hooks are off: socket path too long (%d bytes, at most %d)", len(sock), maxSockPath)

	if h.Notice != want {
		t.Errorf("notice = %q, want %q", h.Notice, want)
	}
}

func TestWarnStatusLineOff(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		want string // what the warning names; empty for no warning
	}{
		{"quotable path", "/opt/muxreveal/bin/muxreveal", ""},
		{"single quote", "/opt/mux'reveal/muxreveal", "single quote"},
		{"backslash", `/opt/mux\reveal/muxreveal`, "backslash"},
		{"escape character", "/opt/mux\x1b[31mreveal/muxreveal", "U+001B"},
		{"newline", "/opt/mux\nreveal/muxreveal", "U+000A"},
		{"invalid UTF-8", "/opt/mux\xffreveal/muxreveal", "UTF-8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			warnStatusLineOff(slog.New(slog.NewTextHandler(&buf, nil)), tt.exe)

			got := buf.String()
			if tt.want == "" {
				if got != "" {
					t.Fatalf("log = %q, want no warning", got)
				}

				return
			}

			if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
				t.Fatalf("log = %q, want one record", got)
			}

			for _, want := range []string{"level=WARN", "status line is off", tt.want} {
				if !strings.Contains(got, want) {
					t.Errorf("log = %q, want it to contain %q", got, want)
				}
			}

			body := strings.TrimSuffix(got, "\n")
			if i := strings.IndexFunc(body, unicode.IsControl); i >= 0 || !utf8.ValidString(body) {
				t.Errorf("log = %q, want no raw control byte and valid UTF-8", got)
			}
		})
	}
}

func TestStartHooksSweepsDeadInstances(t *testing.T) {
	stateDir := shortDir(t)
	runDir := filepath.Join(stateDir, "run")

	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}

	dead := deadPid(t)

	// Another live process: the parent of the test binary.
	live := os.Getppid()

	files := map[string]bool{ // name -> kept
		strconv.Itoa(dead) + ".sock":            false,
		strconv.Itoa(dead) + "-claude.json":     false,
		strconv.Itoa(live) + ".sock":            true,
		strconv.Itoa(live) + "-claude.json":     true,
		"notes.txt":                             true,
		"abc.sock":                              true,
		"007.sock":                              true,
		strconv.Itoa(dead) + "-claude.json~":    true,
		strconv.Itoa(dead) + "-index-a1b2":      false,
		strconv.Itoa(dead) + "-index-a1b2.lock": false,
		strconv.Itoa(live) + "-index-a1b2":      true,
		strconv.Itoa(live) + "-index-a1b2.lock": true,
		"abc-index-a1b2":                        true,
		strconv.Itoa(dead) + "-indexed":         true,
	}
	for name := range files {
		if err := os.WriteFile(filepath.Join(runDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	h := startHooks(discardLogger(), stateDir, "", testWatches(t))
	defer h.Stop()

	if h.Notice != "" {
		t.Fatalf("notice = %q, want none", h.Notice)
	}

	for name, kept := range files {
		if got := exists(filepath.Join(runDir, name)); got != kept {
			t.Errorf("%s exists = %v, want %v", name, got, kept)
		}
	}

	if !exists(h.Sock) || !exists(h.Settings) {
		t.Error("this instance's files are missing")
	}

	h.Stop()

	if exists(h.Sock) || exists(h.Settings) {
		t.Error("Stop left this instance's files behind")
	}
}

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("the running process is not alive")
	}

	if pidAlive(deadPid(t)) {
		t.Error("a reaped process is alive")
	}
}

// deadPid returns the pid of a child that has exited and been reaped.
func deadPid(t *testing.T) int {
	t.Helper()

	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}

	return c.Process.Pid
}

func exists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func testWatches(t *testing.T) *git.WatchSet {
	t.Helper()

	w := git.NewWatchSet(context.Background(), git.WatchOptions{})

	t.Cleanup(func() { _ = w.Close() })

	return w
}

func TestWatchSeqResolvesTheRootFromASubdirectory(t *testing.T) {
	dir := seededRepo(t)
	sub := filepath.Join(dir, "sub")

	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	watches := testWatches(t)
	seq := watchSeq(watches)

	if root, _, ok := seq(ctx, sub); ok || root != dir {
		t.Fatalf("unwatched: root = %q, ok = %v", root, ok)
	}

	r, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	release, err := watches.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}

	defer release()

	if root, _, ok := seq(ctx, sub); !ok || root != dir {
		t.Fatalf("watched: root = %q, ok = %v", root, ok)
	}

	if _, _, ok := seq(ctx, t.TempDir()); ok {
		t.Fatal("a directory outside any repository reported a watcher")
	}
}

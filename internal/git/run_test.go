package git

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunIgnoresInheritedRepoVariables(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "f", "x\n")
	commitAll(t, root, "init")

	t.Setenv("GIT_DIR", "/nonexistent")
	t.Setenv("GIT_INDEX_FILE", "/nonexistent")
	t.Setenv("GIT_WORK_TREE", "/nonexistent")

	envLog := fakeGit(t, `env > "$LOG.env"`)

	out, err := run(context.Background(), call{dir: root, args: []string{"rev-parse", "--show-toplevel"}, read: true})
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(string(out)); got != root {
		t.Errorf("toplevel = %q, want %q", got, root)
	}

	env, err := os.ReadFile(envLog + ".env")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"GIT_DIR=", "GIT_INDEX_FILE=", "GIT_WORK_TREE="} {
		if strings.Contains(string(env), name) {
			t.Errorf("environment still has %s", name)
		}
	}

	if !strings.Contains(string(env), "GIT_OPTIONAL_LOCKS=0") {
		t.Error("read call lacks GIT_OPTIONAL_LOCKS=0")
	}
}

func TestRunEnvAndLocks(t *testing.T) {
	t.Parallel()

	root := newRepo(t)
	ctx, envLog := wrapGit(t, `env > "$LOG.env"`)
	idx := filepath.Join(t.TempDir(), "index")

	if _, err := run(ctx, call{dir: root, args: []string{"status", "-z"}, env: []string{"GIT_INDEX_FILE=" + idx}}); err != nil {
		t.Fatal(err)
	}

	env, err := os.ReadFile(envLog + ".env")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(env), "GIT_INDEX_FILE="+idx) {
		t.Error("call env not passed on")
	}

	if strings.Contains(string(env), "GIT_OPTIONAL_LOCKS") {
		t.Error("write call must not set GIT_OPTIONAL_LOCKS")
	}
}

func TestRunConfigArguments(t *testing.T) {
	t.Parallel()

	root := newRepo(t)
	ctx, logPath := wrapGit(t, "")

	if _, err := run(ctx, call{dir: root, args: []string{"status", "-z"}}); err != nil {
		t.Fatal(err)
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	want := "-c core.fsmonitor=false -c core.quotePath=false status -z"
	if !strings.Contains(string(log), want) {
		t.Errorf("log %q lacks %q", log, want)
	}
}

func TestRunTakesGitFromTheContext(t *testing.T) {
	t.Parallel()

	root := newRepo(t)
	ctx, logPath := wrapGit(t, "")

	if _, err := run(ctx, call{dir: root, args: []string{"status", "-z"}, read: true}); err != nil {
		t.Fatal(err)
	}

	derived, cancel := context.WithCancel(ctx)
	defer cancel()

	rc, err := stream(derived, call{dir: root, args: []string{"ls-files", "-z"}, read: true})
	if err != nil {
		t.Fatal(err)
	}

	_, _ = io.Copy(io.Discard, rc)
	must(t, rc.Close())

	if _, err := run(context.Background(), call{dir: root, args: []string{"rev-parse", "--git-dir"}, read: true}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(logPath)
	must(t, err)

	log := string(data)
	if !strings.Contains(log, "status -z") || !strings.Contains(log, "ls-files -z") {
		t.Errorf("calls given the context did not run the wrapper:\n%s", log)
	}

	if strings.Contains(log, "rev-parse --git-dir") {
		t.Errorf("a call without the context ran the wrapper:\n%s", log)
	}
}

func TestRunStdin(t *testing.T) {
	t.Parallel()
	root := newRepo(t)

	out, err := run(context.Background(), call{dir: root, args: []string{"hash-object", "--stdin"}, stdin: strings.NewReader("hello\n")})
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(string(out)); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Errorf("hash = %s", got)
	}
}

func TestRunErrorCarriesStderrFirstLine(t *testing.T) {
	t.Parallel()
	root := newRepo(t)

	_, err := run(context.Background(), call{dir: root, args: []string{"rev-parse", "--verify", "nope"}, read: true})
	if err == nil {
		t.Fatal("want an error")
	}

	if !strings.Contains(err.Error(), "rev-parse") || !strings.Contains(err.Error(), "fatal:") {
		t.Errorf("err = %q", err)
	}
}

// alive reports whether pid is a running process (a zombie is not).
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}

	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}

	_, rest, _ := strings.Cut(string(stat), ") ")

	return !strings.HasPrefix(rest, "Z")
}

func TestRunCancelKillsProcessGroup(t *testing.T) {
	root := newRepo(t)
	logPath := fakeGit(t, `sleep 30 &
echo $! > "$LOG.pid"
wait`)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := run(ctx, call{dir: root, args: []string{"status"}})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}

	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("run took %v after cancel", d)
	}

	raw, err := os.ReadFile(logPath + ".pid")
	if err != nil {
		t.Fatal(err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d survived cancel", pid)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// The wrapper forks without pause, so the cancel lands mid-fork: a child that
// misses the group kill would hold git's pipes open until WaitDelay.
func TestRunCancelKillsChildrenForkedDuringTheKill(t *testing.T) {
	root := newRepo(t)
	ctx, logPath := wrapGit(t, `echo $$ > "$LOG.pgid"
i=0
while [ $i -lt 300 ]; do
	sh -c 'echo $$ >> "$1"; exec sleep 30' sh "$LOG.pids" &
	i=$((i+1))
done
wait`)

	t.Cleanup(func() {
		if raw, err := os.ReadFile(logPath + ".pgid"); err == nil {
			if pgid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		}
	})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cancelled := make(chan time.Time, 1)

	go func() {
		for ctx.Err() == nil {
			if raw, err := os.ReadFile(logPath + ".pids"); err == nil && strings.Count(string(raw), "\n") >= 5 {
				cancelled <- time.Now()

				cancel()

				return
			}

			time.Sleep(time.Millisecond)
		}
	}()

	_, err := run(ctx, call{dir: root, args: []string{"status"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}

	if d := time.Since(<-cancelled); d > time.Second {
		t.Errorf("run took %v after cancel", d)
	}

	raw, err := os.ReadFile(logPath + ".pids")
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)

	for _, field := range strings.Fields(string(raw)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatal(err)
		}

		for alive(pid) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}

		if alive(pid) {
			t.Errorf("child %d survived cancel", pid)
		}
	}
}

func TestRunLimit(t *testing.T) {
	t.Parallel()

	root := newRepo(t)
	ctx, _ := wrapGit(t, "exec yes")

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := run(ctx, call{dir: root, args: []string{"status"}, limit: 1000})
	if !errors.Is(err, errLimit) {
		t.Fatalf("err = %v, want errLimit", err)
	}
}

func TestRunLimitNotReached(t *testing.T) {
	t.Parallel()
	root := newRepo(t)

	out, err := run(context.Background(), call{dir: root, args: []string{"rev-parse", "--git-dir"}, limit: 1000})
	if err != nil || strings.TrimSpace(string(out)) != ".git" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestStream(t *testing.T) {
	t.Parallel()
	root := newRepo(t)

	rc, err := stream(context.Background(), call{dir: root, args: []string{"rev-parse", "--git-dir"}, read: true})
	if err != nil {
		t.Fatal(err)
	}

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSpace(string(data)) != ".git" {
		t.Errorf("data = %q", data)
	}

	if err := rc.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

func TestStreamCloseKillsEarly(t *testing.T) {
	root := newRepo(t)
	fakeGit(t, "exec yes")

	rc, err := stream(context.Background(), call{dir: root, args: []string{"status"}})
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 16)
	if _, err := io.ReadFull(rc, buf); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- rc.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return")
	}
}

func TestNetCallHasNoTerminal(t *testing.T) {
	for _, name := range []string{"GIT_TERMINAL_PROMPT", "GCM_INTERACTIVE"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}

	hasEnv := func(cmdEnv []string, kv string) bool {
		for _, e := range cmdEnv {
			if e == kv {
				return true
			}
		}

		return false
	}

	netCmd := call{args: []string{"fetch"}, net: true}.command(context.Background())
	if !netCmd.SysProcAttr.Setsid || netCmd.SysProcAttr.Setpgid {
		t.Errorf("net call attr = %+v, want Setsid without Setpgid", netCmd.SysProcAttr)
	}

	for _, kv := range []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"} {
		if !hasEnv(netCmd.Env, kv) {
			t.Errorf("net call lacks %s", kv)
		}
	}

	plain := call{args: []string{"status"}}.command(context.Background())
	if !plain.SysProcAttr.Setpgid || plain.SysProcAttr.Setsid {
		t.Errorf("plain call attr = %+v, want Setpgid without Setsid", plain.SysProcAttr)
	}

	for _, kv := range []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"} {
		if hasEnv(plain.Env, kv) {
			t.Errorf("plain call has %s", kv)
		}
	}
}

func TestHideURLs(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"", ""},
		{"fatal: no url", "fatal: no url"},
		{"fatal: unable to access 'https://user:secret@example.invalid/o/r.git/'", "fatal: unable to access '<url>'"},
		{": ssh://host:22/o/r and git://h/x done", ": <url> and <url> done"},
	}
	for _, tt := range tests {
		if got := hideURLs(tt.in); got != tt.want {
			t.Errorf("hideURLs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

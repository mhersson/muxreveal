package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestMain isolates git from the machine's config once for the binary, so
// tests that build repositories need no t.Setenv.
func TestMain(m *testing.M) {
	env := map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_AUTHOR_NAME":     "Test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "Test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}

	os.Exit(m.Run())
}

// isolateGit points git at an empty global config in a temp dir with a fixed
// identity, so no test depends on the machine's git setup.
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
}

// newRepo creates an empty repository on branch main in a temp dir and returns
// its symlink-free path.
func newRepo(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	gitOut(t, dir, "init", "-q", "-b", "main")

	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()

	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fsAcceptsName reports whether the temp filesystem takes name; APFS refuses names that are not valid UTF-8.
func fsAcceptsName(t *testing.T, name string) bool {
	t.Helper()

	err := os.WriteFile(filepath.Join(t.TempDir(), name), nil, 0o600)
	if errors.Is(err, syscall.EILSEQ) {
		return false
	}

	if err != nil {
		t.Fatal(err)
	}

	return true
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", msg)
}

// gitOut runs git directly for test setup and returns trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	return gitInput(t, dir, "", args...)
}

// gitInput is gitOut with stdin on the command's standard input.
func gitInput(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

// fakeGit puts a git wrapper first on PATH. It appends its arguments, one call
// per line, to the returned log, runs extraShell (which may use $LOG), then
// execs the real git.
func fakeGit(t *testing.T, extraShell string) (logPath string) {
	t.Helper()

	script, logPath := writeGitWrapper(t, extraShell)

	t.Setenv("PATH", filepath.Dir(script)+string(os.PathListSeparator)+os.Getenv("PATH"))

	return logPath
}

// wrapGit returns a context whose git calls go through the logging wrapper of
// fakeGit. Only calls given ctx (or a context derived from it) are wrapped; it
// leaves PATH alone, so the test can run in parallel.
func wrapGit(t *testing.T, extraShell string) (ctx context.Context, logPath string) {
	t.Helper()

	script, logPath := writeGitWrapper(t, extraShell)

	return context.WithValue(context.Background(), gitPathKey{}, script), logPath
}

// wrappedCalls returns the wrapper's log and fails when it is missing or empty,
// so a test that asserts a call never ran cannot pass because nothing was wrapped.
func wrappedCalls(t *testing.T, logPath string) string {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		t.Fatalf("the git wrapper saw no calls: %v", err)
	}

	return string(data)
}

// writeGitWrapper writes a git wrapper script and returns its path and its log.
// The script appends its arguments, one call per line, to the log, runs
// extraShell (which may use $LOG), then execs the real git.
func writeGitWrapper(t *testing.T, extraShell string) (script, logPath string) {
	t.Helper()

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}

	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	body := "#!/bin/sh\nLOG='" + logPath + "'\nprintf '%s ' \"$@\" >> \"$LOG\"\necho >> \"$LOG\"\n" +
		extraShell + "\nexec '" + realGit + "' \"$@\"\n"
	script = filepath.Join(dir, "git")

	// A concurrent fork can inherit the write fd and make exec fail with ETXTBSY (go.dev/issue/22315).
	syscall.ForkLock.RLock()

	err = os.WriteFile(script, []byte(body), 0o755)

	syscall.ForkLock.RUnlock()

	if err != nil {
		t.Fatal(err)
	}

	return script, logPath
}

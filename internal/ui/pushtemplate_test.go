package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// pushTemplate is the repository and bare remote every push rig starts from. It
// is built on first use, copied per test, and removed by TestMain.
var pushTemplate struct {
	once sync.Once
	dir  string
	err  error
}

// removePushTemplate removes the template TestMain's run left behind.
func removePushTemplate() {
	if pushTemplate.dir != "" {
		_ = os.RemoveAll(pushTemplate.dir)
	}
}

// copyPushTemplate copies the template into dst, building it first when it does
// not exist. The remote is reached by a relative URL, so nothing in the copy
// points back at the template.
func copyPushTemplate(t *testing.T, gitBin, dst string) {
	t.Helper()

	pushTemplate.once.Do(func() {
		pushTemplate.dir, pushTemplate.err = os.MkdirTemp("", "muxreveal-push-template-")
		if pushTemplate.err == nil {
			pushTemplate.err = buildPushTemplate(gitBin, pushTemplate.dir)
		}
	})

	if pushTemplate.err != nil {
		t.Fatalf("push template: %v", pushTemplate.err)
	}

	if err := os.CopyFS(dst, os.DirFS(pushTemplate.dir)); err != nil {
		t.Fatalf("copy the push template: %v", err)
	}
}

// buildPushTemplate makes proj on feat/x two commits past main, with one edit
// left in the work tree, and its bare remote with main, in dir. The commit dates
// are fixed so the commit ids are the same on every run.
func buildPushTemplate(gitBin, dir string) error {
	proj := filepath.Join(dir, "proj")
	if err := os.Mkdir(proj, 0o755); err != nil {
		return err
	}

	steps := []struct {
		dir  string
		args []string
	}{
		{proj, []string{"init", "-q", "-b", "main"}},
		{dir, []string{"init", "-q", "--bare", "-b", "main", "remote.git"}},
	}

	for _, s := range steps {
		if err := templateGit(gitBin, s.dir, s.args...); err != nil {
			return err
		}
	}

	if err := os.WriteFile(filepath.Join(proj, "a.txt"), []byte("one\n"), 0o644); err != nil {
		return err
	}

	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-q", "-m", "init"},
		{"checkout", "-q", "-b", "feat/x"},
		{"remote", "add", "origin", pushRemoteURL},
		{"push", "-q", "origin", "main"},
		{"commit", "-q", "--allow-empty", "-m", "add the lexer"},
		{"commit", "-q", "--allow-empty", "-m", "fix the parser"},
	} {
		if err := templateGit(gitBin, proj, args...); err != nil {
			return err
		}
	}

	return os.WriteFile(filepath.Join(proj, "a.txt"), []byte("one\ntwo\n"), 0o644)
}

// templateGit runs the real git as the rigs' own setup does.
func templateGit(gitBin, dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, gitBin, args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_AUTHOR_DATE="+pushDate, "GIT_COMMITTER_DATE="+pushDate, gitFileOnly)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w\n%s", args, err, out)
	}

	return nil
}

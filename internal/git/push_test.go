package git

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// pushFixture is a repository on branch feat with one commit and a bare
// remote named origin.
type pushFixture struct {
	root, bare string
	repo       Repo
}

func newPushFixture(t *testing.T) pushFixture {
	t.Helper()

	root := newRepo(t)
	writeFile(t, root, "f", "1\n")
	commitAll(t, root, "init")
	gitOut(t, root, "switch", "-q", "-c", "feat")

	bare := newBare(t)
	gitOut(t, root, "remote", "add", "origin", bare)

	return pushFixture{root: root, bare: bare, repo: openRepo(t, root)}
}

func newBare(t *testing.T) string {
	t.Helper()

	bare := t.TempDir()
	gitOut(t, bare, "init", "-q", "--bare", "-b", "main")

	return bare
}

// clearSSHEnv makes core.sshCommand the ssh that runs: both variables outrank it.
func clearSSHEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	writeFile(t, dir, "script.sh", "#!/bin/sh\n"+body+"\n")

	path := filepath.Join(dir, "script.sh")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}

	return path
}

func refs(t *testing.T, dir string) string {
	t.Helper()

	return gitOut(t, dir, "for-each-ref", "--format=%(refname) %(objectname)")
}

func TestPushFirstSetsUpstream(t *testing.T) {
	f := newPushFixture(t)

	res, err := Push(context.Background(), f.repo, "feat")
	if err != nil {
		t.Fatal(err)
	}

	want := PushResult{Remote: "origin", Branch: "feat", Dst: "refs/heads/feat", Created: true, SetUpstream: true}
	if res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}

	if got := gitOut(t, f.root, "config", "branch.feat.merge"); got != "refs/heads/feat" {
		t.Errorf("branch.feat.merge = %q", got)
	}

	if got, want := gitOut(t, f.bare, "rev-parse", "refs/heads/feat"), gitOut(t, f.root, "rev-parse", "feat"); got != want {
		t.Errorf("remote feat = %s, want %s", got, want)
	}
}

func TestPushAgainFastForwards(t *testing.T) {
	f := newPushFixture(t)
	ctx := context.Background()

	if _, err := Push(ctx, f.repo, "feat"); err != nil {
		t.Fatal(err)
	}

	writeFile(t, f.root, "f", "2\n")
	commitAll(t, f.root, "second")

	res, err := Push(ctx, f.repo, "feat")
	if err != nil {
		t.Fatal(err)
	}

	if res.Created || res.UpToDate || res.SetUpstream || res.Dst != "refs/heads/feat" {
		t.Errorf("result = %+v", res)
	}

	if got, want := gitOut(t, f.bare, "rev-parse", "refs/heads/feat"), gitOut(t, f.root, "rev-parse", "feat"); got != want {
		t.Errorf("remote feat = %s, want %s", got, want)
	}
}

func TestPushUpToDate(t *testing.T) {
	f := newPushFixture(t)
	ctx := context.Background()

	if _, err := Push(ctx, f.repo, "feat"); err != nil {
		t.Fatal(err)
	}

	res, err := Push(ctx, f.repo, "feat")
	if err != nil {
		t.Fatal(err)
	}

	if !res.UpToDate || res.Created || res.SetUpstream {
		t.Errorf("result = %+v", res)
	}
}

func TestPushToUpstreamName(t *testing.T) {
	f := newPushFixture(t)
	gitOut(t, f.root, "config", "branch.feat.remote", "origin")
	gitOut(t, f.root, "config", "branch.feat.merge", "refs/heads/other")

	res, err := Push(context.Background(), f.repo, "feat")
	if err != nil {
		t.Fatal(err)
	}

	if res.Dst != "refs/heads/other" || res.SetUpstream || !res.Created {
		t.Errorf("result = %+v", res)
	}

	if got := gitOut(t, f.bare, "for-each-ref", "--format=%(refname)"); got != "refs/heads/other" {
		t.Errorf("remote refs = %q", got)
	}
}

func TestPushKeepsUpstreamOnAnotherRemote(t *testing.T) {
	f := newPushFixture(t)
	ctx := context.Background()

	if _, err := Push(ctx, f.repo, "feat"); err != nil {
		t.Fatal(err)
	}

	backup := newBare(t)
	gitOut(t, f.root, "remote", "add", "backup", backup)
	gitOut(t, f.root, "config", "remote.pushDefault", "backup")

	res, err := Push(ctx, f.repo, "feat")
	if err != nil {
		t.Fatal(err)
	}

	if res.Remote != "backup" || res.SetUpstream || !res.Created || res.Dst != "refs/heads/feat" {
		t.Errorf("result = %+v", res)
	}

	if got := gitOut(t, f.root, "config", "branch.feat.remote"); got != "origin" {
		t.Errorf("branch.feat.remote = %q, want origin", got)
	}

	if got := gitOut(t, f.root, "config", "branch.feat.merge"); got != "refs/heads/feat" {
		t.Errorf("branch.feat.merge = %q", got)
	}

	if got, want := gitOut(t, backup, "rev-parse", "refs/heads/feat"), gitOut(t, f.root, "rev-parse", "feat"); got != want {
		t.Errorf("backup feat = %s, want %s", got, want)
	}
}

func TestPushRejectedChangesNothing(t *testing.T) {
	f := newPushFixture(t)
	ctx := context.Background()

	if _, err := Push(ctx, f.repo, "feat"); err != nil {
		t.Fatal(err)
	}

	other := t.TempDir()
	gitOut(t, other, "clone", "-q", "-b", "feat", f.bare, ".")
	writeFile(t, other, "g", "x\n")
	commitAll(t, other, "moved")
	gitOut(t, other, "push", "-q", "origin", "feat")

	writeFile(t, f.root, "f", "2\n")
	commitAll(t, f.root, "local")

	localRefs, bareRefs := refs(t, f.root), refs(t, f.bare)
	localCfg := gitOut(t, f.root, "config", "--local", "--list")

	_, err := Push(ctx, f.repo, "feat")
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}

	if !strings.Contains(err.Error(), "(fetch first)") || strings.Contains(err.Error(), f.bare) {
		t.Errorf("err = %q, want the fetch-first summary and no remote path", err)
	}

	if got := refs(t, f.root); got != localRefs {
		t.Errorf("local refs changed:\n%s\nwas:\n%s", got, localRefs)
	}

	if got := refs(t, f.bare); got != bareRefs {
		t.Errorf("remote refs changed:\n%s\nwas:\n%s", got, bareRefs)
	}

	if got := gitOut(t, f.root, "config", "--local", "--list"); got != localCfg {
		t.Errorf("config changed:\n%s\nwas:\n%s", got, localCfg)
	}
}

func TestPushNeverForces(t *testing.T) {
	f := newPushFixture(t)
	logPath := fakeGit(t, "")

	if _, err := Push(context.Background(), f.repo, "feat"); err != nil {
		t.Fatal(err)
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	seen := false

	for _, line := range strings.Split(string(log), "\n") {
		fields := strings.Fields(line)
		if !slices.Contains(fields, "push") {
			continue
		}

		seen = true

		for _, a := range fields {
			if a == "-f" || a == "--force" || a == "--mirror" || strings.HasPrefix(a, "+") || strings.HasPrefix(a, "--force-") {
				t.Errorf("push argument %q in %q", a, line)
			}
		}
	}

	if !seen {
		t.Error("no push in the git log")
	}
}

func TestPushRefusesBadBranch(t *testing.T) {
	f := newPushFixture(t)

	for _, b := range []string{"-x", "a..b", ""} {
		if _, err := Push(context.Background(), f.repo, b); !errors.Is(err, ErrBadBranch) {
			t.Errorf("branch %q: err = %v, want ErrBadBranch", b, err)
		}
	}
}

// sshRemote points origin at an unresolvable ssh URL and core.sshCommand at
// script, so no real ssh runs.
func sshRemote(t *testing.T, f pushFixture, script string) {
	t.Helper()
	clearSSHEnv(t)
	gitOut(t, f.root, "remote", "set-url", "origin", "ssh://example.invalid/r.git")
	gitOut(t, f.root, "config", "core.sshCommand", script)
}

func TestPushHungRemoteIsKilled(t *testing.T) {
	f := newPushFixture(t)
	sshRemote(t, f, writeScript(t, "sleep 30"))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Push(ctx, f.repo, "feat")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline error", err)
	}

	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestNetErrorHidesURLs(t *testing.T) {
	f := newPushFixture(t)
	sshRemote(t, f, writeScript(t,
		`echo "fatal: unable to access 'https://user:secret@example.invalid/o/r.git/'" >&2; exit 1`))

	_, err := Push(context.Background(), f.repo, "feat")
	if err == nil {
		t.Fatal("push succeeded")
	}

	for _, leak := range []string{"secret", "example.invalid/o/r"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q contains %q", err, leak)
		}
	}
}

const ttyChildEnv = "MUXREVEAL_TTY_CHILD"

// TestNetCallCannotOpenTTY runs itself under a pty, so the child has a
// controlling terminal: a plain call must reach /dev/tty (the control that
// proves the check can fail) and a network call must not.
func TestNetCallCannotOpenTTY(t *testing.T) {
	if os.Getenv(ttyChildEnv) != "" {
		ttyChild(t)

		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(exe, "-test.v", "-test.run=^TestNetCallCannotOpenTTY$")

	cmd.Env = append(os.Environ(), ttyChildEnv+"=1")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer ptmx.Close()

	var childOut bytes.Buffer

	drained := make(chan struct{})

	go func() {
		defer close(drained)

		_, _ = io.Copy(&childOut, ptmx)
	}()

	timer := time.AfterFunc(30*time.Second, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()

	werr := cmd.Wait()

	<-drained

	if werr != nil || !strings.Contains(childOut.String(), "--- PASS: TestNetCallCannotOpenTTY") {
		t.Fatalf("child: %v\n%s", werr, childOut.String())
	}
}

// ttyChild runs with a controlling terminal. The fake ssh reports on stderr,
// which git passes through as the first line of the error.
func ttyChild(t *testing.T) {
	t.Helper()
	isolateGit(t)
	clearSSHEnv(t)

	script := writeScript(t, `if (exec 3</dev/tty) 2>/dev/null; then echo tty; else echo notty; fi >&2
exit 1`)

	for _, tt := range []struct {
		net  bool
		want string
	}{{false, ": tty"}, {true, ": notty"}} {
		_, err := run(context.Background(), call{
			dir:  t.TempDir(),
			args: []string{"-c", "core.sshCommand=" + script, "ls-remote", "ssh://example.invalid/r.git"},
			net:  tt.net,
		})
		if err == nil || !strings.HasSuffix(err.Error(), tt.want) {
			t.Errorf("net=%v: err = %v, want suffix %q", tt.net, err, tt.want)
		}
	}
}

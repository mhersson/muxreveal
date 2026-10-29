package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/github"
	"github.com/mhersson/muxreveal/internal/ui/palette"
)

// pushDate fixes the commit dates of a rig, so commit ids and the goldens that
// show them are the same on every run.
const pushDate = "2026-10-29T12:00:00+0000"

// refreshRec records the repositories the poller was asked to refresh.
type refreshRec struct {
	*fakePRs

	rmu   sync.Mutex
	repos []github.Repo
}

func (r *refreshRec) Refresh(repo github.Repo) {
	r.rmu.Lock()
	defer r.rmu.Unlock()

	r.repos = append(r.repos, repo)
}

func (r *refreshRec) refreshed() []github.Repo {
	r.rmu.Lock()
	defer r.rmu.Unlock()

	return slices.Clone(r.repos)
}

// gitRead runs the real git in dir and returns its trimmed output.
func gitRead(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}

	return strings.TrimSpace(string(out))
}

// churnMsgs is churn that also returns every message it fed back.
func (r *railApp) churnMsgs(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()

	var all []tea.Msg

	queue := []tea.Cmd{cmd}

	for rounds := 0; len(queue) > 0; rounds++ {
		if rounds > 200 {
			t.Fatal("commands keep raising commands")
		}

		next := queue[0]
		queue = queue[1:]

		for _, m := range flatten(next) {
			if m != nil {
				all = append(all, m)
				queue = append(queue, r.step(m))
			}
		}
	}

	return all
}

// pushRig is a rail app with an agent in a repository named proj, on feat/x two
// commits past main. The remote has main. origin reads as github.com/o/r but
// pushes to a local bare repository.
type pushRig struct {
	*prApp
	rec   *refreshRec
	dir   string
	bare  string
	agent core.PaneID
}

func newPushRig(t *testing.T) *pushRig {
	t.Helper()

	// The app's own git reads no user configuration.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_DATE", pushDate)
	t.Setenv("GIT_COMMITTER_DATE", pushDate)

	p := newPRApp(t)
	rec := &refreshRec{fakePRs: p.src}
	p.prs.src = rec

	const url = "https://github.com/o/r.git"

	dir := p.prRepo(t, "proj", "feat/x", url)
	bare := filepath.Join(t.TempDir(), "remote.git")

	gitIn(t, p.real, filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	gitIn(t, p.real, dir, "config", "url."+bare+".pushInsteadOf", url)
	gitIn(t, p.real, dir, "push", "-q", "origin", "main")

	for _, msg := range []string{"add the lexer", "fix the parser"} {
		gitIn(t, p.real, dir, "commit", "-q", "--allow-empty", "-m", msg)
	}

	_ = os.Remove(p.log)

	r := &pushRig{prApp: p, rec: rec, dir: dir, bare: bare}
	r.agent = r.addAgent(t, false, dir)
	r.churn(t, r.settle())

	return r
}

// leaderP presses leader p and returns the command it raised.
func (r *pushRig) leaderP() tea.Cmd {
	press(r.App, keyLeader)

	return press(r.App, keyRune('p'))
}

// ask presses leader p and answers what follows.
func (r *pushRig) ask(t *testing.T) []tea.Msg {
	t.Helper()

	return r.churnMsgs(t, r.leaderP())
}

// askOpen asks and fails unless the push confirm is open.
func (r *pushRig) askOpen(t *testing.T) {
	t.Helper()

	r.ask(t)

	if r.confirm == nil || r.confirm.push == nil {
		t.Fatalf("no push confirm after leader p; notice = %q", r.notice)
	}
}

func (r *pushRig) remoteRefs(t *testing.T) string {
	t.Helper()

	return gitRead(t, r.real, r.bare, "for-each-ref", "--format=%(refname)")
}

// pushCalls counts the git pushes the app ran.
func (r *pushRig) pushCalls() int {
	b, err := os.ReadFile(r.log)
	if err != nil {
		return 0
	}

	n := 0

	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.Contains(line, " push ") {
			n++
		}
	}

	return n
}

func (r *pushRig) overlayText() string { return ansi.Strip(r.View().Content) }

func TestPushConfirmNamesTheTarget(t *testing.T) {
	r := newPushRig(t)
	r.askOpen(t)

	oid := gitRead(t, r.real, r.dir, "rev-parse", "HEAD")
	text := r.overlayText()

	for _, want := range []string{
		"Push feat/x", "proj", "feat/x", "origin", "github.com/o/r", "2 commits", "sets the upstream",
		oid[:7] + " fix the parser", "enter push", "esc cancel",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the overlay lacks %q:\n%s", want, text)
		}
	}

	if strings.Contains(text, "https://") || strings.Contains(text, ".git") {
		t.Errorf("the overlay shows a raw URL:\n%s", text)
	}

	if got := r.remoteRefs(t); got != "refs/heads/main" {
		t.Errorf("the remote holds %q before the answer, want only main", got)
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times before the answer", n)
	}

	for _, icons := range []bool{true, false} {
		name := "icons"
		if !icons {
			name = "no icons"
		}

		t.Run(name, func(t *testing.T) {
			blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", 70)+"\n", 16), "\n")
			golden.RequireEqual(t, []byte(overlayConfirm(blank, 70, 16, *r.confirm, icons, r.styles)))
		})
	}
}

func TestPushConfirmShowsALocalRemote(t *testing.T) {
	r := newPushRig(t)
	gitIn(t, r.real, r.dir, "remote", "set-url", "origin", "../remote.git")
	r.askOpen(t)

	if text := r.overlayText(); !strings.Contains(text, "origin  ../remote.git") {
		t.Errorf("the overlay lacks the remote's path:\n%s", text)
	}
}

func TestPushFirstPush(t *testing.T) {
	r := newPushRig(t)
	r.askOpen(t)

	msgs := r.churnMsgs(t, press(r.App, keyEnter))

	if r.confirm != nil {
		t.Error("the confirm is still open after enter")
	}

	if want := "pushed feat/x to origin: 2 commits"; r.notice != want || r.noticeLevel() != levelSuccess {
		t.Errorf("toast = %q at level %v, want %q at the success level", r.notice, r.noticeLevel(), want)
	}

	if got := r.remoteRefs(t); !strings.Contains(got, "refs/heads/feat/x") {
		t.Errorf("the remote holds %q, want feat/x", got)
	}

	if got := gitRead(t, r.real, r.dir, "config", "branch.feat/x.merge"); got != "refs/heads/feat/x" {
		t.Errorf("upstream merge = %q, want refs/heads/feat/x", got)
	}

	if countOf[railHeadMsg](msgs) == 0 {
		t.Error("the pane's head was not probed again")
	}

	if got := r.rec.refreshed(); !slices.Equal(got, []github.Repo{prRepo}) {
		t.Errorf("refreshed %v, want %v", got, []github.Repo{prRepo})
	}
}

func TestPushConfirmEscCancels(t *testing.T) {
	r := newPushRig(t)
	r.askOpen(t)

	r.churn(t, press(r.App, keyEsc))

	if r.confirm != nil {
		t.Error("the confirm is still open after esc")
	}

	if got := r.remoteRefs(t); got != "refs/heads/main" {
		t.Errorf("the remote holds %q after esc, want only main", got)
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times after esc", n)
	}
}

func TestPushConfirmIgnoresYAndN(t *testing.T) {
	r := newPushRig(t)
	r.askOpen(t)

	for _, k := range []rune{'y', 'n'} {
		r.churn(t, press(r.App, keyRune(k)))

		if r.confirm == nil || r.confirm.push == nil {
			t.Fatalf("%q closed the push confirm", k)
		}
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times for y and n", n)
	}
}

func TestPushConfirmTargetIsFrozen(t *testing.T) {
	tests := []struct {
		name string
		move func(t *testing.T, r *pushRig, other string)
	}{
		{"focus moves to a pane on another branch", func(t *testing.T, r *pushRig, other string) {
			openIn(t, r.App, core.OpenPane{Kind: core.KindShell, Cwd: other, Split: core.SplitVertical, Area: r.bodyArea()})

			if r.store.State().Tab().Focus == r.agent {
				t.Fatal("focus did not move")
			}
		}},
		{"the agent changes directory", func(t *testing.T, r *pushRig, other string) {
			openIn(t, r.App, core.SetAgentCwd{ID: r.agent, Cwd: other})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newPushRig(t)
			other := r.prRepo(t, "other", "elsewhere", "")

			r.askOpen(t)
			tt.move(t, r, other)

			if r.confirm == nil || r.confirm.push == nil {
				t.Fatal("the confirm closed when the focus moved")
			}

			r.churn(t, press(r.App, keyEnter))

			if want := "pushed feat/x to origin: 2 commits"; r.notice != want {
				t.Errorf("toast = %q, want %q", r.notice, want)
			}

			got := r.remoteRefs(t)
			if !strings.Contains(got, "refs/heads/feat/x") || strings.Contains(got, "elsewhere") {
				t.Errorf("the remote holds %q, want feat/x and not elsewhere", got)
			}
		})
	}
}

func TestPushConfirmHeadMoved(t *testing.T) {
	tests := []struct {
		name  string
		moved func(t *testing.T, r *pushRig)
	}{
		{"a commit lands", func(t *testing.T, r *pushRig) {
			gitIn(t, r.real, r.dir, "commit", "-q", "--allow-empty", "-m", "late")
		}},
		{"the branch is reset", func(t *testing.T, r *pushRig) {
			gitIn(t, r.real, r.dir, "reset", "-q", "--hard", "HEAD~1")
		}},
		{"another branch is checked out", func(t *testing.T, r *pushRig) {
			gitIn(t, r.real, r.dir, "switch", "-q", "-c", "other")
		}},
		{"the head is detached", func(t *testing.T, r *pushRig) {
			gitIn(t, r.real, r.dir, "switch", "-q", "--detach")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newPushRig(t)
			r.askOpen(t)

			tt.moved(t, r)
			r.churn(t, press(r.App, keyEnter))

			if want := "push: feat/x changed since you asked; nothing pushed"; r.notice != want || r.noticeLevel() != levelNotice {
				t.Errorf("toast = %q at level %v, want %q at the notice level", r.notice, r.noticeLevel(), want)
			}

			if got := r.remoteRefs(t); got != "refs/heads/main" {
				t.Errorf("the remote holds %q, want only main", got)
			}

			if n := r.pushCalls(); n != 0 {
				t.Errorf("git pushed %d times", n)
			}
		})
	}
}

func TestPushUpToDate(t *testing.T) {
	r := newPushRig(t)
	gitIn(t, r.real, r.dir, "push", "-q", "-u", "origin", "feat/x")

	r.ask(t)

	if r.confirm != nil {
		t.Error("a confirm opened with nothing to push")
	}

	if want := "feat/x is up to date on origin"; r.notice != want || r.noticeLevel() != levelNotice {
		t.Errorf("toast = %q at level %v, want %q at the notice level", r.notice, r.noticeLevel(), want)
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times", n)
	}
}

func TestPushRejected(t *testing.T) {
	r := newPushRig(t)

	clone := filepath.Join(t.TempDir(), "clone")
	gitIn(t, r.real, filepath.Dir(clone), "clone", "-q", r.bare, clone)
	gitIn(t, r.real, clone, "switch", "-q", "-c", "feat/x")
	gitIn(t, r.real, clone, "commit", "-q", "--allow-empty", "-m", "remote work")
	gitIn(t, r.real, clone, "push", "-q", "origin", "feat/x")

	want := gitRead(t, r.real, r.bare, "rev-parse", "feat/x")

	r.askOpen(t)
	r.churn(t, press(r.App, keyEnter))

	if r.noticeLevel() != levelError {
		t.Errorf("toast level = %v, want error; text %q", r.noticeLevel(), r.notice)
	}

	// git says "fetch first" when the remote's tip is not in the clone, else "non-fast-forward".
	if !strings.Contains(r.notice, "push rejected (") || !strings.Contains(r.notice, "fetch first") && !strings.Contains(r.notice, "non-fast-forward") {
		t.Errorf("toast %q lacks git's reason", r.notice)
	}

	for _, leak := range []string{r.bare, "remote.git", "github.com", "https://"} {
		if strings.Contains(r.notice, leak) {
			t.Errorf("toast %q holds the remote %q", r.notice, leak)
		}
	}

	if got := gitRead(t, r.real, r.bare, "rev-parse", "feat/x"); got != want {
		t.Errorf("remote feat/x = %s after a rejected push, want %s", got, want)
	}
}

func TestPushDetached(t *testing.T) {
	r := newPushRig(t)
	gitIn(t, r.real, r.dir, "switch", "-q", "--detach")

	r.ask(t)

	if r.confirm != nil || r.notice != "push: HEAD is detached" || r.noticeLevel() != levelNotice {
		t.Errorf("confirm %v, toast %q at level %v, want the detached notice", r.confirm != nil, r.notice, r.noticeLevel())
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times", n)
	}
}

func TestPushNotRepo(t *testing.T) {
	r := newPushRig(t)
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: t.TempDir()})

	r.ask(t)

	if r.confirm != nil || r.notice != "push: not a git repository" {
		t.Errorf("confirm %v, toast %q, want the not-a-repository notice", r.confirm != nil, r.notice)
	}
}

func TestPushUnborn(t *testing.T) {
	r := newPushRig(t)

	empty := t.TempDir()
	gitIn(t, r.real, empty, "init", "-q", "-b", "main")
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: empty})

	r.ask(t)

	if r.confirm != nil || r.notice != "push: main has no commits yet" {
		t.Errorf("confirm %v, toast %q, want the no-commits notice", r.confirm != nil, r.notice)
	}
}

func TestPushNoRemote(t *testing.T) {
	r := newPushRig(t)
	solo := r.repo(t, "solo")
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: solo})

	r.ask(t)

	if r.confirm != nil || r.notice != "push: main has no remote to push to" {
		t.Errorf("confirm %v, toast %q, want the no-remote notice", r.confirm != nil, r.notice)
	}
}

func TestPushRunsOffTheLoop(t *testing.T) {
	r := newPushRig(t)

	calls := r.gitCalls()
	cmd := r.leaderP()

	if got := r.gitCalls(); got != calls {
		t.Fatalf("leader p ran git %d times on the update loop", got-calls)
	}

	msgs := flatten(cmd)
	if countOf[pushResolvedMsg](msgs) != 1 {
		t.Fatalf("messages = %v, want one pushResolvedMsg", msgs)
	}

	for _, m := range msgs {
		r.step(m)
	}

	if r.confirm == nil {
		t.Fatal("no confirm")
	}

	calls = r.gitCalls()
	cmd = press(r.App, keyEnter)

	if got := r.gitCalls(); got != calls || r.pushCalls() != 0 {
		t.Fatalf("enter ran git %d times on the update loop", got-calls)
	}

	if msgs = flatten(cmd); countOf[pushedMsg](msgs) != 1 {
		t.Fatalf("messages = %v, want one pushedMsg", msgs)
	}
}

func TestPushStopsWithTheApp(t *testing.T) {
	t.Run("while resolving", func(t *testing.T) {
		r := newPushRig(t)
		r.cancel()

		r.ask(t)

		if r.confirm != nil || r.notice != "" {
			t.Errorf("confirm %v, toast %q after the app's context ended, want nothing", r.confirm != nil, r.notice)
		}
	})

	t.Run("while pushing", func(t *testing.T) {
		r := newPushRig(t)
		r.askOpen(t)
		r.cancel()

		r.churn(t, press(r.App, keyEnter))

		if r.notice != "" || r.pushCalls() != 0 {
			t.Errorf("toast %q, %d pushes after the app's context ended, want none", r.notice, r.pushCalls())
		}

		if got := r.remoteRefs(t); got != "refs/heads/main" {
			t.Errorf("the remote holds %q, want only main", got)
		}
	})
}

func TestPushAnswerIsDroppedWhenStale(t *testing.T) {
	t.Run("a newer request", func(t *testing.T) {
		r := newPushRig(t)

		first := flatten(r.leaderP())
		second := flatten(r.leaderP())

		for _, m := range first {
			r.step(m)
		}

		if r.confirm != nil {
			t.Fatal("the first answer opened a confirm after a newer request")
		}

		for _, m := range second {
			r.step(m)
		}

		if r.confirm == nil || r.confirm.push == nil {
			t.Fatal("the newer answer did not open the push confirm")
		}
	})

	t.Run("the focus moved", func(t *testing.T) {
		r := newPushRig(t)
		msgs := flatten(r.leaderP())

		openIn(t, r.App, core.OpenPane{Kind: core.KindShell, Cwd: r.dir, Split: core.SplitVertical, Area: r.bodyArea()})

		for _, m := range msgs {
			r.step(m)
		}

		if r.confirm != nil {
			t.Error("an answer for a pane that lost focus opened a confirm")
		}
	})

	t.Run("another confirm is open", func(t *testing.T) {
		r := newPushRig(t)
		msgs := flatten(r.leaderP())

		press(r.App, keyLeader, keyRune('x'))

		if r.confirm == nil {
			t.Fatal("no close confirm")
		}

		for _, m := range msgs {
			r.step(m)
		}

		if r.confirm == nil || r.confirm.push != nil {
			t.Error("the push answer replaced the confirm that was open")
		}
	})
}

func TestPushCommand(t *testing.T) {
	a := newTestApp(t)

	c, ok := a.reg.get("git.push")
	if !ok || c.title != "Push branch" || c.icon != iconPush {
		t.Fatalf("git.push = %+v, %v; want Push branch with the push icon", c, ok)
	}

	if key, _ := a.keymap.keyOf("git.push"); key != "p" || a.keymap.groupOf("git.push") != "Git" {
		t.Errorf("git.push bound to %q in group %q, want p in Git", key, a.keymap.groupOf("git.push"))
	}

	if iconPush != "\uF403" {
		t.Errorf("iconPush = %q, want U+F403", iconPush)
	}
}

func TestCommitCount(t *testing.T) {
	for n, want := range map[int]string{1: "1 commit", 2: "2 commits", 12: "12 commits"} {
		if got := commitCount(n); got != want {
			t.Errorf("commitCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPushConfirmFitsANarrowScreen(t *testing.T) {
	r := newPushRig(t)
	r.askOpen(t)

	for _, w := range []int{30, 12, 5} {
		blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", w)+"\n", 16), "\n")

		for y, line := range strings.Split(ansi.Strip(overlayConfirm(blank, w, 16, *r.confirm, true, r.styles)), "\n") {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("width %d row %d is %d cells, want at most %d", w, y, got, w)
			}
		}
	}
}

func TestRemoteDisplay(t *testing.T) {
	tests := []struct {
		name, raw, want string
	}{
		{"https", "https://github.com/o/r.git", "github.com/o/r"},
		{"scp-like", "git@github.com:o/r.git", "github.com/o/r"},
		{"ssh url", "ssh://git@ghe.example.com/o/r", "ghe.example.com/o/r"},
		{"a token in the userinfo", "https://x-access-token:ghp_secret@github.com/o/r.git", "github.com/o/r"},
		{"a token in the query", "https://github.com/o/r.git?token=abc", "github.com/o/r"},
		{"userinfo, query and fragment of a deeper path", "https://tok:secret@git.example.com/a/b/c.git?token=abc#frag", "https://git.example.com/a/b/c"},
		{"scp-like with a deeper path", "git@git.example.com:a/b/c.git", "git.example.com:a/b/c"},
		{"a local path in the home directory", "/home/me/code/remote.git", "~/code/remote.git"},
		{"a local path elsewhere", "/srv/git/remote.git", "/srv/git/remote.git"},
		{"a relative path", "../remote.git", "../remote.git"},
		{"a file URL", "file:///home/me/remote.git", "~/remote.git"},
		{"control characters", "/srv/a\x1b[31mb", "/srv/a[31mb"},
		{"an unreadable URL", "https://user:secret@host:notaport/x", "(unreadable url)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remoteDisplay(tt.raw, "/home/me")
			if got != tt.want {
				t.Errorf("remoteDisplay(%q) = %q, want %q", tt.raw, got, tt.want)
			}

			if strings.Contains(got, "secret") || strings.Contains(got, "ghp_") || strings.Contains(got, "token=") {
				t.Errorf("remoteDisplay(%q) = %q shows a credential", tt.raw, got)
			}
		})
	}
}

func TestPushConfirmDrawsRepositoryTextCleaned(t *testing.T) {
	a := newTestApp(t)
	tgt := pushTarget{
		project: "pro\x1b[31mj", branch: "feat/\u200bx", remote: "ori\x07gin", url: "github.com/o/r",
		commits: 1, short: "abc1234", subject: "fix\x1b[2Jthe\u200b parser",
	}

	text := strings.Join(pushBox(tgt, 60, true, a.styles), "\n")

	for _, bad := range []string{"\x1b[31m", "\x1b[2J", "\u200b", "\x07"} {
		if strings.Contains(ansi.Strip(text), bad) {
			t.Errorf("the box holds %q", bad)
		}
	}

	for _, want := range []string{"pro[31mj", "feat/x", "origin", "abc1234 fix[2Jthe parser", "1 commit,"} {
		if !strings.Contains(ansi.Strip(text), want) {
			t.Errorf("the box lacks %q:\n%s", want, ansi.Strip(text))
		}
	}
}

func TestPushFromThePalette(t *testing.T) {
	r := newPushRig(t)

	press(r.App, keyLeader, keyRune(':'))

	if r.overlay == nil {
		t.Fatal("the palette did not open")
	}

	r.churn(t, r.step(palette.ChosenMsg{ID: "git.push"}))

	if r.confirm == nil || r.confirm.push == nil {
		t.Errorf("no push confirm after choosing Push branch; notice = %q", r.notice)
	}
}

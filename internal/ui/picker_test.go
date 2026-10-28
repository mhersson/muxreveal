package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
)

// pickerApp is an app over fake processes whose first shell starts in cwd,
// after its first size of 100x30. $HOME reads as /home/me.
func pickerApp(t *testing.T, cwd string) (*App, *starts) {
	t.Helper()

	a, st := lifeApp(t)
	a.cwd = cwd
	a.getenv = func(k string) string {
		if k == "HOME" {
			return "/home/me"
		}

		return testGetenv(k)
	}

	set := git.NewWatchSet(context.Background(), git.WatchOptions{})

	t.Cleanup(func() { _ = set.Close() })

	a.watches.set = set

	sized(t, a, 100, 30)

	return a, st
}

// plainDir is a temp directory that is not inside a repository.
func plainDir(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	return dir
}

// committedRepo is a repository on main with one commit.
func committedRepo(t *testing.T) string {
	t.Helper()

	dir := newRepoDir(t)
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	commitEverything(t, dir, "init")

	return dir
}

// deliver runs cmd, the one command a picker step raised, and delivers its
// answer; it returns what the answer raised.
func deliver(t *testing.T, a *App, cmd tea.Cmd) tea.Cmd {
	t.Helper()

	if cmd == nil {
		t.Fatal("no command to run")
	}

	_, next := a.Update(cmd())

	return next
}

// askPicker presses leader n and delivers the repository check's answer.
func askPicker(t *testing.T, a *App) {
	t.Helper()

	deliver(t, a, press(a, keyLeader, keyRune('n')))
}

func viewText(a *App) string { return ansi.Strip(a.View().Content) }

// lineWith is the first line of s that holds sub, or "".
func lineWith(s, sub string) string {
	for l := range strings.SplitSeq(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}

	return ""
}

var sampleRepo = git.Repo{Root: "/home/me/code/api", GitDir: "/home/me/code/api/.git", CommonDir: "/home/me/code/api/.git"}

func sampleTrees() []git.Worktree {
	return []git.Worktree{
		{Path: "/home/me/code/api", Head: "1111111aaaa", Branch: "main", Main: true},
		{Path: "/home/me/code/api/.worktrees/feat-auth", Head: "2222222bbbb", Branch: "feat/auth"},
		{Path: "/home/me/code/api.worktrees/hotfix", Head: "3333333cccc", Branch: "hotfix", Locked: true},
		{Path: "/tmp/gone", Head: "4444444dddd", Branch: "stale", Prunable: true},
		{Path: "/home/me/code/api/.worktrees/detached", Head: "abcdef0123456", Detached: true},
	}
}

// agentsAt adds n agent panes whose repository root is root.
func agentsAt(t *testing.T, a *App, root string, n int) {
	t.Helper()

	for range n {
		openIn(t, a, core.OpenPane{
			Kind: core.KindAgent, Cwd: root, Split: core.SplitVertical, Harness: "claude-code", Area: a.bodyArea(),
		})

		a.watches.panes[a.store.State().Tab().Focus] = &paneWatch{cwd: root, want: root}
	}
}

// injectPicker opens the picker on trees, as the answer to a leader n would.
func injectPicker(t *testing.T, a *App, repo git.Repo, trees []git.Worktree) {
	t.Helper()

	press(a, keyLeader, keyRune('n'))

	st := a.store.State()
	a.Update(pickerOpenMsg{
		seq: a.picker.seq, tab: st.Tab().ID, pane: st.Tab().Focus, cwd: st.Panes[st.Tab().Focus].Cwd, repo: repo, trees: trees,
	})

	if a.picker.open == nil {
		t.Fatal("the picker did not open")
	}
}

// openForm goes from the open picker to its new-worktree page and delivers the
// answer of the probe it raised.
func openForm(t *testing.T, a *App) {
	t.Helper()

	press(a, keyUp)
	deliver(t, a, press(a, keyEnter))
}

// openSampleForm is openForm for a picker over the sample repository, which
// does not exist on disk: the probe's answer is made up, with main as the base.
func openSampleForm(t *testing.T, a *App) {
	t.Helper()

	press(a, keyUp)

	if press(a, keyEnter) == nil {
		t.Fatal("enter on the new worktree row raised no probe")
	}

	a.Update(pickerPageMsg{seq: a.picker.open.seq, base: "main"})
}

func TestPickerOutsideARepoOpensAtOnce(t *testing.T) {
	dir := plainDir(t)
	a, st := pickerApp(t, dir)

	cmd := press(a, keyLeader, keyRune('n'))
	if cmd == nil {
		t.Fatal("leader n raised no command for the repository check")
	}

	if a.picker.open != nil || st.count() != 1 {
		t.Fatalf("before the answer: picker open = %v, starts = %d, want nothing yet", a.picker.open != nil, st.count())
	}

	_, _ = a.Update(cmd())

	if a.picker.open != nil {
		t.Error("the picker opened outside a repository")
	}

	if st.count() != 2 {
		t.Fatalf("starts = %d, want the agent to start", st.count())
	}

	if c := st.cmds[1]; c.Dir != dir || !strings.Contains(argvOf(c), "claude") {
		t.Errorf("second start = %+v, want claude in %s", c, dir)
	}

	s := a.store.State()
	if m := s.Panes[s.Tab().Focus]; m.Kind != core.KindAgent || m.Cwd != dir || len(s.Tab().Panes) != 2 {
		t.Errorf("focused pane = %+v of %d panes, want an agent in %s beside the first pane", m, len(s.Tab().Panes), dir)
	}
}

func TestPickerGitFailureOpensTheAgent(t *testing.T) {
	gone := filepath.Join(plainDir(t), "gone")
	a, st := pickerApp(t, gone)

	askPicker(t, a)

	if a.picker.open != nil {
		t.Error("the picker opened though git could not look at the directory")
	}

	if st.count() != 2 || st.cmds[1].Dir != gone {
		t.Fatalf("starts = %d, want the agent to start in %s", st.count(), gone)
	}

	if !strings.HasPrefix(a.notice, "worktrees unavailable: ") || len(a.notice) <= len("worktrees unavailable: ") {
		t.Errorf("notice = %q, want worktrees unavailable with a reason", a.notice)
	}

	if strings.Contains(a.notice, "\n") {
		t.Errorf("notice = %q, want one line", a.notice)
	}
}

func TestPickerGitFailureKeepsThePaneNotice(t *testing.T) {
	gone := filepath.Join(plainDir(t), "gone")
	a, st := pickerApp(t, gone)
	st.err = errors.New("no such directory")

	askPicker(t, a)

	if strings.HasPrefix(a.notice, "worktrees unavailable") || !strings.Contains(a.notice, "cannot start") {
		t.Errorf("notice = %q, want the pane's own failure to stay", a.notice)
	}
}

func TestPickerListsWorktrees(t *testing.T) {
	a, _ := pickerApp(t, "/work")
	agentsAt(t, a, "/home/me/code/api/.worktrees/feat-auth", 2)
	agentsAt(t, a, "/home/me/code/api", 1)
	injectPicker(t, a, sampleRepo, sampleTrees())

	view := viewText(a)

	for _, tt := range []struct {
		row   string
		wants []string
		not   []string
	}{
		{"main", []string{iconGit, "● 1 agent", "~/code/api"}, []string{"agents", "locked", "prunable"}},
		{"feat/auth", []string{"● 2 agents", ".worktrees/feat-auth"}, []string{"~/code"}},
		{"hotfix", []string{"locked", "~/code/api.worktrees/hotfix"}, []string{"agent", "prunable"}},
		{"stale", []string{"prunable", "/tmp/gone"}, []string{"agent", "locked", "~"}},
		{"abcdef0", []string{".worktrees/detached"}, []string{"abcdef01", "agent"}},
	} {
		line := lineWith(view, tt.row)
		if line == "" {
			t.Errorf("no row for %s:\n%s", tt.row, view)

			continue
		}

		for _, w := range tt.wants {
			if !strings.Contains(line, w) {
				t.Errorf("row %q lacks %q", line, w)
			}
		}

		for _, n := range tt.not {
			if strings.Contains(line, n) {
				t.Errorf("row %q has %q, want it absent", line, n)
			}
		}
	}

	for _, want := range []string{" New agent ", "\u276f", "up/down move", "enter open", "esc close", " 1 of 6 ", iconNewWorktree + " new worktree\u2026"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}

	if got := lineWith(view, "new worktree"); !strings.Contains(got, iconNewWorktree) {
		t.Errorf("last row %q lacks the new worktree icon", got)
	}
}

func TestPickerIconsOff(t *testing.T) {
	a, _ := pickerApp(t, "/work")
	a.icons = false
	injectPicker(t, a, sampleRepo, sampleTrees())

	view := viewText(a)

	for _, g := range []string{iconAgent, iconGit, iconNewWorktree} {
		if strings.Contains(view, g) {
			t.Errorf("view has glyph %q with icons off:\n%s", g, view)
		}
	}

	if !strings.Contains(view, "+ new worktree\u2026") || !strings.Contains(view, " New agent ") {
		t.Errorf("view lacks the plain new worktree row or title:\n%s", view)
	}
}

func TestPickerFilter(t *testing.T) {
	a, _ := pickerApp(t, "/work")
	injectPicker(t, a, sampleRepo, sampleTrees())

	typeText(a, "auth")

	view := viewText(a)
	if !strings.Contains(view, "feat/auth") || strings.Contains(view, "hotfix") || strings.Contains(view, "/tmp/gone") {
		t.Errorf("filter auth: want only feat/auth:\n%s", view)
	}

	if !strings.Contains(view, "new worktree\u2026") || !strings.Contains(view, " 1 of 2 ") || !strings.Contains(view, "\u276f auth") {
		t.Errorf("filter auth: want the new worktree row, the count and the query:\n%s", view)
	}

	pressRun(a, keyBackspace, keyBackspace, keyBackspace, keyBackspace)

	if view = viewText(a); !strings.Contains(view, "hotfix") || !strings.Contains(view, " 1 of 6 ") {
		t.Errorf("empty query: want every row again:\n%s", view)
	}

	// The path is searched too.
	typeText(a, "api.work")

	view = viewText(a)
	if !strings.Contains(view, "hotfix") || strings.Contains(view, "feat/auth") || strings.Contains(view, "main") {
		t.Errorf("filter api.work: want only hotfix:\n%s", view)
	}

	// Nothing matches: the new worktree row stays.
	typeText(a, "zzz")

	if view = viewText(a); !strings.Contains(view, "new worktree\u2026") || !strings.Contains(view, " 1 of 1 ") {
		t.Errorf("no match: want only the new worktree row:\n%s", view)
	}
}

func TestPickerNavigationWraps(t *testing.T) {
	a, _ := pickerApp(t, "/work")
	injectPicker(t, a, sampleRepo, sampleTrees())

	pressRun(a, keyUp)

	if view := viewText(a); !strings.Contains(view, " 6 of 6 ") {
		t.Errorf("up from the first row: want the new worktree row selected:\n%s", view)
	}

	pressRun(a, keyDown)

	if view := viewText(a); !strings.Contains(view, " 1 of 6 ") {
		t.Errorf("down from the last row: want the first row selected:\n%s", view)
	}
}

func TestPickerOpensAnAgentInTheWorktree(t *testing.T) {
	dir := committedRepo(t)
	wt := filepath.Join(dir, ".worktrees", "feat-x")
	gitIn(t, "git", dir, "worktree", "add", "-q", "-b", "feat/x", wt)

	a, st := pickerApp(t, dir)
	askPicker(t, a)

	if a.picker.open == nil {
		t.Fatal("the picker did not open in a repository")
	}

	view := viewText(a)
	for _, want := range []string{"main", "feat/x", ".worktrees/feat-x", " 1 of 3 "} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}

	pressRun(a, keyDown)
	press(a, keyEnter)

	if a.picker.open != nil {
		t.Error("the picker stays open after enter")
	}

	if st.count() != 2 {
		t.Fatalf("starts = %d, want the agent to start", st.count())
	}

	if c := st.cmds[1]; c.Dir != wt || !strings.Contains(argvOf(c), "claude") {
		t.Errorf("second start = %+v, want claude in %s", c, wt)
	}

	s := a.store.State()
	if m := s.Panes[s.Tab().Focus]; m.Kind != core.KindAgent || m.Cwd != wt || len(s.Tab().Panes) != 2 {
		t.Errorf("focused pane = %+v of %d panes, want an agent in %s beside the first pane", m, len(s.Tab().Panes), wt)
	}
}

func TestPickerPrunableNotice(t *testing.T) {
	a, st := pickerApp(t, "/work")
	injectPicker(t, a, sampleRepo, sampleTrees())

	pressRun(a, keyDown, keyDown, keyDown)
	press(a, keyEnter)

	const want = "that worktree is gone; git worktree prune removes it"
	if a.notice != want {
		t.Errorf("notice = %q, want %q", a.notice, want)
	}

	if a.picker.open == nil || st.count() != 1 {
		t.Errorf("picker open = %v, starts = %d, want the picker open and no start", a.picker.open != nil, st.count())
	}

	if !strings.Contains(viewText(a), want) {
		t.Errorf("view lacks the notice:\n%s", viewText(a))
	}

	// The next key clears it.
	press(a, keyUp)

	if a.notice != "" {
		t.Errorf("notice after a key = %q, want none", a.notice)
	}
}

func TestPickerEscCloses(t *testing.T) {
	a, st := pickerApp(t, "/work")
	injectPicker(t, a, sampleRepo, sampleTrees())

	press(a, keyEsc)

	if a.picker.open != nil || st.count() != 1 {
		t.Errorf("picker open = %v, starts = %d, want it closed and no start", a.picker.open != nil, st.count())
	}
}

func TestNewWorktreeCreatesAndOpens(t *testing.T) {
	dir := committedRepo(t)
	a, st := pickerApp(t, dir)
	askPicker(t, a)

	press(a, keyUp)
	deliver(t, a, press(a, keyEnter))

	view := viewText(a)
	for _, want := range []string{" New worktree ", "branch", "base", "path", "(from the branch name)", "tab next field", "enter create", "esc back"} {
		if !strings.Contains(view, want) {
			t.Errorf("page lacks %q:\n%s", want, view)
		}
	}

	if got := lineWith(view, "base"); !strings.Contains(got, "main") {
		t.Errorf("base row %q, want it prefilled with main", got)
	}

	typeText(a, "feat/x")

	if got := lineWith(viewText(a), "path"); !strings.Contains(got, ".worktrees/feat-x") {
		t.Errorf("path row %q, want .worktrees/feat-x", got)
	}

	deliver(t, a, press(a, keyEnter))

	want := filepath.Join(dir, ".worktrees", "feat-x")

	if a.picker.open != nil {
		t.Error("the picker stays open after a created worktree")
	}

	if st.count() != 2 {
		t.Fatalf("starts = %d, want the agent to start", st.count())
	}

	if c := st.cmds[1]; c.Dir != want || !strings.Contains(argvOf(c), "claude") {
		t.Errorf("second start = %+v, want claude in %s", c, want)
	}

	if a.notice != "created .worktrees/feat-x" {
		t.Errorf("notice = %q, want %q", a.notice, "created .worktrees/feat-x")
	}

	repo, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	list, err := git.ListWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.ContainsFunc(list, func(w git.Worktree) bool { return w.Path == want && w.Branch == "feat/x" }) {
		t.Errorf("worktrees = %+v, want %s on feat/x", list, want)
	}

	exclude, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(strings.Split(string(exclude), "\n"), "/.worktrees/") {
		t.Errorf("info/exclude = %q, want a /.worktrees/ line", exclude)
	}
}

func TestNewWorktreeErrorStaysInThePage(t *testing.T) {
	tests := []struct {
		name   string
		trunk  bool // the only branch is trunk: no default base
		prep   func(t *testing.T, dir string)
		branch string
		want   string
	}{
		{
			name: "path exists", branch: "feat/x", want: "that path already exists",
			prep: func(t *testing.T, dir string) {
				t.Helper()

				if err := os.MkdirAll(filepath.Join(dir, ".worktrees", "feat-x"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{name: "bad branch", branch: "a..b", want: "not a valid branch name"},
		{name: "no base", trunk: true, branch: "feat/x", want: "no usable base branch; name one in the base field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := committedRepo(t)
			if tt.trunk {
				gitIn(t, "git", dir, "branch", "-m", "trunk")
			}

			if tt.prep != nil {
				tt.prep(t, dir)
			}

			a, st := pickerApp(t, dir)
			askPicker(t, a)
			openForm(t, a)
			typeText(a, tt.branch)
			deliver(t, a, press(a, keyEnter))

			if a.picker.open == nil || a.picker.open.page != pageNewWorktree {
				t.Fatal("the page closed on an error")
			}

			if st.count() != 1 {
				t.Errorf("starts = %d, want no agent", st.count())
			}

			view := viewText(a)
			if !strings.Contains(view, tt.want) || !strings.Contains(view, " New worktree ") {
				t.Errorf("page lacks %q:\n%s", tt.want, view)
			}

			ui := a.current().theme.UI

			if raw := a.View().Content; !strings.Contains(raw, "\x1b[38;2;"+rgb(ui.Error)+"m"+tt.want) {
				t.Errorf("the error is not drawn in the Error colour:\n%q", lineWith(raw, tt.want))
			}

			// An edit clears the error.
			typeText(a, "q")

			if view = viewText(a); strings.Contains(view, tt.want) {
				t.Errorf("page still shows %q after an edit:\n%s", tt.want, view)
			}
		})
	}
}

func TestNewWorktreeShowsGitsReason(t *testing.T) {
	tests := []struct {
		name         string
		branch, base string
		want         string
		cut          bool // the reason is longer than the row
	}{
		{name: "base does not exist", branch: "feat/x", base: "nobase", want: "fatal: invalid reference: nobase"},
		{name: "branch is checked out", branch: "main", want: "fatal: 'main' is already", cut: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := committedRepo(t)
			a, st := pickerApp(t, dir)
			askPicker(t, a)
			openForm(t, a)
			typeText(a, tt.branch)

			if tt.base != "" {
				pressRun(a, keyTab, keyBackspace, keyBackspace, keyBackspace, keyBackspace)
				typeText(a, tt.base)
			}

			deliver(t, a, press(a, keyEnter))

			view := viewText(a)
			row := lineWith(view, tt.want)

			if row == "" {
				t.Fatalf("page lacks git's reason %q:\n%s", tt.want, view)
			}

			for _, junk := range []string{"create worktree", "exit status", "git worktree:"} {
				if strings.Contains(view, junk) {
					t.Errorf("page shows git's wrapper text %q:\n%s", junk, view)
				}
			}

			if cut := strings.Contains(row, "\u2026"); cut != tt.cut {
				t.Errorf("row %q: cut with an ellipsis = %v, want %v", row, cut, tt.cut)
			}

			if width := lipgloss.Width(row); width != 100 {
				t.Errorf("row is %d cells wide, want 100", width)
			}

			if st.count() != 1 || a.picker.open == nil {
				t.Errorf("starts = %d, picker open = %v, want no agent and the page open", st.count(), a.picker.open != nil)
			}
		})
	}
}

func TestNewWorktreeRetryAfterAnError(t *testing.T) {
	dir := committedRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, ".worktrees", "feat-x"), 0o755); err != nil {
		t.Fatal(err)
	}

	a, st := pickerApp(t, dir)
	askPicker(t, a)
	openForm(t, a)
	typeText(a, "feat/x")
	deliver(t, a, press(a, keyEnter))

	pressRun(a, keyBackspace)
	typeText(a, "y")
	deliver(t, a, press(a, keyEnter))

	if st.count() != 2 || st.cmds[1].Dir != filepath.Join(dir, ".worktrees", "feat-y") {
		t.Fatalf("starts = %d, want an agent in feat-y after fixing the name", st.count())
	}
}

func TestNewWorktreeCreatedAnswer(t *testing.T) {
	path := "/home/me/code/api/.worktrees/x"

	t.Run("opens the agent", func(t *testing.T) {
		a, st := pickerApp(t, "/work")
		injectPicker(t, a, sampleRepo, sampleTrees())
		openSampleForm(t, a)

		a.Update(pickerCreatedMsg{seq: a.picker.open.seq, created: git.Created{Worktree: git.Worktree{Path: path}}})

		if a.picker.open != nil || st.count() != 2 || st.cmds[1].Dir != path || a.notice != "created .worktrees/x" {
			t.Errorf("picker open = %v, starts = %d, notice = %q, want an agent in %s", a.picker.open != nil, st.count(), a.notice, path)
		}
	})

	t.Run("a worktree git could not list still opens", func(t *testing.T) {
		a, st := pickerApp(t, "/work")
		injectPicker(t, a, sampleRepo, sampleTrees())
		openSampleForm(t, a)

		a.Update(pickerCreatedMsg{
			seq: a.picker.open.seq, created: git.Created{Worktree: git.Worktree{Path: path}}, err: errors.New("not listed afterwards"),
		})

		if st.count() != 2 || a.picker.open != nil {
			t.Errorf("starts = %d, picker open = %v, want the agent opened", st.count(), a.picker.open != nil)
		}
	})

	t.Run("the pane cannot start", func(t *testing.T) {
		a, st := pickerApp(t, "/work")
		injectPicker(t, a, sampleRepo, sampleTrees())
		openSampleForm(t, a)

		st.err = errors.New("boom")

		a.Update(pickerCreatedMsg{seq: a.picker.open.seq, created: git.Created{Worktree: git.Worktree{Path: path}}})

		if !strings.HasPrefix(a.notice, "created .worktrees/x; cannot start") {
			t.Errorf("notice = %q, want the creation and why there is no pane", a.notice)
		}
	})
}

func TestNewWorktreeNeedsABranch(t *testing.T) {
	a, _ := pickerApp(t, "/work")
	injectPicker(t, a, sampleRepo, sampleTrees())
	openSampleForm(t, a)

	if cmd := press(a, keyEnter); cmd != nil {
		t.Error("enter on an empty branch started a creation")
	}

	if view := viewText(a); !strings.Contains(view, "enter a branch name") {
		t.Errorf("page lacks the empty branch error:\n%s", view)
	}
}

func TestNewWorktreeNameWithoutADirectory(t *testing.T) {
	a, _ := pickerApp(t, "/work")
	injectPicker(t, a, sampleRepo, sampleTrees())
	openSampleForm(t, a)
	typeText(a, "!!!")

	view := viewText(a)
	if got := lineWith(view, "path"); !strings.Contains(got, "not a valid branch name") {
		t.Errorf("path row %q, want the reason", got)
	}

	if cmd := press(a, keyEnter); cmd != nil {
		t.Error("enter on a name with no directory started a creation")
	}
}

func TestNewWorktreeSiblingRoot(t *testing.T) {
	a, _ := pickerApp(t, "/work")
	a.picker.cfg = core.WorktreesConfig{Root: "sibling"}
	injectPicker(t, a, sampleRepo, sampleTrees())
	openSampleForm(t, a)
	typeText(a, "feat/x")

	if got := lineWith(viewText(a), "path"); !strings.Contains(got, "~/code/api.worktrees/feat-x") {
		t.Errorf("path row %q, want ~/code/api.worktrees/feat-x", got)
	}
}

func TestNewWorktreeShowsIgnoreWarnings(t *testing.T) {
	t.Run("inside", func(t *testing.T) {
		dir := committedRepo(t)
		write(t, filepath.Join(dir, ".vscode", "settings.json"), "{}\n")

		a, _ := pickerApp(t, dir)
		askPicker(t, a)
		openForm(t, a)

		const want = `VS Code: add ".worktrees" to files.watcherExclude`

		view := viewText(a)
		if got := lineWith(view, "VS Code"); !strings.Contains(got, iconAlert+" "+want) {
			t.Errorf("warning row %q, want %q after the alert icon:\n%s", got, want, view)
		}

		ui := a.current().theme.UI

		if raw := a.View().Content; !strings.Contains(raw, "\x1b[38;2;"+rgb(ui.Warning)+"m"+want) {
			t.Errorf("the warning is not drawn in the Warning colour:\n%q", lineWith(raw, want))
		}
	})

	t.Run("sibling root asks for nothing", func(t *testing.T) {
		dir := committedRepo(t)
		write(t, filepath.Join(dir, ".vscode", "settings.json"), "{}\n")

		a, _ := pickerApp(t, dir)
		a.picker.cfg = core.WorktreesConfig{Root: "sibling"}
		askPicker(t, a)
		openForm(t, a)

		if view := viewText(a); strings.Contains(view, "VS Code") {
			t.Errorf("sibling page warns about VS Code:\n%s", view)
		}
	})

	t.Run("icons off", func(t *testing.T) {
		dir := committedRepo(t)
		write(t, filepath.Join(dir, ".vscode", "settings.json"), "{}\n")

		a, _ := pickerApp(t, dir)
		a.icons = false
		askPicker(t, a)
		openForm(t, a)

		if got := lineWith(viewText(a), "VS Code"); strings.Contains(got, iconAlert) || !strings.Contains(got, "VS Code: add") {
			t.Errorf("warning row %q, want it without the icon", got)
		}
	})
}

func TestNewWorktreeFieldsAndBack(t *testing.T) {
	a, _ := pickerApp(t, "/work")
	injectPicker(t, a, sampleRepo, sampleTrees())
	openSampleForm(t, a)

	typeText(a, "feat/x")
	pressRun(a, keyTab)
	typeText(a, "2")

	view := viewText(a)
	if got := lineWith(view, "branch"); !strings.Contains(got, "feat/x") || strings.Contains(got, "2") {
		t.Errorf("branch row %q, want feat/x alone", got)
	}

	if got := lineWith(view, "base"); !strings.Contains(got, "2") {
		t.Errorf("base row %q, want the typed 2", got)
	}

	pressRun(a, keyEsc)

	if a.picker.open == nil || a.picker.open.page != pageWorktrees {
		t.Fatal("esc on the page did not go back to the list")
	}

	if view = viewText(a); !strings.Contains(view, " New agent ") {
		t.Errorf("list not shown after esc:\n%s", view)
	}

	// The page keeps what was typed; the selection is still on its row.
	press(a, keyEnter)

	if got := lineWith(viewText(a), "branch"); !strings.Contains(got, "feat/x") {
		t.Errorf("branch row %q after coming back, want feat/x", got)
	}

	pressRun(a, keyEsc, keyEsc)

	if a.picker.open != nil {
		t.Error("esc on the list left the picker open")
	}
}

func TestPickerStaleAnswersAreDropped(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		dir := committedRepo(t)
		a, _ := pickerApp(t, dir)

		first := press(a, keyLeader, keyRune('n'))
		second := press(a, keyLeader, keyRune('n'))

		a.Update(first())

		if a.picker.open != nil {
			t.Fatal("the answer to an earlier leader n opened the picker")
		}

		a.Update(second())

		if a.picker.open == nil {
			t.Fatal("the answer to the latest leader n did not open the picker")
		}
	})

	t.Run("open after the focus moved", func(t *testing.T) {
		dir := committedRepo(t)
		a, _ := pickerApp(t, dir)

		cmd := press(a, keyLeader, keyRune('n'))
		openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: dir, Split: core.SplitVertical, Area: a.bodyArea()})
		a.Update(cmd())

		if a.picker.open != nil {
			t.Error("the answer opened the picker over another pane")
		}
	})

	t.Run("open under a palette", func(t *testing.T) {
		dir := committedRepo(t)
		a, _ := pickerApp(t, dir)

		cmd := press(a, keyLeader, keyRune('n'))
		press(a, keyLeader, keyRune(':'))
		a.Update(cmd())

		if a.picker.open != nil {
			t.Error("the answer opened the picker over the palette")
		}
	})

	t.Run("created after the picker closed", func(t *testing.T) {
		dir := committedRepo(t)
		a, st := pickerApp(t, dir)

		var logs bytes.Buffer

		a.log = slog.New(slog.NewTextHandler(&logs, nil))

		askPicker(t, a)
		openForm(t, a)
		typeText(a, "feat/x")

		cmd := press(a, keyEnter)
		pressRun(a, keyEsc, keyEsc)

		a.Update(cmd())

		if st.count() != 1 || a.picker.open != nil {
			t.Errorf("starts = %d, picker open = %v, want the late answer dropped", st.count(), a.picker.open != nil)
		}

		// The worktree exists, so the log says where.
		if want := filepath.Join(dir, ".worktrees", "feat-x"); !strings.Contains(logs.String(), want) {
			t.Errorf("log = %q, want it to name %s", logs.String(), want)
		}
	})

	t.Run("created for an earlier picker", func(t *testing.T) {
		dir := committedRepo(t)
		a, st := pickerApp(t, dir)
		askPicker(t, a)
		openForm(t, a)
		typeText(a, "feat/x")

		cmd := press(a, keyEnter)
		pressRun(a, keyEsc, keyEsc)
		askPicker(t, a)

		a.Update(cmd())

		if st.count() != 1 || a.picker.open == nil {
			t.Errorf("starts = %d, picker open = %v, want the new picker untouched", st.count(), a.picker.open != nil)
		}
	})
}

func TestPickerDoesNotOpenOverAModal(t *testing.T) {
	tests := []struct {
		name string
		open func(a *App)
	}{
		{"confirm", func(a *App) {
			a.openConfirm(confirm{action: "Quit", prompt: "Quit?", yes: func(*App) tea.Cmd { return nil }})
		}},
		{"prompt", func(a *App) {
			a.openPrompt(a.store.State().Tab().ID, "", func(*App, string) tea.Cmd { return nil })
		}},
		{"copy mode", func(a *App) { a.enterCopyMode() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := committedRepo(t)
			a, st := pickerApp(t, dir)

			cmd := press(a, keyLeader, keyRune('n'))

			tt.open(a)
			a.Update(cmd())

			if a.picker.open != nil || st.count() != 1 {
				t.Errorf("picker open = %v, starts = %d, want the answer dropped", a.picker.open != nil, st.count())
			}
		})
	}
}

func TestPickerGivesWayToAConfirm(t *testing.T) {
	a, st := pickerApp(t, "/work")
	injectPicker(t, a, sampleRepo, sampleTrees())
	typeText(a, "au")

	a.openConfirm(confirm{action: "Quit", prompt: "Quit?", yes: func(*App) tea.Cmd { return nil }})

	// The confirm swallows every key it does not answer (y and n answer it).
	typeText(a, "xzw")

	if got := a.picker.open.query.Value(); got != "au" {
		t.Errorf("query = %q, want au: a key reached the picker under the confirm", got)
	}

	if a.confirm == nil {
		t.Fatal("the confirm closed on keys it does not answer")
	}

	press(a, keyRune('n'))

	if a.confirm != nil || a.picker.open == nil {
		t.Fatalf("confirm open = %v, picker open = %v, want the picker alone", a.confirm != nil, a.picker.open != nil)
	}

	typeText(a, "t")

	if a.picker.open.query.Value() != "aut" {
		t.Errorf("query = %q, want the picker to have its keys back", a.picker.open.query.Value())
	}

	if st.proc(0).sent() != "" {
		t.Errorf("the pane got %q", st.proc(0).sent())
	}
}

func TestPickerClosesWhenItsTabGoes(t *testing.T) {
	a, st := pickerApp(t, "/work")
	openIn(t, a, core.NewTab{Kind: core.KindShell, Cwd: "/work"}, core.SelectTab{Index: 0})
	injectPicker(t, a, sampleRepo, sampleTrees())
	seq := a.picker.open.seq

	// Its last pane exits: the tab goes and another one shows.
	pane := a.store.State().Tab().Focus
	a.Update(paneExitedMsg{id: pane, p: a.panes[pane]})

	if got := len(a.store.State().Tabs); got != 1 {
		t.Fatalf("tabs = %d, want the picker's tab gone", got)
	}

	if a.picker.open != nil {
		t.Error("the picker is still open once its tab is gone")
	}

	if view := viewText(a); strings.Contains(view, "New agent") {
		t.Errorf("the box is still drawn:\n%s", view)
	}

	// A creation that finishes now has nowhere to open.
	a.Update(pickerCreatedMsg{seq: seq, created: git.Created{Worktree: git.Worktree{Path: "/home/me/code/api/.worktrees/x"}}})

	if st.count() != 1 || len(a.store.State().Tab().Panes) != 1 {
		t.Errorf("starts = %d, panes in the shown tab = %d, want no agent opened there", st.count(), len(a.store.State().Tab().Panes))
	}
}

func TestPickerOwnsInput(t *testing.T) {
	a, st := pickerApp(t, "/work")
	agentsAt(t, a, "/home/me/code/api", 1)

	s := a.store.State()
	first := s.Tab().Panes[0]

	if err := a.dispatch(core.FocusPane{ID: first}); err != nil {
		t.Fatal(err)
	}

	injectPicker(t, a, sampleRepo, sampleTrees())

	typeText(a, "xyz")

	if got := st.proc(0).sent(); got != "" {
		t.Errorf("the pane got %q while the picker was open", got)
	}

	if view := viewText(a); !strings.Contains(view, "\u276f xyz") {
		t.Errorf("query does not show xyz:\n%s", view)
	}

	// The leader is just a key here.
	if cmd := press(a, keyLeader, keyRune('q')); isQuit(cmd) || a.pending {
		t.Error("the leader acted while the picker was open")
	}

	// A paste goes to the query.
	pressRun(a, keyBackspace, keyBackspace, keyBackspace, keyBackspace)
	a.Update(tea.PasteMsg{Content: "auth"})

	if view := viewText(a); !strings.Contains(view, "\u276f auth") || st.proc(0).sent() != "" {
		t.Errorf("paste did not reach the query alone:\n%s", view)
	}

	// The pointer cannot reach the panes under the box.
	r := a.layoutRects()[s.Tab().Panes[1]]
	a.Update(tea.MouseClickMsg{X: r.X + 2, Y: r.Y + 2, Button: tea.MouseLeft})
	a.Update(tea.MouseWheelMsg{X: r.X + 2, Y: r.Y + 2, Button: tea.MouseWheelUp})

	if got := a.store.State().Tab().Focus; got != first {
		t.Errorf("focus = %s after a click, want %s", got, first)
	}
}

func TestPickerClosesWhenTheTabChanges(t *testing.T) {
	a, _ := pickerApp(t, "/work")
	injectPicker(t, a, sampleRepo, sampleTrees())

	openIn(t, a, core.NewTab{Kind: core.KindShell, Cwd: "/work"})
	a.Update(struct{}{})

	if a.picker.open != nil {
		t.Error("the picker stays open in another tab")
	}
}

// railHasKeys opens the rail and gives it the keyboard.
func railHasKeys(t *testing.T, a *App) {
	t.Helper()

	openIn(t, a, core.SetRail{Open: true})
	a.rail.focused = true
	a.Update(struct{}{})

	if !a.rail.focused {
		t.Fatal("setup: the rail has no keyboard")
	}
}

func TestPickerReleasesTheRailsKeyboard(t *testing.T) {
	t.Run("outside a repository", func(t *testing.T) {
		dir := plainDir(t)
		a, _ := pickerApp(t, dir)
		railHasKeys(t, a)

		askPicker(t, a)

		if a.rail.focused {
			t.Error("the rail kept the keyboard after leader n opened an agent")
		}
	})

	t.Run("choosing a worktree", func(t *testing.T) {
		dir := committedRepo(t)
		gitIn(t, "git", dir, "worktree", "add", "-q", "-b", "feat/x", filepath.Join(dir, ".worktrees", "feat-x"))

		a, st := pickerApp(t, dir)
		railHasKeys(t, a)

		askPicker(t, a)

		if a.picker.open == nil || !a.rail.focused {
			t.Fatalf("picker open = %v, rail focused = %v, want the picker open over a rail that still has the keyboard",
				a.picker.open != nil, a.rail.focused)
		}

		pressRun(a, keyDown)
		press(a, keyEnter)

		if st.count() != 2 || a.rail.focused {
			t.Errorf("starts = %d, rail focused = %v, want an agent and the keyboard back with the panes", st.count(), a.rail.focused)
		}
	})

	t.Run("creating a worktree", func(t *testing.T) {
		dir := committedRepo(t)
		a, st := pickerApp(t, dir)
		railHasKeys(t, a)

		askPicker(t, a)
		openForm(t, a)
		typeText(a, "feat/x")
		deliver(t, a, press(a, keyEnter))

		if st.count() != 2 || a.rail.focused {
			t.Errorf("starts = %d, rail focused = %v, want an agent and the keyboard back with the panes", st.count(), a.rail.focused)
		}
	})
}

func TestPickerReleasesTheDrawersKeyboard(t *testing.T) {
	t.Run("a drawer over the new pane closes", func(t *testing.T) {
		dir := plainDir(t)
		a, st := pickerApp(t, dir)
		openIn(t, a, core.OpenReview{Pane: a.store.State().Tab().Focus, Mode: core.ReviewBranch})
		a.drawerTake()

		if !a.drawerHasKeys() {
			t.Fatal("setup: the drawer has no keyboard")
		}

		askPicker(t, a)

		if st.count() != 2 || a.drawer.focused || a.store.State().Tab().Review != nil {
			t.Errorf("starts = %d, drawer focused = %v, drawer open = %v, want an agent and the drawer closed",
				st.count(), a.drawer.focused, a.store.State().Tab().Review != nil)
		}
	})

	t.Run("choosing a worktree", func(t *testing.T) {
		dir := committedRepo(t)
		gitIn(t, "git", dir, "worktree", "add", "-q", "-b", "feat/x", filepath.Join(dir, ".worktrees", "feat-x"))

		a, st := pickerApp(t, dir)
		openIn(t, a, core.OpenReview{Pane: a.store.State().Tab().Focus, Mode: core.ReviewBranch})
		a.drawerTake()

		askPicker(t, a)

		if a.picker.open == nil || !a.drawerHasKeys() {
			t.Fatalf("picker open = %v, drawer has keys = %v, want the picker over a drawer that still has the keyboard",
				a.picker.open != nil, a.drawerHasKeys())
		}

		pressRun(a, keyDown)
		press(a, keyEnter)

		if st.count() != 2 || a.drawer.focused {
			t.Errorf("starts = %d, drawer focused = %v, want an agent and the drawer released", st.count(), a.drawer.focused)
		}
	})
}

func TestPickerCursor(t *testing.T) {
	a, _ := pickerApp(t, "/work")
	injectPicker(t, a, sampleRepo, sampleTrees())

	const width, height = 100, 30

	left, top := (width-maxOverlayWidth)/2, height/4

	cur := a.View().Cursor
	if cur == nil || cur.X != left+4 || cur.Y != top+1 {
		t.Fatalf("list cursor = %+v, want the query's start (%d, %d)", cur, left+4, top+1)
	}

	typeText(a, "ab")

	if cur = a.View().Cursor; cur == nil || cur.X != left+6 {
		t.Errorf("list cursor after typing = %+v, want column %d", cur, left+6)
	}

	pressRun(a, keyEsc)
	injectPicker(t, a, sampleRepo, sampleTrees())
	openSampleForm(t, a)
	typeText(a, "feat")

	// The border, a cell, the marker, the label and its gap come first.
	const valueCol = 1 + 1 + 2 + 6 + 2

	if cur = a.View().Cursor; cur == nil || cur.X != left+valueCol+4 || cur.Y != top+1 {
		t.Fatalf("branch cursor = %+v, want (%d, %d)", cur, left+valueCol+4, top+1)
	}

	// The base is the made-up probe answer, main.
	pressRun(a, keyTab)

	if cur = a.View().Cursor; cur == nil || cur.X != left+valueCol+len("main") || cur.Y != top+2 {
		t.Errorf("base cursor = %+v, want (%d, %d)", cur, left+valueCol+len("main"), top+2)
	}
}

func TestPickerSizes(t *testing.T) {
	for _, page := range []string{"list", "page"} {
		for _, sz := range [][2]int{{100, 30}, {40, 12}, {30, 8}, {5, 3}, {1, 1}, {2, 2}} {
			w, h := sz[0], sz[1]
			t.Run(fmt.Sprintf("%s/%dx%d", page, w, h), func(t *testing.T) {
				a, _ := pickerApp(t, "/work")
				injectPicker(t, a, sampleRepo, sampleTrees())

				if page == "page" {
					openSampleForm(t, a)
					typeText(a, "a-long-branch-name-that-needs-more-room-than-a-small-box-has")
				}

				sized(t, a, w, h)

				lines := strings.Split(a.View().Content, "\n")
				if len(lines) != h {
					t.Fatalf("view has %d lines, want %d", len(lines), h)
				}

				for i, l := range lines {
					if got := lipgloss.Width(l); got != w {
						t.Errorf("line %d width = %d, want %d", i, got, w)
					}
				}
			})
		}
	}
}

func TestOpenPaneInUsesTheGivenCwd(t *testing.T) {
	a, st := lifeAppSized(t, 100, 30)

	if cmd := a.openPaneIn(core.KindAgent, core.SplitVertical, "/srv/x"); cmd == nil {
		t.Fatal("openPaneIn returned no command")
	}

	if st.count() != 2 || st.cmds[1].Dir != "/srv/x" {
		t.Fatalf("starts = %d, want a second pane in /srv/x", st.count())
	}

	s := a.store.State()
	if m := s.Panes[s.Tab().Focus]; m.Kind != core.KindAgent || m.Cwd != "/srv/x" {
		t.Errorf("focused pane = %+v, want an agent in /srv/x", m)
	}
}

// pickerScene is the app a golden draws: the sample worktrees, agents in two
// of them, and either the list or the new-worktree page.
func pickerScene(t *testing.T, theme string, icons, page bool) *App {
	t.Helper()

	a, _ := pickerApp(t, "/work")
	if err := a.dispatch(core.SetTheme{Name: theme}); err != nil {
		t.Fatal(err)
	}

	a.icons = icons

	agentsAt(t, a, "/home/me/code/api/.worktrees/feat-auth", 2)
	agentsAt(t, a, "/home/me/code/api", 1)
	injectPicker(t, a, sampleRepo, sampleTrees())

	if !page {
		pressRun(a, keyDown)

		return a
	}

	press(a, keyUp)
	press(a, keyEnter)
	a.Update(pickerPageMsg{seq: a.picker.open.seq, base: "main", tools: []git.Tool{
		{Name: "JetBrains", Path: ".idea", Hint: `mark ".worktrees" as excluded in the project structure`},
		{Name: "VS Code", Path: ".vscode/settings.json", Hint: `add ".worktrees" to files.watcherExclude`},
	}})
	typeText(a, "feat/new-thing")

	return a
}

func TestPickerGolden(t *testing.T) {
	for _, th := range chromeThemes() {
		for _, icons := range []bool{true, false} {
			for _, c := range []struct {
				name string
				page bool
			}{{"list", false}, {"new-page", true}} {
				t.Run(fmt.Sprintf("%s/icons=%v/%s", th, icons, c.name), func(t *testing.T) {
					a := pickerScene(t, th, icons, c.page)
					golden.RequireEqual(t, []byte(a.View().Content))
				})
			}
		}
	}
}

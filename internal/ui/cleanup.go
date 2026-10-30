package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/github"
)

const (
	// cleanupCheckTimeout bounds finding what a cleanup would remove.
	cleanupCheckTimeout = 10 * time.Second
	// cleanupRunTimeout bounds a cleanup, the wait for the panes included.
	cleanupRunTimeout = 30 * time.Second
	// paneCloseWait is how long a cleanup waits for the processes of the panes it closed.
	paneCloseWait = 5 * time.Second
	// maxCleanupWidth caps the cleanup box.
	maxCleanupWidth = 78
)

const noticeNothingToClean = "nothing to clean up"

// cleanupFound is the worktree that has a merged branch checked out and what a
// cleanup would remove with it. managed is false for the main checkout and for a
// worktree outside MuxReveal's roots: those are found but never removed.
type cleanupFound struct {
	main, path, branch, tip string
	managed                 bool
	changes, ahead          int
	headErr                 error // git.ErrNoCommit: the merged head is not in this repository
}

// cleanupPane is a pane that works in the worktree.
type cleanupPane struct {
	id    core.PaneID
	name  string // the agent's card name; "" for a shell
	agent bool
	state core.AgentState
}

// cleanupTarget is what the cleanup confirm names and enter acts on. The
// worktree, its panes and the branch tip are fixed when the box opens; remove
// and del are the two boxes, which w and b toggle.
type cleanupTarget struct {
	repo        github.Repo
	number      int
	title, head string
	home        string
	found       cleanupFound
	panes       []cleanupPane
	remove, del bool
}

// canDelete reports whether the branch holds nothing the merged head does not:
// its tip is the head or behind it.
func (t cleanupTarget) canDelete() bool {
	return t.found.headErr == nil && t.found.ahead == 0 && t.found.tip != ""
}

// toggle handles w and b, and reports whether key was one. The branch box only
// changes while the branch can go and the worktree box is on.
func (t *cleanupTarget) toggle(key string) bool {
	switch key {
	case "w":
		t.remove = !t.remove
	case "b":
		if t.remove && t.canDelete() {
			t.del = !t.del
		}
	default:
		return false
	}

	return true
}

// path is the worktree as the box and the toasts write it.
func (t cleanupTarget) path() string { return shortPath(t.found.path, t.found.main, t.home) }

// keptReason is why the branch is not offered.
func (t cleanupTarget) keptReason() string {
	if t.found.headErr != nil {
		return fmt.Sprintf("%s's head %s is not in this repository", prNumberText(t.number), shortOID(t.head))
	}

	return fmt.Sprintf("it has %s the PR did not merge", plural(t.found.ahead, "commit"))
}

// shortOID abbreviates a commit id.
func shortOID(oid string) string { return oid[:min(shortOIDLen, len(oid))] }

// plural writes n units, with an s unless n is 1.
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}

	return fmt.Sprintf("%d %ss", n, unit)
}

// cleanupFrom says what asked for a cleanup.
type cleanupFrom uint8

const (
	fromMerge cleanupFrom = iota
	fromCommand
)

// cleanupFoundMsg answers a cleanup check.
type cleanupFoundMsg struct {
	seq   int
	from  cleanupFrom
	repo  github.Repo
	pr    core.PRInfo
	head  string
	found cleanupFound
	ok    bool
	err   error
}

// cleanedMsg answers a cleanup run. kept is only the reason found while running
// (the branch moved); the reasons the box already showed come from keptReason.
type cleanedMsg struct {
	target           cleanupTarget
	closed           int
	removed, deleted bool
	kept             string
	err, delErr      error
}

// findCleanup finds the worktree of root's repository that has branch checked
// out, with what a cleanup of it would remove measured against head, the merged
// commit. ok is false when no worktree has the branch.
func findCleanup(ctx context.Context, root, branch, head string) (found cleanupFound, ok bool, err error) {
	if branch == "" {
		return cleanupFound{}, false, nil
	}

	repo, err := git.Open(ctx, root)
	if err != nil {
		return cleanupFound{}, false, err
	}

	list, err := git.ListWorktrees(ctx, repo)
	if err != nil {
		return cleanupFound{}, false, err
	}

	i := slices.IndexFunc(list, func(w git.Worktree) bool { return w.Branch == branch && !w.Bare && !w.Prunable })
	if i < 0 {
		return cleanupFound{}, false, nil
	}

	wt := list[i]

	main, merr := repo.MainRoot()
	if merr != nil {
		main = repo.CommonDir
	}

	found = cleanupFound{main: main, path: wt.Path, branch: branch}
	if wt.Main || !git.ManagedWorktree(repo, wt.Path) {
		return found, true, nil
	}

	found.managed = true

	if found.changes, err = git.WorktreeChanges(ctx, wt.Path); err != nil {
		return cleanupFound{}, false, err
	}

	found.tip, found.ahead, err = git.BranchAhead(ctx, repo, branch, head)

	switch {
	case errors.Is(err, git.ErrNoCommit):
		found.headErr = err
	case err != nil:
		return cleanupFound{}, false, err
	}

	return found, true, nil
}

// cleanupCheck finds, off the update loop, the worktree of root with pr's head
// branch checked out. Only the newest check's answer counts.
func (a *App) cleanupCheck(from cleanupFrom, repo github.Repo, pr core.PRInfo, root, head string) tea.Cmd {
	a.cleanupSeq++

	var (
		seq    = a.cleanupSeq
		appCtx = a.ctx
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, cleanupCheckTimeout)
		defer cancel()

		found, ok, err := findCleanup(ctx, root, pr.HeadBranch, head)

		return cleanupFoundMsg{seq: seq, from: from, repo: repo, pr: pr, head: head, found: found, ok: ok, err: err}
	}
}

// panesIn lists the panes that work in path or below it, in tab order: those
// opened there and those whose agent reported it.
func (a *App) panesIn(st core.State, path string) []cleanupPane {
	var out []cleanupPane

	for _, tab := range st.Tabs {
		for _, id := range tab.Panes {
			p, ok := st.Panes[id]
			if !ok || (!inDir(path, p.Cwd) && !inDir(path, p.WorkDir())) {
				continue
			}

			cp := cleanupPane{id: id, agent: p.Kind == core.KindAgent, state: p.State}
			if cp.agent {
				cp.name = a.cardName(st, id)
			}

			out = append(out, cp)
		}
	}

	return out
}

// inDir reports whether p is dir or lies inside it.
func inDir(dir, p string) bool {
	if p == "" {
		return false
	}

	p = filepath.Clean(p)
	_, inside := within(dir, p)

	return p == dir || inside
}

// workingAgent is the first agent among panes that is working or blocked.
func workingAgent(panes []cleanupPane) (cleanupPane, bool) {
	i := slices.IndexFunc(panes, func(p cleanupPane) bool {
		return p.agent && (p.state == core.AgentWorking || p.state == core.AgentBlocked)
	})
	if i < 0 {
		return cleanupPane{}, false
	}

	return panes[i], true
}

// onCleanupFound says why a worktree stays, or opens the cleanup confirm. An
// answer that is not the newest check's, or that arrives under a modal, is
// dropped.
func (a *App) onCleanupFound(msg cleanupFoundMsg) tea.Cmd { //nolint:unparam // every handler of the update loop's messages has this shape
	if msg.seq != a.cleanupSeq || a.modalOpen() {
		return nil
	}

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case msg.err != nil:
		a.log.Warn("clean up check", "pr", msg.pr.Number, "err", msg.err)

		if msg.from == fromCommand {
			a.toast(levelError, "clean up: "+gitReason(msg.err))
		}

		return nil
	case !msg.ok:
		return nil
	}

	var (
		f    = msg.found
		st   = a.store.State()
		home = a.getenv("HOME")
		path = shortPath(f.path, f.main, home)
	)

	panes := a.panesIn(st, f.path)
	busy, working := workingAgent(panes)

	switch {
	case !f.managed:
		a.cleanupSay(msg, fmt.Sprintf("%s is outside MuxReveal's worktrees, so it stays", path))
	case f.changes > 0:
		a.cleanupSay(msg, fmt.Sprintf("%s has %s, so it stays", path, plural(f.changes, "uncommitted change")))
	case working:
		a.cleanupSay(msg, fmt.Sprintf("%s is still working in %s", busy.name, path))
	default:
		t := cleanupTarget{
			repo: msg.repo, number: msg.pr.Number, title: msg.pr.Title, head: msg.head, home: home,
			found: f, panes: panes, remove: true,
		}
		t.del = t.canDelete()

		a.openConfirm(confirm{tab: st.Tab().ID, cleanup: &t, yes: func(a *App) tea.Cmd { return a.cleanupRun(t) }})
	}

	return nil
}

// cleanupSay tells why a worktree stays: a merge's toast gains it while that
// toast still shows, a command's answer is a notice of its own.
func (a *App) cleanupSay(msg cleanupFoundMsg, why string) {
	if msg.from == fromCommand {
		a.notice = why

		return
	}

	if a.noticeLevel() == levelSuccess && strings.HasPrefix(a.notice, fmt.Sprintf("merged %s (", prNumberText(msg.pr.Number))) {
		a.toast(levelSuccess, a.notice+"; "+why)
	}
}

// cleanupRun cleans up exactly what t named. On the loop it reads the panes
// again and closes the listed ones that are still open; off the loop it waits
// for their processes, removes the worktree and, when asked, deletes the branch.
func (a *App) cleanupRun(t cleanupTarget) tea.Cmd {
	if !t.remove {
		a.notice = noticeNothingToClean

		return nil
	}

	st := a.store.State()

	// The listed panes are what gets closed, so an agent among them that has since
	// left the worktree and started working stops the run as well.
	busy, ok := workingAgent(a.panesIn(st, t.found.path))
	if !ok {
		busy, ok = workingAgent(a.listedPanes(st, t))
	}

	if ok {
		a.notice = fmt.Sprintf("%s is still working in %s", busy.name, t.path())

		return nil
	}

	var (
		cmds  []tea.Cmd
		waits []<-chan struct{}
	)

	for _, p := range t.panes {
		if _, ok := st.Panes[p.id]; !ok {
			continue
		}

		cmd, done := a.closePaneWait(p.id)
		cmds = append(cmds, cmd)
		waits = append(waits, done)
	}

	appCtx := a.ctx

	cmds = append(cmds, func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, cleanupRunTimeout)
		defer cancel()

		return cleanupExec(ctx, t, waits)
	})

	return tea.Batch(cmds...)
}

// listedPanes is the box's panes that are still open, as the state has them now.
func (a *App) listedPanes(st core.State, t cleanupTarget) []cleanupPane {
	var out []cleanupPane

	for _, p := range t.panes {
		cur, ok := st.Panes[p.id]
		if !ok {
			continue
		}

		p.agent, p.state = cur.Kind == core.KindAgent, cur.State
		if p.agent {
			p.name = a.cardName(st, p.id)
		}

		out = append(out, p)
	}

	return out
}

// errLeftBranch means the worktree is no longer on the branch the box named.
var errLeftBranch = errors.New("ui: worktree left the branch")

// stillOnBranch checks, before anything is removed, that the worktree is still
// on the branch the box named: commits made after a switch or a detach would be
// lost with it.
func stillOnBranch(ctx context.Context, repo git.Repo, f cleanupFound) error {
	list, err := git.ListWorktrees(ctx, repo)
	if err != nil {
		return err
	}

	if !slices.ContainsFunc(list, func(w git.Worktree) bool { return w.Path == f.path && w.Branch == f.branch }) {
		return errLeftBranch
	}

	return nil
}

// cleanupExec removes the worktree and deletes the branch, never forced. The
// branch goes only when its tip is still the one the box showed and is not past
// the merged head.
func cleanupExec(ctx context.Context, t cleanupTarget, waits []<-chan struct{}) cleanedMsg {
	msg := cleanedMsg{target: t, closed: len(waits)}

	waitCtx, cancel := context.WithTimeout(ctx, paneCloseWait)
	defer cancel()

	for _, done := range waits {
		select {
		case <-done:
		case <-waitCtx.Done():
		}
	}

	repo, err := openMain(ctx, t.found.main)
	if err == nil {
		err = stillOnBranch(ctx, repo, t.found)
	}

	if err == nil {
		err = git.RemoveWorktree(ctx, repo, t.found.path)
	}

	if err != nil {
		msg.err = withContext(ctx, err)

		return msg
	}

	msg.removed = true

	if !t.del || !t.canDelete() {
		return msg
	}

	branch := t.found.branch

	tip, ahead, err := git.BranchAhead(ctx, repo, branch, t.head)

	switch {
	case err != nil:
		msg.delErr = withContext(ctx, err)
	case tip != t.found.tip || ahead > 0:
		msg.kept = movedReason
	default:
		switch err := git.DeleteBranch(ctx, repo, branch, tip); {
		case errors.Is(err, git.ErrBranchMoved):
			msg.kept = movedReason
		case err != nil:
			msg.delErr = withContext(ctx, err)
		default:
			msg.deleted = true
		}
	}

	return msg
}

const movedReason = "it moved since you asked"

// openMain opens the repository from its main checkout; a bare repository has
// no work tree to open, so its git dir stands in.
func openMain(ctx context.Context, main string) (git.Repo, error) {
	repo, err := git.Open(ctx, main)
	if errors.Is(err, git.ErrNotRepo) {
		return git.Repo{Root: main, GitDir: main, CommonDir: main}, nil
	}

	return repo, err
}

// withContext reports a context that ended as the reason, since git only says
// it was killed.
func withContext(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil && !errors.Is(err, cerr) {
		return fmt.Errorf("%w: %w", cerr, err)
	}

	return err
}

// onCleaned says what a cleanup did. A cleanup cut short by the app closing says nothing.
func (a *App) onCleaned(msg cleanedMsg) {
	t := msg.target
	path, branch := t.path(), printable(t.found.branch)

	switch {
	case errors.Is(msg.err, context.Canceled):
		return
	case errors.Is(msg.err, context.DeadlineExceeded):
		a.toast(levelError, fmt.Sprintf("clean up %s: timed out", path))

		return
	case errors.Is(msg.err, errLeftBranch):
		a.toast(levelError, fmt.Sprintf("kept %s: it is no longer on %s", path, branch))

		return
	case errors.Is(msg.err, git.ErrWorktreeDirty):
		a.toast(levelError, fmt.Sprintf("kept %s: it has uncommitted changes", path))

		return
	case msg.err != nil:
		a.toast(levelError, fmt.Sprintf("kept %s: %s", path, gitReason(msg.err)))

		return
	}

	text := "removed " + path
	if msg.closed > 0 {
		text += " and closed " + plural(msg.closed, "pane")
	}

	switch {
	case msg.deleted:
		text += "; deleted " + branch
	case errors.Is(msg.delErr, context.DeadlineExceeded):
		text += fmt.Sprintf("; deleting %s failed: timed out", branch)
	case msg.delErr != nil:
		text += fmt.Sprintf("; deleting %s failed: %s", branch, gitReason(msg.delErr))
	case msg.kept != "":
		text += fmt.Sprintf("; kept %s: %s", branch, msg.kept)
	case !t.canDelete():
		text += fmt.Sprintf("; kept %s: %s", branch, t.keptReason())
	}

	a.toast(levelSuccess, text)
}

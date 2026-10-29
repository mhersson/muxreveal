package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/github"
)

const (
	// pushResolveTimeout bounds reading what a push would do; it runs local git only.
	pushResolveTimeout = 15 * time.Second
	// pushTimeout bounds the push itself, which talks to the remote.
	pushTimeout = 60 * time.Second
	// maxPushWidth caps the push confirm's width.
	maxPushWidth = 60

	noticePushNotRepo  = "push: not a git repository"
	noticePushDetached = "push: HEAD is detached"
	unreadableURL      = "(unreadable url)"
)

// errPushChanged means the branch, its head or its push remote moved since the
// confirm opened.
var errPushChanged = errors.New("ui: push target changed")

// pushTarget is what the push confirm names and what enter pushes. It is fixed
// when the confirm opens, whatever the focus or the agent does next.
type pushTarget struct {
	pane         core.PaneID // the pane asked from; its head is probed again after a push
	root         string      // the worktree the push runs in
	project      string
	branch       string
	oid          string // HEAD when asked
	remote       string
	url          string // the remote as shown, see remoteDisplay
	commits      int    // commits the remote lacks
	setsUpstream bool
	short        string // the newest commit's abbreviated id
	subject      string // and its subject, as git has it
}

// pushState says what to do with a resolved push.
type pushState uint8

const (
	pushAsk      pushState = iota // there is something to push: the confirm opens
	pushUpToDate                  // the remote has every commit
	pushNotRepo
	pushDetached
	pushUnborn // the branch has no commits
	pushNoRemote
)

// pushResolvedMsg is the answer of reading what a push of one pane's branch would do.
type pushResolvedMsg struct {
	seq    int
	pane   core.PaneID
	target pushTarget
	state  pushState
	err    error // a failure that is none of the states
}

// pushedMsg is the answer of a push. repoOK says repo is the GitHub repository
// the pushed branch belongs to.
type pushedMsg struct {
	target pushTarget
	res    git.PushResult
	repo   github.Repo
	repoOK bool
	err    error
}

// push is the git.push command: it reads, off the update loop, what pushing the
// focused pane's branch would do, then asks before anything is pushed.
func (a *App) push() tea.Cmd {
	st := a.store.State()
	id := st.Tab().Focus

	m, ok := st.Panes[id]
	if !ok || m.WorkDir() == "" {
		a.notice = noticePushNotRepo

		return nil
	}

	a.pushSeq++

	var (
		seq    = a.pushSeq
		cwd    = m.WorkDir()
		home   = a.getenv("HOME")
		appCtx = a.ctx
		log    = a.log
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, pushResolveTimeout)
		defer cancel()

		t, state, err := resolvePush(ctx, log, id, cwd, home)

		return pushResolvedMsg{seq: seq, pane: id, target: t, state: state, err: err}
	}
}

// resolvePush reads what pushing the branch checked out in cwd would do. err is
// a failure that is none of the states.
func resolvePush(ctx context.Context, log *slog.Logger, pane core.PaneID, cwd, home string) (pushTarget, pushState, error) {
	head, err := probeHeadOf(ctx, log, cwd)

	switch {
	case err != nil:
		return pushTarget{}, pushAsk, err
	case !head.repo:
		return pushTarget{}, pushNotRepo, nil
	case head.detached:
		return pushTarget{}, pushDetached, nil
	}

	t := pushTarget{pane: pane, project: head.project, branch: head.branch, oid: head.oid}
	if head.oid == "" {
		return t, pushUnborn, nil
	}

	r, err := git.Open(ctx, cwd)
	if err != nil {
		return pushTarget{}, pushAsk, err
	}

	t.root = r.Root

	t.remote, err = git.PushRemote(ctx, r, t.branch)

	switch {
	case errors.Is(err, git.ErrNoRemote):
		return t, pushNoRemote, nil
	case err != nil:
		return pushTarget{}, pushAsk, err
	}

	raw, err := git.RemoteURL(ctx, r, t.remote)
	if err != nil {
		return pushTarget{}, pushAsk, err
	}

	t.url = remoteDisplay(raw, home)

	if t.commits, err = git.Unpushed(ctx, r, t.remote); err != nil {
		return pushTarget{}, pushAsk, err
	}

	if t.commits == 0 {
		return t, pushUpToDate, nil
	}

	has, err := git.HasUpstream(ctx, r, t.branch)
	if err != nil {
		return pushTarget{}, pushAsk, err
	}

	t.setsUpstream = !has

	c, err := git.HeadCommit(ctx, r)
	if err != nil {
		return pushTarget{}, pushAsk, err
	}

	t.short, t.subject = c.OID[:min(shortOIDLen, len(c.OID))], c.Subject

	return t, pushAsk, nil
}

// onPushResolved opens the confirm, or shows why there is nothing to ask. An
// answer for an earlier request, a pane that lost focus, or under another modal
// is dropped.
func (a *App) onPushResolved(msg pushResolvedMsg) {
	if msg.seq != a.pushSeq || a.store.State().Tab().Focus != msg.pane ||
		a.overlay != nil || a.confirm != nil || a.prompt != nil || a.copy != nil || a.picker.open != nil {
		return
	}

	t := msg.target

	switch {
	case errors.Is(msg.err, context.Canceled):
	case errors.Is(msg.err, context.DeadlineExceeded):
		a.toast(levelError, "push: timed out reading the repository")
	case msg.err != nil:
		a.toast(levelError, "push: "+gitReason(msg.err))
	case msg.state == pushNotRepo:
		a.notice = noticePushNotRepo
	case msg.state == pushDetached:
		a.notice = noticePushDetached
	case msg.state == pushUnborn:
		a.notice = fmt.Sprintf("push: %s has no commits yet", t.branch)
	case msg.state == pushNoRemote:
		a.notice = fmt.Sprintf("push: %s has no remote to push to", t.branch)
	case msg.state == pushUpToDate:
		a.notice = fmt.Sprintf("%s is up to date on %s", t.branch, t.remote)
	default:
		a.openConfirm(confirm{push: &t, yes: func(a *App) tea.Cmd { return a.pushRun(t) }})
	}
}

// pushRun pushes exactly t, off the update loop, under a timeout derived from the
// app's context. It checks the worktree's branch, head and push remote in the
// same command, so the window between the check and the push is as short as it
// can be.
func (a *App) pushRun(t pushTarget) tea.Cmd {
	appCtx, resolve := a.ctx, a.prs.resolve

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, pushTimeout)
		defer cancel()

		msg := pushedMsg{target: t}

		msg.res, msg.err = runPush(ctx, t)
		if msg.err != nil || resolve == nil {
			return msg
		}

		rctx, rcancel := context.WithTimeout(appCtx, prResolveTimeout)
		defer rcancel()

		// A remote that is not on GitHub resolves to nothing, and nothing is refreshed.
		if repo, err := resolve(rctx, t.root, t.branch); err == nil {
			msg.repo, msg.repoOK = repo, true
		}

		return msg
	}
}

// runPush pushes t's branch from its worktree unless the branch, its head or its
// push remote is not what the confirm named.
func runPush(ctx context.Context, t pushTarget) (git.PushResult, error) {
	r, err := git.Open(ctx, t.root)
	if err != nil {
		return git.PushResult{}, err
	}

	h, err := git.Head(ctx, r)
	if err != nil {
		return git.PushResult{}, err
	}

	if h.Detached || h.Branch != t.branch || h.OID != t.oid {
		return git.PushResult{}, errPushChanged
	}

	remote, err := git.PushRemote(ctx, r, t.branch)
	if err != nil {
		return git.PushResult{}, err
	}

	if remote != t.remote {
		return git.PushResult{}, errPushChanged
	}

	return git.Push(ctx, r, t.branch)
}

// onPushed shows what a push did and, after one that reached the remote, probes
// the pane's head again and asks the poller to refresh the repository.
func (a *App) onPushed(msg pushedMsg) tea.Cmd {
	t := msg.target

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case errors.Is(msg.err, errPushChanged):
		a.notice = fmt.Sprintf("push: %s changed since you asked; nothing pushed", t.branch)

		return nil
	case errors.Is(msg.err, context.DeadlineExceeded):
		a.toast(levelError, fmt.Sprintf("push %s to %s: timed out", t.branch, t.remote))

		return nil
	case msg.err != nil:
		a.toast(levelError, gitReason(msg.err))

		return nil
	case msg.res.UpToDate:
		a.notice = fmt.Sprintf("%s is up to date on %s", t.branch, t.remote)
	default:
		a.toast(levelSuccess, fmt.Sprintf("pushed %s to %s: %s", t.branch, t.remote, commitCount(t.commits)))
	}

	if msg.repoOK && a.prs.src != nil {
		a.prs.src.Refresh(msg.repo)
	}

	st := a.store.State()
	if _, ok := st.Panes[t.pane]; !ok {
		return nil
	}

	a.railEntry(t.pane).head.force = true

	return a.railFire(st, t.pane, probeHead)
}

func commitCount(n int) string {
	if n == 1 {
		return "1 commit"
	}

	return fmt.Sprintf("%d commits", n)
}

// remoteDisplay is a remote URL as the confirm shows it: host/owner/name when
// it names a repository that way; any other URL without its userinfo, query,
// fragment and trailing .git; a local path with ~ for the home directory.
// Userinfo and tokens are never shown.
func remoteDisplay(raw, home string) string {
	if repo, err := github.ParseRemoteURL(raw); err == nil {
		return printable(repo.String())
	}

	if scheme, _, ok := strings.Cut(raw, "://"); ok {
		u, err := url.Parse(raw)
		if err != nil {
			return unreadableURL
		}

		if strings.EqualFold(scheme, "file") {
			return shortPath(u.Path, "", home)
		}

		u.User, u.RawQuery, u.Fragment, u.RawPath = nil, "", "", ""
		u.Path = strings.TrimSuffix(u.Path, ".git")

		return printable(u.String())
	}

	// scp-like, unless a slash comes before the colon: that is a local path.
	if before, after, ok := strings.Cut(raw, ":"); ok && before != "" && !strings.Contains(before, "/") {
		if _, host, found := strings.Cut(before, "@"); found {
			before = host
		}

		return printable(before + ":" + strings.TrimSuffix(after, ".git"))
	}

	return shortPath(raw, "", home)
}

// pushBox draws the push confirm w cells wide: a BorderFocus box titled with the
// branch, what is about to be pushed and where, and the two answers. Text that
// comes from the repository is drawn without control and invisible characters.
func pushBox(t pushTarget, w int, icons bool, st styles) []string {
	inner := w - 2
	border := st.popupBorder

	// row indents its content two cells from the border and fills to the edge.
	row := func(content string) string {
		content = ansi.Truncate(st.popup.Render("  ")+content, inner, "…")
		fill := st.popup.Render(strings.Repeat(" ", inner-ansi.StringWidth(content)))

		return border.Render("│") + content + fill + border.Render("│")
	}

	field := func(name, value string) string { return st.popupMuted.Render(fmt.Sprintf("%-9s", name)) + value }
	hint := func(key, label string) string { return st.popupKey.Render(key) + st.popupMuted.Render(" "+label) }

	title := " Push " + printable(t.branch) + " "
	if icons {
		title = " " + iconPush + " Push " + printable(t.branch) + " "
	}

	// The title starts two cells from the corner: the corner, one dash, then the title.
	title = ansi.Truncate(title, max(inner-1, 0), "")
	top := border.Render("╭─") + st.pushTitle.Render(title) +
		border.Render(strings.Repeat("─", max(inner-1-ansi.StringWidth(title), 0))+"╮")

	upstream := "upstream already set"
	if t.setsUpstream {
		upstream = "sets the upstream"
	}

	return []string{
		top,
		row(""),
		row(field("project", st.popup.Render(printable(t.project)))),
		row(field("branch", st.popup.Render(printable(t.branch)))),
		row(field("remote", st.popup.Render(printable(t.remote))+st.popupMuted.Render("  "+printable(t.url)))),
		row(field("commits", st.popup.Render(commitCount(t.commits))+st.popupMuted.Render(", "+upstream))),
		row(field("newest", st.popupSubtle.Render(printable(t.short+" "+t.subject)))),
		row(""),
		row(hint("enter", "push") + st.popup.Render("    ") + hint("esc", "cancel")),
		border.Render("╰" + strings.Repeat("─", inner) + "╯"),
	}
}

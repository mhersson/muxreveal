package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/github"
)

const (
	// createTimeout bounds opening the pull request.
	createTimeout = 60 * time.Second
	// maxComposeWidth caps the compose box.
	maxComposeWidth = 96
	// maxComposeTitle and maxComposeBody are GitHub's limits.
	maxComposeTitle = 256
	maxComposeBody  = 65536

	noticeComposeNotRepo  = "open a PR: not a git repository"
	noticeComposeDetached = "open a PR: HEAD is detached"
)

// Status rows of the compose box.
const (
	statusComposeTitle    = "enter a title"
	statusComposeOpening  = "opening the pull request…"
	statusComposeChanged  = "%s changed since you opened this; nothing created"
	statusComposeURL      = "%s's push URL changed since you opened this; nothing created"
	statusComposeExists   = "%s already has an open pull request"
	statusComposePushBusy = "%s is already being pushed"
)

// composeField is the field of the compose box that has the keyboard.
type composeField uint8

const (
	composeTitle composeField = iota
	composeBase
	composeDraft
	composeBody
)

func (f composeField) String() string {
	return [...]string{"title", "base", "draft", "body"}[f]
}

// composeTarget is what the compose box opens a pull request for. It is fixed
// when the box opens, whatever the focus or the agent does next.
type composeTarget struct {
	root, branch, oid, remote string
	repo                      github.Repo
	unpushed                  int      // commits the remote lacks
	bases                     []string // the remote's branches, by name
	pane                      core.PaneID
	urls                      []string // the remote's push URLs as git has them, to tell a changed destination; never shown
}

// composeView is the open compose box; it owns every key while open.
type composeView struct {
	t         composeTarget
	title     textinput.Model
	base      int // index into t.bases
	draft     bool
	body      textArea
	source    string // where the body started: a template's path, or "the commits"
	commits   int    // commits of <remote>/<base>..HEAD
	logSeq    int    // the newest request for commits; an older answer is stale
	field     composeField
	status    string
	statusErr bool
	busy      bool
}

// composeResolvedMsg answers reading what a pull request of one pane's branch
// would be.
type composeResolvedMsg struct {
	seq       int
	pane      core.PaneID
	t         composeTarget
	state     pushState
	notGitHub bool
	err       error // a failure that is none of the states
	gitBase   string
	tmpl      *github.Template
}

// composeLogMsg answers reading the commits a base would put in the pull
// request. open is the box being opened, nil for a change of base.
type composeLogMsg struct {
	seq     int
	logSeq  int
	open    *composeView
	tmpl    *github.Template
	commits []github.Commit
	err     error
}

// composePushedMsg answers the checks and the push before creating.
type composePushedMsg struct {
	v   *composeView
	pr  github.NewPR
	err error
}

// composeCreatedMsg answers creating the pull request.
type composeCreatedMsg struct {
	v   *composeView
	pr  github.PR
	err error
}

// composeCommand is pr.open: it reads, off the update loop, what a pull
// request of the branch push would use is made of, then opens the box.
func (a *App) composeCommand() tea.Cmd {
	a.endLeader()

	if a.compose != nil {
		return nil
	}

	if a.actions == nil || a.prs.src == nil || a.prs.resolve == nil {
		a.notice = noticePRsOff

		return nil
	}

	st := a.store.State()
	id := st.Tab().Focus

	cwd := a.pushDir(st)
	if cwd == "" {
		a.notice = noticeComposeNotRepo

		return nil
	}

	a.composeSeq++

	var (
		seq     = a.composeSeq
		home    = a.getenv("HOME")
		appCtx  = a.ctx
		log     = a.log
		resolve = a.prs.resolve
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, pushResolveTimeout)
		defer cancel()

		msg := resolveCompose(ctx, log, resolve, id, cwd, home)
		msg.seq, msg.pane = seq, id

		return msg
	}
}

// resolveCompose reads the branch checked out in cwd, where it would be pushed
// and the GitHub repository that is, the remote's branches and the first pull
// request template.
func resolveCompose(ctx context.Context, log *slog.Logger, resolve func(ctx context.Context, cwd, branch string) (github.Repo, error),
	pane core.PaneID, cwd, home string,
) composeResolvedMsg {
	pt, state, err := resolvePush(ctx, log, pane, cwd, home)
	msg := composeResolvedMsg{state: state, err: err}

	switch state {
	case pushFailed:
		return msg
	case pushNotRepo, pushDetached, pushUnborn, pushNoRemote:
		msg.t.branch = pt.branch

		return msg
	case pushAsk, pushUpToDate, pushNothing:
	}

	t := composeTarget{
		root: pt.root, branch: pt.branch, oid: pt.oid, remote: pt.remote,
		unpushed: pt.commits, pane: pane, urls: pt.rawURLs,
	}

	msg.t = t

	switch t.repo, err = resolve(ctx, t.root, t.branch); {
	case errors.Is(err, github.ErrNotGitHub):
		msg.notGitHub = true

		return msg
	case errors.Is(err, git.ErrNoRemote):
		msg.state = pushNoRemote

		return msg
	case err != nil:
		msg.err = fmt.Errorf("resolve repository: %w", err)

		return msg
	}

	r, err := git.Open(ctx, t.root)
	if err != nil {
		msg.err = err

		return msg
	}

	if t.bases, err = git.RemoteBranches(ctx, r, t.remote); err != nil {
		msg.err = err

		return msg
	}

	// A repository without a usable default is one the poll names or none.
	if def, err := git.DefaultBranch(ctx, r); err == nil {
		msg.gitBase = strings.TrimPrefix(def, t.remote+"/")
	}

	tmpls, err := github.Templates(r.Root)
	if err != nil && log != nil {
		log.Warn("read pull request templates", "err", err)
	}

	if len(tmpls) > 0 {
		msg.tmpl = &tmpls[0]
	}

	msg.t = t

	return msg
}

// composeBlocked reports whether a modal other than the compose box is open.
func (a *App) composeBlocked() bool {
	return a.overlay != nil || a.confirm != nil || a.prompt != nil || a.copy != nil || a.picker.open != nil ||
		a.checks != nil || a.merge != nil || a.prList != nil || a.compose != nil
}

// onComposeResolved shows why there is no box, or goes on to read the commits
// the box starts from. An answer for an earlier request, a pane that lost
// focus, or under a modal is dropped.
func (a *App) onComposeResolved(msg composeResolvedMsg) tea.Cmd {
	if msg.seq != a.composeSeq || a.store.State().Tab().Focus != msg.pane || a.composeBlocked() {
		return nil
	}

	t := msg.t
	branch := printable(t.branch)

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case errors.Is(msg.err, context.DeadlineExceeded):
		a.toast(levelError, "open a PR: timed out reading the repository")

		return nil
	case msg.err != nil:
		a.toast(levelError, "open a PR: "+gitReason(msg.err))

		return nil
	case msg.state == pushNotRepo:
		a.notice = noticeComposeNotRepo

		return nil
	case msg.state == pushDetached:
		a.notice = noticeComposeDetached

		return nil
	case msg.state == pushUnborn:
		a.notice = fmt.Sprintf("open a PR: %s has no commits yet", branch)

		return nil
	case msg.state == pushNoRemote:
		a.notice = fmt.Sprintf("open a PR: %s has no remote to push to", branch)

		return nil
	case msg.notGitHub:
		a.notice = fmt.Sprintf(noticePRNotGitHub, branch)

		return nil
	}

	st := a.store.State()
	key := t.repo.String()

	if pr, ok := st.PRFor(key, t.branch); ok && pr.State == prOpenState {
		a.notice = fmt.Sprintf("%s already has #%d", branch, pr.Number)

		return nil
	}

	def := st.PRs[key].DefaultBranch
	if def == "" {
		def = msg.gitBase
	}

	bases := t.bases
	if def != "" && !slices.Contains(bases, def) {
		bases = append([]string{def}, bases...)
	}

	if len(bases) == 0 {
		a.notice = fmt.Sprintf("open a PR: %s has no branches to open against", printable(t.remote))

		return nil
	}

	t.bases = bases

	v := &composeView{t: t}
	if i := slices.Index(bases, def); i >= 0 {
		v.base = i
	}

	v.title = newComposeTitle()

	return a.composeLog(v, msg.seq, 0, true, msg.tmpl)
}

// newComposeTitle returns the title field. The box places the terminal's cursor.
func newComposeTitle() textinput.Model {
	in := textinput.New()
	in.SetVirtualCursor(false)
	in.KeyMap.Paste.SetEnabled(false)
	in.CharLimit = maxComposeTitle
	in.Focus()

	return in
}

// composeLog reads, off the update loop, the commits the base of v puts in
// the pull request. open is v while it is being opened, else nil.
func (a *App) composeLog(v *composeView, seq, logSeq int, opening bool, tmpl *github.Template) tea.Cmd {
	var (
		appCtx = a.ctx
		t      = v.t
		base   = t.remote + "/" + t.bases[v.base]
		open   *composeView
	)

	if opening {
		open = v
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, pushResolveTimeout)
		defer cancel()

		msg := composeLogMsg{seq: seq, logSeq: logSeq, open: open, tmpl: tmpl}

		r, err := git.Open(ctx, t.root)
		if err != nil {
			msg.err = err

			return msg
		}

		// A base the remote-tracking ref of which is gone has no commits to list.
		cs, err := git.Log(ctx, r, base)
		if err != nil {
			msg.err = err

			return msg
		}

		for _, c := range cs {
			msg.commits = append(msg.commits, github.Commit{Subject: c.Subject, Body: c.Body})
		}

		return msg
	}
}

// onComposeLog opens the box with the commits it starts from, or recounts a
// box already open. An answer for an earlier request is dropped.
func (a *App) onComposeLog(msg composeLogMsg) {
	if errors.Is(msg.err, context.Canceled) {
		return
	}

	if msg.open == nil {
		if v := a.compose; v != nil && msg.logSeq == v.logSeq {
			v.commits = len(msg.commits)
		}

		return
	}

	v := msg.open
	if msg.seq != a.composeSeq || a.store.State().Tab().Focus != v.t.pane || a.composeBlocked() {
		return
	}

	template := ""
	v.source = "the commits"

	if msg.tmpl != nil && strings.TrimSpace(msg.tmpl.Body) != "" {
		template, v.source = msg.tmpl.Body, msg.tmpl.Path
	}

	d := github.Fill(v.t.branch, msg.commits, template)

	v.title.SetValue(d.Title)
	v.title.CursorEnd()
	v.body = newTextArea(d.Body, maxComposeBody)
	v.commits = len(msg.commits)

	a.endLeader()
	a.compose = v
}

// closeCompose closes the box.
func (a *App) closeCompose() { a.compose = nil }

// handleComposeKey answers the open compose box. While a run is in flight every
// key but the leader is dropped.
func (a *App) handleComposeKey(msg tea.KeyPressMsg) tea.Cmd {
	v := a.compose

	if v.busy {
		cmd, _ := a.runLeader(msg)

		return cmd
	}

	v.status, v.statusErr = "", false

	switch msg.String() {
	case "esc":
		a.closeCompose()

		return nil
	case "tab":
		v.focus((v.field + 1) % (composeBody + 1))

		return nil
	case "shift+tab":
		v.focus((v.field + composeBody) % (composeBody + 1))

		return nil
	case "ctrl+s":
		return a.composeSubmit(v)
	}

	switch v.field {
	case composeTitle:
		var cmd tea.Cmd

		v.title, cmd = v.title.Update(msg)

		return cmd
	case composeBase:
		switch msg.String() {
		case "left":
			return a.composeStep(v, -1)
		case "right":
			return a.composeStep(v, 1)
		}
	case composeDraft:
		if msg.String() == "space" {
			v.draft = !v.draft
		}
	case composeBody:
		v.body.Key(msg)
	}

	return nil
}

// focus gives f the keyboard; only the title shows the input's cursor.
func (v *composeView) focus(f composeField) {
	v.field = f

	if f == composeTitle {
		v.title.Focus()
	} else {
		v.title.Blur()
	}
}

// composeStep selects the base delta away, wrapping, and recounts its commits.
func (a *App) composeStep(v *composeView, delta int) tea.Cmd {
	n := len(v.t.bases)
	v.base = ((v.base+delta)%n + n) % n
	v.logSeq++

	return a.composeLog(v, a.composeSeq, v.logSeq, false, nil)
}

// handleComposePaste puts pasted text in the field that has the keyboard.
func (a *App) handleComposePaste(msg tea.PasteMsg) tea.Cmd {
	v := a.compose

	switch {
	case v.busy:
	case v.field == composeTitle:
		var cmd tea.Cmd

		v.title, cmd = v.title.Update(msg)

		return cmd
	case v.field == composeBody:
		v.body.Paste(msg.Content)
	}

	return nil
}

// composeSubmit freezes the box and checks, pushes and creates off the update
// loop. The worktree must still be on the branch and head the box opened on.
func (a *App) composeSubmit(v *composeView) tea.Cmd {
	title := strings.TrimSpace(v.title.Value())
	if title == "" {
		v.status, v.statusErr = statusComposeTitle, true

		return nil
	}

	t := v.t
	if busy, ok := a.pushing[t.root]; ok && t.unpushed > 0 {
		v.status, v.statusErr = fmt.Sprintf(statusComposePushBusy, printable(busy)), true

		return nil
	}

	pr := github.NewPR{Title: title, Body: v.body.Value(), Base: t.bases[v.base], Head: t.branch, Draft: v.draft}
	v.busy = true

	v.status = statusComposeOpening
	if t.unpushed > 0 {
		v.status = fmt.Sprintf("pushing %s to %s…", printable(t.branch), printable(t.remote))

		if a.pushing == nil {
			a.pushing = map[string]string{}
		}

		a.pushing[t.root] = t.branch
	}

	appCtx := a.ctx

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, pushTimeout)
		defer cancel()

		return composePushedMsg{v: v, pr: pr, err: composePush(ctx, t)}
	}
}

// composePush checks that the worktree is still on the branch and head t names
// and pushes the branch when the remote lacks commits. It never forces.
func composePush(ctx context.Context, t composeTarget) error {
	if t.unpushed > 0 {
		_, err := runPush(ctx, pushTarget{root: t.root, branch: t.branch, oid: t.oid, remote: t.remote, rawURLs: t.urls})

		return err
	}

	r, err := git.Open(ctx, t.root)
	if err != nil {
		return err
	}

	h, err := git.Head(ctx, r)
	if err != nil {
		return err
	}

	if h.Detached || h.Branch != t.branch || h.OID != t.oid {
		return errPushChanged
	}

	return nil
}

// fail shows why a run stopped and gives the keys back.
func (v *composeView) fail(err error) {
	branch := printable(v.t.branch)
	v.busy, v.statusErr = false, true

	switch {
	case errors.Is(err, errPushChanged):
		v.status = fmt.Sprintf(statusComposeChanged, branch)
	case errors.Is(err, errPushURLChanged):
		v.status = fmt.Sprintf(statusComposeURL, branch)
	case errors.Is(err, github.ErrPRExists):
		v.status = fmt.Sprintf(statusComposeExists, branch)
	case errors.Is(err, context.DeadlineExceeded):
		v.status = "timed out"
	default:
		v.status = gitReason(err)
	}
}

// onComposePushed goes on to create the pull request once the branch is on the
// remote. A run cut short by the app closing says nothing.
func (a *App) onComposePushed(msg composePushedMsg) tea.Cmd {
	v := msg.v

	if v.t.unpushed > 0 {
		delete(a.pushing, v.t.root)
	}

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case a.compose != v:
		return nil
	case msg.err != nil:
		v.fail(msg.err)

		return nil
	}

	var probe tea.Cmd

	if v.t.unpushed > 0 {
		v.t.unpushed = 0

		if _, ok := a.store.State().Panes[v.t.pane]; ok {
			a.railEntry(v.t.pane).head.force = true
			probe = a.railFire(a.store.State(), v.t.pane, probeHead)
		}
	}

	v.status = statusComposeOpening

	var (
		appCtx  = a.ctx
		actions = a.actions
		repo    = v.t.repo
		pr      = msg.pr
	)

	create := func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, createTimeout)
		defer cancel()

		res, err := actions.CreatePR(ctx, repo, pr)

		return composeCreatedMsg{v: v, pr: res, err: err}
	}

	return tea.Batch(probe, create)
}

// onComposeCreated closes the box on success and asks the poller for the
// repository; a failure stays in the box.
func (a *App) onComposeCreated(msg composeCreatedMsg) {
	v := msg.v

	switch {
	case errors.Is(msg.err, context.Canceled), a.compose != v:
		return
	case msg.err != nil:
		v.fail(msg.err)

		return
	}

	a.closeCompose()
	a.toast(levelSuccess, fmt.Sprintf("opened #%d", msg.pr.Number))

	if a.prs.src != nil {
		a.prs.src.Refresh(v.t.repo)
	}
}

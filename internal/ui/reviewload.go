package ui

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
)

const (
	// reviewLoadTimeout bounds one diff load or full-file read.
	reviewLoadTimeout = 30 * time.Second
	// reviewFlashFor is how long rows a live update added flash.
	reviewFlashFor = 600 * time.Millisecond
	// maxQueuedPaths is how many changed paths a stale drawer remembers before
	// it settles for "everything changed".
	maxQueuedPaths = 1000
)

var (
	errNotRepo  = errors.New(noticeNotRepo)
	errDiffsOff = errors.New("diffs are off: no run directory for temporary indexes")
)

// reviewInfo is what a load learns beyond the diff: what the title, the mode
// tag, the mode row and the info rows show, and the files' hidden flags.
type reviewInfo struct {
	project, branch, base string
	turns                 int
	noSnapshot            bool
	hidden                []bool
}

// reviewOpenMsg answers the repository check of a drawer that is opening.
type reviewOpenMsg struct {
	tab   core.TabID
	pane  core.PaneID
	cwd   string
	focus bool
	repo  git.Repo
	turns int // how many turns the pane's session has
	err   error
}

// reviewLoadedMsg answers one load. gen is the view's epoch when it was asked.
type reviewLoadedMsg struct {
	tab  core.TabID
	gen  int
	res  git.Result
	err  error
	live bool // the answer to a watcher batch, not to a new source
	info reviewInfo
}

// reviewFullMsg answers the read of one file's two sides. gen is the view's
// epoch when it was asked and ids the blobs it read.
type reviewFullMsg struct {
	tab  core.TabID
	gen  int
	path string
	ids  blobIDs
	text fullText
}

// reviewFlashMsg ends the flash of rows a live update added.
type reviewFlashMsg struct{ tab core.TabID }

// nextEpoch hands out an epoch no view has used, so an answer from a view that
// was replaced never matches its successor.
func (d *drawerState) nextEpoch() int {
	d.epochs++

	return d.epochs
}

// reviewedPanes are the panes some tab's drawer reviews.
func reviewedPanes(st core.State) map[core.PaneID]bool {
	var out map[core.PaneID]bool

	for _, t := range st.Tabs {
		if t.Review == nil {
			continue
		}

		if out == nil {
			out = map[core.PaneID]bool{}
		}

		out[t.Review.Pane] = true
	}

	return out
}

// paneSession is the session of an agent pane; a shell has none.
func paneSession(st core.State, id core.PaneID) string {
	if m := st.Panes[id]; m.Kind == core.KindAgent {
		return string(m.Session)
	}

	return ""
}

// repoProject is the main checkout's directory name, or for a bare repository
// the common dir's name without ".git".
func repoProject(r git.Repo) string {
	if root, err := r.MainRoot(); err == nil {
		return filepath.Base(root)
	} else if errors.Is(err, git.ErrBare) {
		return strings.TrimSuffix(filepath.Base(r.CommonDir), ".git")
	}

	return filepath.Base(r.Root)
}

// reviewSync brings the active tab's view in line with core's drawer: it makes
// the view for a new or retargeted drawer, loads it when it has no result or
// the pane's repository or session changed, and runs a load that changes seen
// while the tab was hidden left waiting. Views of tabs without a drawer go.
func (a *App) reviewSync() tea.Cmd {
	st := a.store.State()

	for id, v := range a.drawer.views {
		if t, ok := tabByID(st, id); ok && t.Review != nil {
			continue
		}

		if v.cancel != nil {
			v.cancel()
		}

		delete(a.drawer.views, id)
	}

	tab := st.Tab()

	rv := tab.Review
	if rv == nil {
		return nil
	}

	v := a.drawer.views[tab.ID]
	if v == nil || v.pane != rv.Pane {
		v = a.reviewReset(tab.ID, rv.Pane)
	}

	root, resolved := a.paneRoot(rv.Pane)

	switch {
	case !resolved:
		// A pane that is in no repository, or left its repository, shows the error once.
		if w := a.watches.panes[rv.Pane]; (v.root != "" || w != nil && w.notRepo) && !errors.Is(v.err, errNotRepo) {
			v = a.reviewReset(tab.ID, rv.Pane)
			v.err, v.loaded, v.mode = errNotRepo, true, rv.Mode
			v.session = paneSession(st, rv.Pane)
		}

		return nil
	case v.root != "" && v.root != root:
		v = a.reviewReset(tab.ID, rv.Pane)
	}

	need := !v.loaded && !v.loading

	if v.root != root {
		v.root, need = root, true
	}

	if session := paneSession(st, rv.Pane); session != v.session {
		v.session = session
		need = need || rv.Mode == core.ReviewTurn
	}

	if rv.Mode != v.mode {
		v.mode, need = rv.Mode, true
	}

	if need {
		return a.reviewRetarget(tab.ID, v, 0)
	}

	return a.reviewFlush(true, tab.ID, v)
}

func tabByID(st core.State, id core.TabID) (core.Tab, bool) {
	i := slices.IndexFunc(st.Tabs, func(t core.Tab) bool { return t.ID == id })
	if i < 0 {
		return core.Tab{}, false
	}

	return st.Tabs[i], true
}

// reviewReset replaces the tab's view by an empty one for pane, ending the
// work of the old one.
func (a *App) reviewReset(tab core.TabID, pane core.PaneID) *reviewView {
	if old := a.drawer.views[tab]; old != nil && old.cancel != nil {
		old.cancel()
	}

	delete(a.drawer.views, tab)

	v := a.reviewViewOf(tab)
	v.pane, v.split = pane, a.drawer.cfg.Split()

	return v
}

// reviewRetarget points the view at its mode's source, turn being the turn to
// show (0 is the latest), and loads it. A pane without a session has no turns,
// so a turn view of one shows the branch.
func (a *App) reviewRetarget(tab core.TabID, v *reviewView, turn int) tea.Cmd {
	if v.root == "" {
		return nil
	}

	if v.mode == core.ReviewTurn && v.session == "" {
		v.mode = core.ReviewBranch

		_ = a.dispatch(core.SetReviewMode{Mode: core.ReviewBranch})
	}

	if v.mode == core.ReviewTurn {
		v.src = git.TurnSource{Worktree: v.root, Session: v.session, Turn: turn}
	} else {
		v.src = git.BranchSource{Worktree: v.root}
	}

	v.epoch = a.drawer.nextEpoch()
	v.queued = nil

	return a.reviewStart(tab, v, nil)
}

// reviewJob is what one load needs, copied off the view so the command reads
// no view state.
type reviewJob struct {
	tab     core.TabID
	epoch   int
	src     git.Source
	root    string
	session string
	batch   *git.Batch // nil: a full diff of src
	prev    git.Result // the result batch updates
	keep    reviewInfo // what a load that skips a probe keeps
	eng     *git.Engine
	log     *slog.Logger
}

// reviewStart loads the view's source off the update loop, ending a load still
// running for it. It always answers, with an error when diffs are off.
func (a *App) reviewStart(tab core.TabID, v *reviewView, b *git.Batch) tea.Cmd {
	if v.cancel != nil {
		v.cancel()
	}

	ctx, cancel := context.WithTimeout(a.ctx, reviewLoadTimeout)
	v.cancel, v.loading = cancel, true

	job := reviewJob{
		tab: tab, epoch: v.epoch, src: v.src, root: v.root, session: v.session, batch: b, prev: v.res,
		keep: reviewInfo{project: v.project, branch: v.branch, base: v.base, turns: v.turns},
		eng:  a.engine, log: a.log,
	}

	return func() tea.Msg {
		defer cancel()

		return job.run(ctx)
	}
}

func (j reviewJob) run(ctx context.Context) reviewLoadedMsg {
	msg := reviewLoadedMsg{tab: j.tab, gen: j.epoch, live: j.batch != nil, info: j.keep}

	if j.eng == nil {
		msg.err = errDiffsOff

		return msg
	}

	// What the drawing shows besides the diff is found beside the diff, so a
	// save reaches the screen as soon as the diff is done.
	described := make(chan struct{})

	go func() {
		defer close(described)

		j.describe(ctx, &msg.info)
	}()

	var (
		res git.Result
		err error
	)

	if j.batch != nil {
		res, err = j.eng.Update(ctx, j.prev, j.src, *j.batch)
	} else {
		res, err = j.eng.Diff(ctx, j.src)
	}

	<-described

	switch {
	case errors.Is(err, git.ErrNoSnapshot):
		msg.info.noSnapshot = true
	case err != nil:
		msg.err = err
	default:
		msg.res = res
		msg.info.hidden = hiddenFlags(res.Files)
	}

	return msg
}

// describe fills what the drawing shows besides the diff. The head and the base
// change only with git's own state, so a batch of plain paths keeps them.
func (j reviewJob) describe(ctx context.Context, info *reviewInfo) {
	probe := j.batch == nil || j.batch.Git || j.batch.Overflow || info.project == ""
	if !probe && j.session == "" {
		return
	}

	r, err := git.Open(ctx, j.root)
	if err != nil {
		j.log.Debug("review: open", "root", j.root, "err", err)

		return
	}

	if probe {
		info.project = repoProject(r)

		if h, err := git.Head(ctx, r); err == nil {
			info.branch = h.Branch
			if h.Detached {
				info.branch = h.OID[:min(shortOIDLen, len(h.OID))]
			}
		}

		if s, ok := j.src.(git.BranchSource); ok {
			info.base = s.Base
			if info.base == "" {
				if info.base, err = git.DefaultBranch(ctx, r); err != nil {
					j.log.Debug("review: default branch", "root", j.root, "err", err)
				}
			}
		}
	}

	if j.session == "" {
		return
	}

	info.turns = 0

	if turns, err := git.Turns(ctx, r, j.session); err == nil && len(turns) > 0 {
		info.turns = turns[len(turns)-1].N
	}
}

// onReviewLoaded applies a load's answer unless its source was replaced since.
func (a *App) onReviewLoaded(msg reviewLoadedMsg) tea.Cmd {
	v := a.drawer.views[msg.tab]
	if v == nil || msg.gen != v.epoch {
		return nil
	}

	v.loading, v.cancel = false, nil

	flash := a.reviewApply(v, msg)
	active := a.store.State().Tab().ID == msg.tab

	return tea.Batch(flash, a.reviewFullSync(msg.tab, v), a.reviewFlush(active, msg.tab, v))
}

// reviewApply replaces the view's result. A new source starts at the top with
// no full-file view; a live update keeps the cursor on its hunk and flashes
// the rows it added.
func (a *App) reviewApply(v *reviewView, msg reviewLoadedMsg) tea.Cmd {
	in := msg.info
	live := msg.live && v.loaded && v.err == nil && msg.err == nil
	prev := v.res

	v.project, v.branch, v.base, v.turns, v.noSnapshot = in.project, in.branch, in.base, in.turns, in.noSnapshot
	v.hiddenIn, v.err, v.loaded = in.hidden, msg.err, true

	if live {
		v.forgetGone(msg.res)
	} else {
		v.full, v.fullOn, v.fullHave, v.fullAsk, v.folded = nil, nil, nil, nil, nil
	}

	v.gen++
	v.rebuildFor(msg.res, v.rowsSplit)

	if !live {
		v.cursor, v.top, v.treeSel, v.fresh = 0, 0, 0, nil

		return nil
	}

	v.fresh = freshRows(prev, msg.res, v.rows)
	if len(v.fresh) == 0 {
		return nil
	}

	v.flashUntil = a.clock.Now().Add(reviewFlashFor)
	tab := msg.tab

	return a.tick(reviewFlashFor, func(time.Time) tea.Msg { return reviewFlashMsg{tab: tab} })
}

// forgetGone drops the full-file state of files res no longer holds, or holds
// as binary.
func (v *reviewView) forgetGone(res git.Result) {
	for path := range v.fullOn {
		if i := slices.IndexFunc(res.Files, func(f git.FileDiff) bool { return f.Path == path }); i >= 0 && !res.Files[i].Binary {
			continue
		}

		delete(v.fullOn, path)
		delete(v.full, path)
		delete(v.fullHave, path)
		delete(v.fullAsk, path)
	}
}

// onReviewFlash ends the flash once its time is up; a later flash that moved
// the end is left to its own tick.
func (a *App) onReviewFlash(msg reviewFlashMsg) {
	v := a.drawer.views[msg.tab]
	if v == nil || a.clock.Now().Before(v.flashUntil) {
		return
	}

	v.fresh, v.flashUntil = nil, time.Time{}
}

// mergeBatch adds b to the changes q holds. Past maxQueuedPaths it settles for
// "everything changed".
func mergeBatch(q *git.Batch, b git.Batch) *git.Batch {
	if q == nil {
		c := b

		return &c
	}

	out := git.Batch{Git: q.Git || b.Git, Overflow: q.Overflow || b.Overflow}
	if len(q.Paths)+len(b.Paths) > maxQueuedPaths {
		out.Overflow = true

		return &out
	}

	out.Paths = slices.Compact(slices.Sorted(slices.Values(slices.Concat(q.Paths, b.Paths))))

	return &out
}

// reviewBatch routes a watcher batch to the drawer of every tab that reviews
// the root. The active tab's recomputes at once; a hidden tab's waits for its
// tab to show.
func (a *App) reviewBatch(root string, b git.Batch) tea.Cmd {
	if len(a.drawer.views) == 0 {
		return nil
	}

	active := a.store.State().Tab().ID

	var cmds []tea.Cmd

	for id, v := range a.drawer.views {
		if v.root != root {
			continue
		}

		v.queued = mergeBatch(v.queued, b)
		cmds = append(cmds, a.reviewFlush(id == active, id, v))
	}

	return tea.Batch(cmds...)
}

// reviewHook recomputes the active tab's drawer when a hook event arrives for
// its pane and no watch is held: the event stands in for the file changes.
func (a *App) reviewHook(pane core.PaneID) tea.Cmd {
	tab := a.store.State().Tab()

	v := a.drawer.views[tab.ID]
	if tab.Review == nil || tab.Review.Pane != pane || v == nil || v.root == "" {
		return nil
	}

	v.queued = mergeBatch(v.queued, git.Batch{Overflow: true})

	return a.reviewFlush(true, tab.ID, v)
}

// reviewFlush starts the load the changes in v.queued call for. A load already
// running keeps them for its answer; a hidden tab keeps them until it shows. A
// past turn does not change with the files.
func (a *App) reviewFlush(active bool, tab core.TabID, v *reviewView) tea.Cmd {
	if !active || v.queued == nil || v.loading || !v.loaded {
		return nil
	}

	b := *v.queued
	v.queued = nil

	if v.err == nil && !v.res.Live && !v.noSnapshot {
		return nil
	}

	return a.reviewStart(tab, v, &b)
}

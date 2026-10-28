package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/ui/diffview"
)

const (
	// slideFor is how long the drawer takes to slide in or out.
	slideFor = 120 * time.Millisecond
	// drawerMinW is the narrowest the drawer is, unless the body is narrower.
	drawerMinW = 60
	// splitMinCode is the least code columns each side of a side by side row needs.
	splitMinCode = 24
	// treeMinInner is the inner width from which the tree always shows.
	treeMinInner = 80
	// hintsMinInner is the inner height from which the key hints show.
	hintsMinInner = 12
	// reviewLoadTimeout bounds one diff load or full-file read.
	reviewLoadTimeout = 30 * time.Second
	// reviewOpenTimeout bounds the repository check of a drawer that is opening.
	reviewOpenTimeout = 5 * time.Second
	// reviewFlashFor is how long rows a live update added flash.
	reviewFlashFor = 600 * time.Millisecond
	// maxQueuedPaths is how many changed paths a stale drawer remembers before
	// it settles for "everything changed".
	maxQueuedPaths = 1000
)

// The drawer's notices.
const (
	noticeNoDrawer   = "no review drawer is open"
	noticeNoTurns    = "no turns: this pane has no agent session"
	noticeNotRepo    = "not a git repository"
	noticeBinaryFull = "a binary file has no full view"
	noticeFirstTurn  = "turn 1 is the first turn"
)

var (
	errNotRepo  = errors.New(noticeNotRepo)
	errDiffsOff = errors.New("diffs are off: no run directory for temporary indexes")
)

// reviewView is one tab's drawer content: the loaded diff, its rows and where
// the cursor is. Only the update loop touches it.
type reviewView struct {
	pane     core.PaneID     // the pane reviewed
	mode     core.ReviewMode // the mode the view follows, as core holds it
	src      git.Source
	root     string
	session  string // the reviewed agent's session; "" for none
	loaded   bool   // an answer, a result or an error, has been applied
	loading  bool   // a load is in flight
	epoch    int    // names the source in force; every answer carries the one it was asked for
	cancel   context.CancelFunc
	queued   *git.Batch // changes seen since the last load started
	hiddenIn []bool     // per file of res: handed over with it, so derived scans no text

	// The files the operator wants shown whole, the blobs res names for them
	// that full holds, and the blobs being read.
	fullOn   map[string]bool
	fullHave map[string]blobIDs
	fullAsk  map[string]blobIDs

	res         git.Result
	gen         int // bumps whenever res is replaced; the caches follow it
	rows        []reviewRow
	cursor, top int
	split       bool // side by side is wanted; rowsSplit says whether the rows are laid out so
	treeOpen    bool // a drawer too narrow for the tree shows it
	full        map[string]fullText
	folded      map[string]bool
	flashUntil  time.Time
	fresh       map[int]bool
	search      *string // the open search prompt's text; nil when closed
	last        string  // the last search
	err         error

	// What the title, the mode tag, the mode row and the info rows show and
	// git.Result does not carry.
	project, branch string
	base            string // the base the branch is compared with, resolved
	turns           int    // how many turns the session has
	noSnapshot      bool   // the turn shown has no snapshot

	rowsSplit bool // rows hold side by side pairs
	rowsRev   int  // bumps whenever rows are replaced; rebuild is the only place that does
	treeSel   int  // the file selected while the tree has the keyboard

	cache reviewCache
}

// reviewCache holds what is derived from a result once per generation. The
// fields below the spans follow their own keys as well.
type reviewCache struct {
	ok     bool
	gen    int
	files  int
	tree   []treeItem
	hidden []bool // per file: its paths or text carry a hidden character
	digits int    // digits of the highest line number in the hunks
	spans  map[spanKey][][]diffview.Span

	// The tree column's width at one inner width, the layout for one set of
	// inputs, and the rows last searched for chunks to tokenise.
	treeW, treeInner int
	treeOK           bool
	lay              drawerLayout
	layKey           layoutKey
	layOK            bool
	scan             scanKey
	scanned          bool
}

// drawerState is the drawer's UI state outside the views.
type drawerState struct {
	focused, tree bool // the drawer has the keyboard; within it, the tree has
	slideFrom     time.Time
	closing       bool
	views         map[core.TabID]*reviewView
	hl            diffview.Highlighter
	dv            *diffview.Theme // the diff view's theme for dvName
	dvName        string
	dvFlash       *diffview.Theme // the same on the flash ground
	cfg           core.DiffConfig // [diff]: the layout and mode a new drawer starts in
	epochs        int             // the last epoch handed out; unique across views
	tokenised     int             // lines tokenised by the last highlighting pass
	layouts       int             // layouts computed rather than reused; tests read it
	scans         int             // searches of the visible rows for chunks to tokenise; tests read it
}

// reviewViewOf returns the tab's view, creating it.
func (a *App) reviewViewOf(id core.TabID) *reviewView {
	if a.drawer.views == nil {
		a.drawer.views = map[core.TabID]*reviewView{}
	}

	v, ok := a.drawer.views[id]
	if !ok {
		v = &reviewView{}
		a.drawer.views[id] = v
	}

	return v
}

// derived is the view's cache, rebuilt when the result changed.
func (v *reviewView) derived() *reviewCache {
	c := &v.cache
	if c.ok && c.gen == v.gen && c.files == len(v.res.Files) {
		return c
	}

	*c = reviewCache{ok: true, gen: v.gen, files: len(v.res.Files), spans: map[spanKey][][]diffview.Span{}}
	c.tree = treeItems(v.res.Files)

	c.hidden = v.hiddenIn
	if len(c.hidden) != len(v.res.Files) {
		c.hidden = hiddenFlags(v.res.Files)
	}

	hi := 0

	for _, f := range v.res.Files {
		for _, h := range f.Hunks {
			hi = max(hi, h.OldStart+h.OldLines, h.NewStart+h.NewLines)
		}
	}

	c.digits = numDigits(hi)

	return c
}

// treeWidthAt is the tree column's width at an inner width. It walks every
// tree item, so it is computed once per width and generation.
func (c *reviewCache) treeWidthAt(files []git.FileDiff, inner int) int {
	if !c.treeOK || c.treeInner != inner {
		c.treeW, c.treeInner, c.treeOK = treeWidth(c.tree, files, c.hidden, inner), inner, true
	}

	return c.treeW
}

func numDigits(n int) int {
	d := 1
	for ; n >= 10; n /= 10 {
		d++
	}

	return d
}

// rebuild lays the rows out again, for side by side or not, and keeps the
// cursor on what it was on.
func (v *reviewView) rebuild(split bool) { v.rebuildFor(v.res, split) }

// rebuildFor lays res out in place of the current result and keeps the cursor
// on what it was on. It is the only place that replaces the rows.
func (v *reviewView) rebuildFor(res git.Result, split bool) {
	anchor := anchorAt(v.res, v.rows, v.cursor)

	v.res = res
	v.rowsSplit = split
	v.rowsRev++
	v.rows = buildRows(res, rowOpts{split: split, full: v.full, folded: v.folded})
	v.cursor = restoreAnchor(res, v.rows, anchor)
}

// hiddenFlags says per file whether its paths or diff text hold a hidden character.
func hiddenFlags(files []git.FileDiff) []bool {
	out := make([]bool, len(files))
	for i, f := range files {
		out[i] = fileHidden(f)
	}

	return out
}

// easeOutCubic maps the slide's progress to how far the drawer has moved.
func easeOutCubic(t float64) float64 {
	u := 1 - t

	return 1 - u*u*u
}

// slideEase is how far along the slide is, 0 to 1, eased; 1 when none runs.
func (a *App) slideEase() float64 {
	if a.drawer.slideFrom.IsZero() {
		return 1
	}

	t := float64(a.clock.Now().Sub(a.drawer.slideFrom)) / float64(slideFor)

	return easeOutCubic(min(max(t, 0), 1))
}

// drawerFull is the active tab's drawer once it has slid in: the right half of
// the body, at least 60 columns, full height. ok is false with no drawer open
// or a body too small for a frame.
func (a *App) drawerFull(st core.State) (core.Rect, bool) {
	if st.Tab().Review == nil {
		return core.Rect{}, false
	}

	body := a.bodyAreaIn(st.Rail)
	if body.W == 0 {
		return core.Rect{}, false
	}

	w := max(body.W-body.W/2, min(body.W, drawerMinW))

	return core.Rect{X: body.X + body.W - w, Y: body.Y, W: w, H: body.H}, true
}

// drawerRect is the part of the drawer on screen: all of it when it has slid
// in, and less while it slides, from the right edge.
func (a *App) drawerRect(st core.State) (core.Rect, bool) {
	full, ok := a.drawerFull(st)
	if !ok {
		return core.Rect{}, false
	}

	frac := a.slideEase()
	if a.drawer.closing {
		frac = 1 - frac
	}

	shown := int(math.Round(frac * float64(full.W)))
	if shown <= 0 {
		return core.Rect{}, false
	}

	full.X += full.W - shown
	full.W = shown

	return full, true
}

// drawerKeys reports whether the drawer is on screen and has the keyboard.
func (a *App) drawerKeys(st core.State) bool {
	if !a.drawer.focused {
		return false
	}

	_, ok := a.drawerRect(st)

	return ok
}

// blobIDs names the two sides of a file in the object database; a full-file
// text is current while res still names the same pair for the file.
type blobIDs struct{ old, new string }

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

// openReview opens the active tab's drawer on a pane of that tab. Whether the
// pane is in a repository, and whether it has turns, are found off the update
// loop; the drawer opens when they answer, in turn mode when the pane has turns
// and the config allows it, else in branch mode.
func (a *App) openReview(pane core.PaneID, focus bool) tea.Cmd {
	st := a.store.State()
	tab := st.Tab()

	m, ok := st.Panes[pane]
	if !ok || !slices.Contains(tab.Panes, pane) {
		return nil
	}

	if m.Cwd == "" {
		a.notice = noticeNotRepo

		return nil
	}

	cwd, session, tabID := m.Cwd, paneSession(st, pane), tab.ID

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, reviewOpenTimeout)
		defer cancel()

		msg := reviewOpenMsg{tab: tabID, pane: pane, cwd: cwd, focus: focus}

		if msg.repo, msg.err = git.Open(ctx, cwd); msg.err == nil && session != "" {
			if turns, err := git.Turns(ctx, msg.repo, session); err == nil {
				msg.turns = len(turns)
			}
		}

		return msg
	}
}

// onReviewOpen opens the drawer once the repository check answered. An answer
// for another tab, or a pane that moved or went, is dropped.
func (a *App) onReviewOpen(msg reviewOpenMsg) tea.Cmd {
	st := a.store.State()

	m, ok := st.Panes[msg.pane]
	if !ok || st.Tab().ID != msg.tab || m.Cwd != msg.cwd || !slices.Contains(st.Tab().Panes, msg.pane) {
		return nil
	}

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case errors.Is(msg.err, git.ErrNotRepo):
		a.notice = noticeNotRepo

		return nil
	case msg.err != nil:
		a.log.Warn("review: open", "pane", msg.pane, "err", msg.err)
		a.notice = "review: " + msg.err.Error()

		return nil
	}

	mode := core.ReviewBranch
	if msg.turns > 0 && !a.drawer.cfg.BranchFirst() {
		mode = core.ReviewTurn
	}

	if err := a.dispatch(core.OpenReview{Pane: msg.pane, Mode: mode}); err != nil {
		return nil
	}

	if msg.focus {
		a.railRelease()
		a.drawer.focused, a.drawer.tree = true, false
	}

	// The check found the pane's repository: hand it to the watch bookkeeping
	// so the drawer need not resolve it again.
	w := a.watches.panes[msg.pane]
	if w == nil {
		w = &paneWatch{}
		a.watches.panes[msg.pane] = w
	}

	if w.cwd == msg.cwd && w.want == msg.repo.Root {
		return nil
	}

	w.cwd = msg.cwd

	return a.onPaneRepo(paneRepoMsg{id: msg.pane, cwd: msg.cwd, repo: msg.repo})
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

// reviewActive returns the active tab and its view; with no drawer open it
// shows the notice and reports false.
func (a *App) reviewActive() (core.Tab, *reviewView, bool) {
	tab := a.store.State().Tab()
	if tab.Review == nil {
		a.notice = noticeNoDrawer

		return core.Tab{}, nil, false
	}

	return tab, a.reviewViewOf(tab.ID), true
}

// setReviewMode switches the open drawer to branch or turn mode and loads it;
// turn mode starts on the latest turn.
func (a *App) setReviewMode(mode core.ReviewMode) tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	session := paneSession(a.store.State(), tab.Review.Pane)
	if mode == core.ReviewTurn && session == "" {
		a.notice = noticeNoTurns

		return nil
	}

	if err := a.dispatch(core.SetReviewMode{Mode: mode}); err != nil {
		return nil
	}

	v.mode, v.session = mode, session

	return a.reviewRetarget(tab.ID, v, 0)
}

// stepReviewTurn shows the previous (delta -1) or next (1) turn. In branch
// mode it enters turn mode on the latest turn instead.
func (a *App) stepReviewTurn(delta int) tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	ts, isTurn := v.src.(git.TurnSource)
	if tab.Review.Mode != core.ReviewTurn || !isTurn {
		return a.setReviewMode(core.ReviewTurn)
	}

	latest := v.turns
	if latest == 0 {
		return nil
	}

	cur := ts.Turn
	if cur == 0 {
		cur = latest
	}

	next := cur + delta

	switch {
	case next < 1:
		a.notice = noticeFirstTurn

		return nil
	case next > latest:
		a.notice = fmt.Sprintf("turn %d is the latest turn", latest)

		return nil
	case next == latest:
		next = 0
	}

	return a.reviewRetarget(tab.ID, v, next)
}

// toggleReviewFull shows the file under the cursor whole, or back as hunks.
func (a *App) toggleReviewFull() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok || v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}

	f := v.res.Files[v.rows[v.cursor].file]
	if f.Binary {
		a.notice = noticeBinaryFull

		return nil
	}

	if v.fullOn[f.Path] {
		delete(v.fullOn, f.Path)
		delete(v.fullHave, f.Path)
		delete(v.fullAsk, f.Path)

		if _, shown := v.full[f.Path]; shown {
			delete(v.full, f.Path)
			v.gen++
			v.rebuild(v.rowsSplit)
		}

		return nil
	}

	if v.fullOn == nil {
		v.fullOn = map[string]bool{}
	}

	v.fullOn[f.Path] = true

	return a.reviewFullSync(tab.ID, v)
}

// reviewFullSync reads the two sides of every file the operator wants shown
// whole whose blobs differ from the ones the view holds or is reading.
func (a *App) reviewFullSync(tab core.TabID, v *reviewView) tea.Cmd {
	if len(v.fullOn) == 0 || a.engine == nil {
		return nil
	}

	var cmds []tea.Cmd

	for path := range v.fullOn {
		i := slices.IndexFunc(v.res.Files, func(f git.FileDiff) bool { return f.Path == path })
		if i < 0 {
			continue
		}

		f := v.res.Files[i]
		ids := blobIDs{f.OldOID, f.NewOID}

		if have, ok := v.fullHave[path]; ok && have == ids {
			continue
		}

		if ask, ok := v.fullAsk[path]; ok && ask == ids {
			continue
		}

		if v.fullAsk == nil {
			v.fullAsk = map[string]blobIDs{}
		}

		v.fullAsk[path] = ids
		cmds = append(cmds, a.reviewFullCmd(tab, v, f, ids))
	}

	return tea.Batch(cmds...)
}

func (a *App) reviewFullCmd(tab core.TabID, v *reviewView, f git.FileDiff, ids blobIDs) tea.Cmd {
	eng, root, epoch := a.engine, v.root, v.epoch

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, reviewLoadTimeout)
		defer cancel()

		old, nw, err := eng.FileText(ctx, root, f)

		return reviewFullMsg{tab: tab, gen: epoch, path: f.Path, ids: ids, text: fullText{old: old, new: nw, err: err}}
	}
}

// onReviewFull shows a file whole once its text arrived. The text is dropped
// when the source changed since, or the operator turned the full view off; when
// the file changed under the read, the text is read again.
func (a *App) onReviewFull(msg reviewFullMsg) tea.Cmd {
	v := a.drawer.views[msg.tab]
	if v == nil || msg.gen != v.epoch {
		return nil
	}

	if ask, ok := v.fullAsk[msg.path]; ok && ask == msg.ids {
		delete(v.fullAsk, msg.path)
	}

	if !v.fullOn[msg.path] {
		return nil
	}

	i := slices.IndexFunc(v.res.Files, func(f git.FileDiff) bool { return f.Path == msg.path })
	if i < 0 || (blobIDs{v.res.Files[i].OldOID, v.res.Files[i].NewOID}) != msg.ids {
		return a.reviewFullSync(msg.tab, v)
	}

	if v.full == nil {
		v.full = map[string]fullText{}
	}

	if v.fullHave == nil {
		v.fullHave = map[string]blobIDs{}
	}

	v.full[msg.path], v.fullHave[msg.path] = msg.text, msg.ids
	v.gen++
	v.rebuild(v.rowsSplit)

	return nil
}

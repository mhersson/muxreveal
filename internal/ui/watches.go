package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
)

// watchResolveTimeout bounds finding the repository of a pane's cwd.
const watchResolveTimeout = 2 * time.Second

// paneRepoMsg is the answer of resolving the repository of a pane's cwd.
type paneRepoMsg struct {
	id   core.PaneID
	cwd  string
	repo git.Repo
	err  error
}

// watchAcquiredMsg carries the reference a finished Acquire took.
type watchAcquiredMsg struct {
	id      core.PaneID
	repo    git.Repo
	release func()
	err     error
}

// watchMsg carries one batch from the watch set.
type watchMsg struct{ git.RootBatch }

// paneWatch is one agent pane's share of a watch.
type paneWatch struct {
	cwd     string // the cwd the latest resolution was started for
	want    string // the resolved root of cwd; "" outside a repository or until resolved
	root    string // the root release holds a reference on
	release func()
	failed  bool // the latest resolution failed; a hook retries it
}

type watchState struct {
	set    *git.WatchSet
	open   func(context.Context, string) (git.Repo, error)
	panes  map[core.PaneID]*paneWatch
	failed map[string]bool // roots whose failure was already shown
	holds  holds
}

// holds tracks every reference taken, so App.Close frees those whose message
// never reached the update loop.
type holds struct {
	mu     sync.Mutex
	closed bool
	next   int
	refs   map[int]func()
}

// add returns a release that frees rel once and forgets it. After closeAll it
// frees rel at once and returns a no-op.
func (h *holds) add(rel func()) func() {
	h.mu.Lock()

	if h.closed {
		h.mu.Unlock()
		rel()

		return func() {}
	}

	if h.refs == nil {
		h.refs = map[int]func(){}
	}

	n := h.next
	h.next++
	h.refs[n] = rel
	h.mu.Unlock()

	return func() {
		h.mu.Lock()
		r := h.refs[n]
		delete(h.refs, n)
		h.mu.Unlock()

		if r != nil {
			r()
		}
	}
}

func (h *holds) closeAll() {
	h.mu.Lock()
	h.closed = true
	refs := h.refs
	h.refs = nil
	h.mu.Unlock()

	for _, r := range refs {
		r()
	}
}

// paneRoot returns the worktree root resolved for a pane's cwd.
func (a *App) paneRoot(id core.PaneID) (string, bool) {
	w := a.watches.panes[id]
	if w == nil || w.want == "" {
		return "", false
	}

	return w.want, true
}

// waitWatch reads the next watch batch, or returns nothing once the set's
// channel closes or the app's context ends. The update loop re-arms it.
func (a *App) waitWatch() tea.Cmd {
	if a.watches.set == nil {
		return nil
	}

	ch := a.watches.set.Batches()

	return func() tea.Msg {
		select {
		case b, ok := <-ch:
			if !ok {
				return nil
			}

			return watchMsg{RootBatch: b}
		case <-a.ctx.Done():
			return nil
		}
	}
}

// releaseAsync frees a reference off the update loop; releasing stops a
// watcher, which waits for its goroutines.
func (a *App) releaseAsync(rel func()) {
	if rel != nil {
		a.closing.Go(rel)
	}
}

func (w *paneWatch) clear(a *App) {
	a.releaseAsync(w.release)
	w.release, w.root, w.want = nil, "", ""
}

// watchSync reconciles the held watches with the agent panes: it starts
// resolving a pane whose cwd changed and releases those of panes that closed
// or stopped being agents.
func (a *App) watchSync() tea.Cmd {
	if a.watches.set == nil {
		return nil
	}

	st := a.store.State()

	for id, w := range a.watches.panes {
		if m, ok := st.Panes[id]; !ok || m.Kind != core.KindAgent {
			w.clear(a)
			delete(a.watches.panes, id)
		}
	}

	var cmds []tea.Cmd

	for _, id := range st.AgentPanes() {
		cwd := st.Panes[id].Cwd
		if cwd == "" {
			continue
		}

		w := a.watches.panes[id]
		if w == nil {
			w = &paneWatch{}
			a.watches.panes[id] = w
		}

		if w.cwd == cwd {
			continue
		}

		w.cwd = cwd
		cmds = append(cmds, a.resolveCmd(id, cwd))
	}

	return tea.Batch(cmds...)
}

func (a *App) resolveCmd(id core.PaneID, cwd string) tea.Cmd {
	open := a.watches.open

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, watchResolveTimeout)
		defer cancel()

		r, err := open(ctx, cwd)

		return paneRepoMsg{id: id, cwd: cwd, repo: r, err: err}
	}
}

// onPaneRepo starts holding the watch of the resolved root. An answer for a
// pane that closed, stopped being an agent or moved on to another cwd is dropped.
func (a *App) onPaneRepo(msg paneRepoMsg) tea.Cmd {
	w := a.watches.panes[msg.id]
	if w == nil || w.cwd != msg.cwd {
		return nil
	}

	switch {
	case errors.Is(msg.err, git.ErrNotRepo), msg.err != nil && !usableDir(msg.cwd):
		w.clear(a)

		return nil
	case msg.err != nil:
		a.log.Warn("resolve watch repo", "pane", msg.id, "err", msg.err)
		w.clear(a)

		w.failed = true

		a.noticeOnce("project "+filepath.Base(msg.cwd), filepath.Base(msg.cwd), msg.err)

		return nil
	}

	w.failed = false

	w.want = msg.repo.Root
	if w.root == msg.repo.Root {
		return nil
	}

	set, r, id := a.watches.set, msg.repo, msg.id

	return func() tea.Msg {
		rel, err := set.Acquire(a.ctx, r)
		if err != nil {
			return watchAcquiredMsg{id: id, repo: r, err: err}
		}

		return watchAcquiredMsg{id: id, repo: r, release: a.watches.holds.add(rel)}
	}
}

// onWatchAcquired keeps the new reference for its pane. The Acquire error is
// the one source of the failure notice: the set may report one root several
// times and drops reports when its channel is full.
func (a *App) onWatchAcquired(msg watchAcquiredMsg) {
	if msg.err != nil {
		a.noticeWatchFailed(msg.repo, msg.err)

		return
	}

	delete(a.watches.failed, "root "+msg.repo.Root)

	w := a.watches.panes[msg.id]
	if w == nil || w.want != msg.repo.Root || w.root == msg.repo.Root {
		a.releaseAsync(msg.release)

		return
	}

	a.releaseAsync(w.release)
	w.release, w.root = msg.release, msg.repo.Root
}

func (a *App) noticeWatchFailed(r git.Repo, err error) {
	a.log.Warn("watch failed", "root", r.Root, "err", err)

	project := filepath.Base(r.Root)
	if root, merr := r.MainRoot(); merr == nil {
		project = filepath.Base(root)
	} else if errors.Is(merr, git.ErrBare) {
		project = strings.TrimSuffix(filepath.Base(r.CommonDir), ".git")
	}

	a.noticeOnce("root "+r.Root, project, err)
}

// noticeOnce shows the failure notice unless key was shown before.
func (a *App) noticeOnce(key, project string, err error) {
	if a.watches.failed[key] || errors.Is(err, context.Canceled) {
		return
	}

	a.watches.failed[key] = true
	a.notice = fmt.Sprintf("watching %s failed: %v", project, err)
}

// watchRetry makes the next sync resolve a pane whose last resolution failed.
func (a *App) watchRetry(id core.PaneID) {
	if w := a.watches.panes[id]; w != nil && w.failed {
		w.cwd = ""
	}
}

// onWatch re-probes the rail for every agent in the batch's root.
func (a *App) onWatch(msg watchMsg) tea.Cmd {
	if msg.Err != nil {
		a.log.Debug("watch error", "root", msg.Root, "err", msg.Err)

		return nil
	}

	b := msg.Batch
	if !b.Git && !b.Overflow && len(b.Paths) == 0 {
		return nil
	}

	st := a.store.State()

	var cmds []tea.Cmd

	for _, id := range st.AgentPanes() {
		if w := a.watches.panes[id]; w == nil || w.root != msg.Root {
			continue
		}

		// A head change shows at once; its totals follow in onRailHead. Other
		// changes keep the stats interval.
		if b.Git || b.Overflow {
			if a.railIsOpen() {
				a.railEntry(id).head.force = true
			}

			cmds = append(cmds, a.railFire(st, id, probeHead))
		}

		if len(b.Paths) > 0 || b.Overflow {
			cmds = append(cmds, a.railFire(st, id, probeStats))
		}
	}

	return tea.Batch(cmds...)
}

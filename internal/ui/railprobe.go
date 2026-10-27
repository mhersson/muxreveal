package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
)

const (
	railProbeTimeout = 2 * time.Second
	headEvery        = 5 * time.Second
	statsEvery       = 10 * time.Second
	shortOIDLen      = 7
	// railProbeMax bounds the rail's probes running at once.
	railProbeMax = 4
)

type railProbeKind int

const (
	probeHead railProbeKind = iota
	probeStats
)

// probeSlot rate-limits one probe of one pane.
type probeSlot struct {
	last    time.Time // when the latest probe started
	busy    bool      // a probe is in flight
	pending bool      // a trigger arrived that has not run yet
	timer   bool      // a delayed probe is scheduled
	cwd     string    // the cwd the latest probe was started for
	force   bool      // the next run ignores the interval
}

// railHeadMsg is a head probe's answer; it carries the cwd it ran for.
type railHeadMsg struct {
	pane core.PaneID
	cwd  string
	gen  int
	res  railProbe
	err  error
}

// railStatsMsg is a stats probe's answer.
type railStatsMsg struct {
	pane  core.PaneID
	cwd   string
	gen   int
	stats git.Stats
	err   error
}

// railDueMsg ends the wait before a delayed probe.
type railDueMsg struct {
	pane core.PaneID
	kind railProbeKind
	gen  int
}

// railOpened marks the rail open and probes every agent. It does nothing when
// the rail is already open.
func (a *App) railOpened() tea.Cmd {
	if a.railIsOpen() {
		return nil
	}

	a.rail.gen++
	a.rail.ctx, a.rail.cancel = context.WithCancel(a.ctx)

	st := a.store.State()
	a.railNormalize(st)

	var cmds []tea.Cmd

	for _, id := range st.AgentPanes() {
		cmds = append(cmds, a.railFire(st, id, probeHead), a.railFire(st, id, probeStats))
	}

	return tea.Batch(cmds...)
}

// railClosed cancels the running probes and forgets the pending ones.
func (a *App) railClosed() {
	if !a.railIsOpen() {
		return
	}

	a.rail.cancel()
	a.rail.ctx, a.rail.cancel = nil, nil

	for _, rp := range a.rail.panes {
		rp.head = probeSlot{last: rp.head.last, cwd: rp.head.cwd}
		rp.stats = probeSlot{last: rp.stats.last, cwd: rp.stats.cwd}
	}
}

// railSync probes an agent whose cwd differs from the one its probes last ran for.
func (a *App) railSync(st core.State, id core.PaneID, cwd string) tea.Cmd {
	if cwd == "" || !a.railIsOpen() {
		return nil
	}

	rp := a.railEntry(id)

	var cmds []tea.Cmd

	if rp.head.cwd != cwd {
		cmds = append(cmds, a.railFire(st, id, probeHead))
	}

	if rp.stats.cwd != cwd && a.engine != nil {
		cmds = append(cmds, a.railFire(st, id, probeStats))
	}

	return tea.Batch(cmds...)
}

// railHook returns the probes a hook event calls for.
func (a *App) railHook(id core.PaneID, start, idle bool) tea.Cmd {
	var cmds []tea.Cmd

	st := a.store.State()

	if start || idle {
		cmds = append(cmds, a.railFire(st, id, probeHead))
	}

	if idle {
		cmds = append(cmds, a.railFire(st, id, probeStats))
	}

	return tea.Batch(cmds...)
}

func (rp *railPane) slot(k railProbeKind) *probeSlot {
	if k == probeStats {
		return &rp.stats
	}

	return &rp.head
}

// railFire starts a probe, or schedules one delayed probe when the interval has
// not passed or one is running.
func (a *App) railFire(st core.State, id core.PaneID, k railProbeKind) tea.Cmd {
	m := st.Panes[id]
	if !a.railIsOpen() || m.Kind != core.KindAgent || m.Cwd == "" {
		return nil
	}

	if k == probeStats {
		if a.engine == nil {
			return nil
		}

		// Outside a repository there is nothing to count. Only a head result for
		// this cwd says so; the cwd is recorded so that railSync does not ask again.
		if known := a.rail.panes[id]; known != nil && known.probe.known && !known.probe.repo && known.probe.headCwd == m.Cwd {
			known.stats.cwd = m.Cwd

			return nil
		}
	}

	rp := a.railEntry(id)
	s := rp.slot(k)

	if s.busy {
		s.pending = true

		return nil
	}

	every := headEvery
	if k == probeStats {
		every = statsEvery
	}

	force := s.force
	s.force = false

	if since := a.clock.Now().Sub(s.last); !force && !s.last.IsZero() && since < every {
		s.pending = true
		if s.timer {
			return nil
		}

		s.timer = true
		gen := a.rail.gen

		return a.tick(every-since, func(time.Time) tea.Msg { return railDueMsg{pane: id, kind: k, gen: gen} })
	}

	s.pending, s.busy, s.last, s.cwd = false, true, a.clock.Now(), m.Cwd

	ctx, cwd, gen := a.rail.ctx, m.Cwd, a.rail.gen

	sem := a.probeSem

	if k == probeStats {
		eng := a.engine

		return func() tea.Msg {
			ctx, done, err := admit(ctx, sem)
			if err != nil {
				return railStatsMsg{pane: id, cwd: cwd, gen: gen, err: err}
			}
			defer done()

			st, err := eng.Stats(ctx, git.BranchSource{Worktree: cwd})

			return railStatsMsg{pane: id, cwd: cwd, gen: gen, stats: st, err: err}
		}
	}

	return func() tea.Msg {
		ctx, done, err := admit(ctx, sem)
		if err != nil {
			return railHeadMsg{pane: id, cwd: cwd, gen: gen, err: err}
		}
		defer done()

		res, err := probeHeadOf(ctx, cwd)

		return railHeadMsg{pane: id, cwd: cwd, gen: gen, res: res, err: err}
	}
}

// admit waits for a free probe slot, honouring ctx, and returns the probe's
// context with its timeout (counted from the start of the run, not the wait)
// and the function that frees the slot.
func admit(ctx context.Context, sem chan struct{}) (context.Context, func(), error) {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(ctx, railProbeTimeout)

	return ctx, func() {
		cancel()
		<-sem
	}, nil
}

// onRailDue runs the delayed probe if a trigger is still waiting.
func (a *App) onRailDue(msg railDueMsg) tea.Cmd {
	rp, ok := a.rail.panes[msg.pane]
	if !ok || msg.gen != a.rail.gen || !a.railIsOpen() {
		return nil
	}

	s := rp.slot(msg.kind)
	s.timer = false

	if !s.pending {
		return nil
	}

	return a.railFire(a.store.State(), msg.pane, msg.kind)
}

// railFresh reports whether an answer still applies, and frees the slot.
func (a *App) railFresh(id core.PaneID, cwd string, gen int, k railProbeKind) (*railPane, bool) {
	rp, ok := a.rail.panes[id]
	if !ok || gen != a.rail.gen || !a.railIsOpen() {
		return nil, false
	}

	rp.slot(k).busy = false

	m := a.store.State().Panes[id]

	return rp, m.Kind == core.KindAgent && m.Cwd == cwd
}

func (a *App) onRailHead(msg railHeadMsg) tea.Cmd {
	rp, fresh := a.railFresh(msg.pane, msg.cwd, msg.gen, probeHead)
	if rp == nil {
		return nil
	}

	var cmds []tea.Cmd

	if fresh {
		if msg.err != nil {
			a.log.Debug("head probe", "pane", msg.pane, "err", msg.err)

			// A result for another cwd is not kept past a failed probe of this one.
			if rp.probe.headCwd != msg.cwd {
				rp.probe = railProbe{}
			}
		} else {
			stats := rp.probe
			moved := stats.known && stats.headCwd == msg.cwd && stats.repo && msg.res.repo &&
				(stats.branch != msg.res.branch || stats.detached != msg.res.detached || stats.oid != msg.res.oid)
			rp.probe = msg.res
			rp.probe.stats, rp.probe.statsKnown, rp.probe.statsCwd = stats.stats, stats.statsKnown, stats.statsCwd

			if !msg.res.repo {
				rp.probe.stats, rp.probe.statsKnown = git.Stats{}, false
			}

			// A switch or a commit changes the totals; the interval waits otherwise.
			if moved {
				rp.stats.force = true

				cmds = append(cmds, a.railFire(a.store.State(), msg.pane, probeStats))
			}
		}
	}

	if rp.head.pending {
		cmds = append(cmds, a.railFire(a.store.State(), msg.pane, probeHead))
	}

	return tea.Batch(cmds...)
}

func (a *App) onRailStats(msg railStatsMsg) tea.Cmd {
	rp, fresh := a.railFresh(msg.pane, msg.cwd, msg.gen, probeStats)
	if rp == nil {
		return nil
	}

	if fresh {
		switch {
		case errors.Is(msg.err, git.ErrNotRepo):
			rp.probe.stats, rp.probe.statsKnown = git.Stats{}, false
		case msg.err != nil:
			a.log.Debug("stats probe", "pane", msg.pane, "err", msg.err)

			if rp.probe.statsCwd != msg.cwd {
				rp.probe.stats, rp.probe.statsKnown = git.Stats{}, false
			}
		default:
			rp.probe.stats, rp.probe.statsKnown, rp.probe.statsCwd = msg.stats, true, msg.cwd
		}
	}

	if rp.stats.pending {
		return a.railFire(a.store.State(), msg.pane, probeStats)
	}

	return nil
}

// probeHeadOf finds a cwd's project, branch and detached state. A cwd outside a
// repository, or gone, is reported as a project named after it with no branch.
func probeHeadOf(ctx context.Context, cwd string) (railProbe, error) {
	res := railProbe{project: filepath.Base(cwd), known: true, headCwd: cwd}

	if !usableDir(cwd) {
		return res, nil
	}

	r, err := git.Open(ctx, cwd)

	switch {
	case errors.Is(err, git.ErrNotRepo):
		return res, nil
	case err != nil:
		return railProbe{}, err
	}

	res.repo = true

	if root, err := r.MainRoot(); err == nil {
		res.project = filepath.Base(root)
	} else if errors.Is(err, git.ErrBare) {
		res.project = strings.TrimSuffix(filepath.Base(r.CommonDir), ".git")
	} else {
		return railProbe{}, err
	}

	h, err := git.Head(ctx, r)
	if err != nil {
		return railProbe{}, err
	}

	res.branch, res.detached, res.oid = h.Branch, h.Detached, h.OID
	if h.Detached {
		res.branch = h.OID[:min(shortOIDLen, len(h.OID))]
	}

	return res, nil
}

package ui

import (
	"context"
	"errors"
	"log/slog"
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
	nameEvery        = 5 * time.Second
	statsEvery       = 10 * time.Second
	shortOIDLen      = 7
	// railProbeMax bounds the head, stats and name probes running at once.
	railProbeMax = 4
)

type railProbeKind int

const (
	probeHead railProbeKind = iota
	probeStats
	probeName
)

// railBound reports whether probes of this kind exist only while the rail is
// open. The head probe does not: branches are known with the rail closed.
func (k railProbeKind) railBound() bool { return k != probeHead }

// probeSlot rate-limits one probe of one pane.
type probeSlot struct {
	last    time.Time // when the latest probe started
	busy    bool      // a probe is in flight
	pending bool      // a trigger arrived that has not run yet
	timer   bool      // a delayed probe is scheduled
	target  string    // what the latest probe was started for: the cwd, or the transcript for the name probe
	force   bool      // the next run ignores the interval
}

// railHeadMsg is a head probe's answer; it carries the cwd it ran for.
type railHeadMsg struct {
	pane core.PaneID
	cwd  string
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

// railNameMsg is a name probe's answer; it carries the transcript it read.
type railNameMsg struct {
	pane       core.PaneID
	transcript string
	gen        int
	name       string
	err        error
}

// railDueMsg ends the wait before a delayed probe.
type railDueMsg struct {
	pane core.PaneID
	kind railProbeKind
	gen  int
}

// railOpened marks the rail open and probes every agent's stats and name; heads
// are already known. It does nothing when the rail is already open.
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
		cmds = append(cmds, a.railFire(st, id, probeStats), a.railFire(st, id, probeName))
	}

	return tea.Batch(cmds...)
}

// railClosed cancels the running stats and name probes and forgets the pending
// ones. Head probes go on.
func (a *App) railClosed() {
	if !a.railIsOpen() {
		return
	}

	a.rail.cancel()
	a.rail.ctx, a.rail.cancel = nil, nil

	for _, rp := range a.rail.panes {
		rp.stats = probeSlot{last: rp.stats.last, target: rp.stats.target}
		rp.nameSlot = probeSlot{last: rp.nameSlot.last, target: rp.nameSlot.target}
	}
}

// railHeadSync probes the head of each agent whose working directory differs
// from the one its head was last probed for, and of a shell pane that took focus
// or changed directory.
func (a *App) railHeadSync(st core.State) {
	focus := st.Tab().Focus
	took := focus != a.rail.focus
	a.rail.focus = focus

	probe := func(id core.PaneID, force bool) {
		cwd := st.Panes[id].WorkDir()
		if cwd == "" {
			return
		}

		if rp := a.rail.panes[id]; force || rp == nil || rp.head.target != cwd {
			if cmd := a.railFire(st, id, probeHead); cmd != nil {
				a.later = append(a.later, cmd)
			}
		}
	}

	for _, id := range st.AgentPanes() {
		probe(id, false)
	}

	if st.Panes[focus].Kind == core.KindShell {
		probe(focus, took)
	}
}

// railSync probes the stats of an agent whose working directory differs from the
// one they last ran for, and the name of one whose transcript differs from the
// one its name was last read from.
func (a *App) railSync(st core.State, id core.PaneID, cwd string) tea.Cmd {
	if !a.railIsOpen() {
		return nil
	}

	rp := a.railEntry(id)

	var cmds []tea.Cmd

	if cwd != "" && rp.stats.target != cwd && a.engine != nil {
		cmds = append(cmds, a.railFire(st, id, probeStats))
	}

	if tr := st.Panes[id].Transcript; tr != "" && rp.nameSlot.target != tr {
		cmds = append(cmds, a.railFire(st, id, probeName))
	}

	return tea.Batch(cmds...)
}

// railHook returns the probes a hook event calls for.
func (a *App) railHook(id core.PaneID, start, idle bool) tea.Cmd {
	var cmds []tea.Cmd

	st := a.store.State()

	if start || idle {
		cmds = append(cmds, a.railFire(st, id, probeHead), a.railFire(st, id, probeName))
	}

	if idle {
		cmds = append(cmds, a.railFire(st, id, probeStats))
	}

	return tea.Batch(cmds...)
}

func (rp *railPane) slot(k railProbeKind) *probeSlot {
	switch k {
	case probeStats:
		return &rp.stats
	case probeName:
		return &rp.nameSlot
	}

	return &rp.head
}

// probeCtx is the context a probe of kind k runs under, and the open of the rail
// it belongs to. The head probe ends with the app and belongs to no open.
func (a *App) probeCtx(k railProbeKind) (context.Context, int) {
	if k.railBound() {
		return a.rail.ctx, a.rail.gen
	}

	return a.ctx, 0
}

// railFire starts a probe, or schedules one delayed probe when the interval has
// not passed or one is running. Every kind serves agents; the head probe also
// serves the active tab's focused pane.
func (a *App) railFire(st core.State, id core.PaneID, k railProbeKind) tea.Cmd {
	m := st.Panes[id]

	// The git probes run in the agent's directory; the name probe reads the transcript.
	cwd := m.WorkDir()

	target := cwd
	if k == probeName {
		target = m.Transcript
	}

	served := m.Kind == core.KindAgent || (k == probeHead && st.Tab().Focus == id)
	if !served || target == "" || k.railBound() && !a.railIsOpen() {
		return nil
	}

	if k == probeStats {
		if a.engine == nil {
			return nil
		}

		// Outside a repository there is nothing to count. Only a head result for
		// this cwd says so; the cwd is recorded so that railSync does not ask again.
		if known := a.rail.panes[id]; known != nil && known.probe.known && !known.probe.repo && known.probe.headCwd == cwd {
			known.stats.target = cwd

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

	switch k {
	case probeStats:
		every = statsEvery
	case probeName:
		every = nameEvery
	}

	force := s.force
	s.force = false

	if since := a.clock.Now().Sub(s.last); !force && !s.last.IsZero() && since < every {
		s.pending = true
		if s.timer {
			return nil
		}

		s.timer = true
		_, gen := a.probeCtx(k)

		return a.tick(every-since, func(time.Time) tea.Msg { return railDueMsg{pane: id, kind: k, gen: gen} })
	}

	s.pending, s.busy, s.last, s.target = false, true, a.clock.Now(), target

	ctx, gen := a.probeCtx(k)

	sem := a.probeSem

	if k == probeName {
		h := a.harnessOf(m)

		return func() tea.Msg {
			ctx, done, err := admit(ctx, sem)
			if err != nil {
				return railNameMsg{pane: id, transcript: target, gen: gen, err: err}
			}
			defer done()

			name, err := h.CustomName(ctx, target)

			return railNameMsg{pane: id, transcript: target, gen: gen, name: name, err: err}
		}
	}

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

	log := a.log

	return func() tea.Msg {
		ctx, done, err := admit(ctx, sem)
		if err != nil {
			return railHeadMsg{pane: id, cwd: cwd, err: err}
		}
		defer done()

		res, err := probeHeadOf(ctx, log, cwd)

		return railHeadMsg{pane: id, cwd: cwd, res: res, err: err}
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
	if !ok || msg.kind.railBound() && (msg.gen != a.rail.gen || !a.railIsOpen()) {
		return nil
	}

	s := rp.slot(msg.kind)
	s.timer = false

	if !s.pending {
		return nil
	}

	return a.railFire(a.store.State(), msg.pane, msg.kind)
}

// railLive returns the pane's memory for an answer of probe kind k, and frees
// the slot; nil when the pane is gone or, for a rail-bound kind, the answer is
// of an earlier open or the rail was closed since. A head answer ignores gen.
func (a *App) railLive(id core.PaneID, gen int, k railProbeKind) *railPane {
	rp, ok := a.rail.panes[id]
	if !ok || k.railBound() && (gen != a.rail.gen || !a.railIsOpen()) {
		return nil
	}

	rp.slot(k).busy = false

	return rp
}

// railFresh reports whether an answer still applies, and frees the slot.
func (a *App) railFresh(id core.PaneID, cwd string, gen int, k railProbeKind) (*railPane, bool) {
	rp := a.railLive(id, gen, k)
	if rp == nil {
		return nil, false
	}

	m, ok := a.store.State().Panes[id]

	return rp, ok && (k == probeHead || m.Kind == core.KindAgent) && m.WorkDir() == cwd
}

// onRailName keeps a name read for the pane's current transcript. A completed
// read replaces the name, and a failed one clears it, as a missing file does. A
// probe that timed out or was cancelled gave no answer and leaves the name.
func (a *App) onRailName(msg railNameMsg) tea.Cmd {
	rp := a.railLive(msg.pane, msg.gen, probeName)
	if rp == nil {
		return nil
	}

	st := a.store.State()

	if m := st.Panes[msg.pane]; m.Kind == core.KindAgent && m.Transcript == msg.transcript {
		switch {
		case msg.err == nil:
			rp.name = railName{text: msg.name, transcript: msg.transcript}
		case errors.Is(msg.err, context.DeadlineExceeded), errors.Is(msg.err, context.Canceled):
			a.log.Debug("name probe gave no answer", "pane", msg.pane, "err", msg.err)
		default:
			a.log.Debug("name probe", "pane", msg.pane, "err", msg.err)

			rp.name = railName{}
		}
	}

	if rp.nameSlot.pending {
		return a.railFire(st, msg.pane, probeName)
	}

	return nil
}

func (a *App) onRailHead(msg railHeadMsg) tea.Cmd {
	rp, fresh := a.railFresh(msg.pane, msg.cwd, 0, probeHead)
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
			if moved && a.railIsOpen() {
				rp.stats.force = true

				cmds = append(cmds, a.railFire(a.store.State(), msg.pane, probeStats))
			}
		}

		a.prProbed(msg.pane)
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
func probeHeadOf(ctx context.Context, log *slog.Logger, cwd string) (railProbe, error) {
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

	// Only a branch with a commit can be ahead of an upstream. The count is the
	// lesser datum: when it fails the branch still shows, with no count, unless
	// the probe's own budget ran out.
	if h.Branch != "" && h.OID != "" {
		n, _, aerr := git.Ahead(ctx, r)

		switch {
		case aerr == nil:
			res.ahead = n
		case ctx.Err() != nil:
			return railProbe{}, aerr
		default:
			log.Debug("ahead count", "cwd", cwd, "err", aerr)
		}
	}

	return res, nil
}

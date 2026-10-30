package main

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/harness"
)

const (
	// turnHold is how long the hook server holds a prompt hook's answer; the
	// hook client gives up at 2 s.
	turnHold = 1500 * time.Millisecond
	// turnBudget bounds a turn snapshot, counted from when the hook server
	// reads the event (the gate's arrival time for an event without a deadline).
	turnBudget = 1400 * time.Millisecond
	// markerBudget bounds writing a "no snapshot" marker.
	markerBudget = 5 * time.Second
)

var sessionIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// turnWriter records turn snapshots; gitTurnWriter does it with git.
type turnWriter interface {
	// Start records the working tree of the worktree containing dir as the start
	// of turn. ends names an open turn of that worktree to end with the same
	// tree, 0 for none. unchanged says nothing changed since the session's last
	// successful snapshot.
	Start(ctx context.Context, dir, session string, turn, ends int, unchanged bool) error
	// Mark records that turn, or with end its end, has no snapshot.
	Mark(ctx context.Context, dir, session string, turn int, end bool) error
	// End records the working tree of the worktree rooted at root as the end of
	// its open turn. unchanged says nothing changed since that turn's start.
	End(ctx context.Context, root, session string, turn int, unchanged bool) error
	// OpenTurns lists the session's open turns at its highest number in the
	// repository containing dir.
	OpenTurns(ctx context.Context, dir, session string) ([]git.OpenTurn, error)
}

// gitTurnWriter records turns with git, its temporary indexes in tempDir and
// the refreshed index copies in indexCache.
type gitTurnWriter struct{ tempDir, indexCache string }

func (w gitTurnWriter) Start(ctx context.Context, dir, session string, turn, ends int, unchanged bool) error {
	_, err := git.Snapshot(ctx, dir, session, git.SnapshotOptions{
		TempDir: w.tempDir, IndexCache: w.indexCache, Unchanged: unchanged, Turn: turn, Ends: ends,
	})

	return err
}

func (w gitTurnWriter) End(ctx context.Context, root, session string, turn int, unchanged bool) error {
	_, err := git.EndTurn(ctx, root, session, turn, git.SnapshotOptions{
		TempDir: w.tempDir, IndexCache: w.indexCache, Unchanged: unchanged,
	})

	return err
}

func (gitTurnWriter) OpenTurns(ctx context.Context, dir, session string) ([]git.OpenTurn, error) {
	r, err := git.Open(ctx, dir)
	if err != nil {
		return nil, err
	}

	return git.OpenTurns(ctx, r, session)
}

func (gitTurnWriter) Mark(ctx context.Context, dir, session string, turn int, end bool) error {
	_, err := git.MarkMissing(ctx, dir, session, turn, end)

	return err
}

// nextTurnID numbers a turn by when its prompt arrived, in milliseconds, and
// never repeats or goes back: a clock set back gives one more than prev.
func nextTurnID(prev int, at time.Time) int {
	return max(int(at.UnixMilli()), prev+1)
}

// seqFunc resolves the worktree containing dir and reads its watcher's change
// counter; ok is false when that worktree is not watched.
type seqFunc func(ctx context.Context, dir string) (root string, n uint64, ok bool)

// seqKey names what the unchanged skip remembers a counter for: one pane's
// snapshots of one session in one worktree.
type seqKey struct {
	pane          core.PaneID
	session, root string
}

// seqMemory holds the counter read before the last successful snapshot of each
// seqKey. Snapshot goroutines of different panes share it.
type seqMemory struct {
	mu   sync.Mutex
	last map[seqKey]uint64
}

func (m *seqMemory) get(k seqKey) (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.last[k]

	return v, ok
}

func (m *seqMemory) set(k seqKey, v uint64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ok {
		m.last[k] = v
	} else {
		delete(m.last, k)
	}
}

// paneTurn is what the gate remembers of a pane's last turn: the worktrees it
// started with a snapshot, which the pane's next prompt ends.
type paneTurn struct {
	session string
	turn    int
	roots   []string
}

// paneTurns holds each pane's last turn. Snapshot goroutines of different
// panes share it.
type paneTurns struct {
	mu   sync.Mutex
	last map[core.PaneID]paneTurn
}

func (p *paneTurns) get(pane core.PaneID) (paneTurn, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	v, ok := p.last[pane]

	return v, ok
}

func (p *paneTurns) set(pane core.PaneID, v paneTurn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.last[pane] = v
}

// runTurnGate forwards every event from in to out in order and, for a turn
// start, snapshots the working tree before releasing the held hook answer. The
// snapshot starts before the event is forwarded, so a full out channel cannot
// delay it; the forwarded event carries the channel the snapshot closes. Each
// turn gets the number nextTurnID gives it in arrival order. A turn start ends
// every worktree the pane's previous turn started, whatever session that was;
// Stop, StopFailure and SessionEnd end nothing. A snapshot passes unchanged
// when seq reports the worktree watched and its counter is what it was just
// before the last successful snapshot there. It returns when in closes or ctx
// ends, after its snapshot goroutines.
func runTurnGate(ctx context.Context, in <-chan harness.Event, out chan<- harness.Event, w turnWriter, seq seqFunc, log *slog.Logger) {
	var wg sync.WaitGroup
	defer wg.Wait()

	// The last snapshot started per pane; touched only by this goroutine.
	// Each snapshot waits for its predecessor, so a pane's turn starts run one
	// at a time in arrival order.
	tails := make(map[core.PaneID]chan struct{})
	g := &gate{
		w: w, seq: seq, log: log,
		memory: &seqMemory{last: map[seqKey]uint64{}},
		panes:  &paneTurns{last: map[core.PaneID]paneTurn{}},
	}

	// The last number given per pane; touched only by this goroutine.
	numbers := make(map[core.PaneID]int)

	for {
		var ev harness.Event

		select {
		case <-ctx.Done():
			return
		case e, ok := <-in:
			if !ok {
				return
			}

			ev = e
		}

		arrived := time.Now()

		if ev.Before {
			release(ev)
		}

		if ev.TurnStart {
			if ev.Cwd == "" || !sessionIDRE.MatchString(string(ev.Session)) {
				release(ev)
			} else {
				turn := nextTurnID(numbers[ev.Pane], arrived)
				numbers[ev.Pane] = turn
				prev, done := tails[ev.Pane], make(chan struct{})
				tails[ev.Pane] = done
				ev.Snapped = done

				deadline := ev.Deadline
				if deadline.IsZero() {
					deadline = arrived.Add(turnBudget)
				}

				wg.Go(func() {
					defer close(done)

					g.startTurn(ctx, deadline, prev, ev, turn)
				})
			}
		}

		// A full app channel blocks here; the snapshot and the server's hold
		// run on their own.
		select {
		case out <- ev:
		case <-ctx.Done():
			release(ev)

			return
		}
	}
}

func release(ev harness.Event) {
	if ev.Done != nil {
		ev.Done()
	}
}

// gate is what a turn start's goroutine works with.
type gate struct {
	w      turnWriter
	seq    seqFunc
	log    *slog.Logger
	memory *seqMemory
	panes  *paneTurns
}

// pendingEnd is an open turn of an earlier prompt that a turn start ends.
type pendingEnd struct {
	root, session string
	turn          int
	failed        bool // the end was not written; it gets a marker
}

// startTurn ends the pane's previous turn and snapshots the new one after prev
// closes and before deadline, and releases the held answer when it returns or
// the budget ends, whichever is first. A turn that gets no snapshot gets a
// marker, so its number stays taken, and an end that was not written gets an
// end marker.
func (g *gate) startTurn(parent context.Context, deadline time.Time, prev <-chan struct{}, ev harness.Event, turn int) {
	snapCtx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()

	// Releases the answer at the budget even while waiting for the previous snapshot.
	stop := context.AfterFunc(snapCtx, func() { release(ev) })
	defer stop()

	session := string(ev.Session)

	// A pane's first turn start has no predecessor.
	if prev != nil {
		<-prev
	}

	var (
		pending []pendingEnd
		err     error
	)

	// A snapshot taken after the budget could already hold the agent's edits.
	if err = snapCtx.Err(); err == nil {
		pending, err = g.endAndStart(snapCtx, ev, session, turn)
	} else {
		pending = g.forget(ev, session, turn)
	}

	release(ev)

	g.mark(parent, ev, session, turn, pending, err)
}

// forget replaces the pane's turn with one that started nothing and returns
// the previous turn's worktrees, all still open.
func (g *gate) forget(ev harness.Event, session string, turn int) []pendingEnd {
	last, _ := g.panes.get(ev.Pane)
	g.panes.set(ev.Pane, paneTurn{session: session, turn: turn})

	pending := make([]pendingEnd, 0, len(last.roots))
	for _, root := range last.roots {
		pending = append(pending, pendingEnd{root: root, session: last.session, turn: last.turn, failed: true})
	}

	return pending
}

// endAndStart ends what the pane's previous turn started, then takes the new
// turn's start. It returns the ends that failed and the start's error.
func (g *gate) endAndStart(ctx context.Context, ev harness.Event, session string, turn int) ([]pendingEnd, error) {
	root, n, watched := g.seq(ctx, ev.Cwd)

	last, known := g.panes.get(ev.Pane)
	if !known {
		last = g.openTurns(ctx, ev.Cwd, session)
	}

	// The own worktree ends through the new start when it is the same session.
	ends := 0
	pending := make([]pendingEnd, 0, len(last.roots))

	for _, r := range last.roots {
		p := pendingEnd{root: r, session: last.session, turn: last.turn}

		switch {
		case root != "" && r == root && last.session == session:
			ends = last.turn
		default:
			pending = append(pending, p)
		}
	}

	g.endAll(ctx, ev.Pane, pending)

	key := seqKey{pane: ev.Pane, session: strings.ToLower(session), root: root}
	prevN, had := g.memory.get(key)

	err := g.w.Start(ctx, ev.Cwd, session, turn, ends, watched && had && prevN == n)
	g.memory.set(key, n, err == nil && watched)

	next := paneTurn{session: session, turn: turn}
	if err == nil && root != "" {
		next.roots = []string{root}
	}

	g.panes.set(ev.Pane, next)

	if err != nil && ends != 0 {
		pending = append(pending, pendingEnd{root: root, session: session, turn: ends, failed: true})
	}

	return slices.DeleteFunc(pending, func(p pendingEnd) bool { return !p.failed }), err
}

// openTurns finds the session's open turns in dir's repository for a pane with
// no remembered turn, as after a restart.
func (g *gate) openTurns(ctx context.Context, dir, session string) paneTurn {
	open, err := g.w.OpenTurns(ctx, dir, session)
	if err != nil {
		g.log.Debug("open turns not found", "session", session, "err", err)
	}

	// The refs name one number for all of them.
	last := paneTurn{session: session}

	for _, o := range open {
		last.turn = o.N
		last.roots = append(last.roots, o.Root)
	}

	return last
}

// endAll ends every pending turn concurrently and marks the ones that failed.
func (g *gate) endAll(ctx context.Context, pane core.PaneID, pending []pendingEnd) {
	var wg sync.WaitGroup

	for i := range pending {
		wg.Go(func() {
			p := &pending[i]
			_, n, watched := g.seq(ctx, p.root)
			key := seqKey{pane: pane, session: strings.ToLower(p.session), root: p.root}
			prevN, had := g.memory.get(key)

			err := g.w.End(ctx, p.root, p.session, p.turn, watched && had && prevN == n)
			g.memory.set(key, n, err == nil && watched)

			switch {
			case err == nil:
			case errors.Is(err, git.ErrTurnNotOpen):
				g.log.Debug("turn not ended", "session", p.session, "root", p.root, "err", err)
			default:
				g.log.Warn("turn end failed", "session", p.session, "root", p.root, "err", err)

				p.failed = true
			}
		})
	}

	wg.Wait()
}

// mark writes the markers a turn start owes after its answer was released: its
// own start marker and the end markers of ends that failed.
func (g *gate) mark(parent context.Context, ev harness.Event, session string, turn int, failed []pendingEnd, err error) {
	switch {
	case parent.Err() != nil:
		return
	case errors.Is(err, git.ErrNotRepo), errors.Is(err, git.ErrBadSession):
		g.log.Debug("turn outside a repository", "session", session, "err", err)

		err = nil
	}

	markCtx, cancel := context.WithTimeout(parent, markerBudget)
	defer cancel()

	for _, p := range failed {
		if err := g.w.Mark(markCtx, p.root, p.session, p.turn, true); err != nil {
			g.log.Debug("end marker not written", "session", p.session, "root", p.root, "err", err)
		}
	}

	if err == nil {
		return
	}

	g.log.Warn("turn snapshot failed", "session", session, "err", err)

	if err := g.w.Mark(markCtx, ev.Cwd, session, turn, false); err != nil {
		g.log.Warn("turn marker failed", "session", session, "err", err)
	}
}

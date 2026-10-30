package main

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
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

type rootSeq struct {
	root string
	n    uint64
}

// seqMemory holds the (root, counter) read before each session's last
// successful snapshot. Snapshot goroutines of different sessions share it.
type seqMemory struct {
	mu   sync.Mutex
	last map[string]rootSeq
}

func (m *seqMemory) get(session string) (rootSeq, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.last[session]

	return v, ok
}

func (m *seqMemory) set(session string, v rootSeq, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ok {
		m.last[session] = v
	} else {
		delete(m.last, session)
	}
}

// paneTurn is what the gate remembers of a pane's last turn start.
type paneTurn struct {
	turn    int
	root    string // the worktree the start was taken in; "" when it could not be resolved
	snapped bool   // the start is a snapshot, not a marker
}

// paneTurns holds each pane's last turn start. Snapshot goroutines of
// different sessions share it.
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
// turn gets the number nextTurnID gives it in arrival order, and a start in the
// worktree of the pane's previous start ends that turn. A snapshot passes
// unchanged when seq reports the worktree watched and its counter is what it
// was just before the session's last successful snapshot. It returns when in
// closes or ctx ends, after its snapshot goroutines.
func runTurnGate(ctx context.Context, in <-chan harness.Event, out chan<- harness.Event, w turnWriter, seq seqFunc, log *slog.Logger) {
	var wg sync.WaitGroup
	defer wg.Wait()

	// The last snapshot started per session; touched only by this goroutine.
	// Each snapshot waits for its predecessor, so a session's snapshots run
	// one at a time in arrival order.
	tails := make(map[string]chan struct{})
	memory := &seqMemory{last: map[string]rootSeq{}}
	panes := &paneTurns{last: map[core.PaneID]paneTurn{}}

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
				key := strings.ToLower(string(ev.Session))
				turn := nextTurnID(numbers[ev.Pane], arrived)
				numbers[ev.Pane] = turn
				prev, done := tails[key], make(chan struct{})
				tails[key] = done
				ev.Snapped = done

				deadline := ev.Deadline
				if deadline.IsZero() {
					deadline = arrived.Add(turnBudget)
				}

				wg.Go(func() {
					defer close(done)

					snapshotTurn(ctx, deadline, prev, ev, turn, w, seq, memory, panes, log)
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

// snapshotTurn takes one turn's snapshot after prev closes and before deadline,
// and releases the held answer when it returns or the budget ends, whichever is
// first. A turn that gets no snapshot gets a marker, so its number stays taken.
func snapshotTurn(gate context.Context, deadline time.Time, prev <-chan struct{}, ev harness.Event, turn int, w turnWriter, seq seqFunc, memory *seqMemory, panes *paneTurns, log *slog.Logger) {
	snapCtx, cancel := context.WithDeadline(gate, deadline)
	defer cancel()

	// Releases the answer at the budget even while waiting for the previous snapshot.
	stop := context.AfterFunc(snapCtx, func() { release(ev) })
	defer stop()

	session := string(ev.Session)

	// A session's first snapshot has no predecessor.
	if prev != nil {
		<-prev
	}

	// A snapshot taken after the budget could already hold the agent's edits.
	err := snapCtx.Err()
	if err == nil {
		err = startTurn(snapCtx, ev, session, turn, w, seq, memory, panes)
	} else {
		// No snapshot is taken, so the pane's next start must end nothing.
		panes.set(ev.Pane, paneTurn{turn: turn})
	}

	release(ev)

	switch {
	case err == nil, gate.Err() != nil:
		return
	case errors.Is(err, git.ErrNotRepo), errors.Is(err, git.ErrBadSession):
		log.Debug("turn outside a repository", "session", session, "err", err)

		return
	}

	log.Warn("turn snapshot failed", "session", session, "err", err)

	markCtx, markCancel := context.WithTimeout(gate, markerBudget)
	defer markCancel()

	if err := w.Mark(markCtx, ev.Cwd, session, turn, false); err != nil {
		log.Warn("turn marker failed", "session", session, "err", err)
	}
}

// startTurn reads the counter before the snapshot starts, so a change during
// it makes the next snapshot a full one; the counter is remembered only when the
// snapshot succeeds. The start ends the pane's previous turn when that was a
// snapshot in the same worktree.
func startTurn(ctx context.Context, ev harness.Event, session string, turn int, w turnWriter, seq seqFunc, memory *seqMemory, panes *paneTurns) error {
	key := strings.ToLower(session)
	root, n, watched := seq(ctx, ev.Cwd)
	cur := rootSeq{root: root, n: n}
	prev, had := memory.get(key)

	ends := 0
	if last, ok := panes.get(ev.Pane); ok && root != "" && last.root == root && last.snapped {
		ends = last.turn
	}

	err := w.Start(ctx, ev.Cwd, session, turn, ends, watched && had && prev == cur)
	memory.set(key, cur, err == nil && watched)
	panes.set(ev.Pane, paneTurn{turn: turn, root: root, snapped: err == nil})

	return err
}

package main

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

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

// snapshotFunc records something about the repository containing dir for a session.
// unchanged says nothing changed since the session's last successful snapshot;
// a marker ignores it.
type snapshotFunc func(ctx context.Context, dir, session string, unchanged bool) error

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

// runTurnGate forwards every event from in to out in order and, for a turn
// start, snapshots the working tree before releasing the held hook answer. The
// snapshot starts before the event is forwarded, so a full out channel cannot
// delay it; the forwarded event carries the channel the snapshot closes. A snapshot passes unchanged when seq reports the worktree watched
// and its counter is what it was just before the session's last successful
// snapshot. It returns when in closes or ctx ends, after its snapshot goroutines.
func runTurnGate(ctx context.Context, in <-chan harness.Event, out chan<- harness.Event, snap, mark snapshotFunc, seq seqFunc, log *slog.Logger) {
	var wg sync.WaitGroup
	defer wg.Wait()

	// The last snapshot started per session; touched only by this goroutine.
	// Each snapshot waits for its predecessor, so a session's snapshots run
	// one at a time in arrival order.
	tails := make(map[string]chan struct{})
	memory := &seqMemory{last: map[string]rootSeq{}}

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

		if ev.TurnStart {
			if ev.Cwd == "" || !sessionIDRE.MatchString(string(ev.Session)) {
				release(ev)
			} else {
				key := strings.ToLower(string(ev.Session))
				prev, done := tails[key], make(chan struct{})
				tails[key] = done
				ev.Snapped = done

				deadline := ev.Deadline
				if deadline.IsZero() {
					deadline = arrived.Add(turnBudget)
				}

				wg.Go(func() {
					defer close(done)

					snapshotTurn(ctx, deadline, prev, ev, snap, mark, seq, memory, log)
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
// first. A turn that gets no snapshot gets a marker so later turns keep their
// numbers.
func snapshotTurn(gate context.Context, deadline time.Time, prev <-chan struct{}, ev harness.Event, snap, mark snapshotFunc, seq seqFunc, memory *seqMemory, log *slog.Logger) {
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
		err = snapshotUnchanged(snapCtx, ev, session, snap, seq, memory)
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

	if err := mark(markCtx, ev.Cwd, session, false); err != nil {
		log.Warn("turn marker failed", "session", session, "err", err)
	}
}

// snapshotUnchanged reads the counter before the snapshot starts, so a change
// during it makes the next snapshot a full one; the counter is remembered only
// when the snapshot succeeds.
func snapshotUnchanged(ctx context.Context, ev harness.Event, session string, snap snapshotFunc, seq seqFunc, memory *seqMemory) error {
	key := strings.ToLower(session)
	root, n, watched := seq(ctx, ev.Cwd)
	cur := rootSeq{root: root, n: n}
	prev, had := memory.get(key)

	err := snap(ctx, ev.Cwd, session, watched && had && prev == cur)
	memory.set(key, cur, err == nil && watched)

	return err
}

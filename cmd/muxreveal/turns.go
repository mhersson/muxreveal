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
	// turnBudget bounds a turn snapshot, counted from when the gate reads the
	// event; a backed-up app channel delays the read, so it can end after turnHold.
	turnBudget = 1400 * time.Millisecond
	// markerBudget bounds writing a "no snapshot" marker.
	markerBudget = 5 * time.Second
)

var sessionIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// snapshotFunc records something about the repository containing dir for a session.
type snapshotFunc func(ctx context.Context, dir, session string) error

// runTurnGate forwards every event from in to out in order and, for a turn
// start, snapshots the working tree before releasing the held hook answer. It
// returns when in closes or ctx ends, after its snapshot goroutines.
func runTurnGate(ctx context.Context, in <-chan harness.Event, out chan<- harness.Event, snap, mark snapshotFunc, log *slog.Logger) {
	var wg sync.WaitGroup
	defer wg.Wait()

	// Touched only by this goroutine.
	locks := make(map[string]*sync.Mutex)

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

		// A full app channel blocks here; the server's hold still bounds the agent.
		select {
		case out <- ev:
		case <-ctx.Done():
			release(ev)

			return
		}

		if !ev.TurnStart {
			continue
		}

		if ev.Cwd == "" || !sessionIDRE.MatchString(string(ev.Session)) {
			release(ev)

			continue
		}

		key := strings.ToLower(string(ev.Session))

		mu := locks[key]
		if mu == nil {
			mu = new(sync.Mutex)
			locks[key] = mu
		}

		wg.Go(func() { snapshotTurn(ctx, arrived, mu, ev, snap, mark, log) })
	}
}

func release(ev harness.Event) {
	if ev.Done != nil {
		ev.Done()
	}
}

// snapshotTurn takes one turn's snapshot within turnBudget of arrived and
// releases the held answer when it returns or the budget ends, whichever is
// first. A turn that gets no snapshot gets a marker so later turns keep their
// numbers.
func snapshotTurn(gate context.Context, arrived time.Time, mu *sync.Mutex, ev harness.Event, snap, mark snapshotFunc, log *slog.Logger) {
	snapCtx, cancel := context.WithDeadline(gate, arrived.Add(turnBudget))
	defer cancel()

	// Releases the answer at the budget even while waiting for the lock.
	stop := context.AfterFunc(snapCtx, func() { release(ev) })
	defer stop()

	session := string(ev.Session)

	mu.Lock()
	defer mu.Unlock()

	// A snapshot taken after the budget could already hold the agent's edits.
	err := snapCtx.Err()
	if err == nil {
		err = snap(snapCtx, ev.Cwd, session)
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

	if err := mark(markCtx, ev.Cwd, session); err != nil {
		log.Warn("turn marker failed", "session", session, "err", err)
	}
}

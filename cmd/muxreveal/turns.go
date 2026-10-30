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
	// placeBudget bounds one place lookup, apart from the hold's deadline.
	placeBudget = 2 * time.Second
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

// startedRoot is a worktree a turn started. done closes once its start, or its
// marker, is written; marker is true for a worktree with no snapshot, whose
// end is not written either.
type startedRoot struct {
	done   chan struct{}
	marker bool
}

// paneTurn is what the gate remembers of a pane's last turn: the worktrees it
// started, and whether a worktree it could not find makes it unsure.
type paneTurn struct {
	session string
	turn    int
	roots   map[string]*startedRoot
	unsure  bool
}

// snapshotted lists, in order, the worktrees the turn started with a snapshot:
// the ones the pane's next prompt ends.
func (t paneTurn) snapshotted() []string {
	var out []string

	for root, e := range t.roots {
		if !e.marker {
			out = append(out, root)
		}
	}

	slices.Sort(out)

	return out
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)

	return c
}

// paneTurns holds each pane's last turn. Goroutines of different panes and of
// one pane's edits share it.
type paneTurns struct {
	mu   sync.Mutex
	last map[core.PaneID]paneTurn
}

// get returns a copy of the pane's turn.
func (p *paneTurns) get(pane core.PaneID) (paneTurn, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	v, ok := p.last[pane]
	if !ok {
		return v, false
	}

	roots := make(map[string]*startedRoot, len(v.roots))
	for r, e := range v.roots {
		roots[r] = &startedRoot{done: e.done, marker: e.marker}
	}

	v.roots = roots

	return v, true
}

func (p *paneTurns) set(pane core.PaneID, v paneTurn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.last[pane] = v
}

// settled returns the pane's turn once every worktree it started has its start
// or marker written, or ctx ends.
func (p *paneTurns) settled(ctx context.Context, pane core.PaneID) (paneTurn, bool) {
	cur, ok := p.get(pane)

	for _, e := range cur.roots {
		select {
		case <-e.done:
		case <-ctx.Done():
		}
	}

	if !ok {
		return cur, false
	}

	return p.get(pane)
}

// claim records root as started by the pane's turn, unless it already is. mine
// says the caller started it and must close done; marker says it gets no
// snapshot, because the turn is unsure or marker asks for it. ok is false when
// the pane has moved on to another turn.
func (p *paneTurns) claim(pane core.PaneID, turn int, root string, marker bool) (done chan struct{}, mine, noSnapshot, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	cur, found := p.last[pane]
	if !found || cur.turn != turn {
		return nil, false, false, false
	}

	if e, has := cur.roots[root]; has {
		return e.done, false, e.marker, true
	}

	e := &startedRoot{done: make(chan struct{}), marker: marker || cur.unsure}
	if cur.roots == nil {
		cur.roots = map[string]*startedRoot{}
		p.last[pane] = cur
	}

	cur.roots[root] = e

	return e.done, true, e.marker, true
}

// failStart turns a claimed start into a marker after its snapshot failed.
func (p *paneTurns) failStart(pane core.PaneID, turn int, root string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cur, ok := p.last[pane]; ok && cur.turn == turn {
		if e, has := cur.roots[root]; has {
			e.marker = true
		}
	}
}

// markUnsure makes the rest of the pane's turn record markers for the
// worktrees it starts.
func (p *paneTurns) markUnsure(pane core.PaneID, turn int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cur, ok := p.last[pane]; ok && cur.turn == turn {
		cur.unsure = true
		p.last[pane] = cur
	}
}

// placeFunc finds the worktree an edited file belongs to.
type placeFunc func(ctx context.Context, path string) (git.Place, error)

// paneCurrent is the turn the gate's own goroutine last started for a pane.
type paneCurrent struct {
	session string
	turn    int
	tail    <-chan struct{} // closes when that turn's start is done
}

// runTurnGate forwards every event from in to out in order and, for a turn
// start, snapshots the working tree before releasing the held hook answer. The
// snapshot starts before the event is forwarded, so a full out channel cannot
// delay it; the forwarded event carries the channel the snapshot closes. Each
// turn gets the number nextTurnID gives it in arrival order. A turn start ends
// every worktree the pane's previous turn started, whatever session that was;
// Stop, StopFailure and SessionEnd end nothing. An edit tool's before-event is
// held until the worktree of its file has a start for the pane's turn, when the
// edit is the first there; later edits, files that do not count and events
// outside a turn are answered at once. A snapshot passes unchanged
// when seq reports the worktree watched and its counter is what it was just
// before the last successful snapshot there. It returns when in closes or ctx
// ends, after its snapshot goroutines.
func runTurnGate(ctx context.Context, in <-chan harness.Event, out chan<- harness.Event, w turnWriter, places placeFunc, seq seqFunc, log *slog.Logger) {
	var wg sync.WaitGroup
	defer wg.Wait()

	// The last snapshot started per pane; touched only by this goroutine.
	// Each snapshot waits for its predecessor, so a pane's turn starts run one
	// at a time in arrival order.
	tails := make(map[core.PaneID]chan struct{})
	g := &gate{
		w: w, places: places, seq: seq, log: log,
		memory: &seqMemory{last: map[seqKey]uint64{}},
		panes:  &paneTurns{last: map[core.PaneID]paneTurn{}},
	}

	// The last number given per pane; touched only by this goroutine.
	numbers := make(map[core.PaneID]int)
	current := make(map[core.PaneID]paneCurrent)

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

		deadline := ev.Deadline
		if deadline.IsZero() {
			deadline = arrived.Add(turnBudget)
		}

		if cur, ok := current[ev.Pane]; ok && (ev.Before || len(ev.Changed) > 0) && (ev.Subagent || strings.EqualFold(string(ev.Session), cur.session)) {
			if ev.Before {
				wg.Go(func() { g.edit(ctx, deadline, cur, ev) })
			} else {
				wg.Go(func() { g.changed(ctx, cur, ev) })
			}
		} else if ev.Before {
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
				current[ev.Pane] = paneCurrent{session: string(ev.Session), turn: turn, tail: done}

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

// gate is what the goroutines of a turn start and of a turn's edits work with.
type gate struct {
	w      turnWriter
	places placeFunc
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
		pending = g.forget(parent, ev, session, turn)
	}

	release(ev)

	g.mark(parent, ev, session, turn, pending, err)
}

// forget replaces the pane's turn with one whose own worktree has no snapshot
// and returns the previous turn's worktrees, all still open.
func (g *gate) forget(ctx context.Context, ev harness.Event, session string, turn int) []pendingEnd {
	last, _ := g.panes.settled(ctx, ev.Pane)

	next := paneTurn{session: session, turn: turn, roots: map[string]*startedRoot{}}

	lookup, cancel := context.WithTimeout(ctx, placeBudget)
	defer cancel()

	if root, _, _ := g.seq(lookup, ev.Cwd); root != "" {
		next.roots[root] = &startedRoot{done: closedChan(), marker: true}
	}

	g.panes.set(ev.Pane, next)

	roots := last.snapshotted()
	pending := make([]pendingEnd, 0, len(roots))

	for _, root := range roots {
		pending = append(pending, pendingEnd{root: root, session: last.session, turn: last.turn, failed: true})
	}

	return pending
}

// endAndStart ends what the pane's previous turn started, then takes the new
// turn's start. It returns the ends that failed and the start's error.
func (g *gate) endAndStart(ctx context.Context, ev harness.Event, session string, turn int) ([]pendingEnd, error) {
	root, n, watched := g.seq(ctx, ev.Cwd)

	last, known := g.panes.settled(ctx, ev.Pane)
	if !known {
		last = g.openTurns(ctx, ev.Cwd, session)
	}

	// The own worktree ends through the new start when it is the same session.
	ends := 0
	lastRoots := last.snapshotted()
	pending := make([]pendingEnd, 0, len(lastRoots))

	for _, r := range lastRoots {
		p := pendingEnd{root: r, session: last.session, turn: last.turn}

		if root != "" && r == root && last.session == session {
			ends = last.turn
		} else {
			pending = append(pending, p)
		}
	}

	g.endAll(ctx, ev.Pane, pending)

	key := seqKey{pane: ev.Pane, session: strings.ToLower(session), root: root}
	prevN, had := g.memory.get(key)

	err := g.w.Start(ctx, ev.Cwd, session, turn, ends, watched && had && prevN == n)
	g.memory.set(key, n, err == nil && watched)

	next := paneTurn{session: session, turn: turn, roots: map[string]*startedRoot{}}
	if root != "" {
		next.roots[root] = &startedRoot{done: closedChan(), marker: err != nil}
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
	last := paneTurn{session: session, roots: map[string]*startedRoot{}}

	for _, o := range open {
		last.turn = o.N
		last.roots[o.Root] = &startedRoot{done: closedChan()}
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

// lookup resolves what places says of each path, in order. A lookup that ends
// with the gate stops the rest.
func (g *gate) lookup(parent context.Context, paths []string) []placeAnswer {
	out := make([]placeAnswer, 0, len(paths))

	for _, path := range paths {
		ctx, cancel := context.WithTimeout(parent, placeBudget)
		place, err := g.places(ctx, path)

		cancel()

		switch {
		case err == nil:
			out = append(out, placeAnswer{place: place})
		case errors.Is(err, git.ErrNotCounted):
			out = append(out, placeAnswer{skip: true})
		case parent.Err() != nil:
			return out
		default:
			g.log.Warn("edit place not found", "path", path, "err", err)

			out = append(out, placeAnswer{unsure: true})
		}
	}

	return out
}

// placeAnswer is one path's lookup: a counted place, a path that does not
// count, or a failure.
type placeAnswer struct {
	place        git.Place
	skip, unsure bool
}

// edit answers an edit tool's before-event once the worktree of its file has a
// start for the turn, taking it when this is the first edit there. The answer
// goes out at the deadline whatever happens; a lookup or snapshot that runs
// later still records, as a marker.
func (g *gate) edit(parent context.Context, deadline time.Time, cur paneCurrent, ev harness.Event) {
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()

	stop := context.AfterFunc(ctx, func() { release(ev) })
	defer stop()
	defer release(ev)

	answers := g.lookup(parent, []string{ev.Edit})
	if len(answers) == 0 || answers[0].skip {
		return
	}

	// The turn start sets the pane's state last, and replaces it whole.
	select {
	case <-cur.tail:
	case <-parent.Done():
		return
	}

	if answers[0].unsure {
		g.panes.markUnsure(ev.Pane, cur.turn)

		return
	}

	root := answers[0].place.Root

	done, mine, noSnapshot, ok := g.panes.claim(ev.Pane, cur.turn, root, ctx.Err() != nil)
	if !ok {
		return
	}

	if !mine {
		select {
		case <-done:
		case <-ctx.Done():
		}

		return
	}

	if noSnapshot {
		close(done)
		release(ev)
		g.markStart(parent, root, cur)

		return
	}

	err := g.startRoot(ctx, ev.Pane, root, cur)
	if err != nil {
		g.panes.failStart(ev.Pane, cur.turn, root)
	}

	close(done)
	release(ev)

	if err != nil {
		g.log.Warn("before-picture failed", "session", cur.session, "root", root, "err", err)
		g.markStart(parent, root, cur)
	}
}

// startRoot takes the start of cur's turn in root, passing unchanged as
// startTurn does.
func (g *gate) startRoot(ctx context.Context, pane core.PaneID, root string, cur paneCurrent) error {
	_, n, watched := g.seq(ctx, root)
	key := seqKey{pane: pane, session: strings.ToLower(cur.session), root: root}
	prevN, had := g.memory.get(key)

	err := g.w.Start(ctx, root, cur.session, cur.turn, 0, watched && had && prevN == n)
	g.memory.set(key, n, err == nil && watched)

	return err
}

// changed marks the worktrees a finished Bash command changed that the turn has
// not started: Claude Code gives no before-picture of a command's changes, so
// they get a marker. Nothing is held.
func (g *gate) changed(parent context.Context, cur paneCurrent, ev harness.Event) {
	answers := g.lookup(parent, ev.Changed)

	select {
	case <-cur.tail:
	case <-parent.Done():
		return
	}

	for _, a := range answers {
		if a.skip {
			continue
		}

		if a.unsure {
			g.panes.markUnsure(ev.Pane, cur.turn)

			continue
		}

		done, mine, _, ok := g.panes.claim(ev.Pane, cur.turn, a.place.Root, true)
		if !ok {
			return
		}

		if mine {
			close(done)
			g.markStart(parent, a.place.Root, cur)
		}
	}
}

// markStart writes the start marker of cur's turn in root.
func (g *gate) markStart(parent context.Context, root string, cur paneCurrent) {
	if parent.Err() != nil {
		return
	}

	ctx, cancel := context.WithTimeout(parent, markerBudget)
	defer cancel()

	if err := g.w.Mark(ctx, root, cur.session, cur.turn, false); err != nil {
		g.log.Warn("start marker failed", "session", cur.session, "root", root, "err", err)
	}
}

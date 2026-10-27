package git

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// rootBatchBuffer is the capacity of WatchSet.Batches. Only failed-acquire
// reports are dropped when it is full; watcher batches wait for a reader.
const rootBatchBuffer = 16

// ErrWatchSetClosed is returned by Acquire after Close.
var ErrWatchSetClosed = errors.New("watch set closed")

// RootBatch is one watcher batch, or the report of a watch that failed to
// start (Err set), tagged with the worktree root it belongs to.
type RootBatch struct {
	Root  string
	Batch Batch
	Err   error
}

// WatchSet owns one Watcher per worktree root, shared by every holder and
// reference-counted. Its batches arrive on one channel.
type WatchSet struct {
	ctx    context.Context //nolint:containedctx // bounds every watcher the set starts
	cancel context.CancelFunc
	opts   WatchOptions
	out    chan RootBatch

	wg sync.WaitGroup // forwarders, teardowns and watchers being started

	mu      sync.Mutex
	closed  bool
	entries map[string]*watchEntry
	closing map[string]*watchEntry // released entries still being torn down
	lastSeq map[string]uint64      // highest Seq seen per root, so a restart never repeats one
}

type watchEntry struct {
	root string

	// Guarded by WatchSet.mu except where noted.
	refs int
	w    *Watcher
	err  error

	ready   chan struct{} // closed when w or err is set
	stop    chan struct{} // closed to end the forwarder
	fwdDone chan struct{} // closed when the forwarder returned
	gone    chan struct{} // closed when teardown finished
}

// NewWatchSet returns an empty set. Watchers use opts, except SeqBase, and
// stop when ctx ends or Close is called.
func NewWatchSet(ctx context.Context, opts WatchOptions) *WatchSet {
	ctx, cancel := context.WithCancel(ctx)

	return &WatchSet{
		ctx:     ctx,
		cancel:  cancel,
		opts:    opts,
		out:     make(chan RootBatch, rootBatchBuffer),
		entries: map[string]*watchEntry{},
		closing: map[string]*watchEntry{},
		lastSeq: map[string]uint64{},
	}
}

// Batches delivers the batches of every watched root. It is closed by Close.
// A failed Acquire also sends a batch with Err set; that report is dropped when
// the channel is full, since Acquire returned the same error.
func (s *WatchSet) Batches() <-chan RootBatch { return s.out }

// Seq returns the change counter of the watcher for root, and false when root
// is not watched.
func (s *WatchSet) Seq(root string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e := s.entries[root]
	if e == nil || e.w == nil || s.closed || s.ctx.Err() != nil {
		return 0, false
	}

	return e.w.Seq(), true
}

// Acquire holds a watcher for r.Root, starting one when none exists. The
// returned release is idempotent and safe to call after Close; the last release
// stops the watcher. On failure it returns the error and sends one RootBatch
// with Err set.
func (s *WatchSet) Acquire(ctx context.Context, r Repo) (release func(), err error) {
	for {
		s.mu.Lock()

		if s.closed {
			s.mu.Unlock()

			return nil, ErrWatchSetClosed
		}

		if old := s.closing[r.Root]; old != nil {
			s.mu.Unlock()

			select {
			case <-old.gone:
				continue
			case <-ctx.Done():
				return nil, fmt.Errorf("watch %s: %w", r.Root, ctx.Err())
			}
		}

		e := s.entries[r.Root]
		creator := e == nil

		if creator {
			e = &watchEntry{
				root: r.Root, ready: make(chan struct{}), stop: make(chan struct{}),
				fwdDone: make(chan struct{}), gone: make(chan struct{}),
			}
			s.entries[r.Root] = e
			s.wg.Add(1)
		}

		e.refs++
		s.mu.Unlock()

		if creator {
			s.start(e, r)
		}

		select {
		case <-e.ready:
		case <-ctx.Done():
			s.unref(e)

			return nil, fmt.Errorf("watch %s: %w", r.Root, ctx.Err())
		}

		s.mu.Lock()
		failed := e.err
		s.mu.Unlock()

		if failed != nil {
			s.report(r.Root, failed)

			return nil, failed
		}

		var once sync.Once

		return func() { once.Do(func() { s.unref(e) }) }, nil
	}
}

// start runs Watch for e, whose creator holds one wg count.
func (s *WatchSet) start(e *watchEntry, r Repo) {
	defer s.wg.Done()

	s.mu.Lock()
	opts := s.opts
	opts.SeqBase = s.lastSeq[r.Root] + 1
	closed := s.closed
	s.mu.Unlock()

	var (
		w   *Watcher
		err error
	)

	if closed {
		err = ErrWatchSetClosed
	} else {
		w, err = Watch(s.ctx, r, opts)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err == nil && s.closed {
		_ = w.Close()

		w, err = nil, ErrWatchSetClosed
	}

	if err != nil {
		e.err = err
		delete(s.entries, e.root)
		close(e.ready)

		return
	}

	e.w = w

	s.wg.Add(1)

	go s.forward(e)

	close(e.ready)
}

// forward relays e's batches until its watcher closes or the entry is stopped.
func (s *WatchSet) forward(e *watchEntry) {
	defer s.wg.Done()
	defer close(e.fwdDone)

	batches, errs := e.w.Batches(), e.w.Errors()

	for batches != nil {
		select {
		case b, ok := <-batches:
			if !ok {
				return
			}

			select {
			case s.out <- RootBatch{Root: e.root, Batch: b}:
			case <-e.stop:
				return
			case <-s.ctx.Done():
				return
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil

				continue
			}

			s.report(e.root, err)
		case <-e.stop:
			return
		}
	}
}

// report sends an error batch without ever blocking.
func (s *WatchSet) report(root string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	select {
	case s.out <- RootBatch{Root: root, Err: err}:
	default:
	}
}

func (s *WatchSet) unref(e *watchEntry) {
	s.mu.Lock()
	e.refs--

	if e.refs > 0 || s.closed || s.entries[e.root] != e || e.w == nil {
		s.mu.Unlock()

		return
	}

	delete(s.entries, e.root)
	s.closing[e.root] = e
	s.wg.Add(1)
	s.mu.Unlock()

	s.teardown(e)
}

// teardown stops e's forwarder and watcher and records the final Seq.
func (s *WatchSet) teardown(e *watchEntry) {
	defer s.wg.Done()

	close(e.stop)

	_ = e.w.Close()

	<-e.fwdDone

	s.mu.Lock()
	s.lastSeq[e.root] = max(s.lastSeq[e.root], e.w.Seq())
	delete(s.closing, e.root)
	s.mu.Unlock()

	close(e.gone)
}

// Close stops every watcher, waits for their goroutines and closes Batches. It
// is safe to call more than once.
func (s *WatchSet) Close() error {
	s.mu.Lock()

	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()

		return nil
	}

	s.closed = true
	s.cancel()

	live := make([]*watchEntry, 0, len(s.entries))

	for root, e := range s.entries {
		if e.w != nil {
			live = append(live, e)
			s.closing[root] = e
		}
	}

	s.entries = map[string]*watchEntry{}
	s.wg.Add(len(live))
	s.mu.Unlock()

	for _, e := range live {
		go s.teardown(e)
	}

	s.wg.Wait()

	s.mu.Lock()
	close(s.out)
	s.mu.Unlock()

	return nil
}

package git

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	placeTTL = time.Minute // how long an answer is kept
	placeMax = 512         // most answers kept; the oldest goes first
)

// PlaceCache answers EditPlace for many callers: it keeps counted places and
// ErrNotCounted answers for a minute, and makes lookups of one path in flight
// share one resolution. Its zero value is not usable; make one with NewPlaceCache.
type PlaceCache struct {
	resolve func(context.Context, string) (Place, error)
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]placeEntry
	flights map[string]*placeFlight
	seq     uint64 // orders the entries by when they were stored
}

type placeEntry struct {
	place Place
	err   error // nil or ErrNotCounted
	at    time.Time
	seq   uint64
}

var errResolveAborted = errors.New("git: the place lookup did not finish")

// placeFlight is a lookup in flight; done closes once place and err are set.
type placeFlight struct {
	done  chan struct{}
	place Place
	err   error
}

// NewPlaceCache makes a cache over resolve, normally EditPlace, whose entries
// age by now.
func NewPlaceCache(resolve func(context.Context, string) (Place, error), now func() time.Time) *PlaceCache {
	return &PlaceCache{
		resolve: resolve, now: now,
		entries: map[string]placeEntry{}, flights: map[string]*placeFlight{},
	}
}

// Place is EditPlace through the cache. A kept place whose root is no longer a
// directory is a miss, so a removed worktree resolves afresh. It stats the
// root, so call it off the update loop.
func (c *PlaceCache) Place(ctx context.Context, path string) (Place, error) {
	for {
		c.mu.Lock()
		stored := c.seq
		c.mu.Unlock()

		if e, ok := c.kept(path); ok {
			return e.place, e.err
		}

		c.mu.Lock()

		if f, ok := c.flights[path]; ok {
			c.mu.Unlock()

			select {
			case <-f.done:
			case <-ctx.Done():
				return Place{}, ctx.Err()
			}

			// The leader's own context ended, which says nothing about the path.
			if isContextErr(f.err) && ctx.Err() == nil {
				continue
			}

			return f.place, f.err
		}

		// A lookup that ended since the check stored its answer: look again.
		if c.seq != stored {
			c.mu.Unlock()

			continue
		}

		f := &placeFlight{done: make(chan struct{})}
		c.flights[path] = f
		c.mu.Unlock()

		return c.lead(ctx, path, f)
	}
}

// lead resolves path for f and its waiters. The flight ends even if resolve panics.
func (c *PlaceCache) lead(ctx context.Context, path string, f *placeFlight) (Place, error) {
	f.err = errResolveAborted

	defer func() {
		c.mu.Lock()
		delete(c.flights, path)

		if f.err == nil || errors.Is(f.err, ErrNotCounted) {
			c.store(path, f.place, f.err)
		}

		c.mu.Unlock()
		close(f.done)
	}()

	f.place, f.err = c.resolve(ctx, path)

	return f.place, f.err
}

// kept returns the live entry of path, dropping one that is old or whose root is gone.
func (c *PlaceCache) kept(path string) (placeEntry, bool) {
	c.mu.Lock()
	e, ok := c.entries[path]
	c.mu.Unlock()

	if !ok {
		return placeEntry{}, false
	}

	if c.now().Sub(e.at) < placeTTL && (e.err != nil || isDir(e.place.Root)) {
		return e, true
	}

	c.mu.Lock()
	if cur, ok := c.entries[path]; ok && cur.seq == e.seq {
		delete(c.entries, path)
	}
	c.mu.Unlock()

	return placeEntry{}, false
}

// store keeps an answer and drops the oldest entries beyond placeMax. The caller holds mu.
func (c *PlaceCache) store(path string, place Place, err error) {
	c.seq++
	c.entries[path] = placeEntry{place: place, err: err, at: c.now(), seq: c.seq}

	for len(c.entries) > placeMax {
		var oldest string

		var low uint64

		for p, e := range c.entries {
			if low == 0 || e.seq < low {
				oldest, low = p, e.seq
			}
		}

		delete(c.entries, oldest)
	}
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

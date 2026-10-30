package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// fakePlaces is a resolver that counts its calls and answers from a function.
type fakePlaces struct {
	mu    sync.Mutex
	calls map[string]int
	reply func(path string) (Place, error)
}

func (f *fakePlaces) resolve(_ context.Context, path string) (Place, error) {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}

	f.calls[path]++
	f.mu.Unlock()

	return f.reply(path)
}

func (f *fakePlaces) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls[path]
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = c.t.Add(d)
}

var errGit = errors.New("git failed")

func TestPlaceCache(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		reply func(root string) func(string) (Place, error)
		step  func(t *testing.T, c *PlaceCache, clk *testClock, root string)
		want  int // resolutions of "/f" in all
	}{
		{
			name: "a counted place is answered once",
			reply: func(root string) func(string) (Place, error) {
				return func(string) (Place, error) { return Place{Root: root}, nil }
			},
			step: func(t *testing.T, c *PlaceCache, _ *testClock, root string) {
				t.Helper()

				for range 3 {
					p, err := c.Place(context.Background(), "/f")
					if err != nil || p.Root != root {
						t.Fatalf("Place = %+v, %v, want root %q", p, err, root)
					}
				}
			},
			want: 1,
		},
		{
			name: "another error is not kept",
			reply: func(string) func(string) (Place, error) {
				return func(string) (Place, error) { return Place{}, errGit }
			},
			step: func(t *testing.T, c *PlaceCache, _ *testClock, _ string) {
				t.Helper()

				for range 3 {
					if _, err := c.Place(context.Background(), "/f"); !errors.Is(err, errGit) {
						t.Fatalf("err = %v, want errGit", err)
					}
				}
			},
			want: 3,
		},
		{
			name: "an entry is a miss after a minute",
			reply: func(root string) func(string) (Place, error) {
				return func(string) (Place, error) { return Place{Root: root}, nil }
			},
			step: func(t *testing.T, c *PlaceCache, clk *testClock, _ string) {
				t.Helper()

				ctx := context.Background()
				_, _ = c.Place(ctx, "/f")

				clk.advance(placeTTL - time.Second)

				_, _ = c.Place(ctx, "/f")

				clk.advance(time.Second)

				_, _ = c.Place(ctx, "/f")
			},
			want: 2,
		},
		{
			name: "a removed root is a miss",
			reply: func(root string) func(string) (Place, error) {
				return func(string) (Place, error) { return Place{Root: root}, nil }
			},
			step: func(t *testing.T, c *PlaceCache, _ *testClock, root string) {
				t.Helper()

				ctx := context.Background()
				_, _ = c.Place(ctx, "/f")

				if err := os.Remove(root); err != nil {
					t.Fatal(err)
				}

				_, _ = c.Place(ctx, "/f")
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			res := &fakePlaces{reply: tt.reply(root)}
			clk := &testClock{t: time.Unix(1000, 0)}
			c := NewPlaceCache(res.resolve, clk.now)

			tt.step(t, c, clk, root)

			if got := res.count("/f"); got != tt.want {
				t.Errorf("resolutions = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPlaceCacheKeepsNotCounted(t *testing.T) {
	t.Parallel()

	res := &fakePlaces{reply: func(path string) (Place, error) {
		return Place{}, fmt.Errorf("edit %s: %w", path, ErrNotCounted)
	}}
	clk := &testClock{t: time.Unix(1000, 0)}
	c := NewPlaceCache(res.resolve, clk.now)

	for range 5 {
		if _, err := c.Place(context.Background(), "/ledger.log"); !errors.Is(err, ErrNotCounted) {
			t.Fatalf("err = %v, want ErrNotCounted", err)
		}
	}

	if got := res.count("/ledger.log"); got != 1 {
		t.Errorf("resolutions = %d, want 1", got)
	}

	clk.advance(placeTTL)

	_, _ = c.Place(context.Background(), "/ledger.log")

	if got := res.count("/ledger.log"); got != 2 {
		t.Errorf("resolutions after the minute = %d, want 2", got)
	}
}

func TestPlaceCacheDropsTheOldest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	res := &fakePlaces{reply: func(string) (Place, error) { return Place{Root: root}, nil }}
	clk := &testClock{t: time.Unix(1000, 0)}
	c := NewPlaceCache(res.resolve, clk.now)
	ctx := context.Background()

	for i := range placeMax {
		_, _ = c.Place(ctx, fmt.Sprintf("/f%d", i))
	}

	// The 513th path pushes out the first and leaves the second.
	_, _ = c.Place(ctx, "/extra")
	_, _ = c.Place(ctx, "/f1")
	_, _ = c.Place(ctx, "/f0")

	if got := res.count("/f1"); got != 1 {
		t.Errorf("/f1 resolved %d times, want 1: it should still be kept", got)
	}

	if got := res.count("/f0"); got != 2 {
		t.Errorf("/f0 resolved %d times, want 2: it was the oldest", got)
	}
}

// joinCtx is a context whose Done is first asked for by a caller that parks on
// a lookup in flight, which Place does only inside its join; waiting closes then.
type joinCtx struct {
	context.Context //nolint:containedctx // wraps a parent to observe Done

	once    sync.Once
	waiting chan struct{}
}

func newJoinCtx(parent context.Context) *joinCtx {
	return &joinCtx{Context: parent, waiting: make(chan struct{})}
}

func (c *joinCtx) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })

	return c.Context.Done()
}

func TestPlaceCacheJoinsInFlight(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	entered, release := make(chan struct{}, 4), make(chan struct{})

	res := &fakePlaces{}
	res.reply = func(string) (Place, error) {
		entered <- struct{}{}

		<-release

		return Place{Root: root}, nil
	}

	c := NewPlaceCache(res.resolve, (&testClock{t: time.Unix(1000, 0)}).now)

	type answer struct {
		place Place
		err   error
	}

	answers := make(chan answer, 2)
	ask := func(ctx context.Context) {
		go func() {
			p, err := c.Place(ctx, "/f")
			answers <- answer{p, err}
		}()
	}

	ask(context.Background())
	<-entered

	second := newJoinCtx(context.Background())

	ask(second)
	<-second.waiting
	close(release)

	for range 2 {
		if a := <-answers; a.err != nil || a.place.Root != root {
			t.Errorf("answer = %+v, %v, want root %q", a.place, a.err, root)
		}
	}

	if got := res.count("/f"); got != 1 {
		t.Errorf("resolutions = %d, want 1", got)
	}
}

func TestPlaceCacheResolvesAfterTheLeaderIsCancelled(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	leaderIn := make(chan struct{})

	res := &fakePlaces{}
	res.reply = func(string) (Place, error) { return Place{Root: root}, nil }

	var first sync.Once

	cache := NewPlaceCache(func(ctx context.Context, path string) (Place, error) {
		var leader bool

		first.Do(func() { leader = true })

		if leader {
			close(leaderIn)

			<-ctx.Done()

			return Place{}, ctx.Err()
		}

		return res.resolve(ctx, path)
	}, (&testClock{t: time.Unix(1000, 0)}).now)

	leaderCtx, cancel := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)

	go func() {
		_, err := cache.Place(leaderCtx, "/f")
		leaderDone <- err
	}()

	<-leaderIn

	second := newJoinCtx(context.Background())
	waiter := make(chan Place, 1)

	go func() {
		p, _ := cache.Place(second, "/f")
		waiter <- p
	}()

	<-second.waiting
	cancel()

	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Errorf("leader err = %v, want context.Canceled", err)
	}

	if p := <-waiter; p.Root != root {
		t.Errorf("waiter place = %+v, want root %q", p, root)
	}

	if got := res.count("/f"); got != 1 {
		t.Errorf("waiter's own resolutions = %d, want 1", got)
	}
}

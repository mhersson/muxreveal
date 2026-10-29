package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mhersson/muxreveal/internal/github"
)

var testRepo = github.Repo{Host: "github.com", Owner: "o", Name: "r"}

// noClients refuses to build a client, so a run in a test never reaches GitHub.
func noClients(context.Context, string) (*github.Client, error) {
	return nil, errors.New("test: no GitHub client")
}

// offline is a transport that fails every request, so no test reaches GitHub.
type offline struct{ requests atomic.Int32 }

func (o *offline) RoundTrip(*http.Request) (*http.Response, error) {
	o.requests.Add(1)

	return nil, errors.New("offline")
}

func TestStartPRsBuildsThePollerWithTheFactory(t *testing.T) {
	var (
		mu    sync.Mutex
		hosts []string
	)

	failing := errors.New("stub: no client")

	p := startPRs(func(_ context.Context, host string) (*github.Client, error) {
		mu.Lock()
		defer mu.Unlock()

		hosts = append(hosts, host)

		return nil, failing
	})

	p.poller.Set([]github.Watch{{Repo: testRepo}})

	select {
	case s := <-p.poller.Updates():
		if !errors.Is(s.Err, failing) || s.Repo != testRepo {
			t.Errorf("snapshot = %+v, want the factory's error for %s", s, testRepo)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the poller never used the factory")
	}

	if err := p.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	select {
	case _, ok := <-p.poller.Updates():
		if ok {
			t.Error("Updates delivered after Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close left Updates open")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(hosts) == 0 || hosts[0] != "github.com" {
		t.Errorf("factory hosts = %v, want github.com", hosts)
	}
}

func TestPRLookupSharesTheClientCache(t *testing.T) {
	var (
		built atomic.Int32
		net   offline
	)

	p := startPRs(func(_ context.Context, host string) (*github.Client, error) {
		built.Add(1)

		return github.NewClient(host, github.Options{Token: "test-token", Transport: &net})
	})

	t.Cleanup(func() { _ = p.Close() })

	for range 2 {
		if _, err := p.lookup(t.Context(), testRepo, "feat"); err == nil || !strings.Contains(err.Error(), "offline") {
			t.Fatalf("lookup error = %v, want the transport's", err)
		}
	}

	if n := built.Load(); n != 1 {
		t.Errorf("the factory built %d clients for one host, want 1", n)
	}

	if n := net.requests.Load(); n != 2 {
		t.Errorf("the transport saw %d requests, want 2", n)
	}
}

func TestPRLookupDoesNotCacheAFailedClient(t *testing.T) {
	var built atomic.Int32

	failing := errors.New("stub: no token")

	p := startPRs(func(context.Context, string) (*github.Client, error) {
		built.Add(1)

		return nil, failing
	})

	t.Cleanup(func() { _ = p.Close() })

	for range 2 {
		if _, err := p.lookup(t.Context(), testRepo, "feat"); !errors.Is(err, failing) {
			t.Fatalf("lookup error = %v, want the factory's", err)
		}
	}

	if n := built.Load(); n != 2 {
		t.Errorf("the factory ran %d times, want 2: a failure is not kept", n)
	}
}

type closeLog struct {
	mu     sync.Mutex
	events []string
}

func (l *closeLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.events = append(l.events, e)
}

type stubApp struct {
	log *closeLog
	err error
}

func (a stubApp) Close(context.Context) error {
	a.log.add("app")

	return a.err
}

type stubPRs struct{ log *closeLog }

func (p stubPRs) Close() error {
	p.log.add("poller")

	return nil
}

func TestCloseRunClosesThePollerAfterTheApp(t *testing.T) {
	for _, appErr := range []error{nil, errors.New("close panes: deadline")} {
		var l closeLog

		closeRun(t.Context(), discardLogger(), stubApp{log: &l, err: appErr}, stubPRs{log: &l})

		if got := strings.Join(l.events, ","); got != "app,poller" {
			t.Errorf("closed %q with app error %v, want the app first and the poller after", got, appErr)
		}
	}
}

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/mhersson/muxreveal/internal/github"
)

// clientFactory builds the GitHub client of a host.
type clientFactory func(ctx context.Context, host string) (*github.Client, error)

// githubClients builds a client with the host's token from the environment,
// gh's configuration or `gh auth token`.
func githubClients(ctx context.Context, host string) (*github.Client, error) {
	token, err := github.Token(ctx, host, github.TokenOptions{})
	if err != nil {
		return nil, err
	}

	return github.NewClient(host, github.Options{Token: token})
}

// clientCache keeps the first client built for each host, so the poller and the
// branch lookups ask the factory (which may run gh) once per host. A failure is
// not kept.
type clientCache struct {
	factory clientFactory

	mu      sync.Mutex
	clients map[string]*github.Client
}

// client returns the kept client for host, or builds one. The lock is not held
// while the factory runs; a lost race keeps the first client.
func (c *clientCache) client(ctx context.Context, host string) (*github.Client, error) {
	c.mu.Lock()
	kept := c.clients[host]
	c.mu.Unlock()

	if kept != nil {
		return kept, nil
	}

	built, err := c.factory(ctx, host)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if kept := c.clients[host]; kept != nil {
		return kept, nil
	}

	c.clients[host] = built

	return built, nil
}

// prRuntime is the pull request poller and the branch lookup, built on one
// client cache.
type prRuntime struct {
	poller  *github.Poller
	clients *clientCache
}

// startPRs builds a poller that watches nothing until the app gives it a set.
func startPRs(clients clientFactory) *prRuntime {
	cache := &clientCache{factory: clients, clients: map[string]*github.Client{}}

	return &prRuntime{poller: github.NewPoller(github.PollerOptions{Client: cache.client}), clients: cache}
}

// lookup finds the pull request of a branch with the cached client of its host.
func (p *prRuntime) lookup(ctx context.Context, r github.Repo, branch string) (github.PR, error) {
	c, err := p.clients.client(ctx, r.Host)
	if err != nil {
		return github.PR{}, fmt.Errorf("client for %s: %w", r.Host, err)
	}

	return c.PRForBranch(ctx, r, branch)
}

// Close stops the poller: its requests are cancelled, its goroutines end and
// its updates close. It is safe to repeat.
func (p *prRuntime) Close() error { return p.poller.Close() }

// closeRun closes the app, then the poller. The app's context ends what the
// app started (repository resolutions and branch lookups) first, so nothing
// asks a poller that is gone.
func closeRun(ctx context.Context, logger *slog.Logger, app interface{ Close(context.Context) error }, prs io.Closer) {
	if err := app.Close(ctx); err != nil {
		logger.Error("close panes", "err", err)
	}

	if err := prs.Close(); err != nil {
		logger.Warn("close poller", "err", err)
	}
}

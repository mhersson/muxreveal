// Package github resolves a worktree's GitHub repository and token the way gh
// does and talks to the GitHub API.
package github

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/auth"
)

const (
	githubHost = "github.com"
	sshTimeout = 5 * time.Second
)

var (
	ErrNotGitHub = errors.New("github: not a GitHub repository")
	ErrNoToken   = errors.New("github: no token")
)

// Repo identifies a repository on a GitHub host.
type Repo struct{ Host, Owner, Name string }

func (r Repo) String() string { return r.Host + "/" + r.Owner + "/" + r.Name }

// remote is a parsed remote URL before the host is normalised; viaSSH marks
// forms whose host may be an ssh config alias.
type remote struct {
	host, owner, name string
	viaSSH            bool
}

// ParseRemoteURL reads host, owner and name from a remote URL. Errors never
// echo the URL, which may carry credentials.
func ParseRemoteURL(raw string) (Repo, error) {
	p, err := parseRemote(raw)
	if err != nil {
		return Repo{}, err
	}

	return Repo{Host: auth.NormalizeHostname(p.host), Owner: p.owner, Name: p.name}, nil
}

// repoPart is what an owner or a repository name may contain; it keeps the
// userinfo of a malformed scp-like URL out of the owner.
var repoPart = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func errUnsupported() error {
	return fmt.Errorf("%w: unsupported remote URL", ErrNotGitHub)
}

func parseRemote(raw string) (remote, error) {
	var (
		host, path string
		viaSSH     bool
	)

	if scheme, _, ok := strings.Cut(raw, "://"); ok {
		switch strings.ToLower(scheme) {
		case "https", "http", "git":
		case "ssh":
			viaSSH = true
		default:
			return remote{}, errUnsupported()
		}

		u, err := url.Parse(raw)
		if err != nil {
			return remote{}, errUnsupported()
		}

		host, path = u.Hostname(), u.Path
	} else {
		// scp-like: a slash before the first colon makes it a local path.
		before, after, ok := strings.Cut(raw, ":")
		if !ok || strings.Contains(before, "/") {
			return remote{}, errUnsupported()
		}

		if _, h, found := strings.Cut(before, "@"); found {
			before = h
		}

		host, path, viaSSH = before, after, true
	}

	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsAny(host, " \t\n") {
		return remote{}, errUnsupported()
	}

	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, ".git")

	owner, name, ok := strings.Cut(path, "/")
	if !ok || !repoPart.MatchString(owner) || !repoPart.MatchString(name) {
		return remote{}, errUnsupported()
	}

	return remote{host: host, owner: owner, name: name, viaSSH: viaSSH}, nil
}

// ResolverOptions configure a Resolver.
type ResolverOptions struct {
	SSH        string          // the ssh binary; "" = "ssh" on PATH
	KnownHosts func() []string // nil = auth.KnownHosts
}

// Resolver turns remote URLs into repositories, mapping ssh aliases through
// ssh itself and accepting only github.com and hosts gh knows.
type Resolver struct {
	ssh        string
	knownHosts func() []string

	mu      sync.Mutex
	aliases map[string]string
}

func NewResolver(o ResolverOptions) *Resolver {
	r := &Resolver{ssh: o.SSH, knownHosts: o.KnownHosts, aliases: map[string]string{}}
	if r.ssh == "" {
		r.ssh = "ssh"
	}

	if r.knownHosts == nil {
		r.knownHosts = auth.KnownHosts
	}

	return r
}

// Resolve returns the repository a remote URL points at, or ErrNotGitHub. A
// context that ends while ssh runs is an error of its own, never ErrNotGitHub.
func (r *Resolver) Resolve(ctx context.Context, rawURL string) (Repo, error) {
	p, err := parseRemote(rawURL)
	if err != nil {
		return Repo{}, err
	}

	if p.viaSSH {
		if p.host, err = r.hostname(ctx, p.host); err != nil {
			return Repo{}, err
		}
	}

	repo := Repo{Host: auth.NormalizeHostname(p.host), Owner: p.owner, Name: p.name}
	if repo.Host == githubHost {
		return repo, nil
	}

	for _, h := range r.knownHosts() {
		if auth.NormalizeHostname(h) == repo.Host {
			return repo, nil
		}
	}

	return Repo{}, fmt.Errorf("%w: %s", ErrNotGitHub, repo)
}

// hostname maps an ssh alias to its real host; on any other failure than an
// ended context the alias stays.
func (r *Resolver) hostname(ctx context.Context, alias string) (string, error) {
	r.mu.Lock()
	if h, ok := r.aliases[alias]; ok {
		r.mu.Unlock()

		return h, nil
	}
	r.mu.Unlock()

	h, err := r.sshHostname(ctx, alias)
	if err != nil {
		return "", err
	}

	if h == "" {
		return alias, nil
	}

	r.mu.Lock()
	r.aliases[alias] = h
	r.mu.Unlock()

	return h, nil
}

// sshHostname is the hostname ssh -G gives alias, or "" when ssh fails or
// gives none; only a context that ended is an error.
func (r *Resolver) sshHostname(ctx context.Context, alias string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, sshTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.ssh, "-G", "--", alias) //nolint:gosec // "--" ends option parsing, so the alias is never read as a flag
	cmd.WaitDelay = time.Second

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("github: ssh -G %s did not answer: %w", alias, ctx.Err())
		}

		return "", nil
	}

	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), " "); ok && strings.EqualFold(k, "hostname") {
			return strings.TrimSpace(v), nil
		}
	}

	return "", nil
}

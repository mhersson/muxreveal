package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNoRemote means the named remote is not configured.
var ErrNoRemote = errors.New("git: no such remote")

// errNotConfigured carries no remote text: the value may be a URL with credentials.
var errNotConfigured = fmt.Errorf("push remote is not a configured remote: %w", ErrNoRemote)

// configGet reads one config value; ok is false when it is unset.
func configGet(ctx context.Context, r Repo, key string) (value string, ok bool, err error) {
	out, err := run(ctx, call{dir: r.Root, args: []string{"config", "--get", key}, read: true})
	if err != nil {
		if isExitCode(err, 1) {
			return "", false, nil
		}

		return "", false, fmt.Errorf("read config %s: %w", key, err)
	}

	return strings.TrimSpace(string(out)), true, nil
}

// HasUpstream reports whether branch has an upstream configured, whether or not
// its remote-tracking ref exists. Push sets an upstream exactly when this is false.
func HasUpstream(ctx context.Context, r Repo, branch string) (bool, error) {
	if err := checkBranch(ctx, r, branch); err != nil {
		return false, err
	}

	return upstreamSet(ctx, r, branch)
}

// upstreamSet is the config test HasUpstream and Push share, for a branch that
// is already validated.
func upstreamSet(ctx context.Context, r Repo, branch string) (bool, error) {
	_, ok, err := configGet(ctx, r, "branch."+branch+".merge")

	return ok, err
}

// PushRemote names the remote a push of branch goes to: branch.<b>.pushRemote,
// else remote.pushDefault, else branch.<b>.remote (unless "."), else origin.
// It must be a configured remote.
func PushRemote(ctx context.Context, r Repo, branch string) (string, error) {
	if err := checkBranch(ctx, r, branch); err != nil {
		return "", err
	}

	remote := "origin"

	for _, key := range []string{"branch." + branch + ".pushRemote", "remote.pushDefault", "branch." + branch + ".remote"} {
		v, ok, err := configGet(ctx, r, key)
		if err != nil {
			return "", err
		}

		if ok && v != "" && v != "." {
			remote = v

			break
		}
	}

	return configuredRemote(ctx, r, remote)
}

// configuredRemote returns remote when it names a configured remote, else
// ErrNoRemote without the value. A URL or an option-like name is never one.
func configuredRemote(ctx context.Context, r Repo, remote string) (string, error) {
	if strings.HasPrefix(remote, "-") {
		return "", errNotConfigured
	}

	out, err := run(ctx, call{dir: r.Root, args: []string{"remote"}, read: true})
	if err != nil {
		return "", fmt.Errorf("list remotes: %w", err)
	}

	for _, name := range strings.Split(string(out), "\n") {
		if name != "" && name == remote {
			return remote, nil
		}
	}

	return "", errNotConfigured
}

// RemoteURL returns the URL git would use for remote, with url.<base>.insteadOf
// applied. The URL may carry credentials, so never put it in an error or log.
func RemoteURL(ctx context.Context, r Repo, remote string) (string, error) {
	if remote == "" || strings.HasPrefix(remote, "-") {
		return "", errNotConfigured
	}

	out, err := run(ctx, call{dir: r.Root, args: []string{"remote", "get-url", remote}, read: true})
	if err != nil {
		if isExitCode(err, 2) {
			return "", errNotConfigured
		}

		return "", fmt.Errorf("remote url: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// PushURLs returns every URL a push to remote goes to, one per pushurl, with
// url.<base>.pushInsteadOf and insteadOf applied. Without a pushurl it is the
// fetch URL. The URLs may carry credentials, so never put them in an error or log.
func PushURLs(ctx context.Context, r Repo, remote string) ([]string, error) {
	if remote == "" || strings.HasPrefix(remote, "-") {
		return nil, errNotConfigured
	}

	out, err := run(ctx, call{dir: r.Root, args: []string{"remote", "get-url", "--push", "--all", remote}, read: true})
	if err != nil {
		if isExitCode(err, 2) {
			return nil, errNotConfigured
		}

		return nil, fmt.Errorf("push urls: %w", err)
	}

	var urls []string

	for line := range strings.SplitSeq(string(out), "\n") {
		if line != "" {
			urls = append(urls, line)
		}
	}

	return urls, nil
}

// RemoteHasBranch reports whether the remote-tracking ref of branch on remote
// exists, which says nothing about the remote itself until a fetch or push.
func RemoteHasBranch(ctx context.Context, r Repo, remote, branch string) (bool, error) {
	if remote == "" || strings.HasPrefix(remote, "-") {
		return false, errNotConfigured
	}

	if err := checkBranch(ctx, r, branch); err != nil {
		return false, err
	}

	_, err := run(ctx, call{dir: r.Root, args: []string{"rev-parse", "--verify", "--quiet", "refs/remotes/" + remote + "/" + branch}, read: true})
	if err != nil {
		if isExitCode(err, 1) {
			return false, nil
		}

		return false, fmt.Errorf("remote branch: %w", err)
	}

	return true, nil
}

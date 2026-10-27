package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrRejected means the remote refused the push, e.g. a non-fast-forward.
var ErrRejected = errors.New("git: push rejected")

// PushResult reports what one push did.
type PushResult struct {
	Remote, Branch string
	Dst            string // the remote ref pushed to
	Created        bool   // the remote branch is new
	UpToDate       bool
	SetUpstream    bool
}

// Push sends branch to its push remote, never forcing, always to a remote
// branch of the same name (git's simple rule), so a branch cut from origin/main
// cannot land on main. The upstream is set only when the branch has none.
func Push(ctx context.Context, r Repo, branch string) (PushResult, error) {
	remote, err := PushRemote(ctx, r, branch)
	if err != nil {
		return PushResult{}, err
	}

	_, hasUpstream, err := configGet(ctx, r, "branch."+branch+".merge")
	if err != nil {
		return PushResult{}, err
	}

	res := PushResult{Remote: remote, Branch: branch, Dst: "refs/heads/" + branch}
	args := []string{"push", "--porcelain"}

	if !hasUpstream {
		res.SetUpstream = true

		args = append(args, "--set-upstream")
	}

	args = append(args, remote, "refs/heads/"+branch+":"+res.Dst)

	out, err := run(ctx, call{dir: r.Root, args: args, env: []string{"LC_ALL=C"}, net: true})
	status, summary, found := parsePorcelain(string(out), res.Dst)

	switch {
	case found && status == '!':
		return PushResult{}, fmt.Errorf("push %s to %s: %w %s", branch, remote, ErrRejected, summary)
	case err != nil:
		return PushResult{}, fmt.Errorf("push %s to %s: %w", branch, remote, err)
	}

	res.Created = found && status == '*'
	res.UpToDate = found && status == '='

	return res, nil
}

// parsePorcelain finds the ref line for dst in `git push --porcelain` output:
// "<flag>\t<src>:<dst>\t<summary>". summary is git's text after the bracketed
// reason, e.g. "(non-fast-forward)".
func parsePorcelain(out, dst string) (status byte, summary string, found bool) {
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || len(parts[0]) != 1 {
			continue
		}

		_, to, _ := strings.Cut(parts[1], ":")
		if to != dst {
			continue
		}

		summary = parts[2]
		if _, after, ok := strings.Cut(summary, "] "); ok {
			summary = after
		}

		return parts[0][0], strings.TrimSpace(summary), true
	}

	return 0, "", false
}

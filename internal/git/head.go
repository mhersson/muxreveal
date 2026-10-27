package git

import (
	"context"
	"fmt"
	"strings"
)

// HeadInfo is what a worktree's HEAD points at. A detached HEAD has an empty
// Branch; an unborn branch has an empty OID.
type HeadInfo struct {
	Branch   string // without refs/heads/
	Detached bool
	OID      string // full commit id
}

// Head reports the branch or detached commit checked out in r.
func Head(ctx context.Context, r Repo) (HeadInfo, error) {
	var h HeadInfo

	out, err := run(ctx, call{dir: r.Root, args: []string{"symbolic-ref", "--quiet", "HEAD"}, read: true})

	switch {
	case err == nil:
		h.Branch = strings.TrimPrefix(strings.TrimSpace(string(out)), "refs/heads/")
	case isExitCode(err, 1):
		h.Detached = true
	default:
		return HeadInfo{}, fmt.Errorf("head: %w", err)
	}

	if !h.Detached {
		// An absent ref is an unborn branch; a ref that fails to resolve is an error.
		_, err := run(ctx, call{dir: r.Root, args: []string{"show-ref", "--verify", "--quiet", "refs/heads/" + h.Branch}, read: true})
		if isExitCode(err, 1) {
			return h, nil
		}

		if err != nil {
			return HeadInfo{}, fmt.Errorf("head: %w", err)
		}
	}

	out, err = run(ctx, call{dir: r.Root, args: []string{"rev-parse", "--verify", "HEAD^{commit}"}, read: true})
	if err != nil {
		return HeadInfo{}, fmt.Errorf("head: %w", err)
	}

	h.OID = strings.TrimSpace(string(out))

	return h, nil
}

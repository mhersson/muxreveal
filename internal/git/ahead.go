package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Ahead counts the commits HEAD has that its upstream lacks. ok is false when
// there is nothing to compare with: a detached HEAD, an unborn branch, no
// upstream, or an upstream ref that is gone.
func Ahead(ctx context.Context, r Repo) (n int, ok bool, err error) {
	out, err := run(ctx, call{dir: r.Root, args: []string{"rev-list", "--count", "@{upstream}..HEAD"}, read: true})
	if err != nil {
		if !isExitCode(err, 128) {
			return 0, false, fmt.Errorf("ahead: %w", err)
		}

		// git words "no upstream" as a localized fatal error; rev-parse says it with exit 1.
		if _, verr := run(ctx, call{dir: r.Root, args: []string{"rev-parse", "--verify", "--quiet", "@{upstream}"}, read: true}); isExitCode(verr, 1) {
			return 0, false, nil
		}

		return 0, false, fmt.Errorf("ahead: %w", err)
	}

	n, err = strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false, fmt.Errorf("ahead: count %q: %w", strings.TrimSpace(string(out)), err)
	}

	return n, true, nil
}

// Unpushed counts the commits of HEAD that no remote-tracking branch of remote
// has, which is every commit before a first push.
func Unpushed(ctx context.Context, r Repo, remote string) (int, error) {
	if remote == "" || strings.HasPrefix(remote, "-") {
		return 0, errNotConfigured
	}

	out, err := run(ctx, call{dir: r.Root, args: []string{"rev-list", "--count", "HEAD", "--not", "--remotes=" + remote}, read: true})
	if err != nil {
		return 0, fmt.Errorf("unpushed: %w", err)
	}

	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("unpushed: count %q: %w", strings.TrimSpace(string(out)), err)
	}

	return n, nil
}

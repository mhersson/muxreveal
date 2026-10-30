package git

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	// ErrWorktreeDirty means git refused to remove a worktree with modified or untracked files.
	ErrWorktreeDirty = errors.New("git: worktree has uncommitted changes")
	// ErrNoCommit means the commit is not in this repository.
	ErrNoCommit = errors.New("git: commit not in this repository")
	// ErrBranchMoved means the branch tip is not the one the caller saw.
	ErrBranchMoved = errors.New("git: branch moved")
)

// ManagedWorktree reports whether path sits directly under one of the
// repository's worktree roots, inside or sibling, with symlinks resolved.
// The main checkout is not one, and neither is a path whose parent is missing.
func ManagedWorktree(r Repo, path string) bool {
	parent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(path)))
	if err != nil {
		return false
	}

	for _, style := range []RootStyle{RootInside, RootSibling} {
		root, err := worktreeRoot(r, style)
		if err != nil {
			continue
		}

		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}

		if parent == root {
			return true
		}
	}

	return false
}

// WorktreeChanges counts the entries `git status` reports in dir: modified,
// staged and untracked paths, a rename or copy once.
func WorktreeChanges(ctx context.Context, dir string) (int, error) {
	out, err := run(ctx, call{dir: dir, args: []string{"status", "--porcelain=v1", "-z", "--untracked-files=normal"}, read: true})
	if err != nil {
		return 0, fmt.Errorf("count changes in %s: %w", dir, err)
	}

	var n int

	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 3 {
			continue
		}

		n++

		if strings.ContainsAny(entry[:2], "RC") {
			i++ // the second path field
		}
	}

	return n, nil
}

// RemoveWorktree runs `git worktree remove` for path from the main checkout
// (the common dir for a bare repository). It is never forced, so git refuses a
// worktree with changes (ErrWorktreeDirty) or a locked one.
func RemoveWorktree(ctx context.Context, r Repo, path string) error {
	dir := r.CommonDir
	if main, err := r.MainRoot(); err == nil {
		dir = main
	}

	_, err := run(ctx, call{dir: dir, args: []string{"worktree", "remove", "--", path}, env: []string{"LC_ALL=C"}})
	if err == nil {
		return nil
	}

	if strings.Contains(err.Error(), "contains modified or untracked files") {
		return fmt.Errorf("remove worktree %s: %w (%w)", path, ErrWorktreeDirty, err)
	}

	return fmt.Errorf("remove worktree %s: %w", path, err)
}

// branchTip is the commit refs/heads/<branch> points at.
func branchTip(ctx context.Context, r Repo, branch string) (string, error) {
	out, err := run(ctx, call{dir: r.Root, args: []string{"rev-parse", "--verify", "--quiet", "refs/heads/" + branch + "^{commit}"}, read: true})
	if err != nil {
		return "", fmt.Errorf("tip of %s: %w", branch, err)
	}

	return strings.TrimSpace(string(out)), nil
}

// BranchAhead returns the tip of branch and how many commits it has that head
// does not. A tip equal to head needs no object; a head that is not a commit
// here is ErrNoCommit.
func BranchAhead(ctx context.Context, r Repo, branch, head string) (tip string, ahead int, err error) {
	tip, err = branchTip(ctx, r, branch)
	if err != nil {
		return "", 0, err
	}

	if tip == head {
		return tip, 0, nil
	}

	if _, err := run(ctx, call{dir: r.Root, args: []string{"cat-file", "-e", head + "^{commit}"}, read: true}); err != nil {
		if ctx.Err() != nil {
			return "", 0, err
		}

		return "", 0, fmt.Errorf("commit %s: %w (%w)", head, ErrNoCommit, err)
	}

	out, err := run(ctx, call{dir: r.Root, args: []string{"rev-list", "--count", head + "..refs/heads/" + branch}, read: true})
	if err != nil {
		return "", 0, fmt.Errorf("count commits of %s past %s: %w", branch, head, err)
	}

	ahead, err = strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return "", 0, fmt.Errorf("count commits of %s past %s: %w", branch, head, err)
	}

	return tip, ahead, nil
}

// DeleteBranch deletes branch with `branch -D` when its tip is still tip.
// The caller has already checked the branch is merged; git still refuses a
// branch checked out in any worktree.
func DeleteBranch(ctx context.Context, r Repo, branch, tip string) error {
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("delete branch %q: %w", branch, ErrBadBranch)
	}

	cur, err := branchTip(ctx, r, branch)
	if err != nil {
		return err
	}

	if cur != tip {
		return fmt.Errorf("delete branch %s: %w", branch, ErrBranchMoved)
	}

	if _, err := run(ctx, call{dir: r.Root, args: []string{"branch", "-D", "--", branch}}); err != nil {
		return fmt.Errorf("delete branch %s: %w", branch, err)
	}

	return nil
}

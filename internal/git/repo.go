package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	// ErrNotRepo means the directory is not inside a git work tree.
	ErrNotRepo = errors.New("git: not inside a work tree")
	// ErrBare means the repository has no main checkout.
	ErrBare = errors.New("git: bare repository")
)

// Repo locates one worktree and the repository it belongs to.
type Repo struct {
	Root      string // the worktree's top level, absolute
	GitDir    string // this worktree's git dir, absolute
	CommonDir string // the repository's common dir, absolute
}

// Open finds the worktree containing dir.
func Open(ctx context.Context, dir string) (Repo, error) {
	out, err := run(ctx, call{
		dir: dir,
		args: []string{
			"rev-parse", "--path-format=absolute",
			"--show-toplevel", "--absolute-git-dir", "--git-common-dir",
		},
		env:  []string{"LC_ALL=C"}, // stable message text for notRepoMessage
		read: true,
	})
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && notRepoMessage(err.Error()) {
			return Repo{}, fmt.Errorf("open %s: %w (%w)", dir, ErrNotRepo, err)
		}

		return Repo{}, fmt.Errorf("open %s: %w", dir, err)
	}

	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		return Repo{}, fmt.Errorf("open %s: unexpected rev-parse output %q", dir, out)
	}

	return Repo{Root: lines[0], GitDir: lines[1], CommonDir: lines[2]}, nil
}

// notRepoMessage reports whether git's error says there is no work tree here:
// outside a repository, or in a bare one.
func notRepoMessage(msg string) bool {
	return strings.Contains(msg, "not a git repository") ||
		strings.Contains(msg, "this operation must be run in a work tree")
}

// MainRoot returns the main checkout: the parent of the common dir when that
// is a ".git" directory. A checkout with a separate git dir or a submodule
// has another common dir and reports ErrBare.
func (r Repo) MainRoot() (string, error) {
	if filepath.Base(r.CommonDir) != ".git" {
		return "", fmt.Errorf("main checkout of %s: %w", r.CommonDir, ErrBare)
	}

	return filepath.Dir(r.CommonDir), nil
}

// Name is the repository's name as the rail shows it: the main checkout's
// directory name, or for a bare repository the common dir's name without ".git".
func (r Repo) Name() string {
	if root, err := r.MainRoot(); err == nil {
		return filepath.Base(root)
	}

	return strings.TrimSuffix(filepath.Base(r.CommonDir), ".git")
}

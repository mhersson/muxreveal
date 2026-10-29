package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrNotCounted means a file edit does not belong to any worktree's changes: the
// path is not absolute, lies in a git dir, outside any work tree, or is ignored.
var ErrNotCounted = errors.New("git: the edit does not count")

// Place is the worktree holding a file an agent edited.
type Place struct {
	Root   string // the worktree's top level
	Common string // the repository's common dir, shared by its worktrees
	Repo   string // the repository's name, Repo.Name
}

// EditPlace finds the worktree an edited file belongs to. The file need not
// exist yet: the nearest existing directory above it decides, with symlinks
// resolved, since git reports real paths. It fails with ErrNotCounted for a
// path that is not absolute, has a ".git" component, is outside any work tree
// or is ignored by that worktree's own rules.
func EditPlace(ctx context.Context, path string) (Place, error) {
	if !filepath.IsAbs(path) {
		return Place{}, fmt.Errorf("edit %s: %w: not an absolute path", path, ErrNotCounted)
	}

	path = filepath.Clean(path)

	// Not left to Open: rev-parse inside a git dir does not reliably say it is not a work tree.
	if hasGitComponent(path) {
		return Place{}, fmt.Errorf("edit %s: %w: inside a git dir", path, ErrNotCounted)
	}

	dir, tail, err := nearestDir(filepath.Dir(path), []string{filepath.Base(path)})
	if err != nil {
		return Place{}, fmt.Errorf("edit %s: %w", path, err)
	}

	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return Place{}, fmt.Errorf("edit %s: %w", path, err)
	}

	if hasGitComponent(dir) {
		return Place{}, fmt.Errorf("edit %s: %w: inside a git dir", path, ErrNotCounted)
	}

	repo, err := Open(ctx, dir)
	if errors.Is(err, ErrNotRepo) {
		return Place{}, fmt.Errorf("edit %s: %w: outside any work tree", path, ErrNotCounted)
	}

	if err != nil {
		return Place{}, fmt.Errorf("edit %s: %w", path, err)
	}

	rel, err := filepath.Rel(repo.Root, filepath.Join(dir, filepath.Join(tail...)))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Place{}, fmt.Errorf("edit %s: %w: not inside %s", path, ErrNotCounted, repo.Root)
	}

	// Run in the file's own worktree: from the main checkout, /.worktrees/ would
	// make every file of a linked worktree ignored. The "./" keeps a name that
	// starts with ":" from being read as pathspec magic.
	_, err = run(ctx, call{
		dir:  repo.Root,
		args: []string{"check-ignore", "-q", "--", "." + string(filepath.Separator) + rel},
		read: true,
	})

	var exitErr *exec.ExitError

	switch {
	case err == nil:
		return Place{}, fmt.Errorf("edit %s: %w: ignored", path, ErrNotCounted)
	case !errors.As(err, &exitErr) || exitErr.ExitCode() != 1:
		return Place{}, fmt.Errorf("edit %s: %w", path, err)
	}

	return Place{Root: repo.Root, Common: repo.CommonDir, Repo: repo.Name()}, nil
}

// hasGitComponent reports whether any component of an absolute path is ".git",
// in any case, since the filesystem may ignore case.
func hasGitComponent(path string) bool {
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}

	return false
}

// nearestDir walks up from dir to the nearest directory that exists. It returns
// that directory and the components below it, ending with tail.
func nearestDir(dir string, tail []string) (string, []string, error) {
	for {
		info, err := os.Stat(dir)

		switch {
		case err == nil && info.IsDir():
			return dir, tail, nil
		case err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR):
			return "", nil, err
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return dir, tail, nil
		}

		tail = append([]string{filepath.Base(dir)}, tail...)
		dir = parent
	}
}

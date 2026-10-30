package git

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/mhersson/muxreveal/internal/md"
)

// maxMarkdownFiles bounds the listing so a huge tree cannot stall the picker.
const maxMarkdownFiles = 5000

// MarkdownFiles lists the markdown files under root as sorted, slash-separated
// paths relative to root, whether git tracks, ignores or has never seen them.
// It skips .git, node_modules, vendor and every folder below root that holds a
// .git entry (a nested checkout or worktree), and does not follow symlinked
// folders. At the cap the walk stops: the files found so far (the first in walk
// order) come back, sorted, without an error. An unreadable folder below root is
// skipped; an unreadable root and a cancelled ctx are errors.
func MarkdownFiles(ctx context.Context, root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}

			if d != nil && d.IsDir() {
				return fs.SkipDir
			}

			return nil
		}

		if d.IsDir() {
			if err := ctx.Err(); err != nil {
				return err
			}

			if path == root {
				return nil
			}

			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return fs.SkipDir
			}

			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return fs.SkipDir
			}

			return nil
		}

		if !md.IsMarkdown(d.Name()) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		files = append(files, filepath.ToSlash(rel))
		if len(files) >= maxMarkdownFiles {
			return fs.SkipAll
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list markdown files in %s: %w", root, err)
	}

	slices.Sort(files)

	return files, nil
}

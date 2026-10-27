package git

import (
	"context"
	"fmt"
)

// Stats totals a diff: the changed files and their added and removed lines.
// Binary files count as files without lines.
type Stats struct{ Files, Added, Removed int }

// Stats totals the diff of src with the listing pass only: no hunks, and no
// cache. Only branch sources are supported.
func (e *Engine) Stats(ctx context.Context, src Source) (Stats, error) {
	s, ok := src.(BranchSource)
	if !ok {
		return Stats{}, fmt.Errorf("stats: unsupported source %T", src)
	}

	r, oldTree, wt, err := e.branchTrees(ctx, s)
	if err != nil {
		return Stats{}, err
	}

	files, err := listFiles(ctx, r, oldTree, wt.Tree)
	if err != nil {
		return Stats{}, err
	}

	markUntracked(files, wt.Untracked)

	st := Stats{Files: len(files)}
	for _, f := range files {
		st.Added += f.Added
		st.Removed += f.Removed
	}

	return st, nil
}

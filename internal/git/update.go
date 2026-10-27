package git

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
)

// maxIncrementalPaths is the most touched paths Update re-lists; a bigger
// batch is cheaper to recompute whole.
const maxIncrementalPaths = 200

// Update brings prev, the result of Diff for src, up to date after the watcher
// batch b. It rebuilds the working tree and re-lists only the touched paths;
// the result equals a fresh Diff. Anything that could change which files pair
// up, the old side, or which files fall inside the caps runs a full Diff.
func (e *Engine) Update(ctx context.Context, prev Result, src Source, b Batch) (Result, error) {
	if !e.canUpdate(prev, src, b) {
		return e.Diff(ctx, src)
	}

	r, err := Open(ctx, worktreeOf(src))
	if err != nil {
		return Result{}, err
	}

	if ts, ok := src.(TurnSource); ok {
		turns, err := Turns(ctx, r, ts.Session)
		if err != nil {
			return Result{}, err
		}

		if len(turns) == 0 || turns[len(turns)-1].N != prev.Through {
			return e.Diff(ctx, src)
		}
	}

	wt, err := e.workingTree(ctx, r)
	if err != nil {
		return Result{}, fmt.Errorf("update: %w", err)
	}

	touched, err := listFiles(ctx, r, prev.Old, wt.Tree, b.Paths...)
	if err != nil {
		return Result{}, err
	}

	retained := retainedFiles(prev.Files, b.Paths)

	if pairingMayChange(touched, retained) {
		return e.Diff(ctx, src)
	}

	markUntracked(touched, wt.Untracked)

	// A scratch cache keeps hunks of a result that falls back out of the real
	// one: cached hunks skip the byte budget, which would skew the full Diff.
	scratch := &Engine{limits: e.limits, tempDir: e.tempDir, indexCache: e.indexCache, cache: newHunkCache(cacheBytes)}
	if err := scratch.addHunks(ctx, r, prev.Old, wt.Tree, touched); err != nil {
		return Result{}, err
	}

	files := mergeFiles(retained, touched)
	if !e.withinCaps(files) {
		return e.Diff(ctx, src)
	}

	for _, f := range touched {
		if len(f.Hunks) > 0 {
			e.cache.put(hunkKey{f.OldOID, f.NewOID}, f.Hunks)
		}
	}

	res := Result{Old: prev.Old, New: wt.Tree, Live: true, Turn: prev.Turn, Through: prev.Through, Files: files}
	for _, f := range files {
		res.Truncated = res.Truncated || f.Truncated
	}

	return res, nil
}

// worktreeOf returns the worktree of a branch or turn source.
func worktreeOf(src Source) string {
	switch s := src.(type) {
	case BranchSource:
		return s.Worktree
	case TurnSource:
		return s.Worktree
	}

	return ""
}

// canUpdate reports whether prev and the batch allow an incremental update.
func (e *Engine) canUpdate(prev Result, src Source, b Batch) bool {
	switch s := src.(type) {
	case BranchSource:
	case TurnSource:
		if s.Turn != 0 && s.Turn != prev.Turn {
			return false
		}
	default:
		return false
	}

	// A .gitignore change can add or drop untracked files nobody touched.
	if !prev.Live || b.Git || b.Overflow || len(b.Paths) > maxIncrementalPaths ||
		slices.ContainsFunc(b.Paths, func(p string) bool { return path.Base(p) == ".gitignore" }) {
		return false
	}

	for _, f := range prev.Files {
		if f.OldPath != "" || f.Truncated {
			return false
		}
	}

	return true
}

// retainedFiles returns prev's files that lie outside every touched path. A
// touched path stands for everything under it.
func retainedFiles(files []FileDiff, touched []string) []FileDiff {
	var out []FileDiff

	for _, f := range files {
		if !underAny(f.Path, touched) {
			out = append(out, f)
		}
	}

	return out
}

func underAny(p string, roots []string) bool {
	for _, root := range roots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}

	return false
}

// pairingMayChange reports whether a full listing could pair files differently
// than the restricted one: a rename or copy among the touched files, or an
// added file on one side next to any other changed file on the other.
func pairingMayChange(touched, retained []FileDiff) bool {
	var touchedAdded, touchedOther, retainedAdded, retainedOther bool

	for _, f := range touched {
		switch f.Status {
		case Renamed, Copied:
			return true
		case Added:
			touchedAdded = true
		default:
			touchedOther = true
		}
	}

	for _, f := range retained {
		if f.Status == Added {
			retainedAdded = true
		} else {
			retainedOther = true
		}
	}

	return (touchedAdded && retainedOther) || (retainedAdded && touchedOther)
}

func mergeFiles(retained, touched []FileDiff) []FileDiff {
	files := make([]FileDiff, 0, len(retained)+len(touched))
	files = append(files, retained...)
	files = append(files, touched...)
	slices.SortStableFunc(files, func(a, b FileDiff) int { return strings.Compare(a.Path, b.Path) })

	if len(files) == 0 {
		return nil
	}

	return files
}

// withinCaps reports whether a full Diff would give every file its hunks, so
// the merged files need no cap bookkeeping: no file past the file cap, none cut
// by the byte cap, and a diff text that stays under it, estimated from above.
func (e *Engine) withinCaps(files []FileDiff) bool {
	if len(files) > e.limits.Files {
		return false
	}

	// Per-file header, per-hunk header and per-line framing, each rounded up so
	// the sum bounds what git prints.
	size := 0

	for _, f := range files {
		// Past the line cap is a per-file rule; any other cut came from the byte cap.
		if f.Truncated && f.Added+f.Removed <= e.limits.Lines {
			return false
		}

		if len(f.Hunks) == 0 {
			continue
		}

		size += 512 + 4*(len(f.Path)+len(f.OldPath))

		for _, h := range f.Hunks {
			size += 64 + len(h.Section)

			for _, l := range h.Lines {
				size += len(l.Text) + 2
				if l.NoNewline {
					size += 32
				}
			}
		}
	}

	return size <= e.limits.Bytes
}

package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultMaxUntracked = 50 << 20

// WorkTree is a working tree written as a git tree object.
type WorkTree struct {
	Tree      string   // tree id of the working tree as git would stage it
	Untracked []string // untracked paths that entered the tree
	Skipped   []string // untracked paths over the size cap, left out
}

// TreeOptions tune WorkingTree.
type TreeOptions struct {
	TempDir      string // where the temporary index is created
	IndexCache   string // keeps a refreshed index copy per worktree; empty = none
	MaxUntracked int64  // bytes; 0 = 50 MiB, negative = no cap
}

// WorkingTree writes the worktree's current content as a tree through a
// temporary copy of its index, so the real index, refs and files are only read.
func WorkingTree(ctx context.Context, r Repo, o TreeOptions) (WorkTree, error) {
	maxUntracked := o.MaxUntracked
	if maxUntracked == 0 {
		maxUntracked = defaultMaxUntracked
	}

	idx, err := newTempIndex(o.TempDir)
	if err != nil {
		return WorkTree{}, err
	}
	defer idx.remove()

	realIndex := filepath.Join(r.GitDir, "index")
	cache := openIndexCache(o.IndexCache, r.GitDir, realIndex)

	if p := cache.path(); p != "" && idx.copyFrom(p) {
		wt, err := idx.stage(ctx, r, maxUntracked, func(changed bool) {
			if changed {
				cache.store(idx.path)
			}
		})
		if err == nil {
			return wt, nil
		}

		if ctx.Err() != nil {
			return WorkTree{}, err
		}
	}

	if idx.copyFrom(realIndex) && !idx.isSplit(ctx, r) {
		wt, err := idx.stage(ctx, r, maxUntracked, func(bool) { cache.store(idx.path) })
		if err == nil {
			return wt, nil
		}

		if ctx.Err() != nil {
			return WorkTree{}, err
		}
	}

	if err := idx.seedFromHEAD(ctx, r); err != nil {
		return WorkTree{}, err
	}

	return idx.stage(ctx, r, maxUntracked, nil)
}

// tempIndex is a throwaway index file. Its name starts with the pid and
// "-index-" so the run-dir sweep finds the leftovers of a crashed process.
type tempIndex struct {
	path string
}

func newTempIndex(dir string) (*tempIndex, error) {
	f, err := os.CreateTemp(dir, fmt.Sprintf("%d-index-*", os.Getpid()))
	if err != nil {
		return nil, fmt.Errorf("create temporary index: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())

		return nil, fmt.Errorf("create temporary index: %w", err)
	}

	return &tempIndex{path: f.Name()}, nil
}

// remove deletes the file and the lock git may have left next to it.
func (t *tempIndex) remove() {
	_ = os.Remove(t.path)
	_ = os.Remove(t.path + ".lock")
}

// copyFrom copies src with its mtime, so git trusts the stat data inside it.
// It reports false when src is missing, empty or unreadable.
func (t *tempIndex) copyFrom(src string) bool {
	info, err := os.Stat(src)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return false
	}

	data, err := os.ReadFile(src)
	if err != nil || len(data) == 0 {
		return false
	}

	if err := os.WriteFile(t.path, data, 0o600); err != nil {
		return false
	}

	return os.Chtimes(t.path, info.ModTime(), info.ModTime()) == nil
}

// env runs a call against the temporary index.
func (t *tempIndex) env() []string {
	return append([]string{"GIT_INDEX_FILE=" + t.path}, isolatedEnv()...)
}

// isolatedEnv is the configuration every snapshot call runs under: no split
// index (updating one writes shared index files into the git dir), no hooks
// and no signing.
func isolatedEnv() []string {
	return []string{
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.splitIndex", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=core.hooksPath", "GIT_CONFIG_VALUE_1=" + os.DevNull,
		"GIT_CONFIG_KEY_2=commit.gpgSign", "GIT_CONFIG_VALUE_2=false",
	}
}

// isSplit reports whether the copied index points at a shared index file. A
// git dir without sharedindex.* files cannot hold one, which spares the call.
// It runs without the split-index override, which would hide the link.
func (t *tempIndex) isSplit(ctx context.Context, r Repo) bool {
	matches, err := filepath.Glob(filepath.Join(globEscape(r.GitDir), "sharedindex.*"))
	if err != nil || len(matches) == 0 {
		return false
	}

	out, err := run(ctx, call{dir: r.Root, args: []string{"rev-parse", "--shared-index-path"}, env: []string{"GIT_INDEX_FILE=" + t.path}, read: true})

	return err != nil || len(bytes.TrimSpace(out)) > 0
}

func globEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(s)
}

// seedFromHEAD resets the temporary index to HEAD's tree (empty for an unborn HEAD).
func (t *tempIndex) seedFromHEAD(ctx context.Context, r Repo) error {
	if err := os.WriteFile(t.path, nil, 0o600); err != nil {
		return fmt.Errorf("reset temporary index: %w", err)
	}

	args := []string{"read-tree", "HEAD"}

	if _, err := headCommit(ctx, r.Root); err != nil {
		if !errors.Is(err, errUnborn) {
			return err
		}

		args = []string{"read-tree", "--empty"}
	}

	if _, err := run(ctx, call{dir: r.Root, args: args, env: t.env()}); err != nil {
		return fmt.Errorf("seed temporary index: %w", err)
	}

	return nil
}

var errUnborn = errors.New("git: unborn HEAD")

// headCommit returns HEAD's commit id, or errUnborn when HEAD has none.
func headCommit(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, call{dir: dir, args: []string{"rev-parse", "--verify", "--quiet", "HEAD^{commit}"}, read: true})
	if err != nil {
		if isExitCode(err, 1) {
			return "", errUnborn
		}

		return "", fmt.Errorf("resolve HEAD: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// stage brings the temporary index in line with the files on disk and writes
// the tree. afterTracked, when set, runs once the modified tracked paths are
// staged and before deleted and untracked ones are, so a copy taken there holds
// neither; changed tells whether staging wrote the index.
func (t *tempIndex) stage(ctx context.Context, r Repo, maxUntracked int64, afterTracked func(changed bool)) (WorkTree, error) {
	out, err := run(ctx, call{
		dir:  r.Root,
		args: []string{"ls-files", "-t", "-z", "--modified", "--deleted", "--others", "--exclude-standard"},
		env:  t.env(),
		read: true,
	})
	if err != nil {
		return WorkTree{}, err
	}

	tracked, deleted, others := splitListing(out)

	skip, err := t.gitlinks(ctx, r, tracked)
	if err != nil {
		return WorkTree{}, err
	}

	var wt WorkTree

	var trackedIn, othersIn bytes.Buffer

	// Modified paths are staged first and cached. Deleted ones go in the second
	// pass, ahead of untracked paths: a removed file must leave the index before
	// a directory of the same name is added, and the cache keeps its entry so the
	// file is not listed as untracked if it comes back.
	for _, p := range tracked {
		if skip[p] {
			continue
		}

		in := &trackedIn
		if deleted[p] {
			in = &othersIn
		}

		in.WriteString(p)
		in.WriteByte(0)
	}

	for _, p := range others {
		if strings.HasSuffix(p, "/") {
			continue // a nested repository that is not a submodule
		}

		if tooBig(filepath.Join(r.Root, p), maxUntracked) {
			wt.Skipped = append(wt.Skipped, p)

			continue
		}

		wt.Untracked = append(wt.Untracked, p)
		othersIn.WriteString(p)
		othersIn.WriteByte(0)
	}

	if err := t.updateIndex(ctx, r, &trackedIn); err != nil {
		return WorkTree{}, err
	}

	if afterTracked != nil {
		afterTracked(trackedIn.Len() > 0)
	}

	if err := t.updateIndex(ctx, r, &othersIn); err != nil {
		return WorkTree{}, err
	}

	tree, err := run(ctx, call{dir: r.Root, args: []string{"write-tree"}, env: t.env()})
	if err != nil {
		return WorkTree{}, err
	}

	wt.Tree = strings.TrimSpace(string(tree))

	return wt, nil
}

// updateIndex stages the NUL-separated paths in in; an empty list is a no-op.
func (t *tempIndex) updateIndex(ctx context.Context, r Repo, in *bytes.Buffer) error {
	if in.Len() == 0 {
		return nil
	}

	_, err := run(ctx, call{
		dir:   r.Root,
		args:  []string{"update-index", "--add", "--remove", "-z", "--stdin"},
		env:   t.env(),
		stdin: in,
	})

	return err
}

// splitListing separates `ls-files -t -z` records into tracked (modified or
// deleted, each path once), the deleted ones among them, and untracked paths.
func splitListing(out []byte) (tracked []string, deleted map[string]bool, others []string) {
	deleted = map[string]bool{}
	seen := map[string]bool{}

	for rec := range bytes.SplitSeq(out, []byte{0}) {
		tag, path, ok := strings.Cut(string(rec), " ")
		if !ok {
			continue
		}

		switch tag {
		case "?":
			others = append(others, path)
		case "C", "R":
			if tag == "R" {
				deleted[path] = true
			}

			if !seen[path] {
				seen[path] = true

				tracked = append(tracked, path)
			}
		}
	}

	return tracked, deleted, others
}

// gitlinks returns the tracked paths that are submodules. Staging one would
// move it to the submodule's current commit instead of the recorded one. Only
// paths that are directories on disk can be submodules.
func (t *tempIndex) gitlinks(ctx context.Context, r Repo, tracked []string) (map[string]bool, error) {
	var specs []string

	for _, p := range tracked {
		if info, err := os.Lstat(filepath.Join(r.Root, p)); err == nil && info.IsDir() {
			specs = append(specs, ":(literal)"+p)
		}
	}

	if len(specs) == 0 {
		return nil, nil
	}

	out, err := run(ctx, call{
		dir:  r.Root,
		args: append([]string{"ls-files", "-s", "-z", "--"}, specs...),
		env:  t.env(),
		read: true,
	})
	if err != nil {
		return nil, err
	}

	links := map[string]bool{}

	for rec := range bytes.SplitSeq(out, []byte{0}) {
		meta, path, ok := strings.Cut(string(rec), "\t")
		if ok && strings.HasPrefix(meta, "160000 ") {
			links[path] = true
		}
	}

	return links, nil
}

// tooBig reports whether path is a regular file over limit bytes. A path that
// cannot be inspected is left to git.
func tooBig(path string, limit int64) bool {
	if limit < 0 {
		return false
	}

	info, err := os.Lstat(path)

	return err == nil && info.Mode().IsRegular() && info.Size() > limit
}

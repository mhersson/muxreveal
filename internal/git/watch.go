package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	defaultDebounce = 150 * time.Millisecond
	defaultMaxDelay = time.Second
	eventBuffer     = 8192
	errorBuffer     = 16
)

// Batch is one debounced set of changes in a worktree.
type Batch struct {
	// Paths are touched paths relative to the worktree root, sorted,
	// deduplicated, never ignored. A removed directory appears as its own path
	// and its children may be absent: treat a vanished path as a prefix. Files a
	// .gitignore change un-ignores are listed only in newly watched
	// directories: treat a touched .gitignore as "everything may have changed".
	Paths    []string
	Git      bool // HEAD, the index, packed-refs or a ref outside refs/muxreveal changed
	Overflow bool // events were lost; treat everything as changed
}

// WatchOptions tunes the debounce. Zero values select the defaults.
type WatchOptions struct {
	Debounce time.Duration // quiet time before a batch goes out; 150 ms when zero
	MaxDelay time.Duration // longest an unsent change waits while events keep coming; 1 s when zero
	SeqBase  uint64        // Seq's starting value, so a restarted watcher never repeats one
}

// Watcher reports debounced changes of one worktree. Directories git ignores,
// .git internals and the main checkout's .worktrees get no watch.
type Watcher struct {
	repo Repo
	opts WatchOptions
	fs   *fsnotify.Watcher

	batches chan Batch
	errs    chan error

	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error

	seq atomic.Uint64

	skipWorktrees bool
	refsDir       string
	ownRefsDir    string // refs/muxreveal: our snapshot refs must not wake the watcher

	// watched holds the directories with a watch. It is written by Watch, then
	// only by the owner goroutine.
	watched map[string]struct{}
}

// pending accumulates events until the debounce fires.
type pending struct {
	paths    map[string]struct{} // worktree-relative, slash separated
	created  map[string]struct{} // absolute paths that may be new directories
	git      bool
	overflow bool
	regate   bool // a .gitignore changed: directories may no longer be ignored
}

func newPending() *pending {
	return &pending{paths: map[string]struct{}{}, created: map[string]struct{}{}}
}

// Watch starts watching the worktree r. Batches closes when Close is called or
// ctx ends.
func Watch(ctx context.Context, r Repo, opts WatchOptions) (*Watcher, error) {
	if opts.Debounce <= 0 {
		opts.Debounce = defaultDebounce
	}

	if opts.MaxDelay <= 0 {
		opts.MaxDelay = defaultMaxDelay
	}

	fsw, err := fsnotify.NewBufferedWatcher(eventBuffer)
	if err != nil {
		return nil, instanceError(r.Root, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	w := &Watcher{
		repo:       r,
		opts:       opts,
		fs:         fsw,
		batches:    make(chan Batch),
		errs:       make(chan error, errorBuffer),
		cancel:     cancel,
		done:       make(chan struct{}),
		refsDir:    filepath.Join(r.CommonDir, "refs"),
		ownRefsDir: filepath.Join(r.CommonDir, "refs", "muxreveal"),
		watched:    map[string]struct{}{},
	}
	w.seq.Store(opts.SeqBase)

	if main, err := r.MainRoot(); err == nil && main == r.Root {
		w.skipWorktrees = true
	}

	if err := w.start(ctx); err != nil {
		cancel()

		_ = fsw.Close()

		return nil, err
	}

	go w.loop(ctx)

	return w, nil
}

func (w *Watcher) start(ctx context.Context) error {
	for _, dir := range []string{w.repo.GitDir, w.repo.CommonDir} {
		if err := w.add(dir); err != nil {
			return err
		}
	}

	if err := w.addRefs(w.refsDir); err != nil {
		return err
	}

	return w.walk(ctx, []string{w.repo.Root}, false, nil)
}

// Seq counts the events the watcher accepted, from SeqBase, before they are
// debounced into a batch. It is safe to call from any goroutine.
func (w *Watcher) Seq() uint64 { return w.seq.Load() }

// Batches delivers the changes. It is closed by Close or when the context ends.
func (w *Watcher) Batches() <-chan Batch { return w.batches }

// Errors reports non-fatal problems such as a failed ignore check. It is
// closed together with Batches; errors are dropped when nobody reads them.
func (w *Watcher) Errors() <-chan error { return w.errs }

// Close stops the watcher and waits for its goroutine. It is safe to call more
// than once.
func (w *Watcher) Close() error {
	w.closeOnce.Do(w.cancel)
	<-w.done

	return w.closeErr
}

func (w *Watcher) loop(ctx context.Context) {
	defer func() {
		w.closeErr = w.fs.Close()
		close(w.errs)
		close(w.batches)
		close(w.done)
	}()

	debounce := time.NewTimer(time.Hour)
	maxDelay := time.NewTimer(time.Hour)

	debounce.Stop()
	maxDelay.Stop()

	var (
		p        = newPending()
		ready    *Batch
		maxArmed bool
	)

	flush := func() {
		debounce.Stop()
		maxDelay.Stop()

		maxArmed = false

		b, err := w.flush(ctx, p)
		p = newPending()

		if err != nil && ctx.Err() == nil {
			w.report(err)
		}

		if len(b.Paths) == 0 && !b.Git && !b.Overflow {
			return
		}

		if ready == nil {
			ready = &b

			return
		}

		*ready = mergeBatches(*ready, b)
	}

	arm := func() {
		debounce.Reset(w.opts.Debounce)

		if !maxArmed {
			maxDelay.Reset(w.opts.MaxDelay)

			maxArmed = true
		}
	}

	for {
		var (
			out   Batch
			sendC chan<- Batch
		)

		if ready != nil {
			out, sendC = *ready, w.batches
		}

		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}

			if w.handle(ev, p) {
				arm()
			}
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}

			if errors.Is(err, fsnotify.ErrEventOverflow) {
				p.overflow = true

				arm()

				continue
			}

			w.report(err)
		case <-debounce.C:
			flush()
		case <-maxDelay.C:
			flush()
		case sendC <- out:
			ready = nil
		}
	}
}

// report hands err to Errors without ever blocking the owner goroutine.
func (w *Watcher) report(err error) {
	select {
	case w.errs <- err:
	default:
	}
}

func mergeBatches(a, b Batch) Batch {
	seen := make(map[string]struct{}, len(a.Paths)+len(b.Paths))
	paths := make([]string, 0, len(a.Paths)+len(b.Paths))

	for _, p := range append(append([]string(nil), a.Paths...), b.Paths...) {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			paths = append(paths, p)
		}
	}

	sort.Strings(paths)

	return Batch{Paths: paths, Git: a.Git || b.Git, Overflow: a.Overflow || b.Overflow}
}

// handle records one event and reports whether it left something to send.
func (w *Watcher) handle(ev fsnotify.Event, p *pending) bool {
	if !w.classify(ev, p) {
		return false
	}

	w.seq.Add(1)

	return true
}

func (w *Watcher) classify(ev fsnotify.Event, p *pending) bool {
	name := ev.Name

	if w.inGitDir(name) {
		return w.handleMeta(ev, p)
	}

	rel, ok := w.relPath(name)
	if !ok {
		return false
	}

	// kqueue notes a directory's own entry changes as a Write on the directory;
	// the entries' events report the change.
	if ev.Op == fsnotify.Write && w.isWatchedDir(name) {
		return false
	}

	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		w.forget(name)
	}

	if ev.Has(fsnotify.Create) {
		p.created[name] = struct{}{}
	}

	if filepath.Base(rel) == ".gitignore" {
		p.regate = true
	}

	p.paths[rel] = struct{}{}

	return true
}

func (w *Watcher) isWatchedDir(name string) bool {
	_, ok := w.watched[name]

	return ok
}

func (w *Watcher) inGitDir(name string) bool {
	return within(w.repo.GitDir, name) || within(w.repo.CommonDir, name)
}

// within reports whether name is dir or below it.
func within(dir, name string) bool {
	return name == dir || strings.HasPrefix(name, dir+string(filepath.Separator))
}

// relPath returns name relative to the worktree root in slash form, or false
// for the root itself, paths outside it, .git entries and the main checkout's
// .worktrees.
func (w *Watcher) relPath(name string) (string, bool) {
	rel, err := filepath.Rel(w.repo.Root, name)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	parts := strings.Split(rel, string(filepath.Separator))
	if w.skipWorktrees && parts[0] == ".worktrees" {
		return "", false
	}

	for _, part := range parts {
		if part == ".git" {
			return "", false
		}
	}

	return filepath.ToSlash(rel), true
}

// handleMeta classifies an event inside the git dirs: only a Create, Write,
// Remove or Rename of HEAD, the index, packed-refs or a ref outside
// refs/muxreveal counts. Chmod-only events and events on directories do not.
func (w *Watcher) handleMeta(ev fsnotify.Event, p *pending) bool {
	name, base := ev.Name, filepath.Base(ev.Name)

	if !ev.Has(fsnotify.Create) && !ev.Has(fsnotify.Write) && !ev.Has(fsnotify.Remove) && !ev.Has(fsnotify.Rename) {
		return false
	}

	switch {
	case within(w.ownRefsDir, name):
		return false
	case strings.HasPrefix(name, w.refsDir+string(filepath.Separator)):
		if strings.HasSuffix(base, ".lock") || (ev.Op == fsnotify.Write && w.isWatchedDir(name)) {
			return false
		}

		if ev.Has(fsnotify.Create) && isDir(name) {
			if err := w.addRefs(name); err != nil {
				w.report(err)
			}

			// Refs already inside the new directory have no event of their own.
			if !hasRefFile(name, w.ownRefsDir) {
				return false
			}
		}
	case filepath.Dir(name) == w.repo.GitDir && (base == "HEAD" || base == "index"):
	case filepath.Dir(name) == w.repo.CommonDir && base == "packed-refs":
	default:
		return false
	}

	p.git = true

	return true
}

func isDir(name string) bool {
	info, err := os.Lstat(name)

	return err == nil && info.IsDir()
}

// hasRefFile reports whether dir holds a ref file below it, outside skip.
func hasRefFile(dir, skip string) bool {
	if within(skip, dir) {
		return false
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	for _, e := range entries {
		path := filepath.Join(dir, e.Name())

		switch {
		case e.IsDir():
			if hasRefFile(path, skip) {
				return true
			}
		case !strings.HasSuffix(e.Name(), ".lock"):
			return true
		}
	}

	return false
}

// forget drops the bookkeeping for a removed or renamed directory and its
// descendants; the kernel has already removed their watches.
func (w *Watcher) forget(name string) {
	if _, ok := w.watched[name]; !ok {
		return
	}

	for dir := range w.watched {
		if within(name, dir) {
			delete(w.watched, dir)
		}
	}
}

// add watches one directory. A directory that vanished or cannot be read is
// skipped.
func (w *Watcher) add(dir string) error {
	err := w.fs.Add(dir)

	switch {
	case err == nil:
		w.watched[dir] = struct{}{}

		return nil
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.ENOTDIR):
		return nil
	}

	return watchError(dir, err)
}

// watchError names the sysctl to raise when the kernel is out of watches.
func watchError(dir string, err error) error {
	if errors.Is(err, syscall.ENOSPC) {
		return fmt.Errorf("watch %s: out of watches, raise fs.inotify.max_user_watches: %w", dir, err)
	}

	return fmt.Errorf("watch %s: %w", dir, err)
}

// instanceError names the sysctl to raise when the process has no inotify
// instance left.
func instanceError(root string, err error) error {
	if errors.Is(err, syscall.EMFILE) {
		return fmt.Errorf("watch %s: out of inotify instances, raise fs.inotify.max_user_instances: %w", root, err)
	}

	return fmt.Errorf("watch %s: %w", root, err)
}

// addRefs watches dir and the directories below it except refs/muxreveal. It
// marks nothing itself: a ref that already sits in a new directory is covered by
// the event that announced the directory.
func (w *Watcher) addRefs(dir string) error {
	if within(w.ownRefsDir, dir) {
		return nil
	}

	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return nil //nolint:nilerr // a ref that vanished or is a file needs no watch
	}

	if err := w.add(dir); err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil //nolint:nilerr // an unreadable directory is skipped like a vanished one
	}

	for _, e := range entries {
		if e.IsDir() {
			if err := w.addRefs(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}

	return nil
}

// walk watches the non-ignored directories at and below dirs, one ignore check
// per tree level. With files non-nil, the files of directories that were not
// watched before are added to it (relative, slash separated); force counts
// every directory as new.
func (w *Watcher) walk(ctx context.Context, dirs []string, force bool, files map[string]struct{}) error {
	level := dirs

	for len(level) > 0 {
		kept, err := w.dropIgnoredDirs(ctx, level)
		if err != nil {
			return err
		}

		var next []string

		for _, dir := range kept {
			if dir != w.repo.Root && hasGitEntry(dir) {
				continue // submodules and nested repositories change this tree only through a gitlink
			}

			_, had := w.watched[dir]

			if err := w.add(dir); err != nil {
				return err
			}

			if _, ok := w.watched[dir]; !ok {
				continue
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}

			for _, e := range entries {
				path := filepath.Join(dir, e.Name())

				if e.Type().IsDir() {
					if _, ok := w.relPath(path); ok {
						next = append(next, path)
					}

					continue
				}

				if files != nil && (force || !had) {
					if rel, ok := w.relPath(path); ok {
						files[rel] = struct{}{}
					}
				}
			}
		}

		level = next
	}

	return nil
}

func hasGitEntry(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))

	return err == nil
}

// dropIgnoredDirs removes from dirs the directories git ignores. The root is
// never checked.
func (w *Watcher) dropIgnoredDirs(ctx context.Context, dirs []string) ([]string, error) {
	var query []string

	for _, d := range dirs {
		if d == w.repo.Root {
			continue
		}

		if rel, ok := w.relPath(d); ok {
			query = append(query, rel+"/")
		}
	}

	ignored, err := w.checkIgnore(ctx, query)
	if err != nil {
		return nil, err
	}

	kept := make([]string, 0, len(dirs))

	for _, d := range dirs {
		rel, ok := w.relPath(d)
		if d != w.repo.Root && (!ok || ignored[rel+"/"]) {
			continue
		}

		kept = append(kept, d)
	}

	return kept, nil
}

// checkIgnore returns the entries of paths that git ignores. Tracked files are
// never ignored.
func (w *Watcher) checkIgnore(ctx context.Context, paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	out, err := run(ctx, call{
		dir:   w.repo.Root,
		args:  []string{"check-ignore", "-z", "--stdin"},
		stdin: strings.NewReader(strings.Join(paths, "\x00") + "\x00"),
		read:  true,
	})
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil // none of them is ignored
		}

		return nil, fmt.Errorf("check ignored paths: %w", err)
	}

	ignored := map[string]bool{}

	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			ignored[p] = true
		}
	}

	return ignored, nil
}

// flush turns the pending events into a batch: it walks new directories,
// then drops ignored paths with one check for the whole set. When the check
// fails the paths go out unfiltered along with the error.
func (w *Watcher) flush(ctx context.Context, p *pending) (Batch, error) {
	var errs []error

	// An overflow lost Create events, so directories may be unwatched.
	if p.regate || p.overflow {
		if err := w.walk(ctx, []string{w.repo.Root}, false, p.paths); err != nil {
			errs = append(errs, err)
		}
	}

	var fresh []string

	for name := range p.created {
		if info, err := os.Lstat(name); err == nil && info.IsDir() {
			fresh = append(fresh, name)
		}
	}

	if len(fresh) > 0 {
		if err := w.walk(ctx, fresh, true, p.paths); err != nil {
			errs = append(errs, err)
		}
	}

	paths, err := w.filterIgnored(ctx, p.paths)
	if err != nil {
		errs = append(errs, err)
	}

	return Batch{Paths: paths, Git: p.git, Overflow: p.overflow}, errors.Join(errs...)
}

// filterIgnored returns the sorted paths git does not ignore. A path that no
// longer exists is checked both as a file and as a directory, since a removed
// ignored directory cannot be told from a file.
func (w *Watcher) filterIgnored(ctx context.Context, set map[string]struct{}) ([]string, error) {
	paths := make([]string, 0, len(set))
	query := make([]string, 0, len(set))

	for rel := range set {
		paths = append(paths, rel)

		info, err := os.Lstat(filepath.Join(w.repo.Root, filepath.FromSlash(rel)))

		switch {
		case err != nil:
			query = append(query, rel, rel+"/")
		case info.IsDir():
			query = append(query, rel+"/")
		default:
			query = append(query, rel)
		}
	}

	sort.Strings(paths)

	ignored, err := w.checkIgnore(ctx, query)
	if err != nil {
		return paths, err
	}

	kept := paths[:0]

	for _, rel := range paths {
		if !ignored[rel] && !ignored[rel+"/"] {
			kept = append(kept, rel)
		}
	}

	return kept, nil
}

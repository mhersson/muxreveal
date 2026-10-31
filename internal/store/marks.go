package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MarkTTL is how long a reviewed-hunk mark lives without being set or shown.
const MarkTTL = 90 * 24 * time.Hour

const (
	marksVersion = 1
	dayLayout    = "2006-01-02"
)

type marksJSON struct {
	Version int               `json:"version"`
	Repo    string            `json:"repo"`
	Marks   map[string]string `json:"marks"`
}

// resolveCommonDir follows symlinks so every path to a repository names one
// file; it falls back to the cleaned path when the dir cannot be resolved.
func resolveCommonDir(commonDir string) string {
	if r, err := filepath.EvalSymlinks(commonDir); err == nil {
		return r
	}

	return filepath.Clean(commonDir)
}

// MarksPath is the marks file of the repository with the given common dir.
func MarksPath(stateDir, commonDir string) string {
	return marksPathOf(stateDir, resolveCommonDir(commonDir))
}

// marksPathOf is the marks file of an already resolved common dir.
func marksPathOf(stateDir, resolved string) string {
	sum := sha256.Sum256([]byte(resolved))

	return filepath.Join(stateDir, "reviews", hex.EncodeToString(sum[:16])+".json")
}

// marksReadError is a marks file that exists but could not be read.
type marksReadError struct {
	path string
	err  error
}

func (e *marksReadError) Error() string { return fmt.Sprintf("reading marks %s: %v", e.path, e.err) }
func (e *marksReadError) Unwrap() error { return e.err }

// LoadMarks reads the marks at path for the repository with the given common
// dir (resolved like MarksPath's), dropping those whose day is over MarkTTL
// before now. A missing file gives no marks. A file that does not decode, has
// another version or names another repo is renamed to path+".bad", and the
// result is empty with an error that says so.
func LoadMarks(path, commonDir string, now time.Time) (map[string]string, error) {
	commonDir = resolveCommonDir(commonDir)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}

	if err != nil {
		return map[string]string{}, &marksReadError{path: path, err: err}
	}

	var f marksJSON

	reason := ""

	switch uerr := json.Unmarshal(data, &f); {
	case uerr != nil:
		reason = uerr.Error()
	case f.Version != marksVersion:
		reason = fmt.Sprintf("version %d is not supported", f.Version)
	case f.Repo != commonDir:
		reason = fmt.Sprintf("it names repository %q", f.Repo)
	}

	if reason != "" {
		bad := path + ".bad"
		if rerr := os.Rename(path, bad); rerr != nil {
			return map[string]string{}, fmt.Errorf("marks file %s is unusable (%s) and could not be kept as .bad: %w", path, reason, rerr)
		}

		return map[string]string{}, fmt.Errorf("marks file %s is unusable (%s); kept as %s", path, reason, bad)
	}

	today := now.UTC().Truncate(24 * time.Hour)
	limit := int(MarkTTL / (24 * time.Hour))
	out := make(map[string]string, len(f.Marks))

	for id, day := range f.Marks {
		t, perr := time.Parse(dayLayout, day)
		if perr != nil || int(today.Sub(t)/(24*time.Hour)) > limit {
			continue
		}

		out[id] = day
	}

	return out, nil
}

// EncodeMarks returns the marks file for the repository, naming its common dir
// resolved like MarksPath's: indented, keys sorted.
func EncodeMarks(commonDir string, days map[string]string) ([]byte, error) {
	commonDir = resolveCommonDir(commonDir)

	if days == nil {
		days = map[string]string{}
	}

	data, err := json.MarshalIndent(marksJSON{Version: marksVersion, Repo: commonDir, Marks: days}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding marks: %w", err)
	}

	return append(data, '\n'), nil
}

// queuedMarks is a Save waiting for the writer; seq orders the Saves.
type queuedMarks struct {
	days map[string]string
	seq  uint64
}

// MarkFiles reads and writes the per-repository marks files under
// <state dir>/reviews. Writes come from one goroutine, which ends in Close.
type MarkFiles struct {
	dir string
	log *slog.Logger

	// resolve names a repository one way for every spelling of its common dir; it
	// runs only on the writer goroutine.
	resolve func(string) string

	mu      sync.Mutex
	kept    map[string]bool        // marks files that could not be read; never written
	warned  map[string]bool        // the kept files the writer logged; the writer only
	pending map[string]queuedMarks // newest marks per common dir as given, not yet written
	seq     uint64
	closed  bool

	wake chan struct{}
	stop chan struct{}
	done chan struct{}
}

// NewMarkFiles starts the writer for files under stateDir. A nil log discards.
func NewMarkFiles(stateDir string, log *slog.Logger) *MarkFiles {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	m := &MarkFiles{
		dir:     stateDir,
		log:     log,
		resolve: resolveCommonDir,
		kept:    map[string]bool{},
		warned:  map[string]bool{},
		pending: map[string]queuedMarks{},
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go m.run()

	return m
}

// Load reads the marks of the repository with the given common dir. A bad file
// is kept as .bad and the error says so; the marks are empty then. A file that
// exists but cannot be read is left alone, and Saves for its repository are
// dropped, so its marks are not lost to the next write.
func (m *MarkFiles) Load(commonDir string, now time.Time) (map[string]string, error) {
	path := MarksPath(m.dir, commonDir)

	marks, err := LoadMarks(path, commonDir, now)

	if _, ok := errors.AsType[*marksReadError](err); ok {
		m.mu.Lock()
		m.kept[path] = true
		m.mu.Unlock()
	}

	return marks, err
}

// Save queues the marks for writing and returns at once, doing no file IO or
// hashing; the writer resolves the dir. Newer marks for the same repository
// replace ones still waiting, and days is copied. Saves after Close are dropped.
func (m *MarkFiles) Save(commonDir string, days map[string]string) {
	days = maps.Clone(days)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()

		return
	}

	m.seq++
	m.pending[commonDir] = queuedMarks{days: days, seq: m.seq}
	m.mu.Unlock()

	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Close writes what is pending and stops the goroutine. It returns ctx's error
// if the goroutine does not finish first.
func (m *MarkFiles) Close(ctx context.Context) error {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		close(m.stop)
	}
	m.mu.Unlock()

	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("closing mark files: %w", ctx.Err())
	}
}

func (m *MarkFiles) run() {
	defer close(m.done)

	last := map[string][]byte{}

	flush := func() {
		m.mu.Lock()
		batch := m.pending
		m.pending = map[string]queuedMarks{}
		m.mu.Unlock()

		for path, data := range m.encode(batch) {
			if bytes.Equal(data, last[path]) {
				continue
			}

			// A failed write is not retried before the next Save for this repository: marks need not survive.
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				m.log.Error("creating marks dir", "path", path, "err", err)

				continue
			}

			// Marks need not survive a crash, so no sync.
			if err := WriteAtomicNoSync(path, data); err != nil {
				m.log.Error("writing marks", "path", path, "err", err)

				continue
			}

			last[path] = data
		}
	}

	for {
		select {
		case <-m.wake:
			flush()
		case <-m.stop:
			flush()

			return
		}
	}
}

// encode resolves each queued common dir and encodes its marks. Spellings of
// one repository share a file, and the newest Save of them wins.
func (m *MarkFiles) encode(batch map[string]queuedMarks) map[string][]byte {
	type pick struct {
		resolved string
		q        queuedMarks
	}

	newest := map[string]pick{}

	for commonDir, q := range batch {
		resolved := m.resolve(commonDir)
		path := marksPathOf(m.dir, resolved)

		if cur, ok := newest[path]; !ok || q.seq > cur.q.seq {
			newest[path] = pick{resolved: resolved, q: q}
		}
	}

	out := make(map[string][]byte, len(newest))

	m.mu.Lock()
	kept := maps.Clone(m.kept)
	m.mu.Unlock()

	for path, p := range newest {
		if kept[path] {
			if !m.warned[path] {
				m.warned[path] = true
				m.log.Warn("not saving marks: the file could not be read", "path", path)
			}

			continue
		}

		data, err := EncodeMarks(p.resolved, p.q.days)
		if err != nil {
			m.log.Error("encoding marks", "err", err)

			continue
		}

		out[path] = data
	}

	return out
}

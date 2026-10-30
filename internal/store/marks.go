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
	sum := sha256.Sum256([]byte(resolveCommonDir(commonDir)))

	return filepath.Join(stateDir, "reviews", hex.EncodeToString(sum[:16])+".json")
}

// LoadMarks reads the marks at path, dropping those whose day is over MarkTTL
// before now. A missing file gives no marks. A file that does not decode, has
// another version or names another repo is renamed to path+".bad", and the
// result is empty with an error that says so.
func LoadMarks(path, commonDir string, now time.Time) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}

	if err != nil {
		return map[string]string{}, fmt.Errorf("reading marks %s: %w", path, err)
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

// EncodeMarks returns the marks file for the repository: indented, keys sorted.
func EncodeMarks(commonDir string, days map[string]string) ([]byte, error) {
	if days == nil {
		days = map[string]string{}
	}

	data, err := json.MarshalIndent(marksJSON{Version: marksVersion, Repo: commonDir, Marks: days}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding marks: %w", err)
	}

	return append(data, '\n'), nil
}

// MarkFiles reads and writes the per-repository marks files under
// <state dir>/reviews. Writes come from one goroutine, which ends in Close.
type MarkFiles struct {
	dir string
	log *slog.Logger

	mu      sync.Mutex
	pending map[string][]byte // newest encoding per path not yet written
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
		pending: map[string][]byte{},
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go m.run()

	return m
}

// Load reads the marks of the repository with the given common dir. A bad file
// is kept as .bad and the error says so; the marks are empty then.
func (m *MarkFiles) Load(commonDir string, now time.Time) (map[string]string, error) {
	common := resolveCommonDir(commonDir)

	return LoadMarks(MarksPath(m.dir, common), common, now)
}

// Save queues the marks for writing and returns at once; newer marks for the
// same repository replace ones still waiting, and days is not kept. Saves after
// Close are dropped.
func (m *MarkFiles) Save(commonDir string, days map[string]string) {
	common := resolveCommonDir(commonDir)

	data, err := EncodeMarks(common, days)
	if err != nil {
		m.log.Error("encoding marks", "err", err)

		return
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()

		return
	}

	m.pending[MarksPath(m.dir, common)] = data
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
		m.pending = map[string][]byte{}
		m.mu.Unlock()

		for path, data := range batch {
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

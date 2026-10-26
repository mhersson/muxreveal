package ui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/term/rec"
)

// recordDirMode and recordFileMode keep recordings private: they hold whatever the guest printed.
const (
	recordDirMode  = 0o700
	recordFileMode = 0o600
)

// tap writes one pane's PTY output and resizes to a recording file. A write
// error is logged once and stops the recording; the pane is unaffected.
type tap struct {
	log  *slog.Logger
	id   core.PaneID
	file *os.File

	mu     sync.Mutex
	w      *rec.Writer
	failed bool
	closed bool
}

// openTap creates <dir>/<id>.rec; it never overwrites an existing recording.
func openTap(log *slog.Logger, dir string, id core.PaneID, cols, rows int, now func() time.Time) (*tap, error) {
	f, err := os.OpenFile(filepath.Join(dir, string(id)+".rec"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, recordFileMode)
	if err != nil {
		return nil, fmt.Errorf("create recording: %w", err)
	}

	w, err := rec.NewWriter(f, cols, rows, now)
	if err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("start recording: %w", err)
	}

	return &tap{log: log, id: id, file: f, w: w}, nil
}

func (t *tap) data(b []byte) {
	t.write(func(w *rec.Writer) error { return w.Data(b) })
}

func (t *tap) resize(cols, rows int) {
	t.write(func(w *rec.Writer) error { return w.Resize(cols, rows) })
}

func (t *tap) write(f func(*rec.Writer) error) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.failed || t.closed {
		return
	}

	if err := f(t.w); err != nil {
		t.failed = true
		t.log.Warn("recording stopped", "pane", t.id, "err", err)
	}
}

// close ends the recording. The pane calls it after its read loop returned.
func (t *tap) close() error {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	t.closed = true

	if err := t.file.Close(); err != nil {
		return fmt.Errorf("close recording: %w", err)
	}

	return nil
}

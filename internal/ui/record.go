package ui

import (
	"bytes"
	"fmt"
	"io"
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

// tapQueueDepth bounds the records waiting for the writer; a slow disk drops
// records instead of stalling the read loop or the update loop.
const tapQueueDepth = 1024

// tapRecord is one queued record; data is nil for a resize.
type tapRecord struct {
	at         time.Time
	data       []byte
	cols, rows int
}

// tap writes one pane's PTY output and resizes to a recording file from its
// own goroutine, so no file write runs on the update loop or under the pane
// lock. A write error or a full queue is logged once and the pane is unaffected.
type tap struct {
	log   *slog.Logger
	id    core.PaneID
	file  io.Closer
	now   func() time.Time
	queue chan tapRecord
	done  chan struct{}

	mu      sync.Mutex // guards closed and the send on queue
	closed  bool
	dropped bool

	w      *rec.Writer
	failed bool // owned by the writer goroutine; read after done closes
}

// openTap creates <dir>/<id>.rec and writes its header; it never overwrites an
// existing recording. The create and header stay synchronous with the pane
// start, which already forks on the update loop.
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

	return startTap(log, id, f, w, now, tapQueueDepth), nil
}

func startTap(log *slog.Logger, id core.PaneID, file io.Closer, w *rec.Writer, now func() time.Time, depth int) *tap {
	t := &tap{
		log:   log,
		id:    id,
		file:  file,
		now:   now,
		queue: make(chan tapRecord, depth),
		done:  make(chan struct{}),
		w:     w,
	}

	go t.run()

	return t
}

func (t *tap) run() {
	defer close(t.done)

	for r := range t.queue {
		if t.failed {
			continue
		}

		var err error
		if r.data != nil {
			err = t.w.DataAt(r.data, r.at)
		} else {
			err = t.w.ResizeAt(r.cols, r.rows, r.at)
		}

		if err != nil {
			t.failed = true
			t.log.Warn("recording stopped", "pane", t.id, "err", err)
		}
	}
}

// data queues a copy of b; the caller reuses its buffer.
func (t *tap) data(b []byte) {
	if t == nil {
		return
	}

	t.enqueue(tapRecord{at: t.now(), data: bytes.Clone(b)})
}

func (t *tap) resize(cols, rows int) {
	if t == nil {
		return
	}

	t.enqueue(tapRecord{at: t.now(), cols: cols, rows: rows})
}

func (t *tap) enqueue(r tapRecord) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}

	select {
	case t.queue <- r:
	default:
		if !t.dropped {
			t.dropped = true
			t.log.Warn("recording is falling behind, dropping records", "pane", t.id)
		}
	}
}

// close drains the queue, waits for the writer and closes the file. The pane
// calls it after its read loop returned.
func (t *tap) close() error {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()

		return nil
	}

	t.closed = true
	close(t.queue)
	t.mu.Unlock()

	<-t.done

	if err := t.file.Close(); err != nil {
		return fmt.Errorf("close recording: %w", err)
	}

	return nil
}

package ui

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/ui/kitty"
)

const (
	pictureWorkers     = 4
	pictureMemBytes    = 64 << 20
	pictureLocalTime   = 10 * time.Second
	pictureFetchTime   = 20 * time.Second
	pictureCacheMaxAge = 30 * 24 * time.Hour
	pictureCacheDir    = 0o700
	pictureCacheFile   = 0o600
)

var errPictureTooBig = errors.New("image over the size limit")

// pictureFetcher downloads a remote image of at most limit bytes.
type pictureFetcher interface {
	Fetch(ctx context.Context, url, docHost string, limit int64) ([]byte, error)
}

// pictureLoader runs the registry's jobs on worker goroutines: it reads or
// fetches the bytes, decodes sizes and encodes transmissions, and answers with
// pictureDoneMsg. The App owns it and Close stops it.
type pictureLoader struct {
	cancel   context.CancelFunc // ends the workers' context, so a running fetch stops
	fetch    pictureFetcher
	cacheDir string
	log      *slog.Logger
	now      func() time.Time

	out  chan pictureDoneMsg
	done chan struct{}
	wg   sync.WaitGroup
	stop func() bool // deregisters the context hook

	mu      sync.Mutex
	cond    *sync.Cond
	closed  bool
	queue   []pictureJob
	pending map[pictureJob]struct{} // queued or running

	memMu sync.Mutex
	mem   map[pictureKey]*list.Element
	order *list.List // front is the most recent
	used  int
}

type memPicture struct {
	key  pictureKey
	data []byte
}

// newPictureLoader starts the workers. A nil f loads no remote image and an
// empty cacheDir keeps no disk cache.
func newPictureLoader(ctx context.Context, f pictureFetcher, cacheDir string, log *slog.Logger) *pictureLoader {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	ctx, cancel := context.WithCancel(ctx)
	l := &pictureLoader{
		cancel: cancel, fetch: f, cacheDir: cacheDir, log: log, now: time.Now,
		out: make(chan pictureDoneMsg, 256), done: make(chan struct{}),
		pending: map[pictureJob]struct{}{},
		mem:     map[pictureKey]*list.Element{}, order: list.New(),
	}
	l.cond = sync.NewCond(&l.mu)

	for i := range pictureWorkers {
		l.wg.Add(1)

		go l.work(ctx, i == 0)
	}

	l.stop = context.AfterFunc(ctx, l.shutdown)

	return l
}

// add queues j unless it is already queued or running; it never blocks.
func (l *pictureLoader) add(j pictureJob) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return
	}

	if _, ok := l.pending[j]; ok {
		return
	}

	l.pending[j] = struct{}{}
	l.queue = append(l.queue, j)
	l.cond.Signal()
}

// wait returns a command that delivers the next answer, or nothing once the
// loader stops. The update loop re-arms it after each answer.
func (l *pictureLoader) wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case m := <-l.out:
			return m
		case <-l.done:
			return nil
		}
	}
}

// close stops the workers and waits for them.
func (l *pictureLoader) close() {
	l.stop()
	l.cancel()
	l.shutdown()
	l.wg.Wait()
}

func (l *pictureLoader) shutdown() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return
	}

	l.closed = true
	l.queue = nil
	close(l.done)
	l.cond.Broadcast()
}

func (l *pictureLoader) work(ctx context.Context, sweeper bool) {
	defer l.wg.Done()

	if sweeper {
		l.sweep(ctx)
	}

	for {
		l.mu.Lock()
		for len(l.queue) == 0 && !l.closed {
			l.cond.Wait()
		}

		if l.closed {
			l.mu.Unlock()

			return
		}

		j := l.queue[0]
		l.queue = l.queue[1:]
		l.mu.Unlock()

		msg := l.run(ctx, j)

		l.mu.Lock()
		delete(l.pending, j)
		l.mu.Unlock()

		select {
		case l.out <- msg:
		case <-l.done:
			return
		}
	}
}

func (l *pictureLoader) run(ctx context.Context, j pictureJob) pictureDoneMsg {
	msg := pictureDoneMsg{job: j}

	data, err := l.bytes(ctx, j)
	if err != nil {
		msg.err = err

		return msg
	}

	if !j.render {
		msg.w, msg.h, msg.err = kitty.Size(data)

		return msg
	}

	png, err := kitty.Render(data, j.cols, j.rows, j.cell)
	if err != nil {
		msg.err = fmt.Errorf("render picture: %w", err)

		return msg
	}

	msg.seq = kitty.Transmit(j.id, png, j.cols, j.rows)

	return msg
}

// bytes returns the source's bytes from memory, else the file or the network.
func (l *pictureLoader) bytes(ctx context.Context, j pictureJob) ([]byte, error) {
	if data, ok := l.memGet(j.key); ok {
		return data, nil
	}

	var (
		data []byte
		err  error
	)

	if !strings.Contains(j.key.src, "://") {
		data, err = l.readLocal(ctx, j.root, j.key.src)
	} else {
		data, err = l.readRemote(ctx, j)
	}

	if err != nil {
		return nil, err
	}

	l.memPut(j.key, data)

	return data, nil
}

func (l *pictureLoader) readLocal(ctx context.Context, root, src string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, pictureLocalTime)
	defer cancel()

	rel, err := filepath.Rel(root, src)
	if err != nil {
		return nil, fmt.Errorf("picture path: %w", err)
	}

	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open picture root: %w", err)
	}

	defer func() { _ = r.Close() }()

	f, err := r.Open(rel)
	if err != nil {
		return nil, fmt.Errorf("open picture: %w", err)
	}

	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat picture: %w", err)
	}

	if !fi.Mode().IsRegular() {
		return nil, errors.New("picture is not a regular file")
	}

	data, err := io.ReadAll(io.LimitReader(f, pictureMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read picture: %w", err)
	}

	if len(data) > pictureMaxBytes {
		return nil, errPictureTooBig
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read picture: %w", err)
	}

	return data, nil
}

func (l *pictureLoader) readRemote(ctx context.Context, j pictureJob) ([]byte, error) {
	if l.fetch == nil {
		return nil, errors.New("remote pictures are off")
	}

	path := l.cachePath(j.key.src)
	if data, ok := l.cacheGet(path); ok {
		return data, nil
	}

	ctx, cancel := context.WithTimeout(ctx, pictureFetchTime)
	defer cancel()

	data, err := l.fetch.Fetch(ctx, j.key.src, j.host, pictureMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch picture: %w", err)
	}

	if len(data) > pictureMaxBytes {
		return nil, errPictureTooBig
	}

	l.cachePut(path, data)

	return data, nil
}

func (l *pictureLoader) cachePath(src string) string {
	if l.cacheDir == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(src))

	return filepath.Join(l.cacheDir, hex.EncodeToString(sum[:]))
}

func (l *pictureLoader) cacheGet(path string) ([]byte, bool) {
	if path == "" {
		return nil, false
	}

	data, err := os.ReadFile(path)
	if err != nil || len(data) > pictureMaxBytes {
		return nil, false
	}

	now := l.now()
	if err := os.Chtimes(path, now, now); err != nil {
		l.log.Debug("touch picture cache", "err", err)
	}

	return data, true
}

// cachePut writes through a temp file and a rename; a failure only costs the
// next fetch.
func (l *pictureLoader) cachePut(path string, data []byte) {
	if path == "" {
		return
	}

	if err := os.MkdirAll(l.cacheDir, pictureCacheDir); err != nil {
		l.log.Debug("picture cache dir", "err", err)

		return
	}

	tmp, err := os.CreateTemp(l.cacheDir, ".tmp-*")
	if err != nil {
		l.log.Debug("picture cache write", "err", err)

		return
	}

	_, werr := tmp.Write(data)
	cerr := tmp.Close()

	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), pictureCacheFile), os.Rename(tmp.Name(), path)); err != nil {
		l.log.Debug("picture cache write", "err", err)

		_ = os.Remove(tmp.Name())
	}
}

// sweep removes cache files untouched for 30 days.
func (l *pictureLoader) sweep(ctx context.Context) {
	if l.cacheDir == "" {
		return
	}

	entries, err := os.ReadDir(l.cacheDir)
	if err != nil {
		return
	}

	cutoff := l.now().Add(-pictureCacheMaxAge)

	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}

		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() || !fi.ModTime().Before(cutoff) {
			continue
		}

		if err := os.Remove(filepath.Join(l.cacheDir, e.Name())); err != nil {
			l.log.Debug("sweep picture cache", "err", err)
		}
	}
}

func (l *pictureLoader) memGet(k pictureKey) ([]byte, bool) {
	l.memMu.Lock()
	defer l.memMu.Unlock()

	el, ok := l.mem[k]
	if !ok {
		return nil, false
	}

	l.order.MoveToFront(el)

	return el.Value.(*memPicture).data, true
}

func (l *pictureLoader) memPut(k pictureKey, data []byte) {
	l.memMu.Lock()
	defer l.memMu.Unlock()

	if _, ok := l.mem[k]; ok || len(data) > pictureMemBytes {
		return
	}

	l.mem[k] = l.order.PushFront(&memPicture{k, data})
	l.used += len(data)

	for l.used > pictureMemBytes {
		el := l.order.Back()
		m := el.Value.(*memPicture)
		l.order.Remove(el)
		delete(l.mem, m.key)
		l.used -= len(m.data)
	}
}

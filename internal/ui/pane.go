package ui

import (
	"image/color"
	"io"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/term"
)

// readChunk is the PTY read size; large enough that a flooding child costs
// few lock round trips.
const readChunk = 32 << 10

// proc is the child process behind a pane; *term.Process implements it.
type proc interface {
	io.ReadWriter
	Resize(cols, rows int) error
	Done() <-chan struct{}
	Cwd() (string, error)
	Close() error
}

// paneDirtyMsg tells the App a pane has output to render.
type paneDirtyMsg struct{ id core.PaneID }

// paneExitedMsg tells the App a pane's output ended.
type paneExitedMsg struct{ id core.PaneID }

// holdExpiredMsg ends a sync hold; the App renders the pane again.
type holdExpiredMsg struct{ id core.PaneID }

// pane owns one process and its emulator, and keeps a per-row render cache.
// Its mutex serialises every VT call.
type pane struct {
	id    core.PaneID
	proc  proc
	clock clock
	tap   *tap // nil unless recording; closed after the read loop

	dirty  chan struct{} // cap 1: at most one outstanding notification
	exited chan struct{} // closed when the read loop ends
	wg     sync.WaitGroup

	// Update-goroutine state, outside mu.
	holdArmed bool      // a holdTick is outstanding
	cwdAt     time.Time // start of the last cwd lookup
	cwdBusy   bool      // a lookup is running
	cwdWait   bool      // a deferred lookup is scheduled

	mu         sync.Mutex
	vt         term.VT
	rows       []string
	forced     bool
	closed     bool
	syncSerial uint64
	openedAt   time.Time
	closeOnce  sync.Once
	closeErr   error
}

// paneOption configures newPane.
type paneOption func(*pane)

// withTap records the pane's output and resizes.
func withTap(t *tap) paneOption { return func(p *pane) { p.tap = t } }

// withColors sets the colours the guest's OSC 10 and 11 queries are answered with.
func withColors(fg, bg color.Color) paneOption {
	return func(p *pane) { p.vt.SetDefaultColors(fg, bg) }
}

// setColors changes them on a running pane.
func (p *pane) setColors(fg, bg color.Color) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.closed {
		p.vt.SetDefaultColors(fg, bg)
	}
}

// newPane starts the emulator, renders every row once and starts the read loop.
func newPane(id core.PaneID, p proc, cols, rows int, c clock, opts ...paneOption) *pane {
	pn := &pane{
		id:     id,
		proc:   p,
		clock:  c,
		dirty:  make(chan struct{}, 1),
		exited: make(chan struct{}),
		vt:     term.NewVT(cols, rows, p),
		rows:   make([]string, rows),
	}

	for _, o := range opts {
		o(pn)
	}

	for y := range pn.rows {
		pn.rows[y] = renderRow(pn.vt, y, cols)
	}

	pn.wg.Go(pn.readLoop)

	return pn
}

func (p *pane) readLoop() {
	defer close(p.exited)

	buf := make([]byte, readChunk)

	for {
		n, err := p.proc.Read(buf)
		if n > 0 {
			p.tap.data(buf[:n])
			p.feed(buf[:n])
		}

		if err != nil {
			return
		}
	}
}

func (p *pane) feed(b []byte) {
	p.mu.Lock()

	if p.closed {
		p.mu.Unlock()

		return
	}

	_, _ = p.vt.Write(b)

	if open, serial := p.vt.SyncState(); open && serial != p.syncSerial {
		p.syncSerial = serial
		p.openedAt = p.clock.Now()
	}

	p.mu.Unlock()

	select {
	case p.dirty <- struct{}{}:
	default:
	}
}

// wait blocks until the pane has output or its process ended (output EOF or
// the process being reaped, whichever comes first). The App re-arms it after
// every dirty message. Pending output is reported before the exit.
func (p *pane) wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case <-p.dirty:
			return paneDirtyMsg{id: p.id}
		default:
		}

		select {
		case <-p.dirty:
			return paneDirtyMsg{id: p.id}
		case <-p.exited:
			return paneExitedMsg{id: p.id}
		case <-p.proc.Done():
			return paneExitedMsg{id: p.id}
		}
	}
}

// holdTick fires holdExpiredMsg when a sync hold ends at until.
func (p *pane) holdTick(until time.Time) tea.Cmd {
	return tea.Tick(until.Sub(p.clock.Now()), func(time.Time) tea.Msg {
		return holdExpiredMsg{id: p.id}
	})
}

// render re-renders the rows and reports whether any cached row changed.
// While a sync update is held it renders nothing and returns the hold's end.
func (p *pane) render() (changed bool, heldUntil time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if open, _ := p.vt.SyncState(); open {
		if end := p.openedAt.Add(syncMaxHold); p.clock.Now().Before(end) {
			return false, end
		}
	}

	w, _ := p.vt.Size()

	changed = p.forced
	p.forced = false

	for y := range p.rows {
		if s := renderRow(p.vt, y, w); s != p.rows[y] {
			p.rows[y] = s
			changed = true
		}
	}

	return changed, time.Time{}
}

// resize resizes the emulator and the process and blanks the cache; the
// caller renders afterwards. It reports false when the size is unchanged.
func (p *pane) resize(cols, rows int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c, r := p.vt.Size(); c == cols && r == rows {
		return false
	}

	p.vt.Resize(cols, rows)
	p.tap.resize(cols, rows)
	// A dead child cannot take a resize; its exit is reported by wait.
	_ = p.proc.Resize(cols, rows)

	blank := strings.Repeat(" ", cols)
	p.rows = make([]string, rows)

	for y := range p.rows {
		p.rows[y] = blank
	}

	p.forced = true

	return true
}

// lines returns the row cache, each row exactly the pane width. It is only
// valid until the next render or resize.
func (p *pane) lines() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.rows
}

func (p *pane) size() (cols, rows int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.vt.Size()
}

func (p *pane) cursor() (uv.Position, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.vt.Cursor()
}

func (p *pane) title() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.vt.Title()
}

// workingDir prefers the guest's OSC 7 report, then the process's own cwd. A
// closed pane has none.
func (p *pane) workingDir() string {
	p.mu.Lock()
	d := p.vt.WorkingDir()
	closed := p.closed
	p.mu.Unlock()

	if closed {
		return ""
	}

	if d != "" {
		return d
	}

	// Outside mu: the lookup is file IO.
	if d, err := p.proc.Cwd(); err == nil {
		return d
	}

	return ""
}

func (p *pane) sendKey(k uv.KeyPressEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.closed {
		p.vt.SendKey(k)
	}
}

func (p *pane) sendPaste(text string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.closed {
		p.vt.SendBytes(term.EncodePaste(text, p.vt.BracketedPaste()))
	}
}

// sendFocus reports focus in or out, only when the guest asked for it.
func (p *pane) sendFocus(in bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed || !p.vt.FocusReporting() {
		return
	}

	if in {
		p.vt.SendBytes([]byte("\x1b[I"))
	} else {
		p.vt.SendBytes([]byte("\x1b[O"))
	}
}

// close stops the process, waits for the read loop, then closes the emulator.
// It is idempotent.
func (p *pane) close() error {
	p.closeOnce.Do(func() {
		// Close the process before taking mu: VT.Close waits on a reply drain that may be blocked writing to the PTY.
		err := p.proc.Close()

		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()

		p.wg.Wait()

		if terr := p.tap.close(); err == nil {
			err = terr
		}

		p.mu.Lock()
		defer p.mu.Unlock()

		if verr := p.vt.Close(); err == nil {
			err = verr
		}

		p.closeErr = err
	})

	return p.closeErr
}

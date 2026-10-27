// Package term hosts terminal emulation for agent panes behind the VT interface.
package term

import (
	"image/color"
	"io"
	"net/url"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	tvt "tuios.dev/vt"
)

// ScrollbackLines is the scrollback depth of every pane emulator.
const ScrollbackLines = 10000

// VT is one pane's emulator. Not safe for concurrent use; the pane serialises calls.
type VT interface {
	Write(p []byte) (int, error)
	Resize(cols, rows int)
	Size() (cols, rows int)
	CellAt(x, y int) *uv.Cell // nil outside the grid
	Cursor() (pos uv.Position, visible bool)
	ScrollbackLen() int
	ScrollbackLine(i int) []uv.Cell // copy of history line i (0 oldest); nil out of range
	ScrollbackWrapped(i int) bool   // history line i soft-wraps into i+1
	HistoryTrimmed() uint64         // lines dropped from the front of the history so far: ring overwrite, CSI 3J, reflow
	RowWrapped(y int) bool          // live row y soft-wraps into y+1
	AltScreen() bool
	MouseTracking() bool                                    // guest enabled any of mouse modes 9, 1000, 1001, 1002, 1003
	SendMouse(ev uv.MouseEvent)                             // encoded per the guest's modes onto the reply stream; nothing when tracking is off
	SetClipboardHandler(fn func(selection, content string)) // guest OSC 52 writes; runs on the goroutine calling Write under the pane lock, so it must only enqueue
	SyncState() (open bool, serial uint64)                  // raw DEC 2026; serial bumps on each closed-to-open edge
	Title() string                                          // last OSC 0/2 title
	WorkingDir() string                                     // last OSC 7 path (file URL decoded to a path), "" if none
	FocusReporting() bool                                   // DECSET 1004
	KeyModes() KeyModes                                     // for EncodeKey
	BracketedPaste() bool                                   // DECSET 2004
	SetDefaultColors(fg, bg color.Color)                    // answers OSC 10 and 11 queries; mode 2031 guests get a notice on change
	SendBytes(b []byte)                                     // raw bytes queued on the reply stream, ordered with query replies
	SendKey(k uv.KeyPressEvent)                             // EncodeKey with the current KeyModes; unencodable keys send nothing
	Close() error                                           // idempotent: emulator Close, then wait for the drain
}

// KeyModes are the guest's keyboard modes that change how keys are encoded.
type KeyModes struct {
	KittyFlags, ModifyOtherKeys int
	AppCursor, AppKeypad        bool
}

// NewVT starts an emulator and a goroutine that copies its replies (query
// responses and SendBytes input) to reply. Close stops it.
func NewVT(cols, rows int, reply io.Writer) VT {
	host, _ := os.Hostname()
	t := &tuiosVT{em: tvt.NewWithScrollback(cols, rows, ScrollbackLines), host: host}
	// SetCallbacks replaces the whole set, so add callbacks here.
	t.applyCallbacks()
	t.countTrims()
	t.drain = startDrain(t.em, reply)

	return t
}

type tuiosVT struct {
	em        tvt.Terminal
	clipboard func(selection, content string)
	sync      syncTracker
	drain     *drain
	title     string
	cwd       string
	host      string // local hostname; OSC 7 from any other host is ignored
	trimmed   atomic.Uint64
}

// countTrims counts the lines dropped from the front of the history. The
// emulator's own trim hook keeps its OSC 133 markers aligned, so it is
// replaced by one that does both. A backend without the accessors counts nothing.
func (t *tuiosVT) countTrims() {
	em, ok := t.em.(interface {
		Scrollback() *tvt.Scrollback
		SemanticMarkers() *tvt.SemanticMarkerList
	})
	if !ok {
		return
	}

	sb := em.Scrollback()
	if sb == nil {
		return
	}

	markers := em.SemanticMarkers()

	sb.SetOnTrim(func(n int) {
		t.trimmed.Add(uint64(n))

		if markers != nil {
			markers.AdjustForScrollbackTrim(n)
		}
	})
}

// applyCallbacks installs the whole callback set; SetCallbacks replaces it, so
// every callback is listed here. ClipboardQuery stays unset: guests cannot read
// the clipboard.
func (t *tuiosVT) applyCallbacks() {
	cb := tvt.Callbacks{
		EnableMode:  t.sync.enable,
		DisableMode: t.sync.disable,
		Title:       func(s string) { t.title = s },
		WorkingDirectory: func(s string) {
			if path, ok := fileURLPath(s, t.host); ok {
				t.cwd = path
			}
		},
	}
	if t.clipboard != nil {
		cb.ClipboardSet = t.clipboard
	}

	t.em.SetCallbacks(cb)
}

func (t *tuiosVT) SetClipboardHandler(fn func(selection, content string)) {
	t.clipboard = fn
	t.applyCallbacks()
}

func (t *tuiosVT) ScrollbackLine(i int) []uv.Cell {
	// The emulator may return a slice it caches.
	return slices.Clone(t.em.ScrollbackLine(i))
}

func (t *tuiosVT) HistoryTrimmed() uint64 { return t.trimmed.Load() }

func (t *tuiosVT) ScrollbackWrapped(i int) bool {
	wrapped, known := t.em.ScrollbackSoftWrapped(i)

	return wrapped && known
}

func (t *tuiosVT) RowWrapped(y int) bool {
	wrapped, known := t.em.RowSoftWrapped(y)

	return wrapped && known
}

func (t *tuiosVT) AltScreen() bool             { return t.em.IsAltScreen() }
func (t *tuiosVT) MouseTracking() bool         { return t.em.HasMouseMode() }
func (t *tuiosVT) SendMouse(ev uv.MouseEvent)  { t.em.SendMouse(ev) }
func (t *tuiosVT) Write(p []byte) (int, error) { return t.em.Write(p) }
func (t *tuiosVT) Resize(cols, rows int)       { t.em.Resize(cols, rows) }
func (t *tuiosVT) Size() (cols, rows int)      { return t.em.Width(), t.em.Height() }
func (t *tuiosVT) CellAt(x, y int) *uv.Cell    { return t.em.CellAt(x, y) }
func (t *tuiosVT) ScrollbackLen() int          { return t.em.ScrollbackLen() }
func (t *tuiosVT) Title() string               { return t.title }
func (t *tuiosVT) WorkingDir() string          { return t.cwd }
func (t *tuiosVT) FocusReporting() bool        { return t.em.FocusReportingEnabled() }
func (t *tuiosVT) BracketedPaste() bool        { return t.em.BracketedPasteEnabled() }
func (t *tuiosVT) SetDefaultColors(fg, bg color.Color) {
	t.em.SetReportColors(fg, bg, [16]color.Color{})
}
func (t *tuiosVT) SendBytes(b []byte) { t.em.WriteResponse(b) }

func (t *tuiosVT) SendKey(k uv.KeyPressEvent) {
	if b := EncodeKey(k, t.KeyModes()); b != nil {
		t.SendBytes(b)
	}
}

func (t *tuiosVT) Cursor() (uv.Position, bool) {
	return t.em.CursorPosition(), !t.em.IsCursorHidden()
}

func (t *tuiosVT) SyncState() (bool, uint64) { return t.sync.open, t.sync.serial }

func (t *tuiosVT) KeyModes() KeyModes {
	return KeyModes{
		KittyFlags:      t.em.KittyKeyboardFlags(),
		ModifyOtherKeys: t.em.ModifyOtherKeys(),
		AppCursor:       t.em.ApplicationCursorKeys(),
		AppKeypad:       t.em.GetModes()[66],
	}
}

func (t *tuiosVT) Close() error {
	var err error

	t.drain.once.Do(func() {
		err = t.em.Close()
		<-t.drain.done
	})

	return err
}

// fileURLPath decodes an OSC 7 file URL to a path; a bare path passes through.
// It reports false for a URL on another host (a remote shell), whose path means
// nothing here.
func fileURLPath(s, localHost string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "file" {
		return s, true
	}

	switch u.Hostname() {
	case "", "localhost", localHost:
		return u.Path, true
	}

	return "", false
}

// syncTracker follows DEC mode 2026 from the emulator's mode callbacks, which
// run on the goroutine calling Write.
type syncTracker struct {
	open   bool
	serial uint64
}

func (s *syncTracker) enable(m ansi.Mode) {
	if m != ansi.ModeSynchronizedOutput {
		return
	}

	if !s.open {
		s.serial++
	}

	s.open = true
}

func (s *syncTracker) disable(m ansi.Mode) {
	if m == ansi.ModeSynchronizedOutput {
		s.open = false
	}
}

// drain copies src to dst until src fails. A failing dst only drops bytes, so
// a blocked pipe never stalls Write.
type drain struct {
	done chan struct{}
	once sync.Once
}

func startDrain(src io.Reader, dst io.Writer) *drain {
	d := &drain{done: make(chan struct{})}

	go func() {
		defer close(d.done)

		buf := make([]byte, 4096)
		dropping := false

		for {
			n, err := src.Read(buf)
			if n > 0 && !dropping {
				if _, werr := dst.Write(buf[:n]); werr != nil {
					dropping = true
				}
			}

			if err != nil {
				return
			}
		}
	}()

	return d
}

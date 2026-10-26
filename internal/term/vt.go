// Package term hosts terminal emulation for agent panes behind the VT interface.
package term

import (
	"image/color"
	"io"
	"net/url"
	"os"
	"sync"

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
	SyncState() (open bool, serial uint64) // raw DEC 2026; serial bumps on each closed-to-open edge
	Title() string                         // last OSC 0/2 title
	WorkingDir() string                    // last OSC 7 path (file URL decoded to a path), "" if none
	FocusReporting() bool                  // DECSET 1004
	KeyModes() KeyModes                    // for EncodeKey
	BracketedPaste() bool                  // DECSET 2004
	SetDefaultColors(fg, bg color.Color)   // answers OSC 10 and 11 queries; mode 2031 guests get a notice on change
	SendBytes(b []byte)                    // raw bytes queued on the reply stream, ordered with query replies
	SendKey(k uv.KeyPressEvent)            // EncodeKey with the current KeyModes; unencodable keys send nothing
	Close() error                          // idempotent: emulator Close, then wait for the drain
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
	t.em.SetCallbacks(tvt.Callbacks{
		EnableMode:  t.sync.enable,
		DisableMode: t.sync.disable,
		Title:       func(s string) { t.title = s },
		WorkingDirectory: func(s string) {
			if path, ok := fileURLPath(s, t.host); ok {
				t.cwd = path
			}
		},
	})
	t.drain = startDrain(t.em, reply)

	return t
}

type tuiosVT struct {
	em    tvt.Terminal
	sync  syncTracker
	drain *drain
	title string
	cwd   string
	host  string // local hostname; OSC 7 from any other host is ignored
}

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

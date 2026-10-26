package term

import (
	"bytes"
	"errors"
	"image/color"
	"runtime"
	"sync"
	"testing"
	"time"
)

type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met before deadline")
		}

		time.Sleep(time.Millisecond)
	}
}

func newTestVT(t *testing.T, cols, rows int) (VT, *safeBuf) {
	t.Helper()

	reply := &safeBuf{}
	v := NewVT(cols, rows, reply)
	t.Cleanup(func() { _ = v.Close() })

	return v, reply
}

func TestWriteAndCells(t *testing.T) {
	v, _ := newTestVT(t, 20, 5)

	if _, err := v.Write([]byte("hi\x1b[38;2;1;2;3mX中")); err != nil {
		t.Fatal(err)
	}

	if c := v.CellAt(0, 0); c == nil || c.Content != "h" {
		t.Fatalf("cell 0 = %+v", c)
	}

	c := v.CellAt(2, 0)
	if c == nil || c.Content != "X" {
		t.Fatalf("cell 2 = %+v", c)
	}

	r, g, b, _ := c.Style.Fg.RGBA()
	if got := (color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0}); got.R != 1 || got.G != 2 || got.B != 3 {
		t.Fatalf("fg = %+v", got)
	}

	if w := v.CellAt(3, 0); w == nil || w.Content != "中" || w.Width != 2 {
		t.Fatalf("wide cell = %+v", w)
	}

	if v.CellAt(-1, 0) != nil || v.CellAt(0, 5) != nil || v.CellAt(20, 0) != nil {
		t.Fatal("cells outside the grid must be nil")
	}
}

func TestSyncState(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		open   bool
		serial uint64
	}{
		{"open", "\x1b[?2026h", true, 1},
		{"open then close", "\x1b[?2026h\x1b[?2026l", false, 1},
		{"reopen", "\x1b[?2026h\x1b[?2026l\x1b[?2026h", true, 2},
		{"open twice", "\x1b[?2026h\x1b[?2026h", true, 1},
		{"other mode ignored", "\x1b[?25l", false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := newTestVT(t, 10, 3)
			if _, err := v.Write([]byte(tc.in)); err != nil {
				t.Fatal(err)
			}

			open, serial := v.SyncState()
			if open != tc.open || serial != tc.serial {
				t.Fatalf("SyncState = %v, %d; want %v, %d", open, serial, tc.open, tc.serial)
			}
		})
	}
}

func TestTitleAndWorkingDir(t *testing.T) {
	v, _ := newTestVT(t, 10, 3)

	if v.Title() != "" || v.WorkingDir() != "" {
		t.Fatal("title and working dir must start empty")
	}

	if _, err := v.Write([]byte("\x1b]2;build\x07\x1b]7;file://host/tmp/x%20y\x07")); err != nil {
		t.Fatal(err)
	}

	if got := v.Title(); got != "build" {
		t.Fatalf("Title = %q", got)
	}

	if got := v.WorkingDir(); got != "/tmp/x y" {
		t.Fatalf("WorkingDir = %q", got)
	}
}

func TestRepliesOrdered(t *testing.T) {
	v, reply := newTestVT(t, 10, 3)

	if _, err := v.Write([]byte("\x1b[c")); err != nil {
		t.Fatal(err)
	}

	v.SendBytes([]byte("k"))

	waitFor(t, func() bool {
		s := reply.String()

		return len(s) > 1 && s[len(s)-1] == 'k'
	})

	s := reply.String()
	if s[:2] != "\x1b[" || bytes.Count([]byte(s), []byte("k")) != 1 {
		t.Fatalf("reply = %q; want DA1 reply followed by k", s)
	}
}

func TestFocusReportingAndModes(t *testing.T) {
	v, _ := newTestVT(t, 10, 3)

	if v.FocusReporting() || v.BracketedPaste() || v.KeyModes() != (KeyModes{}) {
		t.Fatal("modes must start off")
	}

	if _, err := v.Write([]byte("\x1b[?1004h\x1b[?2004h\x1b[>1u\x1b[?1h")); err != nil {
		t.Fatal(err)
	}

	if !v.FocusReporting() {
		t.Fatal("FocusReporting not set")
	}

	if !v.BracketedPaste() {
		t.Fatal("BracketedPaste not set")
	}

	km := v.KeyModes()
	if km.KittyFlags != 1 || !km.AppCursor {
		t.Fatalf("KeyModes = %+v", km)
	}

	if _, err := v.Write([]byte("\x1b=")); err != nil {
		t.Fatal(err)
	}

	if !v.KeyModes().AppKeypad {
		t.Fatal("AppKeypad not set")
	}
}

func TestResizeAndSize(t *testing.T) {
	v, _ := newTestVT(t, 20, 5)

	if c, r := v.Size(); c != 20 || r != 5 {
		t.Fatalf("Size = %d, %d", c, r)
	}

	v.Resize(30, 8)

	if c, r := v.Size(); c != 30 || r != 8 {
		t.Fatalf("Size after resize = %d, %d", c, r)
	}

	if v.CellAt(29, 7) == nil || v.CellAt(30, 0) != nil {
		t.Fatal("grid bounds did not follow the resize")
	}
}

func TestScrollbackLen(t *testing.T) {
	v, _ := newTestVT(t, 10, 3)

	for range 10 {
		if _, err := v.Write([]byte("line\r\n")); err != nil {
			t.Fatal(err)
		}
	}

	if v.ScrollbackLen() == 0 {
		t.Fatal("scrollback empty after scrolling output")
	}
}

func TestCloseIdempotent(t *testing.T) {
	base := runtime.NumGoroutine()

	v := NewVT(10, 3, &safeBuf{})

	if err := v.Close(); err != nil {
		t.Fatal(err)
	}

	if err := v.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}

	waitFor(t, func() bool { return runtime.NumGoroutine() <= base })
}

type failOnce struct {
	mu    sync.Mutex
	calls int
}

func (f *failOnce) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.calls == 1 {
		return 0, errors.New("broken pipe")
	}

	return len(p), nil
}

func TestReplyWriteErrorKeepsDraining(t *testing.T) {
	w := &failOnce{}
	v := NewVT(10, 3, w)

	if _, err := v.Write([]byte("\x1b[c")); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()

		return w.calls == 1
	})

	for range 50 {
		if _, err := v.Write([]byte("\x1b[c")); err != nil {
			t.Fatal(err)
		}

		v.SendBytes([]byte("x"))
	}

	// A live drain empties the reply queue even though every write is dropped.
	time.Sleep(200 * time.Millisecond)

	if left := v.(*tuiosVT).em.TakeResponses(); len(left) != 0 {
		t.Fatalf("drain stopped after the write error; %d reply bytes left queued", len(left))
	}

	if err := v.Close(); err != nil {
		t.Fatal(err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.calls != 1 {
		t.Fatalf("writer called %d times; replies after its error must be dropped", w.calls)
	}
}

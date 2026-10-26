package ui

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/term"
	"github.com/mhersson/muxreveal/internal/term/rec"
)

// shCommand runs script instead of the pane's login shell.
func shCommand(script string) func(context.Context, term.Command, int, int) (proc, error) {
	return func(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
		c.Argv = []string{"/bin/sh", "-c", script}

		return termStart(ctx, c, cols, rows)
	}
}

func TestRecordFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec")

	app, err := New(Options{
		Logger: testLogger(), Store: core.NewStore(core.InitialState(core.DefaultConfig())),
		Cwd: t.TempDir(), RecordDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	app.start = shCommand("printf hi")

	t.Cleanup(func() { _ = app.Close(context.Background()) })

	app.Update(tea.WindowSizeMsg{Width: 40, Height: 10})

	pn := app.panes["p1"]
	if pn == nil {
		t.Fatal("no pane opened")
	}

	// The read loop ends once the child's output is drained.
	select {
	case <-pn.exited:
	case <-time.After(testDeadline):
		t.Fatal("pane output did not end")
	}

	ctx, cancel := context.WithTimeout(context.Background(), testDeadline)
	defer cancel()

	if err := app.Close(ctx); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir = %v, %v; want mode 0700", st, err)
	}

	path := filepath.Join(dir, "p1.rec")

	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file = %v, %v; want mode 0600", fi, err)
	}

	recs, err := rec.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if recs[0].Data != nil || recs[0].Cols != 40 || recs[0].Rows != 8 {
		t.Fatalf("initial record = %+v, want a 40x8 resize", recs[0])
	}

	vt := term.NewVT(recs[0].Cols, recs[0].Rows, io.Discard)
	defer func() { _ = vt.Close() }()

	for _, r := range recs[1:] {
		if r.Data != nil {
			_, _ = vt.Write(r.Data)
		}
	}

	var row strings.Builder

	for x := range 2 {
		row.WriteString(vt.CellAt(x, 0).String())
	}

	if row.String() != "hi" {
		t.Fatalf("replayed row 0 = %q, want hi", row.String())
	}
}

func TestRecordTapRecordsResizes(t *testing.T) {
	dir := t.TempDir()
	clk := &fakeClock{t: t0}

	tp, err := openTap(testLogger(), dir, "p9", 20, 5, clk.Now)
	if err != nil {
		t.Fatal(err)
	}

	fp := newFakeProc()
	p := newPane("p9", fp, 20, 5, clk, withTap(tp))

	clk.Advance(time.Millisecond)
	p.resize(30, 6)

	if err := p.close(); err != nil {
		t.Fatal(err)
	}

	recs, err := rec.Load(filepath.Join(dir, "p9.rec"))
	if err != nil {
		t.Fatal(err)
	}

	if len(recs) != 2 || recs[1].Cols != 30 || recs[1].Rows != 6 {
		t.Fatalf("records = %+v, want the initial size and the resize", recs)
	}
}

// failAfter accepts n writes, then fails every write and counts the attempts.
type failAfter struct{ ok, calls int }

func (f *failAfter) Write(b []byte) (int, error) {
	f.calls++

	if f.calls > f.ok {
		return 0, io.ErrClosedPipe
	}

	return len(b), nil
}

func TestRecordTapFailureStopsRecording(t *testing.T) {
	var logs strings.Builder

	log := slog.New(slog.NewTextHandler(&logs, nil))
	// The header and the initial size succeed; the next write fails.
	fw := &failAfter{ok: 2}

	w, err := rec.NewWriter(fw, 20, 5, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	tp := &tap{log: log, id: "p1", w: w}

	tp.data([]byte("x"))

	calls := fw.calls

	tp.data([]byte("y"))
	tp.resize(10, 10)

	if !tp.failed {
		t.Fatal("tap kept recording after a write error")
	}

	if fw.calls != calls {
		t.Errorf("writes after the failure = %d, want none", fw.calls-calls)
	}

	if n := strings.Count(logs.String(), "recording stopped"); n != 1 {
		t.Errorf("failure logged %d times, want once:\n%s", n, logs.String())
	}
}

func TestRecordDirCollisionSetsNotice(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "p1.rec"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	app, err := New(Options{
		Logger: testLogger(), Store: core.NewStore(core.InitialState(core.DefaultConfig())),
		Cwd: t.TempDir(), RecordDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	app.start = func(context.Context, term.Command, int, int) (proc, error) { return newFakeProc(), nil }

	t.Cleanup(func() { _ = app.Close(context.Background()) })

	app.Update(tea.WindowSizeMsg{Width: 40, Height: 10})

	if app.panes["p1"] == nil || !strings.Contains(app.notice, "recording is off for p1") {
		t.Fatalf("pane = %v, notice = %q; want the pane running and a recording notice", app.panes["p1"], app.notice)
	}
}

func TestOpenTapRefusesExistingFile(t *testing.T) {
	dir := t.TempDir()

	tp, err := openTap(testLogger(), dir, "p1", 20, 5, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = tp.close() }()

	if _, err := openTap(testLogger(), dir, "p1", 20, 5, time.Now); err == nil {
		t.Fatal("second recording overwrote the first")
	}
}

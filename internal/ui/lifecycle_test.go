package ui

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/term"
)

const testCwd = "/work"

func testGetenv(k string) string {
	if k == "SHELL" {
		return "/bin/testsh"
	}

	return ""
}

func shellGetenv(k string) string {
	if k == "SHELL" {
		return "/bin/sh"
	}

	return ""
}

// starts is a fake term.Start that records every request.
type starts struct {
	mu    sync.Mutex
	cmds  []term.Command
	sizes [][2]int
	procs []*fakeProc
	err   error
}

func (s *starts) start(_ context.Context, c term.Command, cols, rows int) (proc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return nil, s.err
	}

	fp := newFakeProc()
	s.cmds = append(s.cmds, c)
	s.sizes = append(s.sizes, [2]int{cols, rows})
	s.procs = append(s.procs, fp)

	return fp, nil
}

func (s *starts) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.cmds)
}

func (s *starts) proc(i int) *fakeProc {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.procs[i]
}

func closeApp(t *testing.T, a *App) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.Close(ctx); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// lifeApp is an App that has not started yet and starts fake processes.
func lifeApp(t *testing.T) (*App, *starts) {
	t.Helper()

	a, err := New(Options{
		Logger: testLogger(),
		Store:  core.NewStore(core.InitialState(core.Config{Theme: "catppuccin-mocha"})),
		Getenv: testGetenv,
		Cwd:    testCwd,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	st := &starts{}
	a.start = st.start

	t.Cleanup(func() { closeApp(t, a) })

	return a, st
}

// lifeAppSized is lifeApp after the first size message opened the shell.
func lifeAppSized(t *testing.T, w, h int) (*App, *starts) {
	t.Helper()

	a, st := lifeApp(t)
	sized(t, a, w, h)

	return a, st
}

// chord presses the leader and then the key.
func chord(a *App, key rune) tea.Cmd {
	return press(a, keyLeader, keyRune(key))
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}

	_, ok := cmd().(tea.QuitMsg)

	return ok
}

// collect runs the commands of cmd (a batch or one command) and returns the
// messages that arrive within a short wait; blocking pane waits are left running.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}

	first := cmd()

	cmds, ok := first.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{first}
	}

	out := make(chan tea.Msg, len(cmds))

	for _, c := range cmds {
		go func() { out <- c() }()
	}

	var msgs []tea.Msg

	timeout := time.After(300 * time.Millisecond)

	for range cmds {
		select {
		case m := <-out:
			msgs = append(msgs, m)
		case <-timeout:
			return msgs
		}
	}

	return msgs
}

func TestStartupOpensShell(t *testing.T) {
	a, st := lifeApp(t)

	sized(t, a, 100, 30)

	if st.count() != 1 {
		t.Fatalf("starts = %d, want 1", st.count())
	}

	c := st.cmds[0]
	want := term.ShellCommand(testGetenv, testCwd)

	if !slices.Equal(c.Argv, want.Argv) || c.Dir != testCwd {
		t.Errorf("command = %v in %q, want %v in %q", c.Argv, c.Dir, want.Argv, testCwd)
	}

	if st.sizes[0] != [2]int{100, 28} {
		t.Errorf("start size = %v, want [100 28]", st.sizes[0])
	}

	s := a.store.State()
	if len(s.Panes) != 1 || s.Panes["p1"].Kind != core.KindShell || s.Panes["p1"].Cwd != testCwd || s.Focus != "p1" {
		t.Errorf("state = %+v, want one focused shell pane p1 in %s", s, testCwd)
	}

	if a.panes["p1"] == nil {
		t.Fatal("pane p1 is not in the registry")
	}

	sized(t, a, 120, 40)

	if st.count() != 1 {
		t.Errorf("second size message started a process (starts = %d)", st.count())
	}

	if got := st.proc(0).resized(); len(got) == 0 || got[len(got)-1] != [2]int{120, 38} {
		t.Errorf("resizes = %v, want the last to be [120 38]", got)
	}
}

func TestStartupSizeBeforeFirstMessage(t *testing.T) {
	a, st := lifeApp(t)

	for _, k := range []rune{'v', 's', 'n'} {
		chord(a, k)
	}

	if st.count() != 0 || a.started || len(a.store.State().Panes) != 0 {
		t.Errorf("a pane opened before the first size (starts = %d)", st.count())
	}

	sized(t, a, 100, 30)

	if st.count() != 1 {
		t.Errorf("starts after the first size = %d, want the one startup shell", st.count())
	}
}

func TestStartFailure(t *testing.T) {
	a, st := lifeApp(t)
	st.err = errors.New("no such shell")

	sized(t, a, 100, 30)

	if got := len(a.store.State().Panes); got != 0 || len(a.panes) != 0 {
		t.Errorf("failed start left %d panes in state and %d in the registry", got, len(a.panes))
	}

	if !strings.Contains(a.notice, "no such shell") {
		t.Errorf("notice = %q, want the start error", a.notice)
	}

	st.err = nil
	chord(a, 'v')

	if got := len(a.store.State().Panes); got != 1 {
		t.Errorf("panes after a later successful split = %d, want 1", got)
	}
}

func TestSplitCommands(t *testing.T) {
	tests := []struct {
		key   rune
		split core.Split
		kind  core.PaneKind
	}{
		{'s', core.SplitHorizontal, core.KindShell},
		{'v', core.SplitVertical, core.KindShell},
		{'n', core.SplitVertical, core.KindAgent},
	}

	for _, tt := range tests {
		t.Run(string(tt.key), func(t *testing.T) {
			a, st := lifeAppSized(t, 100, 30)

			if err := a.dispatch(core.SetPaneCwd{ID: "p1", Cwd: "/elsewhere"}); err != nil {
				t.Fatal(err)
			}

			chord(a, tt.key)

			s := a.store.State()
			if s.Layout == nil || s.Layout.Split != tt.split || s.Layout.A.Pane != "p1" || s.Layout.B.Pane != "p2" {
				t.Fatalf("layout = %+v, want a %s split of p1 and p2", s.Layout, tt.split)
			}

			if s.Focus != "p2" || s.Panes["p2"].Kind != tt.kind || s.Panes["p2"].Cwd != "/elsewhere" {
				t.Errorf("state = %+v, want p2 (%s) focused in /elsewhere", s, tt.kind)
			}

			want := term.ShellCommand(testGetenv, "/elsewhere")
			if tt.kind == core.KindAgent {
				want = term.AgentCommand(testGetenv, "/elsewhere")
			}

			if got := st.cmds[1]; !slices.Equal(got.Argv, want.Argv) || got.Dir != "/elsewhere" {
				t.Errorf("command = %v in %q, want %v in /elsewhere", got.Argv, got.Dir, want.Argv)
			}

			assertSizes(t, a)

			if got := st.sizes[1]; got != paneSize(t, a, "p2") {
				t.Errorf("start size = %v, want the content rect %v", got, paneSize(t, a, "p2"))
			}
		})
	}
}

// paneSize is the content rect of a pane as {cols, rows}.
func paneSize(t *testing.T, a *App, id core.PaneID) [2]int {
	t.Helper()

	r, ok := a.layoutRects()[id]
	if !ok {
		t.Fatalf("pane %s has no rect", id)
	}

	c := contentRect(r)

	return [2]int{max(c.W, 1), max(c.H, 1)}
}

// assertSizes checks that every pane with a rect has its content size and
// every pane without one is 1x1, unless zoom hides it.
func assertSizes(t *testing.T, a *App) {
	t.Helper()

	st := a.store.State()
	rects := a.layoutRects()

	for id, p := range a.panes {
		cols, rows := p.size()
		want := [2]int{1, 1}

		if r, ok := rects[id]; ok {
			c := contentRect(r)
			want = [2]int{max(c.W, 1), max(c.H, 1)}
		} else if st.Zoomed {
			continue
		}

		if got := [2]int{cols, rows}; got != want {
			t.Errorf("pane %s size = %v, want %v", id, got, want)
		}
	}
}

func TestFocusResizeZoom(t *testing.T) {
	a, st := lifeAppSized(t, 100, 30)

	chord(a, 'v')
	chord(a, 's')

	// p1 | (p2 over p3), p3 focused.
	steps := []struct {
		name  string
		key   rune
		check func(s core.State) bool
	}{
		{"focus up", 'k', func(s core.State) bool { return s.Focus == "p2" }},
		{"focus down", 'j', func(s core.State) bool { return s.Focus == "p3" }},
		{"focus left", 'h', func(s core.State) bool { return s.Focus == "p1" }},
		{"focus right", 'l', func(s core.State) bool { return s.Focus == "p2" }},
		{"focus down again", 'j', func(s core.State) bool { return s.Focus == "p3" }},
		{"shrink up", 'K', func(s core.State) bool { return s.Layout.B.Ratio == 0.45 }},
		{"grow down", 'J', func(s core.State) bool { return s.Layout.B.Ratio == 0.5 }},
		{"shrink left", 'H', func(s core.State) bool { return s.Layout.Ratio == 0.45 }},
		{"grow right", 'L', func(s core.State) bool { return s.Layout.Ratio == 0.5 }},
		{"zoom", 'z', func(s core.State) bool { return s.Zoomed }},
		{"unzoom", 'z', func(s core.State) bool { return !s.Zoomed }},
	}

	for _, step := range steps {
		chord(a, step.key)

		if !step.check(a.store.State()) {
			t.Fatalf("%s: state after %c = %+v", step.name, step.key, a.store.State())
		}

		assertSizes(t, a)
	}

	// Resizes reach the process: p1 shrank to 44 columns and grew back to 49.
	var sawNarrow bool

	for _, r := range st.proc(0).resized() {
		if r[0] == 44 {
			sawNarrow = true
		}
	}

	if !sawNarrow {
		t.Errorf("p1 resizes = %v, want one with 44 columns", st.proc(0).resized())
	}
}

func TestZoomedPanesKeepTheirSize(t *testing.T) {
	a, st := lifeAppSized(t, 100, 30)

	chord(a, 'v')
	before := len(st.proc(0).resized())

	chord(a, 'z')

	if got := paneSize(t, a, "p2"); got != [2]int{100, 28} {
		t.Errorf("zoomed pane size = %v, want [100 28]", got)
	}

	if got := len(st.proc(0).resized()); got != before {
		t.Errorf("hidden pane p1 was resized while zoomed (%d resizes, had %d)", got, before)
	}

	chord(a, 'z')
	assertSizes(t, a)
}

func TestCloseShellAndAgent(t *testing.T) {
	t.Run("shell closes at once", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)
		chord(a, 'v')
		chord(a, 'x')

		s := a.store.State()
		if len(s.Panes) != 1 || s.Focus != "p1" || a.confirm != nil || a.panes["p2"] != nil {
			t.Fatalf("state = %+v, confirm = %v, want only p1 and no confirm", s, a.confirm)
		}

		eventually(t, "p2 process closed", func() bool { return isClosed(st.proc(1)) })
		assertSizes(t, a)
	})

	t.Run("agent asks", func(t *testing.T) {
		tests := []struct {
			name      string
			keys      []tea.KeyPressMsg
			wantPanes int
		}{
			{"n keeps", []tea.KeyPressMsg{keyRune('n')}, 2},
			{"esc keeps", []tea.KeyPressMsg{keyEsc}, 2},
			{"y closes", []tea.KeyPressMsg{keyRune('y')}, 1},
			{"enter closes", []tea.KeyPressMsg{keyEnter}, 1},
			{"other keys are swallowed", []tea.KeyPressMsg{keyRune('q'), keyLeader, keyRune('a')}, 2},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				a, st := lifeAppSized(t, 100, 30)
				chord(a, 'n')
				chord(a, 'x')

				if a.confirm == nil {
					t.Fatal("no confirm for an agent pane")
				}

				if got := len(a.store.State().Panes); got != 2 {
					t.Fatalf("panes before the answer = %d, want 2", got)
				}

				if v := ansi.Strip(a.View().Content); !strings.Contains(v, "y / n") {
					t.Errorf("view lacks the confirm prompt:\n%s", v)
				}

				var cmd tea.Cmd
				for _, k := range tt.keys {
					cmd = press(a, k)
				}

				if isQuit(cmd) {
					t.Error("a key reached the app behind the confirm")
				}

				if got := len(a.store.State().Panes); got != tt.wantPanes {
					t.Errorf("panes = %d, want %d", got, tt.wantPanes)
				}

				open := tt.wantPanes == 2 && tt.name == "other keys are swallowed"
				if (a.confirm != nil) != open {
					t.Errorf("confirm open = %v, want %v", a.confirm != nil, open)
				}

				if got := st.proc(1).sent(); got != "" {
					t.Errorf("agent pane received %q behind the confirm", got)
				}

				if tt.wantPanes == 1 {
					eventually(t, "agent process closed", func() bool { return isClosed(st.proc(1)) })
				}
			})
		}
	})
}

func isClosed(f *fakeProc) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.closed
}

func TestQuitConfirm(t *testing.T) {
	t.Run("shells quit at once", func(t *testing.T) {
		a, _ := lifeAppSized(t, 100, 30)
		chord(a, 'v')

		if !isQuit(chord(a, 'q')) {
			t.Error("quit with only shells did not quit")
		}
	})

	t.Run("agents ask first", func(t *testing.T) {
		a, _ := lifeAppSized(t, 100, 30)
		chord(a, 'n')

		if cmd := chord(a, 'q'); cmd != nil || a.confirm == nil {
			t.Fatalf("quit with an agent: cmd = %v, confirm = %v, want a confirm", cmd, a.confirm)
		}

		if cmd := press(a, keyRune('n')); isQuit(cmd) || a.confirm != nil {
			t.Fatal("n quit or kept the confirm")
		}

		chord(a, 'q')

		if !isQuit(press(a, keyRune('y'))) {
			t.Error("y did not quit")
		}
	})
}

// exitOf makes the pane's process end and returns the exit message the pane reports.
func exitOf(t *testing.T, a *App, id core.PaneID, fp *fakeProc) tea.Msg {
	t.Helper()

	_ = fp.feed.Close()

	for {
		m := next(t, a.panes[id])
		if _, ok := m.(paneExitedMsg); ok {
			return m
		}

		a.Update(m)
	}
}

func TestPaneExit(t *testing.T) {
	t.Run("during resizes", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)
		chord(a, 'v')
		chord(a, 'v')

		fp := st.proc(1)
		done := make(chan struct{})

		go func() {
			defer close(done)

			_ = fp.feed.Close()
		}()

		for i := range 50 {
			sized(t, a, 100+i%7, 30+i%5)
		}

		<-done

		msg := exitOf(t, a, "p2", fp)
		a.Update(msg)
		sized(t, a, 90, 25)

		s := a.store.State()
		if len(s.Panes) != 2 || s.Panes["p2"].Kind != "" || a.panes["p2"] != nil {
			t.Fatalf("panes after the exit = %+v", s.Panes)
		}

		if s.Focus == "p2" {
			t.Error("focus stayed on the exited pane")
		}

		assertSizes(t, a)
		eventually(t, "exited pane's process closed", func() bool { return isClosed(fp) })
	})

	t.Run("with its confirm open", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)
		chord(a, 'n')
		chord(a, 'x')

		if a.confirm == nil {
			t.Fatal("no confirm")
		}

		a.Update(exitOf(t, a, "p2", st.proc(1)))

		if a.confirm != nil {
			t.Error("the confirm for the exited pane stayed open")
		}

		if s := a.store.State(); len(s.Panes) != 1 || s.Focus != "p1" {
			t.Errorf("state = %+v, want only p1 focused", s)
		}

		// Late answers are harmless.
		press(a, keyRune('y'))
		a.Update(paneExitedMsg{id: "p2"})
		a.Update(paneDirtyMsg{id: "p2"})
		a.Update(holdExpiredMsg{id: "p2"})
	})

	t.Run("quit confirm survives an agent exit", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)
		chord(a, 'n')
		chord(a, 'q')

		a.Update(exitOf(t, a, "p2", st.proc(1)))

		if a.confirm == nil {
			t.Error("the quit confirm closed with an unrelated pane")
		}
	})

	t.Run("last pane shows the welcome card", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)

		a.Update(exitOf(t, a, "p1", st.proc(0)))

		if s := a.store.State(); s.Layout != nil || len(s.Panes) != 0 {
			t.Fatalf("state = %+v, want no panes", s)
		}

		if v := ansi.Strip(a.View().Content); !strings.Contains(v, "No panes yet.") {
			t.Errorf("view lacks the welcome card:\n%s", v)
		}

		sized(t, a, 100, 30)

		if st.count() != 1 {
			t.Errorf("a size message after the last pane closed started a process (starts = %d)", st.count())
		}

		chord(a, 'v')

		if st.count() != 2 || len(a.store.State().Panes) != 1 {
			t.Errorf("a split from the empty layout: starts = %d, panes = %d", st.count(), len(a.store.State().Panes))
		}
	})
}

func TestMessagesForUnknownPane(t *testing.T) {
	a, _ := lifeAppSized(t, 100, 30)

	for _, m := range []tea.Msg{
		paneDirtyMsg{id: "p9"}, holdExpiredMsg{id: "p9"}, paneExitedMsg{id: "p9"},
		paneCwdMsg{id: "p9", cwd: "/x"}, paneCwdDueMsg{id: "p9"},
	} {
		if _, cmd := a.Update(m); cmd != nil {
			t.Errorf("%T for an unknown pane returned a command", m)
		}
	}
}

func TestPaneMessagesRearmWait(t *testing.T) {
	a, st := lifeAppSized(t, 100, 30)

	if _, err := st.proc(0).feed.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}

	m := next(t, a.panes["p1"])
	if _, ok := m.(paneDirtyMsg); !ok {
		t.Fatalf("message = %T, want paneDirtyMsg", m)
	}

	_, cmd := a.Update(m)
	if cmd == nil {
		t.Fatal("a dirty message returned no command, so the pane would never be waited on again")
	}

	if !strings.Contains(ansi.Strip(a.View().Content), "hello") {
		t.Errorf("the dirty message did not render the pane:\n%s", ansi.Strip(a.View().Content))
	}
}

func TestCwdTracking(t *testing.T) {
	a, st := lifeAppSized(t, 100, 30)
	clk := &fakeClock{t: t0}
	a.clock = clk
	st.proc(0).cwd = "/moved"

	p := a.panes["p1"]

	cmd := a.probeCwd(p)
	if cmd == nil {
		t.Fatal("first probe returned no command")
	}

	msg := cmd()
	if m, ok := msg.(paneCwdMsg); !ok || m.id != "p1" || m.cwd != "/moved" {
		t.Fatalf("probe message = %#v, want paneCwdMsg for p1 in /moved", msg)
	}

	// A probe is in flight: no second one.
	if cmd := a.probeCwd(p); cmd != nil {
		t.Error("a probe started while another is in flight")
	}

	a.Update(msg)

	if got := a.store.State().Panes["p1"].Cwd; got != "/moved" {
		t.Errorf("cwd = %q, want /moved", got)
	}

	// Within 500 ms the check waits for the window to end, once.
	clk.Advance(100 * time.Millisecond)

	cmd = a.probeCwd(p)
	if cmd == nil {
		t.Fatal("throttled probe scheduled no trailing check")
	}

	if a.probeCwd(p) != nil {
		t.Error("a second trailing check was scheduled")
	}

	clk.Advance(400 * time.Millisecond)

	_, cmd = a.Update(paneCwdDueMsg{id: "p1"})
	if cmd == nil {
		t.Fatal("the trailing check did not probe")
	}

	st.proc(0).cwd = "/again"
	a.Update(cmd())

	if got := a.store.State().Panes["p1"].Cwd; got != "/again" {
		t.Errorf("cwd = %q, want /again", got)
	}

	// An empty answer keeps the old value.
	a.Update(paneCwdMsg{id: "p1", cwd: ""})

	if got := a.store.State().Panes["p1"].Cwd; got != "/again" {
		t.Errorf("cwd after an empty answer = %q, want /again", got)
	}
}

func TestDirtyMessageSchedulesCwdProbe(t *testing.T) {
	a, st := lifeAppSized(t, 100, 30)
	st.proc(0).cwd = "/moved"

	_, cmd := a.Update(paneDirtyMsg{id: "p1"})

	var got bool

	for _, m := range collect(cmd) {
		if c, ok := m.(paneCwdMsg); ok && c.cwd == "/moved" {
			got = true
		}
	}

	if !got {
		t.Error("a dirty message did not probe the pane's cwd")
	}
}

func TestTinyTerminals(t *testing.T) {
	a, st := lifeAppSized(t, 100, 30)

	for _, k := range "vsvsv" {
		chord(a, k)
	}

	if got := len(a.panes); got != 6 {
		t.Fatalf("panes = %d, want 6", got)
	}

	for _, sz := range [][2]int{{20, 5}, {1, 1}, {2, 2}, {3, 1}, {100, 30}, {20, 5}} {
		sized(t, a, sz[0], sz[1])

		lines := strings.Split(a.View().Content, "\n")
		if len(lines) != sz[1] {
			t.Errorf("%dx%d: %d lines, want %d", sz[0], sz[1], len(lines), sz[1])
		}

		for i, l := range lines {
			if w := ansi.StringWidth(l); w != sz[0] {
				t.Errorf("%dx%d: line %d is %d cells wide", sz[0], sz[1], i, w)
			}
		}

		assertSizes(t, a)
	}

	for i := range st.count() {
		for _, r := range st.proc(i).resized() {
			if r[0] < 1 || r[1] < 1 {
				t.Errorf("process %d got resize %v", i, r)
			}
		}

		if s := st.sizes[i]; s[0] < 1 || s[1] < 1 {
			t.Errorf("process %d started at %v", i, s)
		}
	}
}

// realApp is an App that starts real processes running the given /bin/sh -c
// scripts in order (the last one repeats); it records every pid.
type realApp struct {
	*App
	mu   sync.Mutex
	pids []int
	n    int
}

func newRealApp(t *testing.T, scripts ...string) *realApp {
	t.Helper()

	a, err := New(Options{
		Logger: testLogger(),
		Store:  core.NewStore(core.InitialState(core.Config{Theme: "catppuccin-mocha"})),
		Getenv: shellGetenv,
		Cwd:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	r := &realApp{App: a}

	t.Cleanup(func() { closeApp(t, a) })

	a.start = func(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
		r.mu.Lock()
		script := scripts[min(r.n, len(scripts)-1)]
		r.n++
		r.mu.Unlock()

		c.Argv = []string{"/bin/sh", "-c", script}

		p, err := term.Start(ctx, c, cols, rows)
		if err != nil {
			return nil, err
		}

		r.mu.Lock()
		r.pids = append(r.pids, p.Pid())
		r.mu.Unlock()

		return p, nil
	}

	return r
}

func (r *realApp) pidList() []int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.pids)
}

// gone reports whether nothing is left in the process group of pid.
func gone(pid int) bool {
	return errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func TestAppCloseNoLeaks(t *testing.T) {
	base := runtime.NumGoroutine()
	r := newRealApp(t, "sleep 30")

	sized(t, r.App, 100, 30)
	chord(r.App, 'v')
	chord(r.App, 's')

	pids := r.pidList()
	if len(pids) != 3 {
		t.Fatalf("started %d processes, want 3", len(pids))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	for _, pid := range pids {
		if !gone(pid) {
			t.Errorf("process group %d survived Close", pid)
		}
	}

	if err := r.Close(ctx); err != nil {
		t.Errorf("second Close() error = %v", err)
	}

	eventually2(t, 2*time.Second, "goroutines to return to baseline", func() bool { return runtime.NumGoroutine() <= base })
}

// eventually2 is eventually with its own limit.
func eventually2(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(limit)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (goroutines: %d)", what, runtime.NumGoroutine())
		}

		time.Sleep(5 * time.Millisecond)
	}
}

// pump stands in for the Bubble Tea runtime: it runs commands off the update
// loop and queues their messages for the test to feed to Update.
type pump struct {
	msgs chan tea.Msg
	done chan struct{}
}

func newPump(t *testing.T) *pump {
	t.Helper()

	p := &pump{msgs: make(chan tea.Msg, 64), done: make(chan struct{})}
	t.Cleanup(func() { close(p.done) })

	return p
}

func (p *pump) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}

	go func() {
		m := cmd()
		if b, ok := m.(tea.BatchMsg); ok {
			for _, c := range b {
				p.run(c)
			}

			return
		}

		if m == nil {
			return
		}

		select {
		case p.msgs <- m:
		case <-p.done:
		}
	}()
}

// step feeds the next queued message to Update, or reports false at the deadline.
func (p *pump) step(a *App, deadline time.Time) bool {
	select {
	case m := <-p.msgs:
		_, cmd := a.Update(m)
		p.run(cmd)

		return true
	case <-time.After(time.Until(deadline)):
		return false
	}
}

func TestFloodingPaneQuits(t *testing.T) {
	r := newRealApp(t, `while :; do echo xxxxxxxxxxxxxxxx; done`, `cat`)
	pm := newPump(t)

	_, cmd := r.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pm.run(cmd)
	pm.run(chord(r.App, 'v'))
	// p2 (cat) is focused; the flood runs in p1.

	deadline := time.Now().Add(5 * time.Second)
	floods := 0

	// Let the flood run through Update before the key arrives.
	for floods < 50 {
		if !pm.step(r.App, deadline) {
			t.Fatal("the flood produced no messages")
		}

		floods++
	}

	pm.run(press(r.App, keyRune('z')))

	typed := func() bool {
		return strings.Contains(strings.Join(r.panes["p2"].lines(), "\n"), "z")
	}

	for after := 0; !typed(); after++ {
		if !pm.step(r.App, deadline) {
			t.Fatal("the key did not reach the second pane while the first flooded")
		}

		if after > 100000 {
			t.Fatal("too many messages without the key arriving")
		}
	}

	// The flood is still going.
	for range 20 {
		if !pm.step(r.App, deadline) {
			t.Fatal("the flood stopped")
		}
	}

	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Close took %v, want under 2s", d)
	}

	for _, pid := range r.pidList() {
		if !gone(pid) {
			t.Errorf("process group %d survived Close", pid)
		}
	}
}

func TestOrphanedExitClosesPane(t *testing.T) {
	r := newRealApp(t, `sleep 30 & exit 0`)

	sized(t, r.App, 100, 30)

	pid := r.pidList()[0]
	p := r.panes["p1"]

	deadline := time.After(2 * time.Second)
	done := make(chan tea.Msg, 1)

	go func() {
		for {
			m := p.wait()()
			if _, ok := m.(paneExitedMsg); ok {
				done <- m

				return
			}
		}
	}()

	select {
	case m := <-done:
		r.Update(m)
	case <-deadline:
		t.Fatal("the pane did not report its exit within 2s")
	}

	if got := len(r.store.State().Panes); got != 0 || len(r.panes) != 0 {
		t.Errorf("panes after the exit: state %d, registry %d", got, len(r.panes))
	}

	eventually2(t, 3*time.Second, "the orphan to be killed", func() bool { return gone(pid) })
}

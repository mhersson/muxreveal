package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
)

type paneSpec struct {
	kind  core.PaneKind
	cwd   string
	split core.Split
	text  string // fed to the guest after the pane is sized
}

// framedApp opens the specs through the store, starts a fake-process pane for
// each sized to its content rect, and feeds the text.
func framedApp(t *testing.T, w, h int, specs ...paneSpec) (*App, map[core.PaneID]*fakeProc) {
	t.Helper()

	a := newTestApp(t)
	sized(t, a, w, h)

	for _, s := range specs {
		if err := a.dispatch(core.OpenPane{Kind: s.kind, Cwd: s.cwd, Split: s.split}); err != nil {
			t.Fatal(err)
		}
	}

	rects := a.layoutRects()
	procs := map[core.PaneID]*fakeProc{}
	i := 0

	for _, s := range specs {
		id := core.PaneID(fmt.Sprintf("p%d", i+1))
		i++

		c := contentRect(rects[id])
		fp := newFakeProc()
		p := newPane(id, fp, max(c.W, 1), max(c.H, 1), realClock{})
		t.Cleanup(func() { _ = p.close() })

		a.panes[id] = p
		procs[id] = fp

		if s.text != "" {
			feedAndWait(t, p, fp, s.text)
			p.render()
		}
	}

	return a, procs
}

func shellSpec(cwd, text string) paneSpec {
	return paneSpec{kind: core.KindShell, cwd: cwd, split: core.SplitVertical, text: text}
}

func TestFrameGolden(t *testing.T) {
	right := func(cwd, text string) paneSpec { return shellSpec(cwd, text) }
	below := paneSpec{kind: core.KindAgent, cwd: "/work/api", split: core.SplitHorizontal, text: "\x1b[2;1Hagent output"}

	tests := []struct {
		name  string
		build func(t *testing.T) *App
	}{
		{"one", func(t *testing.T) *App {
			a, _ := framedApp(t, 100, 30, shellSpec("/home/u/proj", "$ ls\r\nREADME.md"))
			return a
		}},
		{"vsplit", func(t *testing.T) *App {
			a, _ := framedApp(t, 100, 30, shellSpec("/home/u/proj", "left"), right("/home/u/other", "right"))
			return a
		}},
		{"nested", func(t *testing.T) *App {
			a, _ := framedApp(t, 100, 30, shellSpec("/home/u/proj", "left"), right("/home/u/other", "top right"), below)
			if err := a.dispatch(core.FocusPane{ID: "p2"}); err != nil {
				t.Fatal(err)
			}
			return a
		}},
		{"zoomed", func(t *testing.T) *App {
			a, _ := framedApp(t, 100, 30, shellSpec("/home/u/proj", "left"), right("/home/u/other", "zoomed text"))
			if err := a.dispatch(core.ToggleZoom{}); err != nil {
				t.Fatal(err)
			}
			return a
		}},
		{"empty", func(t *testing.T) *App {
			a, _ := framedApp(t, 100, 30)
			return a
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(tt.build(t).View().Content))
		})
	}
}

// A tiny terminal with many panes still draws a whole frame.
func TestFrameSizes(t *testing.T) {
	for _, sz := range [][2]int{{100, 30}, {20, 5}, {1, 1}, {2, 2}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			specs := make([]paneSpec, 6)
			for i := range specs {
				split := core.SplitVertical
				if i%2 == 1 {
					split = core.SplitHorizontal
				}
				specs[i] = paneSpec{kind: core.KindShell, cwd: "/a/b", split: split, text: "hello world"}
			}

			a, _ := framedApp(t, sz[0], sz[1], specs...)
			for _, zoomed := range []bool{false, true} {
				if zoomed {
					if err := a.dispatch(core.ToggleZoom{}); err != nil {
						t.Fatal(err)
					}
				}

				lines := strings.Split(a.View().Content, "\n")
				if len(lines) != sz[1] {
					t.Fatalf("zoomed=%v: %d lines, want %d", zoomed, len(lines), sz[1])
				}

				for i, l := range lines {
					if got := lipgloss.Width(l); got != sz[0] {
						t.Errorf("zoomed=%v line %d: width %d, want %d: %q", zoomed, i, got, sz[0], l)
					}
				}
			}
		})
	}
}

func TestHeaderText(t *testing.T) {
	tests := []struct {
		name  string
		kind  core.PaneKind
		cwd   string
		text  string
		width int
		want  string
	}{
		{"shell default", core.KindShell, "/home/u/proj", "", 40, " shell · proj"},
		{"agent default", core.KindAgent, "/home/u/proj", "", 40, " claude · proj"},
		{"osc title wins", core.KindShell, "/home/u/proj", "\x1b]2;vim main.go\x07", 40, " vim main.go · proj"},
		{"no cwd", core.KindShell, "", "", 40, " shell"},
		{"cwd with newline", core.KindShell, "/home/u/a\nb", "", 40, " shell · ab"},
		{"cwd with escape", core.KindShell, "/home/u/a\x1b[31mb", "", 40, " shell · a[31mb"},
		{"truncated", core.KindShell, "/home/u/proj", "", 8, " shell ·"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := framedApp(t, tt.width, 6, paneSpec{kind: tt.kind, cwd: tt.cwd, split: core.SplitVertical, text: tt.text})

			lines := strings.Split(a.View().Content, "\n")
			if len(lines) != 6 {
				t.Fatalf("frame has %d lines, want 6", len(lines))
			}
			if w := ansi.StringWidth(lines[0]); w != tt.width {
				t.Errorf("header width = %d, want %d", w, tt.width)
			}
			if got := strings.TrimRight(ansi.Strip(lines[0]), " "); got != tt.want {
				t.Errorf("header = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHeaderZoomTag(t *testing.T) {
	a, _ := framedApp(t, 40, 6, shellSpec("/home/u/proj", ""))
	if err := a.dispatch(core.ToggleZoom{}); err != nil {
		t.Fatal(err)
	}

	first := ansi.Strip(strings.Split(a.View().Content, "\n")[0])
	if !strings.HasSuffix(first, " zoom ") || !strings.HasPrefix(first, " shell · proj") {
		t.Errorf("zoomed header = %q", first)
	}
}

func TestCursorView(t *testing.T) {
	a, procs := framedApp(t, 100, 30, shellSpec("/a", ""), shellSpec("/b", "\x1b[3;5Hx"))

	// p2 is focused and sits right of p1: A gets floor(99*0.5) = 49 columns, so p2 starts at x=50.
	cur := a.cursorView()
	if cur == nil {
		t.Fatal("cursorView() = nil, want the focused pane's cursor")
	}

	if cur.X != 50+5 || cur.Y != 1+2 {
		t.Errorf("cursor = (%d, %d), want (55, 3)", cur.X, cur.Y)
	}

	if err := a.dispatch(core.ToggleZoom{}); err != nil {
		t.Fatal(err)
	}

	// Zoomed, p2 fills the body, so the cursor is offset from the origin only by the header.
	if cur := a.cursorView(); cur == nil || cur.X != 5 || cur.Y != 3 {
		t.Errorf("zoomed cursor = %+v, want (5, 3)", cur)
	}

	a.overlay = &overlay{}
	if a.cursorView() != nil {
		t.Error("cursorView() with an overlay open, want nil")
	}
	a.overlay = nil

	feedAndWait(t, a.panes["p2"], procs["p2"], "\x1b[?25l")

	if a.cursorView() != nil {
		t.Error("cursorView() with the cursor hidden, want nil")
	}
}

func TestCursorViewNoRect(t *testing.T) {
	a, _ := framedApp(t, 1, 1, shellSpec("/a", ""))
	if a.cursorView() != nil {
		t.Error("cursorView() with no rect for the focused pane, want nil")
	}

	if empty := newTestApp(t); empty.cursorView() != nil {
		t.Error("cursorView() with no panes, want nil")
	}
}

func TestCursorViewOutsideContent(t *testing.T) {
	a, procs := framedApp(t, 100, 30, shellSpec("/a", ""))
	// The pane is still 100 wide when the layout shrinks to 40: the cursor is clipped.
	feedAndWait(t, a.panes["p1"], procs["p1"], "\x1b[3;60Hx")
	a.width = 40

	if cur := a.cursorView(); cur != nil {
		t.Errorf("cursorView() = %+v for a cursor outside the content rect, want nil", cur)
	}
}

func TestCursorViewWhichKeyPopup(t *testing.T) {
	a, _ := framedApp(t, 100, 30, shellSpec("/a", ""))
	a.popup = true

	if a.cursorView() != nil {
		t.Error("cursorView() with the which-key popup open, want nil")
	}
}

func TestViewSetsCursor(t *testing.T) {
	a, _ := framedApp(t, 100, 30, shellSpec("/a", "ab"))

	want := a.cursorView()
	got := a.View().Cursor

	if want == nil || got == nil || got.Position != want.Position {
		t.Errorf("View().Cursor = %+v, want %+v", got, want)
	}
}

func TestHeaderWideTitleZoomWidth(t *testing.T) {
	for w := 3; w <= 14; w++ {
		a, _ := framedApp(t, w, 4, shellSpec("/home/u/proj", "\x1b]2;日本語のタイトル\x07"))
		if err := a.dispatch(core.ToggleZoom{}); err != nil {
			t.Fatal(err)
		}

		first := strings.Split(a.View().Content, "\n")[0]
		if got := lipgloss.Width(first); got != w {
			t.Errorf("width %d: header width %d: %q", w, got, ansi.Strip(first))
		}
	}
}

func TestNoticeDropsControlCharacters(t *testing.T) {
	a, _ := framedApp(t, 40, 6, shellSpec("/home/u/proj", ""))
	a.notice = "cannot start shell: chdir /a\nb\x1b[31m"

	lines := strings.Split(a.View().Content, "\n")
	if len(lines) != 6 {
		t.Fatalf("frame has %d lines, want 6", len(lines))
	}

	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 40 {
			t.Errorf("line %d width = %d, want 40", i, w)
		}
	}
}

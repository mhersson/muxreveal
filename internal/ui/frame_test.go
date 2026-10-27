package ui

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/term"
	"github.com/mhersson/muxreveal/internal/ui/theme"
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

	texts := map[core.PaneID]string{}

	for i, s := range specs {
		if err := a.dispatch(core.OpenPane{Kind: s.kind, Cwd: s.cwd, Split: s.split}); err != nil {
			t.Fatal(err)
		}

		texts[core.PaneID(fmt.Sprintf("p%d", i+1))] = s.text
	}

	return a, attachPanes(t, a, texts)
}

// attachPanes gives every pane in the state a fake process sized to its
// content rect and feeds it its text.
func attachPanes(t *testing.T, a *App, texts map[core.PaneID]string) map[core.PaneID]*fakeProc {
	t.Helper()

	rects := a.layoutRects()
	procs := map[core.PaneID]*fakeProc{}

	for id := range a.store.State().Panes {
		c := contentRect(rects[id])
		fp := newFakeProc()
		p := newPane(id, fp, max(c.W, 1), max(c.H, 1), realClock{})
		t.Cleanup(func() { _ = p.close() })

		a.panes[id] = p
		procs[id] = fp

		if texts[id] != "" {
			feedAndWait(t, p, fp, texts[id])
			p.render()
		}
	}

	return procs
}

func shellSpec(cwd, text string) paneSpec {
	return paneSpec{kind: core.KindShell, cwd: cwd, split: core.SplitVertical, text: text}
}

// frameCases are layouts at 40x12, each focused on its last-opened pane unless
// the builder moves focus.
func frameCases() []struct {
	name  string
	build func(t *testing.T) *App
} {
	open := func(t *testing.T, a *App, kind core.PaneKind, cwd string, split core.Split) {
		t.Helper()

		if err := a.dispatch(core.OpenPane{Kind: kind, Cwd: cwd, Split: split}); err != nil {
			t.Fatal(err)
		}
	}
	focus := func(t *testing.T, a *App, id core.PaneID) {
		t.Helper()

		if err := a.dispatch(core.FocusPane{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	started := func(t *testing.T, build func(a *App)) *App {
		t.Helper()

		a := newTestApp(t)
		sized(t, a, 40, 12)
		build(a)
		attachPanes(t, a, map[core.PaneID]string{"p1": "left", "p2": "right"})

		return a
	}

	return []struct {
		name  string
		build func(t *testing.T) *App
	}{
		{"one", func(t *testing.T) *App {
			return started(t, func(a *App) { open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical) })
		}},
		{"agent", func(t *testing.T) *App {
			return started(t, func(a *App) { open(t, a, core.KindAgent, "/home/u/proj", core.SplitVertical) })
		}},
		{"vsplit", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindAgent, "/home/u/other", core.SplitVertical)
			})
		}},
		{"hsplit", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitHorizontal)
			})
		}},
		{"tee", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitVertical)
				open(t, a, core.KindAgent, "/home/u/third", core.SplitHorizontal)
			})
		}},
		{"tee-inner-focus", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitVertical)
				open(t, a, core.KindAgent, "/home/u/third", core.SplitHorizontal)
				focus(t, a, "p2")
			})
		}},
		{"tee-down", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitHorizontal)
				open(t, a, core.KindAgent, "/home/u/third", core.SplitVertical)
			})
		}},
		{"cross", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/third", core.SplitHorizontal)
				focus(t, a, "p1")
				open(t, a, core.KindAgent, "/home/u/fourth", core.SplitHorizontal)
			})
		}},
		{"zoomed", func(t *testing.T) *App {
			a := started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitVertical)
				if err := a.dispatch(core.ToggleZoom{}); err != nil {
					t.Fatal(err)
				}
			})

			return a
		}},
		{"empty", func(t *testing.T) *App { return started(t, func(*App) {}) }},
	}
}

func TestFrameGolden(t *testing.T) {
	for _, tt := range frameCases() {
		t.Run(tt.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(tt.build(t).View().Content))
		})
	}
}

// TestFrameText is the same frames without styling, so the line work reads.
func TestFrameText(t *testing.T) {
	for _, tt := range frameCases() {
		t.Run(tt.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(ansi.Strip(tt.build(t).View().Content)))
		})
	}
}

// Every line is exactly the terminal width, whatever the size, layout or zoom.
func TestFrameSizes(t *testing.T) {
	for w := 1; w <= 40; w++ {
		for h := 1; h <= 12; h++ {
			specs := make([]paneSpec, 6)
			for i := range specs {
				split := core.SplitVertical
				if i%2 == 1 {
					split = core.SplitHorizontal
				}

				specs[i] = paneSpec{kind: core.KindShell, cwd: "/a/b", split: split, text: "\x1b]2;日本語のタイトル\x07hello world"}
			}

			a, _ := framedApp(t, w, h, specs...)

			for _, zoomed := range []bool{false, true} {
				if zoomed {
					if err := a.dispatch(core.ToggleZoom{}); err != nil {
						t.Fatal(err)
					}
				}

				lines := strings.Split(a.View().Content, "\n")
				if len(lines) != h {
					t.Fatalf("%dx%d zoomed=%v: %d lines, want %d", w, h, zoomed, len(lines), h)
				}

				for i, l := range lines {
					if got := lipgloss.Width(l); got != w {
						t.Errorf("%dx%d zoomed=%v line %d: width %d, want %d: %q", w, h, zoomed, i, got, w, ansi.Strip(l))
					}
				}
			}
		}
	}
}

// A body under 3 rows or columns stays blank.
func TestFrameMinimumBody(t *testing.T) {
	for _, sz := range [][2]int{{2, 12}, {40, 3}, {40, 2}, {2, 2}} {
		a, _ := framedApp(t, sz[0], sz[1], shellSpec("/a", "x"))

		lines := strings.Split(ansi.Strip(a.View().Content), "\n")
		for _, l := range lines[1 : len(lines)-1] {
			if strings.TrimSpace(l) != "" {
				t.Errorf("%v: body line %q, want blank", sz, l)
			}
		}
	}
}

// A 3x3 pane is a box around one content cell; a tile of 2x2 is left out.
func TestSmallestPanes(t *testing.T) {
	t.Run("3x3 draws a box with one content cell", func(t *testing.T) {
		a, _ := framedApp(t, 3, 5, shellSpec("/a", "x"))

		if got := a.layoutRects()["p1"]; got != (core.Rect{X: 0, Y: 1, W: 3, H: 3}) {
			t.Fatalf("rect = %+v, want 3x3 at the body", got)
		}

		if cols, rows := paneSizeOf(a, "p1"); cols != 1 || rows != 1 {
			t.Errorf("PTY = %dx%d, want 1x1", cols, rows)
		}

		got := strings.Split(ansi.Strip(a.View().Content), "\n")[1:4]
		if want := []string{"╭─╮", "│x│", "╰─╯"}; !slices.Equal(got, want) {
			t.Errorf("box = %q, want %q", got, want)
		}
	})

	t.Run("a 2x2 tile is left out", func(t *testing.T) {
		// A 5x3 body split into 2 and 3 columns: the 2-wide tile is too small.
		a, _ := framedApp(t, 5, 5, shellSpec("/a", "x"), shellSpec("/b", "y"))

		rects := a.layoutRects()
		if _, drawn := rects["p1"]; drawn || len(rects) != 1 {
			t.Fatalf("rects = %v, want only p2", rects)
		}

		if cols, rows := paneSizeOf(a, "p1"); cols != 1 || rows != 1 {
			t.Errorf("left-out PTY = %dx%d, want 1x1", cols, rows)
		}

		got := strings.Split(ansi.Strip(a.View().Content), "\n")[1:4]
		if want := []string{"  ╭─╮", "  │y│", "  ╰─╯"}; !slices.Equal(got, want) {
			t.Errorf("body = %q, want %q", got, want)
		}
	})
}

// topBorder is the top row of the frame, under the workspace bar, without styling.
func topBorder(a *App) string {
	return ansi.Strip(strings.Split(a.View().Content, "\n")[1])
}

// fill pads prefix with border cells to w cells.
func fill(prefix string, w int) string {
	return prefix + strings.Repeat("─", w-lipgloss.Width(prefix))
}

// dashes pads prefix with border cells to w cells and closes it with a corner.
func dashes(prefix string, w int) string { return fill(prefix, w-1) + "╮" }

func TestTitle(t *testing.T) {
	zoom := " " + iconZoom + " zoom "
	tests := []struct {
		name  string
		kind  core.PaneKind
		cwd   string
		osc   string
		width int
		icons bool
		zoom  bool
		want  string
	}{
		{"shell", core.KindShell, "/home/u/proj", "", 40, true, false, dashes("╭─ "+iconShell+" shell · proj ", 40)},
		{"agent", core.KindAgent, "/home/u/proj", "", 40, true, false, dashes("╭─ "+iconAgent+" claude · proj ", 40)},
		{"osc title", core.KindShell, "/home/u/proj", "\x1b]2;vim main.go\x07", 40, true, false, dashes("╭─ "+iconShell+" vim main.go · proj ", 40)},
		{"no cwd", core.KindShell, "", "", 40, true, false, dashes("╭─ "+iconShell+" shell ", 40)},
		{"no icons", core.KindAgent, "/home/u/proj", "", 40, false, false, dashes("╭─ claude · proj ", 40)},
		{"cwd fits exactly", core.KindAgent, "/home/u/proj", "", 20, true, false, dashes("╭─ "+iconAgent+" claude · proj ", 20)},
		{"cwd goes first", core.KindAgent, "/home/u/proj", "", 19, true, false, dashes("╭─ "+iconAgent+" claude ", 19)},
		{"title shrinks", core.KindShell, "/home/u/proj", "\x1b]2;abcdefghij\x07", 14, true, false, dashes("╭─ "+iconShell+" abcdef… ", 14)},
		{"title shrinks to one", core.KindShell, "/home/u/proj", "\x1b]2;abcdefghij\x07", 11, true, false, dashes("╭─ "+iconShell+" abc… ", 11)},
		{"title goes", core.KindShell, "/home/u/proj", "\x1b]2;abcdefghij\x07", 10, true, false, dashes("╭", 10)},
		{"zoom tag", core.KindShell, "/home/u/proj", "", 40, true, true, fill("╭─ "+iconShell+" shell · proj ", 30) + zoom + "─╮"},
		{"zoom tag no icons", core.KindShell, "/home/u/proj", "", 40, false, true, fill("╭─ shell · proj ", 32) + " zoom ─╮"},
		{"zoom tag takes the title room", core.KindShell, "/home/u/proj", "", 21, true, true, "╭─ " + iconShell + " shell " + zoom + "─╮"},
		{"zoom tag alone", core.KindShell, "/home/u/proj", "", 11, true, true, "╭" + zoom + "─╮"},
		{"zoom tag does not fit", core.KindShell, "/home/u/proj", "", 10, true, true, dashes("╭", 10)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := framedApp(t, tt.width, 6, paneSpec{kind: tt.kind, cwd: tt.cwd, split: core.SplitVertical, text: tt.osc})
			a.icons = tt.icons

			if tt.zoom {
				if err := a.dispatch(core.ToggleZoom{}); err != nil {
					t.Fatal(err)
				}
			}

			if got := topBorder(a); got != tt.want {
				t.Errorf("top border = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTitleControlCharacters(t *testing.T) {
	a, _ := framedApp(t, 40, 6, paneSpec{kind: core.KindShell, cwd: "/home/u/a\nb\x1b[31mc", split: core.SplitVertical})

	if got, want := topBorder(a), dashes("╭─ "+iconShell+" shell · ab[31mc ", 40); got != want {
		t.Errorf("top border = %q, want %q", got, want)
	}
}

// An unfocused pane's title is Muted and its cwd Subtle; the focused pane's
// title is bold in its kind colour and its cwd Fg.
func TestTitleColours(t *testing.T) {
	ui := mochaUI(t)
	tests := []struct {
		name  string
		kind  core.PaneKind
		kindC theme.Hex
	}{
		{"shell", core.KindShell, ui.Shell},
		{"agent", core.KindAgent, ui.Agent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := framedApp(t, 60, 8,
				paneSpec{kind: tt.kind, cwd: "/w/first", split: core.SplitVertical},
				paneSpec{kind: tt.kind, cwd: "/w/second", split: core.SplitVertical})

			if err := a.dispatch(core.FocusPane{ID: "p1"}); err != nil {
				t.Fatal(err)
			}

			vt := viewVT(t, a)
			p1, p2 := a.layoutRects()["p1"], a.layoutRects()["p2"]
			title := map[core.PaneKind]string{core.KindShell: "shell", core.KindAgent: "claude"}[tt.kind]
			start := func(r core.Rect) int { return r.X + 5 } // after "╭─ icon "

			// Focused p1: icon and title bold in the kind colour, cwd Fg.
			assertCell(t, vt, start(p1)-2, 1, tt.kindC, true)
			assertCell(t, vt, start(p1), 1, tt.kindC, true)
			assertCell(t, vt, start(p1)+len(title)+3, 1, ui.Fg, false)
			// Unfocused p2: Muted title, Subtle cwd, the icon still in the kind colour.
			assertCell(t, vt, start(p2)-2, 1, tt.kindC, true)
			assertCell(t, vt, start(p2), 1, ui.Muted, false)
			assertCell(t, vt, start(p2)+len(title)+3, 1, ui.Subtle, false)
		})
	}
}

// The focused pane's whole box is in its kind colour; every other border cell
// is Border.
func TestBorderColours(t *testing.T) {
	ui := mochaUI(t)

	for _, tt := range frameCases() {
		if tt.name == "zoomed" || tt.name == "empty" {
			continue
		}

		t.Run(tt.name, func(t *testing.T) {
			a := tt.build(t)
			vt := viewVT(t, a)
			st := a.store.State()
			focus := st.Tab().Focus
			r := a.layoutRects()[focus]
			kindC := ui.Shell
			if st.Panes[focus].Kind == core.KindAgent {
				kindC = ui.Agent
			}

			onFocus := func(x, y int) bool {
				return (x == r.X || x == r.X+r.W-1) && y >= r.Y && y < r.Y+r.H ||
					(y == r.Y || y == r.Y+r.H-1) && x >= r.X && x < r.X+r.W
			}

			for y := range a.height - 1 {
				for x := range a.width {
					c := vt.CellAt(x, y)
					if c == nil || !strings.Contains("─│╭╮╰╯", c.Content) || c.Content == "" {
						continue
					}

					want := ui.Border
					if onFocus(x, y) {
						want = kindC
					}

					assertCell(t, vt, x, y, want, false)
				}
			}
		})
	}
}

func TestIconsAreOneCell(t *testing.T) {
	for name, g := range map[string]string{"agent": iconAgent, "shell": iconShell, "zoom": iconZoom} {
		if w := lipgloss.Width(g); w != 1 {
			t.Errorf("%s icon is %d cells wide, want 1", name, w)
		}
	}
}

func TestNoIconsNoGlyphs(t *testing.T) {
	for _, tt := range frameCases() {
		t.Run(tt.name, func(t *testing.T) {
			a := tt.build(t)
			a.icons = false

			for _, r := range a.View().Content {
				if r >= 0xE000 && r <= 0xF8FF || r >= 0xF0000 {
					t.Fatalf("frame has the private-use glyph %U with icons off", r)
				}
			}
		})
	}
}

// Every pane is its own rounded box: the corners sit on the rect's corners,
// neighbours touch, and no junction glyph or shared cell exists.
func TestFramesAreOneBoxPerPane(t *testing.T) {
	for w := 8; w <= 40; w++ {
		a, _ := framedApp(t, w, 12,
			paneSpec{kind: core.KindShell, cwd: "/w/top", split: core.SplitVertical},
			paneSpec{kind: core.KindShell, cwd: "/w/bottom", split: core.SplitHorizontal},
			paneSpec{kind: core.KindShell, cwd: "/w/right", split: core.SplitVertical})

		lines := strings.Split(ansi.Strip(a.View().Content), "\n")
		for _, l := range lines {
			if strings.ContainsAny(l, "┬┴├┤┼") {
				t.Fatalf("width %d: junction glyph in\n%s", w, strings.Join(lines, "\n"))
			}
		}

		cell := func(x, y int) string { return string([]rune(lines[y])[x]) }

		for id, r := range a.layoutRects() {
			for _, c := range []struct {
				x, y int
				want string
			}{
				{r.X, r.Y, "╭"}, {r.X + r.W - 1, r.Y, "╮"}, {r.X, r.Y + r.H - 1, "╰"}, {r.X + r.W - 1, r.Y + r.H - 1, "╯"},
			} {
				if got := cell(c.x, c.y); got != c.want {
					t.Errorf("width %d: %s corner (%d, %d) = %q, want %q", w, id, c.x, c.y, got, c.want)
				}
			}
		}
	}
}

func TestCursorView(t *testing.T) {
	a, procs := framedApp(t, 100, 30, shellSpec("/a", ""), shellSpec("/b", "\x1b[3;5Hx"))

	// p2 is focused and sits right of p1: p1 takes floor(100*0.5) = 50 columns, so p2 starts at x=50 and its content at x=51.
	cur := a.cursorView()
	if cur == nil {
		t.Fatal("cursorView() = nil, want the focused pane's cursor")
	}

	if cur.X != 51+5 || cur.Y != 2+2 {
		t.Errorf("cursor = (%d, %d), want (56, 4)", cur.X, cur.Y)
	}

	if err := a.dispatch(core.ToggleZoom{}); err != nil {
		t.Fatal(err)
	}

	// Zoomed, p2 fills the body, so the cursor is offset by the left frame column and the top border.
	if cur := a.cursorView(); cur == nil || cur.X != 6 || cur.Y != 4 {
		t.Errorf("zoomed cursor = %+v, want (6, 4)", cur)
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

func mochaUI(t *testing.T) theme.UI {
	t.Helper()

	th, err := theme.ByName("catppuccin-mocha")
	if err != nil {
		t.Fatal(err)
	}

	return th.UI
}

// viewVT replays the rendered frame into a virtual terminal to read cell colours.
func viewVT(t *testing.T, a *App) term.VT {
	t.Helper()

	vt := term.NewVT(a.width, a.height, io.Discard)
	t.Cleanup(func() { _ = vt.Close() })

	if _, err := vt.Write([]byte(strings.ReplaceAll(a.View().Content, "\n", "\r\n"))); err != nil {
		t.Fatal(err)
	}

	return vt
}

func assertCell(t *testing.T, vt term.VT, x, y int, want theme.Hex, bold bool) {
	t.Helper()

	c := vt.CellAt(x, y)
	if c == nil || c.Style.Fg == nil {
		t.Errorf("cell (%d, %d) has no foreground", x, y)
		return
	}

	r, g, b, _ := c.Style.Fg.RGBA()
	got := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)

	if got != string(want) {
		t.Errorf("cell (%d, %d) %q foreground = %s, want %s", x, y, c.Content, got, want)
	}

	if isBold := c.Style.Attrs&uv.AttrBold != 0; isBold != bold {
		t.Errorf("cell (%d, %d) %q bold = %v, want %v", x, y, c.Content, isBold, bold)
	}
}

// A full-width pane's long title is cut with an ellipsis before its right corner.
func TestTitleCutBeforeCorner(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 40, 12)

	for _, act := range []core.Action{
		core.OpenPane{Kind: core.KindShell, Cwd: "/w/top", Split: core.SplitVertical},
		core.OpenPane{Kind: core.KindShell, Cwd: "/w/bottom", Split: core.SplitHorizontal},
		core.FocusPane{ID: "p1"},
		core.OpenPane{Kind: core.KindShell, Cwd: "/w/right", Split: core.SplitVertical},
	} {
		if err := a.dispatch(act); err != nil {
			t.Fatal(err)
		}
	}

	attachPanes(t, a, map[core.PaneID]string{"p2": "\x1b]2;a-very-long-title-for-the-bottom-pane\x07"})

	r := a.layoutRects()["p2"]
	if r.W != 40 {
		t.Fatalf("p2 width = %d, want the full body width 40", r.W)
	}

	row := []rune(ansi.Strip(strings.Split(a.View().Content, "\n")[r.Y]))
	if len(row) != 40 || row[39] != '╮' || !strings.Contains(string(row), "…") {
		t.Errorf("top border = %q, want a cut title and a closing corner", string(row))
	}
}

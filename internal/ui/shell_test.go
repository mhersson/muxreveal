package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

func mustTheme(t *testing.T, name string) theme.Theme {
	t.Helper()
	th, err := theme.ByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func sized(t *testing.T, a *App, w, h int) {
	t.Helper()
	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
}

func TestNewRejectsUnknownTheme(t *testing.T) {
	_, err := New(Options{Logger: testLogger(), Store: core.NewStore(core.State{Theme: "nope"})})
	if !errors.Is(err, theme.ErrUnknownTheme) {
		t.Errorf("New() error = %v, want one wrapping ErrUnknownTheme", err)
	}
}

func TestShellGolden(t *testing.T) {
	for _, name := range theme.Names() {
		t.Run(name, func(t *testing.T) {
			a := newThemedApp(t, name)
			sized(t, a, 100, 30)
			golden.RequireEqual(t, []byte(a.View().Content))
		})
	}
}

func TestViewColours(t *testing.T) {
	for _, name := range theme.Names() {
		t.Run(name, func(t *testing.T) {
			a := newThemedApp(t, name)
			sized(t, a, 100, 30)
			v := a.View()
			th := mustTheme(t, name)
			if v.BackgroundColor != th.UI.Bg {
				t.Errorf("BackgroundColor = %v, want %v", v.BackgroundColor, th.UI.Bg)
			}
			if v.ForegroundColor != th.UI.Fg {
				t.Errorf("ForegroundColor = %v, want %v", v.ForegroundColor, th.UI.Fg)
			}
		})
	}
}

func TestThemeFollowsStore(t *testing.T) {
	a := newThemedApp(t, "catppuccin-mocha")
	sized(t, a, 100, 30)
	if err := a.dispatch(core.SetTheme{Name: "catppuccin-latte"}); err != nil {
		t.Fatal(err)
	}
	v := a.View()
	latte := mustTheme(t, "catppuccin-latte")
	if v.BackgroundColor != latte.UI.Bg {
		t.Errorf("BackgroundColor = %v, want %v", v.BackgroundColor, latte.UI.Bg)
	}
}

func TestDispatchUnknownThemeKeepsPrevious(t *testing.T) {
	a := newThemedApp(t, "catppuccin-mocha")
	sized(t, a, 100, 30)
	before := a.View()
	err := a.dispatch(core.SetTheme{Name: "nope"})
	if !errors.Is(err, theme.ErrUnknownTheme) {
		t.Fatalf("dispatch() error = %v, want one wrapping ErrUnknownTheme", err)
	}
	if got := a.store.State().Theme; got != "catppuccin-mocha" {
		t.Errorf("store theme = %q, want catppuccin-mocha", got)
	}
	if after := a.View(); after.Content != before.Content || after.BackgroundColor != before.BackgroundColor {
		t.Error("view changed after a rejected theme")
	}
}

func TestShellSizes(t *testing.T) {
	for _, name := range theme.Names() {
		for _, sz := range [][2]int{{100, 30}, {40, 12}, {20, 5}, {1, 1}, {3, 1}, {200, 3}} {
			t.Run(fmt.Sprintf("%s/%dx%d", name, sz[0], sz[1]), func(t *testing.T) {
				a := newThemedApp(t, name)
				sized(t, a, sz[0], sz[1])
				content := a.View().Content
				lines := strings.Split(content, "\n")
				if len(lines) != sz[1] {
					t.Fatalf("view has %d lines, want %d", len(lines), sz[1])
				}
				for i, line := range lines {
					if got := lipgloss.Width(line); got != sz[0] {
						t.Errorf("line %d width = %d, want %d: %q", i, got, sz[0], line)
					}
				}
				if strings.Contains(content, "\t") {
					t.Error("view contains a raw tab")
				}
			})
		}
	}
}

func TestShellContent(t *testing.T) {
	tests := []struct {
		w, h     int
		wantCard bool
		wantBar  string
	}{
		{100, 30, true, "MuxReveal"},
		{40, 12, false, "MuxReveal"},
		{5, 3, false, "Mu"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			a := newThemedApp(t, "catppuccin-mocha")
			sized(t, a, tt.w, tt.h)
			plain := ansi.Strip(a.View().Content)
			if got := strings.Contains(plain, "No panes yet."); got != tt.wantCard {
				t.Errorf("card present = %v, want %v:\n%s", got, tt.wantCard, plain)
			}
			if !strings.Contains(plain, tt.wantBar) {
				t.Errorf("view lacks %q:\n%s", tt.wantBar, plain)
			}
		})
	}
}

// barApp has one shell pane in /w/proj.
func barApp(t *testing.T, w int) *App {
	t.Helper()

	a := newTestApp(t)
	sized(t, a, w, 12)

	if err := a.dispatch(core.OpenPane{Kind: core.KindShell, Cwd: "/w/proj", Split: core.SplitVertical}); err != nil {
		t.Fatal(err)
	}

	return a
}

func lastRow(a *App) string {
	lines := strings.Split(ansi.Strip(a.View().Content), "\n")

	return lines[len(lines)-1]
}

func TestStatusBar(t *testing.T) {
	mode := " " + iconApp + " MuxReveal " + edgeSolid + " "
	// The legend up to and including each pair, as the width allows.
	legend := "ctrl+space then  : palette   c new tab   , rename   z zoom   q quit"
	toZoom := "ctrl+space then  : palette   c new tab   , rename   z zoom"
	toRename := "ctrl+space then  : palette   c new tab   , rename"
	toNewTab := "ctrl+space then  : palette   c new tab"

	// pad fills the row to w cells; right is placed flush at the edge.
	row := func(w int, left, right string) string {
		return left + strings.Repeat(" ", w-lipgloss.Width(left)-lipgloss.Width(right)) + right
	}

	tests := []struct {
		name  string
		setup func(a *App)
		icons bool
		w     int
		want  string
	}{
		{"normal with the cwd", func(*App) {}, true, 100, row(100, mode+legend, iconFolder+" /w/proj ")},
		{"icons off", func(*App) {}, false, 100, row(100, " MuxReveal "+" "+legend, "/w/proj ")},
		{"leader waiting", func(a *App) { a.pending = true }, true, 100,
			row(100, " "+iconLeader+" ctrl+space "+edgeSolid+" esc cancel", iconFolder+" /w/proj ")},
		{"notice", func(a *App) { a.notice = "cannot start shell" }, true, 100,
			row(100, mode+toZoom, iconAlert+" cannot start shell ")},
		{"notice with control characters", func(a *App) { a.notice = "a\nb\x1b[31m" }, true, 100,
			row(100, mode+legend, iconAlert+" ab[31m ")},
		{"long cwd is cut", func(a *App) {
			a.panes = map[core.PaneID]*pane{}
			a.store = core.NewStore(core.State{Theme: "catppuccin-mocha"})
			_ = a.dispatch(core.OpenPane{Kind: core.KindShell, Cwd: "/a/very/long/path/to/some/project/dir", Split: core.SplitVertical})
		}, true, 100, row(100, mode+toRename, iconFolder+" /a/very/long/path/to/som…"+" ")},
		{"pairs drop from the end", func(*App) {}, true, 53, row(53, mode+toNewTab, "")},
		{"one pair left", func(*App) {}, true, 49, row(49, mode+"ctrl+space then  : palette", "")},
		{"lead-in only", func(*App) {}, true, 40, row(40, mode+"ctrl+space then", "")},
		{"mode only", func(*App) {}, true, 29, row(29, mode, "")},
		{"no room for the right side", func(*App) {}, true, 76, row(76, mode+toZoom, "")},
		{"just room for the right side", func(*App) {}, true, 77, row(77, mode+toRename, iconFolder+" /…"+" ")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := barApp(t, tt.w)
			a.icons = tt.icons
			tt.setup(a)

			if got := lastRow(a); got != tt.want {
				t.Errorf("status bar =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// The right side leaves 40 cells for the legend after the mode segment and the
// lead-in; the legend stops two cells before it.
func TestStatusBarRightRoom(t *testing.T) {
	a := barApp(t, 80)
	if err := a.dispatch(core.SetPaneCwd{ID: "p1", Cwd: "/" + strings.Repeat("c", 60)}); err != nil {
		t.Fatal(err)
	}

	got := lastRow(a)
	// 80 - mode(15) - lead-in(17) - 40 - 1 = 7 cells for the icon, a space and the path
	if want := iconFolder + " /ccc…" + " "; !strings.HasSuffix(got, want) {
		t.Errorf("status bar = %q, want it to end with %q", got, want)
	}

	if !strings.Contains(got, "c new tab   , rename") {
		t.Errorf("legend lost pairs to the cwd: %q", got)
	}
}

// Each legend pair shows only when its command is bound.
func TestStatusBarLegendFollowsKeymap(t *testing.T) {
	a := barApp(t, 120)
	a.keymap.bindings = append(a.keymap.bindings, binding{key: "c", command: "palette.open"})

	if !strings.Contains(lastRow(a), ": palette") {
		t.Errorf("legend lost a bound command: %q", lastRow(a))
	}

	a.keymap.bindings = slices.DeleteFunc(a.keymap.bindings, func(b binding) bool { return b.command == "pane.zoom" })

	if got := lastRow(a); strings.Contains(got, "zoom") {
		t.Errorf("legend shows the unbound zoom: %q", got)
	}
}

func TestStatusBarExactWidth(t *testing.T) {
	for _, icons := range []bool{true, false} {
		for _, state := range []string{"normal", "pending", "notice"} {
			for w := 1; w <= 160; w++ {
				a := barApp(t, w)
				a.icons = icons

				switch state {
				case "pending":
					a.pending = true
				case "notice":
					a.notice = "something went wrong with a long explanation"
				}

				if got := lipgloss.Width(a.statusBar(w, a.store.State())); got != w {
					t.Fatalf("icons=%v %s width=%d: bar is %d cells", icons, state, w, got)
				}
			}
		}
	}
}

func TestStatusBarColours(t *testing.T) {
	ui := mochaUI(t)
	a := barApp(t, 100)
	vt := lineVT(t, a.statusBar(100, a.store.State()), 100)

	assertColours(t, vt, 0, 0, " ", ui.OnAccent, ui.Accent, true)
	assertColours(t, vt, 1, 0, iconApp, ui.OnAccent, ui.Accent, true)
	assertColours(t, vt, 13, 0, edgeSolid, ui.Accent, ui.Bar, false)
	assertColours(t, vt, 15, 0, "c", ui.Subtle, ui.Bar, false)
	// after "ctrl+space then" (15 cells) and two blanks
	assertColours(t, vt, 32, 0, ":", ui.Key, ui.Bar, true)
	assertColours(t, vt, 34, 0, "p", ui.Muted, ui.Bar, false)
	assertColours(t, vt, 92, 0, "/", ui.Muted, ui.Bar, false)

	a.pending = true
	vt = lineVT(t, a.statusBar(100, a.store.State()), 100)
	assertColours(t, vt, 1, 0, iconLeader, ui.OnAccent, ui.Warning, true)
	assertColours(t, vt, 14, 0, edgeSolid, ui.Warning, ui.Bar, false)
}

// A notice takes the room it needs before the legend: pairs drop from the end,
// then the lead-in, and the notice is cut only when it still does not fit.
func TestStatusBarNoticePriority(t *testing.T) {
	mode := " " + iconApp + " MuxReveal " + edgeSolid + " "
	long := strings.Repeat("n", 60)

	tests := []struct {
		name, notice, left, right string
	}{
		{"pairs give way", "boom", "ctrl+space then  : palette", iconAlert + " boom "},
		{"lead-in gives way", strings.Repeat("x", 30), "", iconAlert + " " + strings.Repeat("x", 30) + " "},
		{"cut last", long, "", iconAlert + " " + strings.Repeat("n", 41) + "… "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := barApp(t, 60)
			a.notice = tt.notice

			got := lastRow(a)
			left := mode + tt.left
			want := left + strings.Repeat(" ", 60-lipgloss.Width(left)-lipgloss.Width(tt.right)) + tt.right

			if got != want {
				t.Errorf("status bar =\n%q\nwant\n%q", got, want)
			}

			if w := lipgloss.Width(got); w != 60 {
				t.Errorf("width = %d, want 60", w)
			}
		})
	}
}

func TestTildeHome(t *testing.T) {
	tests := []struct{ name, path, home, want string }{
		{"equal", "/home/morten", "/home/morten", "~"},
		{"under home", "/home/morten/dev/x", "/home/morten", "~/dev/x"},
		{"prefix-only lookalike", "/home/mortenx/dev", "/home/morten", "/home/mortenx/dev"},
		{"outside home", "/srv/app", "/home/morten", "/srv/app"},
		{"empty home", "/home/morten", "", "/home/morten"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tildeHome(tt.path, tt.home); got != tt.want {
				t.Errorf("tildeHome(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
			}
		})
	}
}

package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

var (
	usageFive = core.UsageWindow{Used: 23, ResetsAt: t0.Add(2 * time.Hour)}
	usageWeek = core.UsageWindow{Used: 41, ResetsAt: t0.Add(52 * time.Hour)}
)

// seedUsage offers the windows to the store at the app's clock and fails the
// test when they are refused.
func seedUsage(t *testing.T, a *App, five, week core.UsageWindow) {
	t.Helper()

	act := core.NoteUsage{FiveHour: five, SevenDay: week, At: a.clock.Now()}
	if !act.Changes(a.store.State().Usage) {
		t.Fatalf("usage %+v is refused at %v", act, act.At)
	}

	if err := a.dispatch(act); err != nil {
		t.Fatal(err)
	}
}

func TestUsageModuleText(t *testing.T) {
	t.Parallel()

	withUsed := func(w core.UsageWindow, used int) core.UsageWindow {
		w.Used = used

		return w
	}

	tests := []struct {
		name  string
		icons bool
		five  core.UsageWindow
		week  core.UsageWindow
		at    time.Duration
		want  string // empty: no module
	}{
		{"both", true, usageFive, usageWeek, 0, " \U000F04C5 5h 23%  7d 41% "},
		{"both, no icons", false, usageFive, usageWeek, 0, " 5h 23%  7d 41% "},
		{"weekly only", true, core.UsageWindow{}, usageWeek, 0, " \U000F04C5 7d 41% "},
		{"one digit", false, withUsed(usageFive, 5), core.UsageWindow{}, 0, " 5h  5% "},
		{"full", false, core.UsageWindow{}, withUsed(usageWeek, 100), 0, " 7d 100% "},
		{"5h reset passed", true, usageFive, usageWeek, 3 * time.Hour, " \U000F04C5 7d 41% "},
		{"both passed", true, usageFive, usageWeek, 53 * time.Hour, ""},
		{"none", true, core.UsageWindow{}, core.UsageWindow{}, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u := core.Usage{FiveHour: tt.five, SevenDay: tt.week}
			m, ok := usageModule(u, t0.Add(tt.at), tt.icons, mochaUI(t))

			if (tt.want != "") != ok {
				t.Fatalf("ok = %v, want %v", ok, tt.want != "")
			}

			if !ok {
				return
			}

			if got := ansi.Strip(m.render()); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}

			if got := lipgloss.Width(m.render()); got != m.width {
				t.Errorf("renders %d cells, width says %d", got, m.width)
			}
		})
	}
}

func TestUsageWindows(t *testing.T) {
	t.Parallel()

	u := core.Usage{FiveHour: usageFive, SevenDay: usageWeek}
	got := usageWindows(u, t0)

	if len(got) != 2 || got[0].label != "5h" || got[0].name != "Session" || got[1].label != "7d" || got[1].name != "Week" {
		t.Fatalf("windows = %+v", got)
	}

	if got := usageWindows(u, t0.Add(3*time.Hour)); len(got) != 1 || got[0].label != "7d" {
		t.Errorf("after the 5h reset: %+v", got)
	}
}

func TestUsageModuleColours(t *testing.T) {
	t.Parallel()

	ui := mochaUI(t)
	win := func(used int, resets time.Duration) core.UsageWindow {
		return core.UsageWindow{Used: used, ResetsAt: t0.Add(resets)}
	}

	tests := []struct {
		name       string
		five, week int
		fiveFg     theme.Hex
		weekFg     theme.Hex
		iconFg     theme.Hex
		fiveBold   bool
		weekBold   bool
	}{
		{"cool", 23, 69, ui.Meter, ui.Meter, ui.Meter, false, false},
		{"warm", 70, 89, ui.Warning, ui.Warning, ui.Warning, false, false},
		{"hot", 90, 100, ui.Error, ui.Error, ui.Error, true, true},
		{"hottest icon", 95, 41, ui.Error, ui.Meter, ui.Error, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := trayApp(t, true)
			a.clock = &fakeClock{t: t0}
			seedUsage(t, a, win(tt.five, 2*time.Hour), win(tt.week, 52*time.Hour))

			w := 100
			vt := lineVT(t, a.workspaceBar(w), w)

			clockW := lipgloss.Width(trayModuleByID(a, modClock).render())
			loadX := w - clockW - 1 - trayModuleByID(a, modLoad).width
			x := loadX - 1 - trayModuleByID(a, modUsage).width

			text := func(at int, s string, fg theme.Hex, bold bool) {
				for i, r := range []rune(s) {
					assertColours(t, vt, at+i, 0, string(r), fg, ui.Surface, bold)
				}
			}

			// " icon 5h NN%  7d NN% "
			text(x+1, iconUsage, tt.iconFg, false)
			text(x+3, "5h", ui.Muted, false)
			text(x+6, fmt.Sprintf("%2d%%", tt.five), tt.fiveFg, tt.fiveBold)
			text(x+11, "7d", ui.Muted, false)
			text(x+14, fmt.Sprintf("%2d%%", tt.week), tt.weekFg, tt.weekBold)
		})
	}
}

func TestTrayUsageModuleSitsBetweenPRAndLoad(t *testing.T) {
	t.Parallel()

	a := prTrayApp(t, true, prInfo("OPEN", checksRunning))
	a.clock = &fakeClock{t: t0}
	seedUsage(t, a, usageFive, usageWeek)

	want := []string{modLayout, modPR, modUsage, modLoad, modClock}
	if got := modIDs(a.trayModules()); !slices.Equal(got, want) {
		t.Errorf("modules = %v, want %v", got, want)
	}
}

// usageAppWith is a tray app on a fake clock with the windows seeded three
// minutes before t0, so the age reads "updated 3 min ago"; the clock then
// stands at t0.
func usageAppWith(t *testing.T, icons bool, five, week core.UsageWindow) (*App, *fakeClock) {
	t.Helper()

	a := trayApp(t, icons)
	clk := &fakeClock{t: t0.Add(-3 * time.Minute)}
	a.clock = clk
	seedUsage(t, a, five, week)
	clk.Set(t0)

	return a, clk
}

func usageApp(t *testing.T, icons bool) (*App, *fakeClock) {
	t.Helper()

	return usageAppWith(t, icons, usageFive, usageWeek)
}

// usageBox is the popover's lines, stripped, and its rect.
func usageBox(t *testing.T, a *App) ([]string, core.Rect) {
	t.Helper()

	lines, r, ok := a.usagePopover(a.current())
	if !ok {
		t.Fatal("the popover does not show")
	}

	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}

	return out, r
}

func TestUsagePopoverText(t *testing.T) {
	t.Parallel()

	bar := func(n int) string { return ansi.Strip(usageBar(n, 20, "", mochaUI(t))) }
	both := []string{
		"Session  " + bar(23) + "  23%",
		"         resets in 2h 0m (02:00)",
		"",
		"Week     " + bar(41) + "  41%",
		"         resets in 2d 4h (Sat 04:00)",
		"",
		"updated 3 min ago",
		"",
		"esc close",
	}

	tests := []struct {
		name  string
		icons bool
		five  core.UsageWindow
		title string
		rows  []string
		w     int
	}{
		{"both", true, usageFive, "\u256d\u2500 \U000F04C5 Claude usage ", both, 42},
		{"icons off", false, usageFive, "\u256d\u2500 Claude usage ", both, 42},
		{"weekly only", true, core.UsageWindow{}, "\u256d\u2500 \U000F04C5 Claude usage ", []string{
			"Week  " + bar(41) + "  41%",
			"      resets in 2d 4h (Sat 04:00)",
			"",
			"updated 3 min ago",
			"",
			"esc close",
		}, 39},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, _ := usageAppWith(t, tt.icons, tt.five, usageWeek)
			chord(a, 'u')

			lines, r := usageBox(t, a)
			if len(lines) != len(tt.rows)+2 || r.W != tt.w || r.H != len(lines) {
				t.Fatalf("box is %dx%d with %d lines, want %d wide and %d tall", r.W, r.H, len(lines), tt.w, len(tt.rows)+2)
			}

			if !strings.HasPrefix(lines[0], tt.title) {
				t.Errorf("top = %q, want prefix %q", lines[0], tt.title)
			}

			for i, want := range tt.rows {
				row := strings.TrimPrefix(lines[i+1], "\u2502")
				row = strings.TrimSuffix(row, "\u2502")

				if got := strings.TrimRight(strings.TrimPrefix(row, "  "), " "); got != want {
					t.Errorf("row %d = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestUsagePopoverGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		icons      bool
		five, week int
	}{
		{"both calm", true, 23, 41},
		{"hot and warm", true, 95, 74},
		{"icons off", false, 23, 41},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			five, week := usageFive, usageWeek
			five.Used, week.Used = tt.five, tt.week

			a, _ := usageAppWith(t, tt.icons, five, week)
			chord(a, 'u')

			lines, _, ok := a.usagePopover(a.current())
			if !ok {
				t.Fatal("the popover does not show")
			}

			golden.RequireEqual(t, []byte(strings.Join(lines, "\n")))
		})
	}
}

func TestUsagePopoverPlacement(t *testing.T) {
	t.Parallel()

	a, _ := usageApp(t, true)
	chord(a, 'u')

	check := func(t *testing.T, width int) core.Rect {
		t.Helper()

		sized(t, a, width, 30)

		lines, r := usageBox(t, a)
		view := strings.Split(a.View().Content, "\n")

		for i, l := range view {
			if w := lipgloss.Width(l); w != width {
				t.Fatalf("width %d: line %d is %d cells", width, i, w)
			}
		}

		got := ansi.Strip(ansi.Cut(view[1], r.X, r.X+r.W))
		if got != lines[0] {
			t.Errorf("width %d: row 1 at the box shows %q, want %q", width, got, lines[0])
		}

		return r
	}

	t.Run("on the bar", func(t *testing.T) {
		r := check(t, 100)

		p := planBar(a.tabInfos(), a.store.State().Active, a.trayModules(), 100, a.icons)

		x, w, ok := trayModuleAt(p, 100, a.icons, modUsage)
		if !ok || r.Y != 1 || r.X != x || w != trayModuleByID(a, modUsage).width {
			t.Errorf("rect %+v, module at %d, %d wide (%v), want y 1 and x %d", r, x, w, ok, x)
		}
	})

	t.Run("off the bar", func(t *testing.T) {
		width := 99

		for ; width > 40; width-- {
			sized(t, a, width, 30)

			p := planBar(a.tabInfos(), a.store.State().Active, a.trayModules(), width, a.icons)
			if _, _, ok := trayModuleAt(p, width, a.icons, modUsage); !ok {
				break
			}
		}

		r := check(t, width)
		if r.X != width-1-r.W {
			t.Errorf("width %d: x = %d, want %d", width, r.X, width-1-r.W)
		}
	})

	t.Run("narrow", func(t *testing.T) {
		r := check(t, 30)
		if r.X != 0 || r.W != 30 {
			t.Errorf("rect %+v, want x 0 and 30 wide", r)
		}
	})
}

func TestUsageToggle(t *testing.T) {
	t.Parallel()

	t.Run("no usage", func(t *testing.T) {
		t.Parallel()

		a := trayApp(t, true)
		a.clock = &fakeClock{t: t0}
		chord(a, 'u')

		if a.tray.usageOpen || a.notice != "no Claude usage yet" {
			t.Errorf("open = %v, notice = %q", a.tray.usageOpen, a.notice)
		}
	})

	t.Run("usage", func(t *testing.T) {
		t.Parallel()

		a, _ := usageApp(t, true)

		chord(a, 'u')

		if !a.usageShows() {
			t.Fatal("leader u did not open it")
		}

		chord(a, 'u')

		if a.usageShows() {
			t.Fatal("leader u again did not close it")
		}

		a.openCommandPalette()

		for _, it := range a.overlay.items {
			if it.ID == "usage.toggle" {
				if it.Title != "Claude usage" || it.Hint != "ctrl+space u" {
					t.Errorf("palette row = %+v", it)
				}

				return
			}
		}

		t.Error("the palette has no usage.toggle row")
	})
}

func TestUsagePopoverHoldsKeys(t *testing.T) {
	t.Parallel()

	a, _ := escApp(t)
	id := a.store.State().Tab().Focus
	seedUsage(t, a, usageFive, usageWeek)

	chord(a, 'u')
	press(a, keyRune('x'))
	a.Update(tea.PasteMsg{Content: "pasted"})

	if got := sentSince(t, a, id); got != "" {
		t.Errorf("the pane was sent %q while the popover showed", got)
	}

	if !a.usageShows() {
		t.Fatal("a key or paste closed it")
	}

	chord(a, ':')

	if a.overlay == nil || !a.usageShows() {
		t.Fatalf("palette open = %v, popover shows = %v", a.overlay != nil, a.usageShows())
	}

	press(a, keyEsc)

	if a.overlay != nil || !a.usageShows() {
		t.Fatalf("after esc: palette open = %v, popover shows = %v", a.overlay != nil, a.usageShows())
	}

	press(a, keyEsc)

	if a.usageShows() {
		t.Error("esc did not close the popover")
	}
}

func TestUsagePopoverClosesWhenSpent(t *testing.T) {
	t.Parallel()

	a, clk := usageApp(t, true)
	chord(a, 'u')

	clk.Set(t0.Add(53 * time.Hour))
	a.Update(clockMsg{text: "05:00"})

	if a.tray.usageOpen || slices.Contains(modIDs(a.trayModules()), modUsage) {
		t.Fatalf("open = %v, modules = %v", a.tray.usageOpen, modIDs(a.trayModules()))
	}

	seedUsage(t, a, core.UsageWindow{Used: 5, ResetsAt: t0.Add(58 * time.Hour)}, core.UsageWindow{Used: 6, ResetsAt: t0.Add(200 * time.Hour)})

	if !slices.Contains(modIDs(a.trayModules()), modUsage) {
		t.Error("the module did not return with new data")
	}

	if a.usageShows() {
		t.Error("new data reopened the popover")
	}
}

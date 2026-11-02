package ui

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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

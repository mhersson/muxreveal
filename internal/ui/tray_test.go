package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
)

func TestUntilNextMinute(t *testing.T) {
	base := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{"on the boundary", base, time.Minute},
		{"mid minute", base.Add(30*time.Second + 250*time.Millisecond), 29*time.Second + 750*time.Millisecond},
		{"just before", base.Add(59*time.Second + 999*time.Millisecond), time.Millisecond},
		{"just after", base.Add(time.Nanosecond), time.Minute - time.Nanosecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := untilNextMinute(tt.now); got != tt.want {
				t.Errorf("untilNextMinute(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

// The clock text changes at minute boundaries only, and a message that repeats
// it changes nothing.
func TestClockTextChangesAtMinuteBoundaries(t *testing.T) {
	a := newTestApp(t)
	clk := &fakeClock{t: time.Date(2026, 10, 26, 14, 32, 5, 0, time.UTC)}
	a.clock = clk

	step := func() string {
		msg := a.clockCmd(false)()

		_, cmd := a.Update(msg)
		if cmd == nil {
			t.Fatal("clockMsg did not re-arm the clock")
		}

		return a.tray.clock
	}

	if got := step(); got != "14:32" {
		t.Fatalf("clock = %q, want 14:32", got)
	}

	clk.Advance(54 * time.Second)

	if got := step(); got != "14:32" {
		t.Errorf("clock at 14:32:59 = %q, want 14:32", got)
	}

	clk.Advance(time.Second)

	if got := step(); got != "14:33" {
		t.Errorf("clock at 14:33:00 = %q, want 14:33", got)
	}
}

func modIDs(mods []trayModule) []string {
	var ids []string
	for _, m := range mods {
		ids = append(ids, m.id)
	}

	return ids
}

func TestTrayHasNoAgentsModule(t *testing.T) {
	a := trayApp(t, true)
	openIn(t, a, core.OpenPane{Kind: core.KindAgent, Cwd: "/w/b", Split: core.SplitVertical})

	for _, id := range modIDs(a.trayModules()) {
		if id == "agents" {
			t.Fatal("the tray carries an agents module")
		}
	}
}

func TestSparkline(t *testing.T) {
	const blank = "\u2800"

	tests := []struct {
		name string
		in   []int
		want string
	}{
		{"nothing sampled yet", nil, strings.Repeat(blank, 5)},
		{"idle fills one dot each", make([]int, 10), strings.Repeat("\u28C0", 5)},
		{"quarters", []int{1, 25, 26, 50, 51, 75, 76, 100, 100, 100}, "\u28C0\u28E4\u28F6\u28FF\u28FF"},
		{"right column", []int{0, 1, 0, 26, 0, 51, 0, 100, 0, 0}, "\u28C0\u28E0\u28F0\u28F8\u28C0"},
		{"newest sample is last", []int{100, 0}, strings.Repeat(blank, 4) + "\u28C7"},
		{"only the last ten", []int{100, 100, 100, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, strings.Repeat("\u28C0", 5)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sparkline(tt.in); got != tt.want {
				t.Errorf("sparkline(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatMem(t *testing.T) {
	const gib = 1 << 30

	tests := []struct {
		in   uint64
		want string
	}{
		{0, "0M"},
		{512 << 20, "512M"},
		{gib, "1.0G"},
		{gib * 98 / 10, "9.8G"},
		{15*gib + gib/2, "15.5G"},
		{128 * gib, "128G"},
	}

	for _, tt := range tests {
		if got := formatMem(tt.in); got != tt.want {
			t.Errorf("formatMem(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// fixtureTray fills every sampled module with fixed values.
func fixtureTray(a *App) {
	a.tray.clock = "14:32"
	a.tray.load = &loadInfo{CPU: []int{5, 12, 30, 55, 80, 12, 3, 12, 40, 12}, Mem: "9.8G"}
}

func trayApp(t *testing.T, icons bool) *App {
	t.Helper()

	a := newTestApp(t)
	a.icons = icons
	sized(t, a, 100, 30)
	openIn(t, a,
		core.OpenPane{Kind: core.KindAgent, Cwd: "/w/api", Split: core.SplitVertical},
		core.NewTab{Kind: core.KindShell, Cwd: "/w/web"},
	)
	fixtureTray(a)

	return a
}

func TestTrayModuleWidths(t *testing.T) {
	for _, icons := range []bool{true, false} {
		a := trayApp(t, icons)
		mods := a.trayModules()

		if want := []string{modLayout, modLoad, modClock}; !slices.Equal(modIDs(mods), want) {
			t.Fatalf("icons=%v modules = %v, want %v", icons, modIDs(mods), want)
		}

		for _, m := range mods {
			if got := lipgloss.Width(m.render()); got != m.width {
				t.Errorf("icons=%v %s renders %d cells, width says %d", icons, m.id, got, m.width)
			}
		}
	}
}

func TestTrayOmitsAbsentModules(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)

	if got := a.trayModules(); len(got) != 0 {
		t.Errorf("modules before any sample = %v, want none", modIDs(got))
	}

	a.tray.clock = "09:05"

	if got := modIDs(a.trayModules()); !slices.Equal(got, []string{modClock}) {
		t.Errorf("modules = %v, want only the clock", got)
	}
}

func TestBarLinesStayExactWidth(t *testing.T) {
	for _, icons := range []bool{true, false} {
		a := trayApp(t, icons)

		for w := 1; w <= 130; w++ {
			if got := lipgloss.Width(a.workspaceBar(w)); got != w {
				t.Fatalf("icons=%v width %d: bar is %d cells", icons, w, got)
			}
		}
	}
}

// The tray gives way in DESIGN's order as the bar narrows.
func TestTrayDropOrder(t *testing.T) {
	rank := map[string]int{modLoad: 0, modPR: 1, modLayout: 2, modClock: 3}

	for _, icons := range []bool{true, false} {
		a := trayApp(t, icons)
		tabs := a.tabInfos()
		full := modIDs(a.trayModules())

		for w := 130; w >= 1; w-- {
			p := planBar(tabs, a.store.State().Active, a.trayModules(), w, icons)
			kept := modIDs(p.tray)

			for _, id := range full {
				if slices.Contains(kept, id) {
					continue
				}

				for _, other := range kept {
					if rank[other] < rank[id] {
						t.Fatalf("icons=%v width %d: %s stayed after %s left (kept %v)", icons, w, other, id, kept)
					}
				}
			}
		}
	}
}

func TestBarGoldens(t *testing.T) {
	tests := []struct {
		name  string
		icons bool
		width int
	}{
		{"icons wide", true, 100},
		{"icons without load", true, 60},
		{"icons layout and clock", true, 50},
		{"plain wide", false, 100},
		{"plain without load", false, 60},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := trayApp(t, tt.icons)
			golden.RequireEqual(t, []byte(a.workspaceBar(tt.width)))
		})
	}
}

func TestTrayOnlyClockEdge(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 40, 10)
	a.tray.clock = "14:32"

	vt := lineVT(t, a.workspaceBar(40), 40)
	ui := mochaUI(t)

	assertColours(t, vt, 30, 0, trayOpen, ui.Clock, ui.Bar, false)
	assertColours(t, vt, 32, 0, iconClock, ui.OnAccent, ui.Clock, true)
}

func TestTrayColours(t *testing.T) {
	a := trayApp(t, true)
	ui := mochaUI(t)
	w := 100
	vt := lineVT(t, a.workspaceBar(w), w)

	// Walk the tray from the right: clock segment, load, layout.
	clockW := lipgloss.Width(trayModuleByID(a, modClock).render())
	x := w - clockW
	assertColours(t, vt, x-1, 0, trayOpen, ui.Clock, ui.Surface, false)
	assertColours(t, vt, x+1, 0, iconClock, ui.OnAccent, ui.Clock, true)
	assertColours(t, vt, x+3, 0, "1", ui.OnAccent, ui.Clock, true)

	loadW := trayModuleByID(a, modLoad).width
	lx := x - 1 - loadW
	assertColours(t, vt, lx+1, 0, iconLoad, ui.Load, ui.Surface, false)

	lyW := trayModuleByID(a, modLayout).width
	yx := lx - 1 - lyW
	assertColours(t, vt, lx-1, 0, edgeThin, ui.Bar, ui.Surface, false)
	assertColours(t, vt, yx-1, 0, trayOpen, ui.Surface, ui.Bar, false)
}

func trayModuleByID(a *App, id string) trayModule {
	for _, m := range a.trayModules() {
		if m.id == id {
			return m
		}
	}

	return trayModule{}
}

// Messages that change nothing neither re-render state nor start work.
// Samples that repeat what the bar shows leave its row byte-identical.
func TestUnchangedSamplesLeaveBarIdentical(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)

	for range loadHistory {
		a.Update(loadMsg{sample: loadSample{CPU: 12, Mem: 9 << 30}})
	}

	a.Update(clockMsg{text: "14:32"})

	before := a.workspaceBar(100)

	a.Update(clockMsg{text: "14:32"})
	a.Update(loadMsg{sample: loadSample{CPU: 12, Mem: 9<<30 + 1}})

	if after := a.workspaceBar(100); after != before {
		t.Errorf("bar changed:\n%q\n%q", before, after)
	}
}

func TestClockWake(t *testing.T) {
	base := time.Date(2026, 10, 26, 14, 32, 0, 0, time.UTC)
	tests := []struct {
		name      string
		now       time.Time
		last      string
		wantText  string
		wantAgain time.Duration
	}{
		{"new minute", base.Add(time.Minute), "14:32", "14:33", 0},
		{"woke early", base.Add(59*time.Second + 900*time.Millisecond), "14:32", "", 100 * time.Millisecond},
		{"clock stepped back", base.Add(-30 * time.Second), "14:32", "14:31", 0},
		{"first reading", base, "", "14:32", 0},
	}

	for _, tt := range tests {
		text, again := clockWake(tt.now, tt.last)
		if text != tt.wantText || again != tt.wantAgain {
			t.Errorf("%s: clockWake = %q, %v, want %q, %v", tt.name, text, again, tt.wantText, tt.wantAgain)
		}
	}
}

func TestLoadHistoryKeepsLastTen(t *testing.T) {
	a := newTestApp(t)

	for i := range 15 {
		a.Update(loadMsg{sample: loadSample{CPU: i, Mem: 1 << 30}})
	}

	want := []int{5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	if got := a.tray.load.CPU; !slices.Equal(got, want) {
		t.Errorf("history = %v, want %v", got, want)
	}
}

func TestLoadErrorKeepsPreviousAndRearms(t *testing.T) {
	a := newTestApp(t)
	a.load = &fakeLoad{}

	a.Update(loadMsg{sample: loadSample{CPU: 7, Mem: 1 << 30}})

	_, cmd := a.Update(loadMsg{err: errors.New("boom")})
	if cmd == nil {
		t.Error("a failed sample did not re-arm")
	}

	if a.tray.load == nil || a.tray.load.CPU[len(a.tray.load.CPU)-1] != 7 {
		t.Errorf("load after an error = %+v, want the previous value", a.tray.load)
	}
}

type fakeLoad struct{ calls atomic.Int32 }

func (f *fakeLoad) Sample(context.Context) (loadSample, error) {
	f.calls.Add(1)

	return loadSample{CPU: 3, Mem: 2 << 30}, nil
}

// No tray work runs inside Update: it only returns commands.
func TestNoIOInUpdate(t *testing.T) {
	a := newTestApp(t)
	fl := &fakeLoad{}
	a.load = fl

	a.Init()
	sized(t, a, 100, 30)
	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical})
	a.Update(clockMsg{text: "10:00"})
	a.Update(loadMsg{sample: loadSample{CPU: 1, Mem: 1 << 30}})

	if fl.calls.Load() != 0 {
		t.Errorf("Update ran sampling: load %d", fl.calls.Load())
	}
}

func TestTrayHasNoGitModule(t *testing.T) {
	for _, icons := range []bool{true, false} {
		a := trayApp(t, icons)

		if got := modIDs(a.trayModules()); slices.Contains(got, "git") {
			t.Errorf("icons=%v modules = %v, want no git module", icons, got)
		}

		if bar := a.workspaceBar(100); strings.Contains(bar, "feat/tabs") || strings.Contains(bar, iconGit) {
			t.Errorf("icons=%v bar shows a branch: %q", icons, bar)
		}
	}
}

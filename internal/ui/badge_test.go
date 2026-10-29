package ui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// badgeApp is a w x 12 app with one agent pane, p1, in state st.
func badgeApp(t *testing.T, w int, st core.AgentState, icons bool) *App {
	t.Helper()

	a := newTestApp(t)
	a.icons = icons
	sized(t, a, w, 12)
	openIn(t, a, core.OpenPane{Kind: core.KindAgent, Cwd: "/home/u/proj", Split: core.SplitVertical})
	attachPanes(t, a, map[core.PaneID]string{"p1": "hello"})
	setAgentStates(t, a, map[core.PaneID]core.AgentState{"p1": st})

	return a
}

func setAgentStates(t *testing.T, a *App, states map[core.PaneID]core.AgentState) {
	t.Helper()

	for id, st := range states {
		if err := a.dispatch(core.SetAgentState{ID: id, State: st}); err != nil {
			t.Fatal(err)
		}
	}
}

// topRow is the first pane border row, below the workspace bar, without styling.
func topRow(a *App) string {
	return ansi.Strip(strings.Split(a.View().Content, "\n")[2])
}

func TestAgentGlyphs(t *testing.T) {
	for name, g := range map[string]struct{ got, want string }{
		"agent":   {iconAgent, "\U000F167A"},
		"blocked": {iconBlocked, "\U000F0026"},
		"idle":    {iconIdle, "\U000F05E1"},
	} {
		if g.got != g.want {
			t.Errorf("%s icon = %U, want %U", name, []rune(g.got), []rune(g.want))
		}
	}
}

func TestStateBadge(t *testing.T) {
	tests := []struct {
		name  string
		st    core.AgentState
		spin  int
		icons bool
		want  string
	}{
		{"working frame 0", core.AgentWorking, 0, true, " ⠋ working "},
		{"working frame 3", core.AgentWorking, 3, true, " ⠸ working "},
		{"working wraps", core.AgentWorking, 10, true, " ⠋ working "},
		{"blocked", core.AgentBlocked, 0, true, " " + iconBlocked + " blocked "},
		{"idle", core.AgentIdle, 0, true, " " + iconIdle + " idle "},
		{"working without icons", core.AgentWorking, 3, false, " ⠸ working "},
		{"blocked without icons", core.AgentBlocked, 0, false, " blocked "},
		{"idle without icons", core.AgentIdle, 0, false, " idle "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := badgeApp(t, 50, tt.st, tt.icons)
			a.spin = tt.spin

			if row, want := topRow(a), tt.want+"─╮"; !strings.HasSuffix(row, want) {
				t.Errorf("top border = %q, want it to end with %q", row, want)
			}
		})
	}
}

func TestStateBadgeAbsent(t *testing.T) {
	a := badgeApp(t, 50, core.AgentUnknown, true)

	row := topRow(a)
	for _, s := range []string{"working", "blocked", "idle"} {
		if strings.Contains(row, s) {
			t.Errorf("top border = %q, want no badge for an agent with no state", row)
		}
	}

	// A shell never has a badge, whatever its recorded state.
	sh := newTestApp(t)
	sized(t, sh, 50, 12)
	openIn(t, sh, core.OpenPane{Kind: core.KindShell, Cwd: "/home/u/proj", Split: core.SplitVertical})
	attachPanes(t, sh, nil)

	if row := topRow(sh); strings.Contains(row, "idle") {
		t.Errorf("shell top border = %q, want no badge", row)
	}
}

func TestStateBadgeBesideTags(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 70, 12)
	openIn(t, a,
		core.OpenPane{Kind: core.KindAgent, Cwd: "/home/u/proj", Split: core.SplitVertical},
		core.OpenPane{Kind: core.KindShell, Cwd: "/home/u/other", Split: core.SplitVertical},
		core.FocusPane{ID: "p1"},
	)

	procs := attachPanes(t, a, nil)
	p := a.panes["p1"]
	feedAndWait(t, p, procs["p1"], strings.Repeat("line\r\n", 40))
	p.render()

	if !p.scrollBy(3) {
		t.Fatal("setup: the pane did not scroll")
	}

	openIn(t, a, core.ToggleZoom{})
	setAgentStates(t, a, map[core.PaneID]core.AgentState{"p1": core.AgentBlocked})

	row := topRow(a)
	zoom, scroll, badge := strings.Index(row, "zoom"), strings.Index(row, iconScroll), strings.Index(row, "blocked")

	if zoom < 0 || scroll < 0 || badge < 0 || scroll > zoom || zoom > badge || !strings.HasSuffix(row, "blocked ─╮") {
		t.Errorf("top border = %q, want scroll, zoom, then the badge rightmost", row)
	}
}

func TestStateBadgeDroppedWhenNarrow(t *testing.T) {
	for w := 8; w <= 40; w++ {
		a := badgeApp(t, w, core.AgentWorking, true)
		row := topRow(a)

		if got := ansi.StringWidth(row); got != w {
			t.Errorf("width %d: top border is %d cells wide", w, got)
		}

		if !strings.HasSuffix(row, "╮") {
			t.Errorf("width %d: top border %q lost its corner", w, row)
		}

		fits := strings.Contains(row, "working")
		// The badge is 11 cells and needs the corner and one border cell on each side.
		if want := w-2 >= 11+1; fits != want {
			t.Errorf("width %d: badge shown = %v, want %v (%q)", w, fits, want, row)
		}
	}
}

func TestStateBadgeColours(t *testing.T) {
	ui := mochaUI(t)

	for _, tt := range []struct {
		st   core.AgentState
		icon string
		text string
		fg   theme.Hex
	}{
		{core.AgentBlocked, iconBlocked, "blocked", ui.Blocked},
		{core.AgentIdle, iconIdle, "idle", ui.Idle},
		{core.AgentWorking, "⠋", "working", ui.Working},
	} {
		t.Run(tt.text, func(t *testing.T) {
			a := badgeApp(t, 50, tt.st, true)
			vt := viewVT(t, a)

			x := strings.Index(topRow(a), tt.icon)
			if x < 0 {
				t.Fatalf("no %q in %q", tt.icon, topRow(a))
			}

			// The row has only single-width runes before the badge.
			x = len([]rune(topRow(a)[:x]))
			assertColours(t, vt, x, 2, tt.icon, tt.fg, ui.Bg, true)
			assertColours(t, vt, x+2, 2, tt.text[:1], tt.fg, ui.Bg, true)
		})
	}
}

func TestStateBadgeGolden(t *testing.T) {
	tests := []struct {
		name  string
		st    core.AgentState
		spin  int
		icons bool
	}{
		{"working-0", core.AgentWorking, 0, true},
		{"working-3", core.AgentWorking, 3, true},
		{"blocked", core.AgentBlocked, 0, true},
		{"idle", core.AgentIdle, 0, true},
		{"none", core.AgentUnknown, 0, true},
		{"working-plain", core.AgentWorking, 3, false},
		{"blocked-plain", core.AgentBlocked, 0, false},
		{"idle-plain", core.AgentIdle, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := badgeApp(t, 40, tt.st, tt.icons)
			a.spin = tt.spin
			golden.RequireEqual(t, []byte(a.View().Content))
		})
	}
}

func TestInactiveTabIconTakesMostUrgentState(t *testing.T) {
	ui := mochaUI(t)

	a := tabsApp(t, 80, 12,
		tabSpec{core.KindShell, "/w/a", "one"},
		tabSpec{core.KindAgent, "/w/b", "two"},
		tabSpec{core.KindAgent, "/w/c", "three"},
	)

	// Tab 2 also holds a working agent beside its blocked one.
	selectTab(t, a, 1)
	openIn(t, a, core.OpenPane{Kind: core.KindAgent, Cwd: "/w/d", Split: core.SplitVertical})

	panes := a.store.State().AgentPanes()
	if len(panes) != 3 {
		t.Fatalf("agent panes = %v, want 3", panes)
	}

	setAgentStates(t, a, map[core.PaneID]core.AgentState{
		panes[0]: core.AgentBlocked, panes[1]: core.AgentWorking, panes[2]: core.AgentIdle,
	})

	// Tab 1 is inactive and has no agent; tab 2 is active; tab 3 is idle.
	// " 1 <shell> one " is 0..8 and the edge sits at 9; the active segment follows.
	vt := lineVT(t, a.workspaceBar(80), 80)
	assertColours(t, vt, 3, 0, iconShell, ui.Dim, ui.Surface, false)
	assertColours(t, vt, 13, 0, iconAgent, ui.OnAccent, ui.Accent, true)
	assertColours(t, vt, 23, 0, iconAgent, ui.Idle, ui.Surface, false)

	selectTab(t, a, 0)
	vt = lineVT(t, a.workspaceBar(80), 80)

	// " 1 <shell> one " active (0..8), edge, then " 2 <agent> two " with the blocked agent.
	assertColours(t, vt, 13, 0, iconAgent, ui.Blocked, ui.Surface, false)
	// Tab 3 holds an idle agent: " 3 <agent> three " follows tab 2's thin edge.
	assertColours(t, vt, 23, 0, iconAgent, ui.Idle, ui.Surface, false)

	if err := a.dispatch(core.SetAgentState{ID: panes[0], State: core.AgentUnknown}); err != nil {
		t.Fatal(err)
	}

	vt = lineVT(t, a.workspaceBar(80), 80)
	assertColours(t, vt, 13, 0, iconAgent, ui.Working, ui.Surface, false)
}

func TestTrayBlockedAgentGolden(t *testing.T) {
	for _, icons := range []bool{true, false} {
		t.Run(fmt.Sprintf("icons=%v", icons), func(t *testing.T) {
			a := trayApp(t, icons)
			setAgentStates(t, a, map[core.PaneID]core.AgentState{a.store.State().AgentPanes()[0]: core.AgentBlocked})
			golden.RequireEqual(t, []byte(a.workspaceBar(100)))
		})
	}
}

// modelApp is a w x 12 app with one working agent, p1, whose status line
// reported model and, unless meter is nil, a context percentage.
func modelApp(t *testing.T, w int, model string, meter *int, icons bool) *App {
	t.Helper()

	a := badgeApp(t, w, core.AgentWorking, icons)
	status := core.SetAgentStatus{ID: "p1", Session: "s1", Model: model}

	if meter != nil {
		status.Context, status.ContextKnown = float64(*meter), true
	}

	openIn(t, a, status)

	return a
}

// zoomedModelApp is modelApp with a shell beside the agent and the agent zoomed
// and, when scrolled, scrolled back three lines, so its top border holds every
// tag but the preselection and COPY.
func zoomedModelApp(t *testing.T, w int, scrolled bool) *App {
	t.Helper()

	a := newTestApp(t)
	sized(t, a, w, 12)
	openIn(t, a,
		core.OpenPane{Kind: core.KindAgent, Cwd: "/home/u/proj", Split: core.SplitVertical},
		core.OpenPane{Kind: core.KindShell, Cwd: "/home/u/other", Split: core.SplitVertical},
		core.FocusPane{ID: "p1"},
	)

	procs := attachPanes(t, a, nil)

	if scrolled {
		p := a.panes["p1"]
		feedAndWait(t, p, procs["p1"], strings.Repeat("line\r\n", 40))
		p.render()

		if !p.scrollBy(3) {
			t.Fatal("setup: the pane did not scroll")
		}
	}

	openIn(t, a,
		core.ToggleZoom{},
		core.SetAgentState{ID: "p1", State: core.AgentWorking},
		core.SetAgentStatus{ID: "p1", Session: "s1", Model: "Opus 5.5", Context: 62, ContextKnown: true},
	)

	return a
}

func TestFrameModelTag(t *testing.T) {
	t.Run("left of the badge", func(t *testing.T) {
		for _, icons := range []bool{true, false} {
			a := modelApp(t, 50, "Opus 5.5", pct(62), icons)

			if row, want := topRow(a), " Opus 5.5 62% ─ ⠋ working ─╮"; !strings.HasSuffix(row, want) {
				t.Errorf("icons=%v: top border = %q, want it to end with %q", icons, row, want)
			}
		}
	})

	t.Run("right of the zoom tag", func(t *testing.T) {
		row := topRow(zoomedModelApp(t, 60, false))

		if want := "zoom ─ Opus 5.5 62% ─ ⠋ working ─╮"; !strings.HasSuffix(row, want) {
			t.Errorf("top border = %q, want it to end with %q", row, want)
		}
	})

	t.Run("right of the zoom and scroll tags", func(t *testing.T) {
		row := topRow(zoomedModelApp(t, 70, true))
		scroll, zoom, model, badge := strings.Index(row, iconScroll), strings.Index(row, "zoom"), strings.Index(row, "Opus"), strings.Index(row, "working")

		if scroll < 0 || zoom < scroll || model < zoom || badge < model || !strings.HasSuffix(row, "Opus 5.5 62% ─ ⠋ working ─╮") {
			t.Errorf("top border = %q, want scroll, zoom, model, then the badge rightmost", row)
		}
	})

	t.Run("without a badge", func(t *testing.T) {
		a := modelApp(t, 50, "Opus 5.5", pct(62), true)
		openIn(t, a, core.SetAgentState{ID: "p1", State: core.AgentUnknown})

		if row, want := topRow(a), " Opus 5.5 62% ─╮"; !strings.HasSuffix(row, want) {
			t.Errorf("top border = %q, want it to end with %q", row, want)
		}
	})

	t.Run("dropped before every other tag", func(t *testing.T) {
		badge, zoom, scroll, model := " ⠋ working ", " "+iconZoom+" zoom ", " "+iconScroll+" 3 ", " Opus 5.5 62% "
		// Each tag takes one border cell more than its width, and the right corner one.
		fits := func(w int, tags ...string) bool {
			need := 0

			for _, tg := range tags {
				need += ansi.StringWidth(tg) + 1
			}

			return w-2 >= need
		}

		for w := 20; w <= 70; w++ {
			row := topRow(zoomedModelApp(t, w, true))

			if got := ansi.StringWidth(row); got != w {
				t.Errorf("width %d: top border is %d cells wide", w, got)
			}

			shown := map[string]bool{
				"badge": strings.Contains(row, "working"), "zoom": strings.Contains(row, "zoom"),
				"scroll": strings.Contains(row, iconScroll), "model": strings.Contains(row, "Opus"),
			}

			if want := fits(w, badge, zoom, scroll, model); shown["model"] != want {
				t.Errorf("width %d: model shown = %v, want %v (%q)", w, shown["model"], want, row)
			}

			if shown["model"] && (!shown["badge"] || !shown["zoom"] || !shown["scroll"]) {
				t.Errorf("width %d: model shown but another tag is not (%q)", w, row)
			}
		}

		// One cell short of the model's room, the other tags are all still there.
		w := ansi.StringWidth(badge+zoom+scroll+model) + 4 + 2 - 1
		if row := topRow(zoomedModelApp(t, w, true)); strings.Contains(row, "Opus") || !strings.Contains(row, "working") || !strings.Contains(row, "zoom") || !strings.Contains(row, iconScroll) {
			t.Errorf("width %d: top border = %q, want the model dropped and the other tags kept", w, row)
		}
	})

	t.Run("no meter shows the model alone", func(t *testing.T) {
		row := topRow(modelApp(t, 50, "Opus 5.5", nil, true))

		if want := " Opus 5.5 ─ ⠋ working ─╮"; !strings.HasSuffix(row, want) || strings.Contains(row, "%") {
			t.Errorf("top border = %q, want it to end with %q and show no meter", row, want)
		}
	})

	t.Run("no model shows nothing", func(t *testing.T) {
		for name, a := range map[string]*App{
			"a meter without a model": modelApp(t, 50, "", pct(62), true),
			"no status":               badgeApp(t, 50, core.AgentWorking, true),
		} {
			if row := topRow(a); strings.Contains(row, "%") || strings.Contains(row, "Opus") || !strings.HasSuffix(row, " ⠋ working ─╮") {
				t.Errorf("%s: top border = %q, want only the badge", name, row)
			}
		}
	})

	t.Run("a shell has none", func(t *testing.T) {
		a := newTestApp(t)
		sized(t, a, 50, 12)
		openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/home/u/proj", Split: core.SplitVertical})
		attachPanes(t, a, nil)

		if row := topRow(a); strings.Contains(row, "%") {
			t.Errorf("shell top border = %q, want no model tag", row)
		}
	})
}

func TestFrameModelTagColours(t *testing.T) {
	ui := mochaUI(t)

	tests := []struct {
		pct  int
		fg   theme.Hex
		bold bool
	}{
		{62, ui.Subtle, false},
		{75, ui.Warning, false},
		{95, ui.Error, true},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.pct), func(t *testing.T) {
			a := modelApp(t, 50, "Opus 5.5", pct(tt.pct), true)
			vt := viewVT(t, a)
			row := topRow(a)

			meter := strconv.Itoa(tt.pct) + "%"
			model, at := strings.Index(row, "Opus"), strings.Index(row, meter)

			if model < 0 || at < 0 {
				t.Fatalf("top border = %q, want the model and %q", row, meter)
			}

			// Everything left of the tag is a single-width rune.
			x, mx := len([]rune(row[:model])), len([]rune(row[:at]))
			assertColours(t, vt, x, 2, "O", ui.Muted, ui.Bg, false)

			for i, r := range []rune(meter) {
				assertColours(t, vt, mx+i, 2, string(r), tt.fg, ui.Bg, tt.bold)
			}
		})
	}
}

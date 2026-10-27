package ui

import (
	"fmt"
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
	return ansi.Strip(strings.Split(a.View().Content, "\n")[1])
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
			assertColours(t, vt, x, 1, tt.icon, tt.fg, ui.Bg, true)
			assertColours(t, vt, x+2, 1, tt.text[:1], tt.fg, ui.Bg, true)
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

func TestTrayAgentIcons(t *testing.T) {
	ui := mochaUI(t)

	a := newTestApp(t)
	sized(t, a, 100, 30)
	openIn(t, a,
		core.OpenPane{Kind: core.KindAgent, Cwd: "/w/a", Split: core.SplitVertical},
		core.OpenPane{Kind: core.KindAgent, Cwd: "/w/b", Split: core.SplitVertical},
		core.NewTab{Kind: core.KindAgent, Cwd: "/w/c"},
		core.NewTab{Kind: core.KindAgent, Cwd: "/w/d"},
		core.NewTab{Kind: core.KindAgent, Cwd: "/w/e"},
	)

	ids := a.store.State().AgentPanes()
	setAgentStates(t, a, map[core.PaneID]core.AgentState{
		ids[0]: core.AgentWorking, ids[1]: core.AgentBlocked, ids[2]: core.AgentIdle, ids[3]: core.AgentBlocked,
	})

	m := trayModuleByID(a, modAgents)
	if got, want := ansi.Strip(m.render()), " "+strings.Repeat(iconAgent, 5)+" "+iconBlocked+"2 "; got != want {
		t.Errorf("agents module = %q, want %q", got, want)
	}

	if m.width != ansi.StringWidth(ansi.Strip(m.render())) {
		t.Errorf("module width = %d, rendered %d", m.width, ansi.StringWidth(ansi.Strip(m.render())))
	}

	vt := lineVT(t, m.render(), m.width)
	for i, fg := range []theme.Hex{ui.Working, ui.Blocked, ui.Idle, ui.Blocked, ui.Agent} {
		assertColours(t, vt, 1+i, 0, iconAgent, fg, ui.Surface, false)
	}

	assertColours(t, vt, 7, 0, iconBlocked, ui.Blocked, ui.Surface, true)
	assertColours(t, vt, 8, 0, "2", ui.Blocked, ui.Surface, true)
}

func TestTrayAgentCountWithoutIcons(t *testing.T) {
	a := newTestApp(t)
	a.icons = false
	sized(t, a, 100, 30)
	openIn(t, a,
		core.OpenPane{Kind: core.KindAgent, Cwd: "/w/a", Split: core.SplitVertical},
		core.OpenPane{Kind: core.KindAgent, Cwd: "/w/b", Split: core.SplitVertical},
		core.OpenPane{Kind: core.KindAgent, Cwd: "/w/c", Split: core.SplitVertical},
	)

	got := func() string { return ansi.Strip(trayModuleByID(a, modAgents).render()) }

	if want := " agents 3 "; got() != want {
		t.Errorf("agents module = %q, want %q", got(), want)
	}

	ids := a.store.State().AgentPanes()
	setAgentStates(t, a, map[core.PaneID]core.AgentState{ids[0]: core.AgentBlocked})

	if want := " agents 3 blocked 1 "; got() != want {
		t.Errorf("agents module = %q, want %q", got(), want)
	}

	m := trayModuleByID(a, modAgents)
	if m.width != len(got()) {
		t.Errorf("module width = %d, want %d", m.width, len(got()))
	}

	vt := lineVT(t, m.render(), m.width)
	ui := mochaUI(t)
	assertColours(t, vt, 1, 0, "a", ui.Fg, ui.Surface, false)
	assertColours(t, vt, 10, 0, "b", ui.Blocked, ui.Surface, true)
}

func TestTrayAgentModuleGolden(t *testing.T) {
	for _, icons := range []bool{true, false} {
		t.Run(fmt.Sprintf("icons=%v", icons), func(t *testing.T) {
			a := trayApp(t, icons)
			setAgentStates(t, a, map[core.PaneID]core.AgentState{a.store.State().AgentPanes()[0]: core.AgentBlocked})
			golden.RequireEqual(t, []byte(a.workspaceBar(100)))
		})
	}
}

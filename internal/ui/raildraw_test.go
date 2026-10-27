package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// railAgent is one agent of the rail fixtures.
type railAgent struct {
	newTab         bool
	project        string
	branch         string
	detached       bool
	state          core.AgentState
	action         string
	age            time.Duration
	stats          *git.Stats
	unseen         bool
	noRepo, noHook bool
}

// longNotice is wide enough that its toast box reaches into the rail's columns at 64 columns.
const longNotice = "ctrl+space > x is not bound, press ctrl+space for the keys"

func railFixtureAgents() []railAgent {
	return []railAgent{
		{newTab: true, project: "api", branch: "main", state: core.AgentBlocked, action: "Allow Bash rm -rf build/?", age: 8 * time.Second, stats: &git.Stats{Files: 2, Added: 12, Removed: 3}, unseen: true},
		{project: "web", branch: "feat/login", state: core.AgentWorking, action: "Edit src/login.tsx", age: 4 * time.Minute, stats: &git.Stats{Files: 5, Added: 120, Removed: 14}},
		{newTab: true, project: "muxreveal", branch: "feat/agents-rail", state: core.AgentWorking, action: "Bash go test ./...", age: 95 * time.Second, stats: &git.Stats{Files: 9, Added: 340, Removed: 12}},
		{project: "docs", branch: "main", state: core.AgentIdle, age: 26 * time.Hour, stats: &git.Stats{}},
		{newTab: true, project: "infra", branch: "a1b2c3d", detached: true, state: core.AgentIdle, age: 12 * time.Minute, stats: &git.Stats{Files: 1, Added: 2, Removed: 0}},
		{project: "scratch", noRepo: true, noHook: true},
	}
}

// drawApp builds an app with the agents opened through the store, the rail
// open, a fixed clock and the probe results set by hand.
func drawApp(t *testing.T, themeName string, icons bool, w, h int, agents []railAgent) *App {
	t.Helper()

	a := newThemedApp(t, themeName)
	a.icons = icons
	a.clock = &fakeClock{t: t0}

	sized(t, a, w, h)

	for i, ag := range agents {
		var act core.Action = core.OpenPane{Kind: core.KindAgent, Cwd: "/w/" + ag.project, Split: core.SplitVertical}
		if ag.newTab {
			act = core.NewTab{Kind: core.KindAgent, Cwd: "/w/" + ag.project}
		}

		if err := a.dispatch(act); err != nil {
			t.Fatal(err)
		}

		id := core.PaneID(fmt.Sprintf("p%d", i+1))
		if !ag.noHook {
			openIn(t, a, core.SetAgentState{ID: id, State: ag.state}, core.SetAgentAction{ID: id, Action: ag.action})
		}

		rp := a.railEntry(id)
		rp.unseen = ag.unseen

		if !ag.noHook {
			rp.hookAt = t0.Add(-ag.age)
		}

		if !ag.noRepo {
			rp.probe = railProbe{known: true, repo: true, project: ag.project, branch: ag.branch, detached: ag.detached}
		}

		if ag.stats != nil {
			rp.probe.stats, rp.probe.statsKnown = *ag.stats, true
		}
	}

	openIn(t, a, core.SetRail{Open: true})

	return a
}

func manyAgents(n, tabs int) []railAgent {
	states := []core.AgentState{core.AgentWorking, core.AgentIdle, core.AgentBlocked, core.AgentUnknown}
	out := make([]railAgent, n)

	for i := range out {
		out[i] = railAgent{
			newTab:  i == 0 || i*tabs/n != (i-1)*tabs/n,
			project: fmt.Sprintf("proj%02d", i+1),
			branch:  fmt.Sprintf("feat/b%d", i+1),
			state:   states[i%len(states)],
			action:  fmt.Sprintf("Edit file%d.go", i+1),
			age:     time.Duration(i+1) * time.Minute,
			stats:   &git.Stats{Files: i % 7, Added: i * 3, Removed: i},
			noHook:  states[i%len(states)] == core.AgentUnknown,
		}
	}

	return out
}

type railCase struct {
	name  string
	build func(t *testing.T, th string, icons bool) *App
}

func railCases() []railCase {
	fixture := func(w, h int, tune func(a *App)) func(t *testing.T, th string, icons bool) *App {
		return func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, w, h, railFixtureAgents())
			a.rail.sel = "p2"

			if tune != nil {
				tune(a)
			}

			return a
		}
	}

	return []railCase{
		{"cards", fixture(100, 30, nil)},
		{"focused-blocked-selected", fixture(100, 30, func(a *App) { a.rail.focused, a.rail.sel = true, "p1" })},
		{"pulse-dim", fixture(100, 30, func(a *App) { a.rail.pulseDim = true })},
		{"search-typing", fixture(100, 30, func(a *App) {
			a.rail.focused, a.rail.searching, a.rail.query = true, true, "feat"
			a.railNormalize(a.store.State())
		})},
		{"search-kept", fixture(100, 30, func(a *App) {
			a.rail.focused, a.rail.query = true, "ma"
			a.railNormalize(a.store.State())
		})},
		{"search-no-match", fixture(100, 30, func(a *App) {
			a.rail.focused, a.rail.searching, a.rail.query = true, true, "zzz"
			a.railNormalize(a.store.State())
		})},
		{"width-84", fixture(84, 30, nil)},
		{"width-70", fixture(70, 30, nil)},
		{"width-64", fixture(64, 30, nil)},
		{"overflow-70", fixture(70, 30, func(a *App) { a.rail.sel = "p5" })},
		{"fits-no-counts", fixture(100, 50, nil)},
		{"width-63", fixture(63, 30, nil)},
		{"body-20-rows", fixture(100, 22, func(a *App) { a.rail.sel = "p5" })},
		{"body-9-rows", fixture(100, 11, func(a *App) { a.rail.sel = "p3" })},
		{"body-3-rows", fixture(100, 5, nil)},
		{"flash", fixture(100, 30, func(a *App) { a.rail.flashPane, a.rail.flashUntil = "p2", t0.Add(600*time.Millisecond) })},
		{"toast", fixture(100, 30, func(a *App) { a.notice = "layout: bsp" })},
		{"toast-over-rail", fixture(64, 30, func(a *App) { a.notice = longNotice })},
		{"no-hooks-selected", fixture(100, 30, func(a *App) { a.rail.sel = "p6" })},
		{"palette", fixture(100, 30, func(a *App) { pressRun(a, keyLeader, keyRune(':')) })},
		{"many", func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, 100, 30, manyAgents(64, 9))
			a.rail.focused, a.rail.sel = true, "p58"

			return a
		}},
		{"many-small", func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, 64, 20, manyAgents(64, 9))
			a.rail.focused, a.rail.sel = true, "p37"

			return a
		}},
		{"no-agents", func(t *testing.T, th string, icons bool) *App { return drawApp(t, th, icons, 100, 30, nil) }},
	}
}

func TestRailGolden(t *testing.T) {
	for _, th := range chromeThemes() {
		for _, icons := range []bool{true, false} {
			for _, c := range railCases() {
				t.Run(fmt.Sprintf("%s/icons=%v/%s", th, icons, c.name), func(t *testing.T) {
					golden.RequireEqual(t, []byte(c.build(t, th, icons).View().Content))
				})
			}
		}
	}
}

// TestRailText is the same frames without styling, so the layout reads.
func TestRailText(t *testing.T) {
	for _, icons := range []bool{true, false} {
		for _, c := range railCases() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, c.name), func(t *testing.T) {
				golden.RequireEqual(t, []byte(ansi.Strip(c.build(t, "catppuccin-mocha", icons).View().Content)))
			})
		}
	}
}

func TestRailTakesColumnsFromPanes(t *testing.T) {
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	a.relayout()

	if !a.railVisible() {
		t.Fatal("rail not visible at 100 columns")
	}

	if got, want := a.railRect(), (core.Rect{X: 0, Y: 2, W: 44, H: 28}); got != want {
		t.Errorf("railRect() = %+v, want %+v", got, want)
	}

	if got, want := a.bodyArea(), (core.Rect{X: 44, Y: 2, W: 56, H: 28}); got != want {
		t.Errorf("bodyArea() = %+v, want %+v", got, want)
	}

	for i := range a.store.State().Tabs {
		for id, r := range a.tabRects(i) {
			if r.X < 44 {
				t.Errorf("tab %d pane %s at x=%d, under the rail", i, id, r.X)
			}
		}
	}

	// The active tab holds p5 and p6, side by side across the body.
	rects := a.layoutRects()
	if got := rects["p5"].W + rects["p6"].W; got != 56 {
		t.Errorf("panes of the active tab are %d columns wide with frames, want the body's 56", got)
	}

	st := a.store.State()
	for i := range st.Tabs {
		for id, r := range a.sizesIn(st, i) {
			if r.X < 44 {
				t.Errorf("PTY size rect of %s in tab %d at x=%d, under the rail", id, i, r.X)
			}
		}
	}

	openIn(t, a, core.SetRail{Open: false})

	if got := a.bodyArea(); got != (core.Rect{Y: 2, W: 100, H: 28}) {
		t.Errorf("closed rail: bodyArea() = %+v, want the whole body", got)
	}

	openIn(t, a, core.SetRail{Open: true})
	sized(t, a, 63, 30)

	if a.railVisible() {
		t.Error("rail visible at 63 columns")
	}

	if got := a.bodyArea(); got.X != 0 || got.W != 63 {
		t.Errorf("hidden rail: bodyArea() = %+v, want the whole body", got)
	}
}

func TestRailFramesKeepTheirWidth(t *testing.T) {
	for _, w := range []int{1, 2, 5, 23, 40, 62, 63, 64, 65, 70, 83, 84, 85, 100, 110} {
		for _, h := range []int{1, 2, 3, 4, 5, 8, 11, 12, 13, 20, 30} {
			a := drawApp(t, "catppuccin-mocha", true, w, h, railFixtureAgents())
			a.rail.focused, a.rail.searching, a.rail.query = true, true, "a"
			a.rail.sel = "p3"

			lines := strings.Split(a.View().Content, "\n")
			if len(lines) != h {
				t.Fatalf("%dx%d: %d lines, want %d", w, h, len(lines), h)
			}

			for i, l := range lines {
				if got := lipgloss.Width(l); got != w {
					t.Fatalf("%dx%d line %d: width %d, want %d: %q", w, h, i, got, w, ansi.Strip(l))
				}
			}
		}
	}
}

func TestRailCutRuns(t *testing.T) {
	runs := []railRun{{text: "ab"}, {text: "日本語テキスト"}}

	for w := 0; w <= 16; w++ {
		var sb strings.Builder
		for _, r := range cutRuns(runs, w) {
			sb.WriteString(r.text)
		}

		if got := ansi.StringWidth(sb.String()); got > w {
			t.Errorf("cutRuns(%d) is %d cells wide: %q", w, got, sb.String())
		}
	}

	var sb strings.Builder
	for _, r := range cutRuns(runs, 7) {
		sb.WriteString(r.text)
	}

	if got, want := sb.String(), "ab日本…"; got != want {
		t.Errorf("cutRuns(7) = %q, want %q", got, want)
	}
}

func TestRailColours(t *testing.T) {
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	ui := a.current().theme.UI

	a.rail.sel = "p2"

	vt := lineVT(t, strings.Split(a.View().Content, "\n")[2], 100)
	assertColours(t, vt, 0, 0, "╭", ui.Border, ui.Bg, false)

	a.rail.focused = true
	vt = lineVT(t, strings.Split(a.View().Content, "\n")[2], 100)
	assertColours(t, vt, 0, 0, "╭", ui.Accent, ui.Bg, false)
	assertColours(t, vt, 5, 0, "A", ui.Accent, ui.Bg, true)

	// The first card is p1, blocked, unselected: its bar pulses between Blocked and Border.
	lines := strings.Split(a.View().Content, "\n")
	barY := -1

	for y, l := range lines {
		if strings.Contains(ansi.Strip(l), "▎") {
			barY = y

			break
		}
	}

	if barY < 0 {
		t.Fatal("no blocked bar drawn")
	}

	assertColours(t, lineVT(t, lines[barY], 100), 1, 0, "▎", ui.Blocked, ui.Bg, false)

	a.rail.pulseDim = true
	assertColours(t, lineVT(t, strings.Split(a.View().Content, "\n")[barY], 100), 1, 0, "▎", ui.Border, ui.Bg, false)

	// The selected card's outline is Accent.
	a.rail.sel = "p1"
	assertColours(t, lineVT(t, strings.Split(a.View().Content, "\n")[barY], 100), 1, 0, "│", ui.Accent, ui.Bg, false)
}

func TestRailSearchMatchColour(t *testing.T) {
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	ui := a.current().theme.UI
	a.rail.focused, a.rail.searching, a.rail.query = true, true, "web"
	a.railNormalize(a.store.State())

	lines := strings.Split(a.View().Content, "\n")

	// Search row: below the top border, icon and the text in Fg.
	assertColours(t, lineVT(t, lines[3], 100), 4, 0, "w", ui.Fg, ui.Bg, false)

	for y, l := range lines {
		if x := strings.Index(ansi.Strip(l), "web"); x >= 0 && y > 4 {
			vt := lineVT(t, l, 100)
			assertColours(t, vt, ansi.StringWidth(ansi.Strip(l)[:x]), 0, "w", ui.Match, ui.Bg, true)

			return
		}
	}

	t.Fatal("no card line carries the match")
}

func TestRailCursor(t *testing.T) {
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())

	a.rail.searching, a.rail.query = true, "ab"
	if c := a.cursorView(); c != nil {
		t.Errorf("cursor with the rail unfocused = %+v, want none", c)
	}

	a.rail.focused = true

	c := a.cursorView()
	if c == nil {
		t.Fatal("no cursor while typing in the rail")
	}

	// Content starts at column 2; the icon, a space and the text come before the cursor cell.
	if c.X != 2+2+2 || c.Y != 3 {
		t.Errorf("cursor at (%d, %d), want (6, 3)", c.X, c.Y)
	}

	a.rail.searching = false
	if c := a.cursorView(); c != nil && c.X < 44 {
		t.Errorf("cursor at (%d, %d) in the rail while not typing", c.X, c.Y)
	}

	a.rail.searching = true
	a.overlay = nil
	a.notice = "layout: bsp"
	// The toast sits at the right edge; a rail cursor on the left stays.
	if a.cursorView() == nil {
		t.Error("toast away from the cursor hid it")
	}
}

func TestRailFlashColoursTheWholeFrame(t *testing.T) {
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	ui := a.current().theme.UI
	a.relayout()

	r := a.layoutRects()["p5"]
	a.rail.flashPane, a.rail.flashUntil = "p5", t0.Add(600*time.Millisecond)

	lines := strings.Split(a.View().Content, "\n")
	assertColours(t, lineVT(t, lines[r.Y], 100), r.X, 0, "╭", ui.Match, ui.Bg, false)
	assertColours(t, lineVT(t, lines[r.Y+r.H-1], 100), r.X+r.W-1, 0, "╯", ui.Match, ui.Bg, false)

	// The flash ends with the clock.
	a.clock = &fakeClock{t: t0.Add(600 * time.Millisecond)}
	lines = strings.Split(a.View().Content, "\n")
	assertColours(t, lineVT(t, lines[r.Y], 100), r.X, 0, "╭", ui.Border, ui.Bg, false)
}

func TestRailHeaderCounts(t *testing.T) {
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	row := []rune(ansi.Strip(strings.Split(a.View().Content, "\n")[2]))
	line := string(row[:44])

	for _, want := range []string{" " + iconAgent + " Agents 6 ", iconBlocked + "1", "2", iconIdle + "2"} {
		if !strings.Contains(line, want) {
			t.Errorf("header %q lacks %q", line, want)
		}
	}

	a.icons = false
	line = string([]rune(ansi.Strip(strings.Split(a.View().Content, "\n")[2]))[:44])

	for _, want := range []string{" Agents 6 ", "blocked 1", "idle 2"} {
		if !strings.Contains(line, want) {
			t.Errorf("plain header %q lacks %q", line, want)
		}
	}
}

func TestRailToastOverRailKeepsItsRows(t *testing.T) {
	base := drawApp(t, "catppuccin-mocha", true, 64, 30, railFixtureAgents())
	with := drawApp(t, "catppuccin-mocha", true, 64, 30, railFixtureAgents())
	with.notice = longNotice

	_, x, y, w, ok := toastLayout(64, 30, longNotice, true)
	if !ok {
		t.Fatal("no toast box")
	}

	if x >= with.railRect().W {
		t.Fatalf("toast starts at column %d, right of the rail (%d wide): it does not overlap", x, with.railRect().W)
	}

	// The toast pass re-encodes styles, so the rows are compared by their cells' text.
	was := strings.Split(ansi.Strip(base.View().Content), "\n")
	got := strings.Split(ansi.Strip(with.View().Content), "\n")

	for i := range was {
		if i < y || i >= y+toastRows {
			if was[i] != got[i] {
				t.Errorf("row %d changed outside the toast: %q vs %q", i, was[i], got[i])
			}

			continue
		}

		if l, r := ansi.Cut(was[i], 0, x), ansi.Cut(got[i], 0, x); l != r {
			t.Errorf("row %d left of the toast changed: %q vs %q", i, l, r)
		}

		if l, r := ansi.Cut(was[i], x+w, 64), ansi.Cut(got[i], x+w, 64); l != r {
			t.Errorf("row %d right of the toast changed: %q vs %q", i, l, r)
		}
	}

	if !strings.Contains(got[y+1], "ctrl+space") {
		t.Errorf("toast text row = %q", got[y+1])
	}
}

func TestRailNoHooksCard(t *testing.T) {
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	ui := a.current().theme.UI
	a.rail.sel = "p6"

	lines := strings.Split(a.View().Content, "\n")
	head, card := -1, -1

	for y, l := range lines {
		text := ansi.Strip(l)
		if strings.Contains(text, "No hooks") {
			head = y
		}

		if y >= 3 && strings.Contains(text, "scratch") && card < 0 {
			card = y
		}
	}

	if head < 0 || card < 0 {
		t.Fatalf("No hooks header at %d, card at %d", head, card)
	}

	assertColours(t, lineVT(t, lines[card], 100), 2, 0, iconAgent, ui.Muted, ui.Bg, false)
	assertColours(t, lineVT(t, lines[head], 100), 2, 0, "N", ui.Muted, ui.Bg, true)

	// Outside a repository with no hooks, the three lines under the project are empty.
	for i := 1; i <= 3; i++ {
		if got := strings.Trim(ansi.Strip(lines[card+i])[:44], "│ "); got != "" {
			t.Errorf("line %d of the hook-less card = %q, want empty", i+1, got)
		}
	}

	a.icons = false

	plain := ansi.Strip(strings.Split(a.View().Content, "\n")[card])
	if !strings.HasPrefix(plain, "│   scratch") && !strings.HasPrefix(plain, "││  scratch") {
		t.Errorf("plain hook-less line = %q, want a blank glyph cell before the project", plain)
	}
}

func TestRailHighlightOnCutText(t *testing.T) {
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	ui := a.current().theme.UI
	a.rail.query = "web"

	marked := func(project string, cw int) (hit string, cut bool) {
		c := railCard{pane: "p1", project: project, state: core.AgentIdle}
		hits, _ := c.matches(a.rail.query)
		runs := a.railCardLine(ui, c, hits, 0, cw)

		for _, r := range runs {
			if r.fg == ui.Match && r.bold {
				hit += r.text
			}

			cut = cut || strings.HasSuffix(r.text, "…")
		}

		return hit, cut
	}

	// Wide runes before the match: the offsets are runes, the widths are cells.
	if hit, _ := marked("日本web", 20); hit != "web" {
		t.Errorf("highlight after wide runes = %q, want web", hit)
	}

	// The match lies past the cut: nothing is highlighted and the line ends in the ellipsis.
	if hit, cut := marked("日本語日本語日本語日本語web", 12); hit != "" || !cut {
		t.Errorf("highlight past the cut = %q, cut = %v, want none and a cut", hit, cut)
	}

	// A match that straddles the cut keeps only the shown part.
	const word = "web"

	if hit, _ := marked("日本語日本語web", 17); !strings.HasPrefix(word, hit) {
		t.Errorf("highlight across the cut = %q", hit)
	}
}

func TestRailCardsAreMadePrintableOnce(t *testing.T) {
	agents := railFixtureAgents()[:2]
	agents[1].project, agents[1].branch, agents[1].action = "we\x07b", "ma\x1bin", "Ed\x01it web"
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, agents)
	a.rail.query = "web"

	var web railCard

	for _, c := range a.railShown(a.store.State()) {
		if c.pane == "p2" {
			web = c
		}
	}

	if web.project != "web" || web.branch != "main" || web.action != "Edit web" {
		t.Fatalf("card = %q %q %q, want printable fields", web.project, web.branch, web.action)
	}

	hits, ok := web.matches("web")
	if !ok || len(hitsOf(hits, railFieldProject)) != 1 || len(hitsOf(hits, railFieldAction)) != 1 {
		t.Errorf("hits = %+v, want one in project and one in action", hits)
	}
}

func TestSpinFrame(t *testing.T) {
	frames := []rune(spinFrames)

	for n := range 3 * len(frames) {
		if got, want := spinFrame(n), string(frames[n%len(frames)]); got != want {
			t.Errorf("spinFrame(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRailBottomCounts(t *testing.T) {
	ui := mustTheme(t, "catppuccin-mocha").UI

	tests := []struct {
		name         string
		w            int
		above, below int
		want         string
	}{
		{"none", 12, 0, 0, "╰──────────╯"},
		{"both", 20, 3, 12, "╰─ ↑ 3 ───── 12 ↓ ─╯"},
		{"above only", 12, 3, 0, "╰─ ↑ 3 ────╯"},
		{"below only", 12, 0, 3, "╰──── 3 ↓ ─╯"},
		{"the right count is left out when both do not fit", 10, 3, 3, "╰─ ↑ 3 ──╯"},
		{"neither fits", 6, 3, 3, "╰────╯"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ansi.Strip(railBottom(ui, tt.w, ui.Border, tt.above, tt.below))
			if got != tt.want || ansi.StringWidth(got) != tt.w {
				t.Errorf("railBottom = %q (%d wide), want %q", got, ansi.StringWidth(got), tt.want)
			}
		})
	}
}

// While the rail has the keyboard the tab's focused pane draws as an unfocused
// one: Border frame, Muted title. The jump flash still wins.
func TestRailFocusedPaneDrawsUnfocused(t *testing.T) {
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	ui := a.current().theme.UI
	a.relayout()

	st := a.store.State()
	id := st.Tab().Focus
	r := a.layoutRects()[id]

	kindC := ui.Shell
	if st.Panes[id].Kind == core.KindAgent {
		kindC = ui.Agent
	}

	// The corner is the frame, the cell after "icon " is the first title cell.
	check := func(name string, border, title theme.Hex) {
		t.Helper()

		vt := lineVT(t, strings.Split(a.View().Content, "\n")[r.Y], 100)
		if c := vt.CellAt(r.X, 0); c == nil || hexOf(c.Style.Fg) != string(border) {
			t.Errorf("%s: frame = %+v, want %v", name, c, border)
		}

		if c := vt.CellAt(r.X+5, 0); c == nil || hexOf(c.Style.Fg) != string(title) {
			t.Errorf("%s: title = %+v, want %v", name, c, title)
		}
	}

	check("panes have the keyboard", kindC, kindC)

	a.rail.focused = true

	check("rail has the keyboard", ui.Border, ui.Muted)

	a.rail.flashPane, a.rail.flashUntil = id, t0.Add(600*time.Millisecond)

	check("flashed", ui.Match, ui.Match)
}

// paneColours is the colour of a pane's frame corner and of the first cell of
// its title, as the frame draws them.
func paneColours(t *testing.T, a *App, id core.PaneID) (border, title string) {
	t.Helper()

	r := a.layoutRects()[id]
	vt := lineVT(t, strings.Split(a.View().Content, "\n")[r.Y], a.width)

	b, ti := vt.CellAt(r.X, 0), vt.CellAt(r.X+5, 0)
	if b == nil || ti == nil {
		t.Fatalf("pane %s: no cell at its corner or title", id)
	}

	return hexOf(b.Style.Fg), hexOf(ti.Style.Fg)
}

// frameCells is every cell right of the rail and below the bar, as colours and
// content, leaving out the rect skip (the focused pane's).
func frameCells(t *testing.T, a *App, skip core.Rect) map[[2]int]string {
	t.Helper()

	out := map[[2]int]string{}
	railW := railWidthFor(bodyRect(a.width, a.height))

	for y, line := range strings.Split(a.View().Content, "\n") {
		if y < 2 {
			continue
		}

		vt := lineVT(t, line, a.width)

		for x := railW; x < a.width; x++ {
			if x >= skip.X && x < skip.X+skip.W && y >= skip.Y && y < skip.Y+skip.H {
				continue
			}

			c := vt.CellAt(x, 0)
			if c == nil {
				continue
			}

			fg, bg := "", ""
			if c.Style.Fg != nil {
				fg = hexOf(c.Style.Fg)
			}

			if c.Style.Bg != nil {
				bg = hexOf(c.Style.Bg)
			}

			out[[2]int{x, y}] = c.Content + fg + bg
		}
	}

	return out
}

func TestRailFocusRules(t *testing.T) {
	t.Run("a rail the width rule hides cannot take the colours", func(t *testing.T) {
		a := drawApp(t, "catppuccin-mocha", true, 63, 30, railFixtureAgents())
		ui := a.current().theme.UI
		a.relayout()

		id := a.store.State().Tab().Focus
		kindC := ui.Shell

		if a.store.State().Panes[id].Kind == core.KindAgent {
			kindC = ui.Agent
		}

		a.rail.focused = true

		if a.railShows(true) {
			t.Fatal("setup: the rail shows at 63 columns")
		}

		if border, title := paneColours(t, a, id); border != string(kindC) || title != string(kindC) {
			t.Errorf("focused pane under a hidden rail: frame %s, title %s, want %s", border, title, kindC)
		}
	})

	t.Run("other panes are unchanged while the rail has the keyboard", func(t *testing.T) {
		a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
		a.relayout()

		for tab := range a.store.State().Tabs {
			openIn(t, a, core.SelectTab{Index: tab})
			a.relayout()

			a.rail.focused = false
			skip := a.layoutRects()[a.store.State().Tab().Focus]
			before := frameCells(t, a, skip)

			a.rail.focused = true
			after := frameCells(t, a, skip)

			if len(after) != len(before) || len(before) == 0 {
				t.Fatalf("tab %d: %d cells before, %d after", tab, len(before), len(after))
			}

			for pos, c := range before {
				if after[pos] != c {
					t.Errorf("tab %d: cell %v changed from %q to %q", tab, pos, c, after[pos])

					break
				}
			}
		}
	})

	t.Run("esc in the rail gives the colours back", func(t *testing.T) {
		a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
		ui := a.current().theme.UI
		a.relayout()

		id := a.store.State().Tab().Focus
		kindC := ui.Shell

		if a.store.State().Panes[id].Kind == core.KindAgent {
			kindC = ui.Agent
		}

		pressRun(a, keyLeader, keyRune('a'))

		if !a.rail.focused {
			t.Fatal("setup: leader a did not give the rail the keyboard")
		}

		if border, _ := paneColours(t, a, id); border != string(ui.Border) {
			t.Fatalf("setup: frame %s while the rail has the keyboard, want %s", border, ui.Border)
		}

		pressRun(a, keyEsc)

		if a.rail.focused {
			t.Fatal("esc left the keyboard on the rail")
		}

		if border, title := paneColours(t, a, id); border != string(kindC) || title != string(kindC) {
			t.Errorf("after esc: frame %s, title %s, want %s", border, title, kindC)
		}
	})
}

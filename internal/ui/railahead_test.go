package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
)

func TestRailAheadColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)
	agents := []railAgent{{newTab: true, project: "api", branch: "feat/login", state: core.AgentIdle, ahead: 2}}
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, agents)
	vt := viewVT(t, a)

	// The rail's cards start under the header; row 3 is the first blank row.
	x, y := cellBelow(t, a.View().Content, 3, "↑2")

	assertColours(t, vt, x, y, "↑", ui.Info, ui.Bg, true)
	assertColours(t, vt, x+1, y, "2", ui.Info, ui.Bg, true)

	// It shares the branch's row, at the right end of the card.
	if _, by := cellBelow(t, a.View().Content, 3, "feat/login"); by != y {
		t.Errorf("branch on row %d, ahead count on row %d", by, y)
	}

	// The rail's content ends two columns short of its box: an edge column and the border.
	if want := railWidthFor(bodyRect(a.width, a.height)) - 2; x+2 != want {
		t.Errorf("the count ends before column %d, want %d", x+2, want)
	}
}

func TestRailAheadIsLeftOutWithNothingAhead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		agent railAgent
	}{
		{"none ahead", railAgent{newTab: true, project: "api", branch: "main", state: core.AgentIdle}},
		{"not in a repository", railAgent{newTab: true, project: "api", state: core.AgentIdle, noRepo: true, ahead: 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := drawApp(t, "catppuccin-mocha", true, 100, 30, []railAgent{tt.agent})

			if out := ansi.Strip(a.View().Content); strings.Contains(out, "↑") {
				t.Errorf("the view shows an ahead count:\n%s", out)
			}
		})
	}
}

func TestRailBranchLineAhead(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	ui := a.current().theme.UI

	prefix := "  " + iconGit + " "
	noIcons := "  "

	tests := []struct {
		name   string
		cw     int
		icons  bool
		branch string
		ahead  int
		want   string
	}{
		{"at the right end", 30, true, "feat/login", 2, prefix + "feat/login" + strings.Repeat(" ", 14) + "↑2"},
		{"more digits", 30, true, "feat/login", 123, prefix + "feat/login" + strings.Repeat(" ", 12) + "↑123"},
		{"fits exactly", 17, true, "feat/login", 2, prefix + "feat/login ↑2"},
		{"the branch is cut first", 16, true, "feat/login", 2, prefix + "feat/log… ↑2"},
		{"down to 8 cells", 15, true, "feat/login", 2, prefix + "feat/lo… ↑2"},
		{"then the count goes", 14, true, "feat/login", 2, prefix + "feat/login"},
		{"and the branch is cut to the rail", 11, true, "feat/login", 2, prefix + "feat/l…"},
		{"a short branch is not cut for the count", 11, true, "main", 2, prefix + "main" + " " + "↑2"},
		{"a short branch, then the count goes", 10, true, "main", 2, prefix + "main"},
		{"nothing ahead", 30, true, "feat/login", 0, prefix + "feat/login"},
		{"no icon", 30, false, "feat/login", 2, noIcons + "feat/login" + strings.Repeat(" ", 16) + "↑2"},
		{"no icon, the count goes at the same place", 12, false, "feat/login", 2, noIcons + "feat/login"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.icons = tt.icons

			c := railCard{pane: "p1", state: core.AgentIdle, project: "api", branch: tt.branch, ahead: tt.ahead}
			runs := a.railCardLine(ui, c, nil, 1, tt.cw)

			if got := runsText(runs); got != tt.want {
				t.Errorf("line = %q, want %q", got, tt.want)
			}

			if w := runsWidth(runs); w > tt.cw {
				t.Errorf("line is %d cells wide, want at most %d", w, tt.cw)
			}

			if tt.ahead > 0 && strings.Contains(tt.want, "↑") {
				last := runs[len(runs)-1]
				if last.fg != ui.Info || !last.bold {
					t.Errorf("the count is %v bold %v, want Info bold", last.fg, last.bold)
				}
			}
		})
	}
}

func TestRailAheadKeepsTheSearchHighlight(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	ui := a.current().theme.UI

	c := railCard{pane: "p1", state: core.AgentIdle, project: "api", branch: "feat/login", ahead: 2}

	hits, _ := c.matches("log")
	runs := a.railCardLine(ui, c, hits, 1, 30)

	var matched string

	for _, r := range runs {
		if r.fg == ui.Match {
			matched += r.text
		}
	}

	if matched != "log" {
		t.Errorf("highlighted %q, want log", matched)
	}
}

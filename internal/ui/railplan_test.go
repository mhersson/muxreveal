package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
)

func titleText(a *App, c railCard, cw int) string {
	var b strings.Builder
	for _, r := range a.railTitleLine(a.current().theme.UI, c, nil, cw) {
		b.WriteString(r.text)
	}

	return b.String()
}

func TestRailPlanMark(t *testing.T) {
	t.Parallel()

	base := railCard{
		pane: "p2", state: core.AgentWorking, project: "web", model: "Opus 5.5",
		meter: 41, meterKnown: true, active: t0.Add(-2 * time.Minute), unseen: true, plan: true,
	}

	tests := []struct {
		name  string
		icons bool
		card  func(c *railCard)
		want  string
		mark  bool
	}{
		{name: "meter, age and dot", icons: true, want: "41%  " + iconPlan + "  2m ●", mark: true},
		{name: "icons off", want: "41%  plan  2m ●", mark: true},
		{name: "long name keeps the tail", icons: true, card: func(c *railCard) { c.project = strings.Repeat("n", 60) }, want: iconPlan + "  2m ●", mark: true},
		{name: "read plan", icons: true, card: func(c *railCard) { c.plan = false }, want: "41%  2m ●"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newThemedApp(t, "catppuccin-mocha")
			a.icons = tt.icons
			a.clock = &fakeClock{t: t0}

			c := base
			if tt.card != nil {
				tt.card(&c)
			}

			got := titleText(a, c, 40)
			if !strings.HasSuffix(got, tt.want) {
				t.Errorf("title %q, want it to end in %q", got, tt.want)
			}

			if w := ansi.StringWidth(got); w != 40 {
				t.Errorf("title is %d cells, want 40", w)
			}

			info := a.current().theme.UI.Info

			for _, r := range a.railTitleLine(a.current().theme.UI, c, nil, 40) {
				if r.mark && (r.fg != info || !tt.mark) {
					t.Errorf("mark run %+v, want Info (marked: %v)", r, tt.mark)
				}
			}
		})
	}
}

func TestRailHintsPlan(t *testing.T) {
	t.Parallel()

	a := newThemedApp(t, "catppuccin-mocha")
	ui := a.current().theme.UI

	text := func(cw int) string {
		var b strings.Builder
		for _, r := range railHints(ui, cw, a.keys.rail) {
			b.WriteString(r.text)
		}

		return b.String()
	}

	if got, want := text(40), "j/k move  enter jump  p plan  / search"; got != want {
		t.Errorf("hints at 40 cells %q, want %q", got, want)
	}

	if got, want := text(60), "j/k move  enter jump  p plan  / search  esc back"; got != want {
		t.Errorf("hints at 60 cells %q, want %q", got, want)
	}
}

// railPlanRig is a plan rig with the rail open and p2's plan noted.
func railPlanRig(t *testing.T) *planRig {
	t.Helper()

	r := newPlanRig(t, 100, 30)
	r.filePlan(t, "docs/plans/a.md", "# Title\n\nbody\n")
	openIn(t, r.a, core.SetRail{Open: true})
	r.a.railTake()
	r.a.railSelect("p2")

	return r
}

func TestRailPlanKey(t *testing.T) {
	t.Parallel()

	t.Run("opens the plan and esc returns to the rail", func(t *testing.T) {
		t.Parallel()

		r := railPlanRig(t)

		msgs := planMsgs(press(r.a, keyRune('p')))
		if len(msgs) != 1 || msgs[0].pane != "p2" {
			t.Fatalf("p raised %+v, want one read of p2's plan", msgs)
		}

		r.a.Update(msgs[0])

		if r.unread() {
			t.Error("the plan is still unread while shown")
		}

		press(r.a, keyEsc)

		if !r.a.rail.focused {
			t.Error("esc did not return the keyboard to the rail")
		}
	})

	t.Run("no plan", func(t *testing.T) {
		t.Parallel()

		r := railPlanRig(t)
		chord(r.a, 'c')
		chord(r.a, 'n')
		r.a.railTake()
		r.a.railSelect("p4")

		if msgs := planMsgs(press(r.a, keyRune('p'))); len(msgs) != 0 {
			t.Errorf("p raised %d reads for an agent with no plan", len(msgs))
		}

		if got := r.a.notice; !strings.HasPrefix(got, "no plan from ") || !strings.HasSuffix(got, " yet") {
			t.Errorf("notice %q, want %q", got, "no plan from <card name> yet")
		}
	})

	t.Run("typing in the search", func(t *testing.T) {
		t.Parallel()

		r := railPlanRig(t)
		r.a.railSetSearch(true, "")

		if msgs := planMsgs(press(r.a, keyRune('p'))); len(msgs) != 0 {
			t.Error("p opened a plan while the search types")
		}

		if r.a.rail.query != "p" {
			t.Errorf("query %q, want %q", r.a.rail.query, "p")
		}
	})
}

// markXY is the cell of p2's plan mark.
func markXY(t *testing.T, a *App) (int, int) {
	t.Helper()

	v, ok := a.railViewOf(a.store.State())
	if !ok {
		t.Fatal("the rail is hidden")
	}

	for i, r := range v.rows {
		if r.kind != railRowCard || r.line != 0 || v.shown[r.card].pane != "p2" {
			continue
		}

		m := v.marks[r.card]
		if m.w == 0 {
			t.Fatal("p2 shows no mark")
		}

		return v.geom.box.X + 2 + m.x, v.geom.list.Y + i - v.top
	}

	t.Fatal("no card for p2")

	return 0, 0
}

func leftClick(a *App, x, y int) tea.Cmd {
	_, cmd := a.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})

	return cmd
}

func TestRailPlanClick(t *testing.T) {
	t.Parallel()

	t.Run("on the mark", func(t *testing.T) {
		t.Parallel()

		r := railPlanRig(t)
		r.a.railRelease()
		r.a.rail.sel = ""

		x, y := markXY(t, r.a)

		msgs := planMsgs(leftClick(r.a, x, y))
		if len(msgs) != 1 || msgs[0].pane != "p2" {
			t.Fatalf("click raised %+v, want one read of p2's plan", msgs)
		}

		if r.a.rail.sel != "p2" || !r.a.rail.focused {
			t.Errorf("selected %s, focused %v, want p2 and the rail focused", r.a.rail.sel, r.a.rail.focused)
		}
	})

	t.Run("on the name", func(t *testing.T) {
		t.Parallel()

		r := railPlanRig(t)
		r.a.rail.sel = ""

		_, y := markXY(t, r.a)

		if msgs := planMsgs(leftClick(r.a, 4, y)); len(msgs) != 0 {
			t.Error("a click on the name opened the plan")
		}

		if r.a.rail.sel != "p2" {
			t.Errorf("selected %q, want p2", r.a.rail.sel)
		}
	})

	t.Run("under a modal", func(t *testing.T) {
		t.Parallel()

		r := railPlanRig(t)
		x, y := markXY(t, r.a)
		chord(r.a, ':')

		if msgs := planMsgs(leftClick(r.a, x, y)); len(msgs) != 0 {
			t.Error("a click opened the plan under the palette")
		}
	})
}

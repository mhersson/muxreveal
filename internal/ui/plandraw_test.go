package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
)

func TestPlanIndicator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lines, rows, top int
		want             string
	}{
		{10, 10, 0, " all "},
		{5, 10, 0, " all "},
		{100, 10, 0, " top "},
		{100, 10, 90, " end "},
		{100, 10, 95, " end "},
		{100, 10, 45, " 50% "},
		{101, 10, 1, " 1% "},
		{200, 31, 1, " 1% "},
		{200, 31, 168, " 99% "},
		{200, 31, 84, " 50% "},
	}

	for _, tt := range tests {
		if got := planIndicator(tt.lines, tt.rows, tt.top); got != tt.want {
			t.Errorf("planIndicator(%d, %d, %d) = %q, want %q", tt.lines, tt.rows, tt.top, got, tt.want)
		}
	}
}

func TestPlanHints(t *testing.T) {
	t.Parallel()

	const (
		line    = "j/k line"
		half    = "ctrl+d/u half page"
		heading = "]/[ heading"
		search  = "/ search"
		open    = "enter open"
		closeIt = "esc close"
	)

	tests := []struct {
		width int
		want  []string
	}{
		{100, []string{line, half, heading, search, open, closeIt}},
		{84, []string{line, half, heading, search, open, closeIt}},
		{83, []string{line, half, heading, open, closeIt}},
		{72, []string{line, half, heading, open, closeIt}},
		{71, []string{line, half, open, closeIt}},
		{57, []string{line, half, open, closeIt}},
		{56, []string{line, open, closeIt}},
		{35, []string{line, open, closeIt}},
		{34, []string{line, closeIt}},
		{5, []string{line, closeIt}},
	}

	for _, tt := range tests {
		var got []string
		for _, p := range planHints(tt.width) {
			got = append(got, p[0]+" "+p[1])
		}

		if !slices.Equal(got, tt.want) {
			t.Errorf("planHints(%d) = %q, want %q", tt.width, got, tt.want)
		}
	}
}

func TestPlanRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		h                int
		rows             int
		blanks, hintsRow bool
	}{
		{36, 31, true, true},
		{8, 3, true, true},
		{7, 4, false, true},
		{6, 3, false, true},
		{5, 3, false, false},
		{4, 2, false, false},
		{3, 1, false, false},
	}

	for _, tt := range tests {
		rows, blanks, hint := planRows(tt.h)
		if rows != tt.rows || blanks != tt.blanks || hint != tt.hintsRow {
			t.Errorf("planRows(%d) = %d, %v, %v; want %d, %v, %v", tt.h, rows, blanks, hint, tt.rows, tt.blanks, tt.hintsRow)
		}
	}
}

func TestPlanBox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		width, height int
		x, y, w, h    int
		ok            bool
	}{
		{120, 40, 5, 2, 110, 36, true},
		{80, 24, 2, 2, 76, 20, true},
		{40, 12, 2, 2, 36, 8, true},
		{28, 8, 2, 2, 24, 4, true},
		{27, 12, 0, 0, 0, 0, false},
		{20, 6, 0, 0, 0, 0, false},
		{120, 6, 0, 0, 0, 0, false},
		{120, 5, 0, 0, 0, 0, false},
	}

	for _, tt := range tests {
		x, y, w, h, ok := planBox(tt.width, tt.height)
		if ok != tt.ok || (ok && (x != tt.x || y != tt.y || w != tt.w || h != tt.h)) {
			t.Errorf("planBox(%d, %d) = %d, %d, %d, %d, %v; want %d, %d, %d, %d, %v", tt.width, tt.height, x, y, w, h, ok, tt.x, tt.y, tt.w, tt.h, tt.ok)
		}
	}
}

func TestPlanTitleCut(t *testing.T) {
	t.Parallel()

	topLine := func(content string, y int) string { return strings.Split(ansi.Strip(content), "\n")[y] }

	t.Run("a long path is cut from its start", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		rel := "docs/superpowers/plans/" + strings.Repeat("a-long-folder-name/", 6) + "p.md"
		r.filePlan(t, rel, "# x\n")
		r.open(t)

		line := topLine(r.a.View().Content, 2)
		if !strings.Contains(line, "…") || !strings.Contains(line, "p.md ") || strings.Contains(line, "docs/superpowers") {
			t.Errorf("top border = %q, want the path cut from its start and ending p.md", line)
		}

		if !strings.Contains(line, " plan file · 4m ─╮") {
			t.Errorf("top border = %q, want the tag one dash before the corner", line)
		}
	})

	t.Run("the tag drops when the path would get under 12 cells", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 30, 20)
		r.filePlan(t, "docs/superpowers/plans/p.md", "# x\n")
		r.open(t)

		line := topLine(r.a.View().Content, 2)
		if strings.Contains(line, "plan file") || !strings.Contains(line, "…") || !strings.Contains(line, "p.md ") {
			t.Errorf("top border = %q, want no tag and the cut path", line)
		}

		assertScreen(t, r.a, 30, 20)
	})

	t.Run("control and invisible characters in the path and the name", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		r.a.railEntry("p2").probe = railProbe{known: true, project: "ho\x1b[31mst\x07ile\u200bname"}
		r.filePlan(t, "docs/superpowers/plans/a\x1b[32mb\x07c\u200bd.md", "# x\n")
		r.open(t)

		if r.a.plan == nil {
			t.Fatal("the overlay did not open")
		}

		raw := assertScreen(t, r.a, 120, 40)

		for _, bad := range []string{"\x1b[31m", "\x1b[32m", "\x07", "\u200b"} {
			if strings.Contains(raw, bad) {
				t.Errorf("view holds %q", bad)
			}
		}

		if line := topLine(raw, 2); !strings.Contains(line, "ho[31mstilename") || !strings.Contains(line, "a[32mbcd.md") {
			t.Errorf("top border = %q, want the cleaned name and path", line)
		}
	})
}

func TestPlanOverlaySizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		w, h int
		rows int // 0 for a box that is not drawn
	}{
		{120, 40, 31},
		{80, 24, 15},
		{40, 12, 3},
		{20, 6, 0},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			t.Parallel()

			r := newPlanRig(t, tt.w, tt.h)
			r.filePlan(t, "docs/superpowers/plans/p.md", longPlan(t))
			r.open(t)

			if r.a.plan == nil {
				t.Fatal("the overlay did not open")
			}

			content := assertScreen(t, r.a, tt.w, tt.h)
			lines := strings.Split(ansi.Strip(content), "\n")

			if tt.rows == 0 {
				if _, _, _, _, ok := planBox(tt.w, tt.h); ok {
					t.Fatal("planBox is drawable")
				}

				if text := strings.Join(lines, "\n"); strings.Contains(text, "esc close") || strings.Contains(text, "plan file") {
					t.Errorf("a box that is too small drew:\n%s", text)
				}

				r.keys("j", "G")
				r.keys("esc")

				if r.a.plan != nil {
					t.Error("esc did not close an overlay that is not drawn")
				}

				return
			}

			x, y, w, h, _ := planBox(tt.w, tt.h)
			if got := r.a.plan.view.h; got != tt.rows {
				t.Errorf("doc rows = %d, want %d", got, tt.rows)
			}

			if !strings.HasPrefix(string([]rune(lines[y])[x:]), "╭─") || !strings.HasPrefix(string([]rune(lines[y+h-1])[x:]), "╰") {
				t.Errorf("box corners are not at rows %d and %d:\n%s", y, y+h-1, strings.Join(lines, "\n"))
			}

			if !strings.Contains(lines[y+h-2], "esc close") {
				t.Errorf("hint row %q lacks esc close", lines[y+h-2])
			}

			if row := string([]rune(lines[y+1])[x : x+w]); strings.Trim(row, "│ ") != "" {
				t.Errorf("row below the top border is not blank: %q", row)
			}

			if w != min(110, tt.w-4) || h != tt.h-4 {
				t.Errorf("box is %dx%d", w, h)
			}
		})
	}
}

func TestPlanCommand(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	sized(t, a, 120, 40)

	c, ok := a.reg.get("plan.open")
	if !ok || c.title != "Open the agent's plan" || c.icon != iconPlan {
		t.Fatalf("plan.open = %+v, %v", c, ok)
	}

	a.openCommandPalette()

	found := false

	for _, it := range a.overlay.items {
		found = found || (it.ID == "plan.open" && it.Icon == iconPlan)
	}

	if !found {
		t.Error("the palette does not list plan.open with its icon")
	}

	a.closeOverlay()
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})

	rows := strings.Split(ansi.Strip(a.View().Content), "\n")
	rowOf := func(s string) int {
		for i, l := range rows {
			if strings.Contains(l, s) {
				return i
			}
		}

		return -1
	}

	review, plan := rowOf("Review drawer"), rowOf("Open the agent's plan")
	if review < 0 || plan != review+1 {
		t.Errorf("which-key rows: review drawer at %d, plan at %d; want plan right after it", review, plan)
	}
}

type planScene struct {
	name string
	w, h int
	mode bool
}

func planScenes() []planScene {
	return []planScene{{"plan-file", 120, 40, false}, {"plan-mode", 120, 40, true}, {"plan-80x24", 80, 24, false}}
}

func planSceneApp(t *testing.T, icons bool, sc planScene) *App {
	t.Helper()

	r := newPlanRig(t, sc.w, sc.h)
	r.a.icons = icons

	if sc.mode {
		path := filepath.Join(r.home, ".claude", "plans", "x.md")
		write(t, path, "# Retry the push\n\n## Context\n\nA push that fails on a flaky network should try twice more before it reports an error.\n\n## Steps\n\n1. Wrap `push` in `retry`.\n2. Add a test with a fake remote.\n3. Update the docs.\n")
		r.notePlan(t, core.Plan{Path: path, Kind: core.PlanMode})
	} else {
		r.filePlan(t, "docs/superpowers/plans/p.md", demoPlan(t))
	}

	chord(r.a, 'a')
	r.open(t)

	if r.a.plan == nil {
		t.Fatal("the overlay did not open")
	}

	return r.a
}

// TestPlanOverlayGolden draws each scene once in colour; it compares
// highlighted colours, so it runs serially.
func TestPlanOverlayGolden(t *testing.T) {
	for _, sc := range planScenes() {
		t.Run("catppuccin-mocha/icons=true/"+sc.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(planSceneApp(t, true, sc).View().Content))
		})
	}
}

// TestPlanOverlayText is the same screens without styling, so the layout reads.
func TestPlanOverlayText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, sc := range planScenes() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, sc.name), func(t *testing.T) {
				t.Parallel()
				golden.RequireEqual(t, []byte(ansi.Strip(planSceneApp(t, icons, sc).View().Content)))
			})
		}
	}
}

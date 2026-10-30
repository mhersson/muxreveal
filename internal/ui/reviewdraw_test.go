package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/git/invisible"
)

type reviewCase struct {
	name  string
	scene func(th string, icons bool) reviewScene
}

func reviewCases() []reviewCase {
	base := func(th string, icons bool) reviewScene {
		return reviewScene{theme: th, icons: icons, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	}
	with := func(tune func(s *reviewScene)) func(th string, icons bool) reviewScene {
		return func(th string, icons bool) reviewScene {
			s := base(th, icons)
			tune(&s)

			return s
		}
	}

	return []reviewCase{
		{"unified", with(func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.cursor = rowWith(t, v, "a review pane needs an agent pane")
			}
		})},
		{"split", with(func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.split = true
				v.rebuild(true)
				v.cursor = rowWith(t, v, "p.Kind != core.KindAgent")
			}
		})},
		{"full-file", with(func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.full = map[string]fullText{appGoPath: appGoFull()}
				v.folded = map[string]bool{foldKey(appGoPath, 1): true}
				v.rebuild(false)
				v.cursor = rowWith(t, v, "notice  string")
			}
		})},
		{"narrow-tree-hidden", with(func(s *reviewScene) { s.w, s.h, s.rail = 160, 48, true })},
		{"narrow-tree-open", with(func(s *reviewScene) {
			s.w, s.h, s.rail, s.tree = 160, 48, true, true
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.treeSel = 1
				a.drawer.tree = true
			}
		})},
		{"hidden-characters", with(func(s *reviewScene) {
			s.res, s.turn = hostileResult(), 2
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.cursor = rowWith(t, v, "bidi") }
		})},
		{"binary-and-truncated", with(func(s *reviewScene) { s.res, s.turn = edgeResult(), 3 })},
		{"no-snapshot", with(func(s *reviewScene) { s.res, s.turn, s.noSnap = git.Result{}, 1, true })},
		{"empty", with(func(s *reviewScene) { s.res, s.branch = git.Result{}, true })},
		{"sticky-header", with(func(s *reviewScene) {
			s.h = 24
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.top = rowWith(t, v, "ReviewSource is the agent")
				v.cursor = v.top + 4
			}
		})},
		{"search-prompt", with(func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.search = ptr("review")
				v.cursor = rowWith(t, v, "review.toggle")
			}
		})},
		{"pinned", with(func(s *reviewScene) { s.pinned = true })},
		{"pinned-split", with(func(s *reviewScene) {
			s.pinned = true
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.split = true
				v.rebuild(true)
			}
		})},
		{"pinned-narrow", with(func(s *reviewScene) { s.pinned, s.w, s.h = true, 40, 40 })},
		{"pinned-narrower", with(func(s *reviewScene) { s.pinned, s.w, s.h = true, 28, 40 })},
		{"worktree-removed", with(func(s *reviewScene) {
			s.pinned, s.branch, s.res = true, true, git.Result{}
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				a.getenv = func(string) string { return "/home/me" }
				v.err = errWorktreeRemoved{root: "/home/me/code/muxreveal/.worktrees/b"}
				v.rebuild(false)
			}
		})},
		{"worktree-list", with(func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) { showList(a, wtDemoRows(), "other · main", 0) }
		})},
		{"worktree-list-pinned", with(func(s *reviewScene) {
			s.pinned = true
			s.tune = func(t *testing.T, a *App, v *reviewView) { showList(a, wtDemoRows(), "other · main", 4) }
		})},
		{"worktree-list-narrow", with(func(s *reviewScene) {
			s.w, s.h = 40, 40
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				rows := wtDemoRows()
				rows[2].branch = "feature/follow-the-agents-edits"
				rows[3].branch = "feature/worktree-list"

				showList(a, rows, "other · feature/follow-the-agents-edits", 3)
			}
		})},
		{"unfocused", with(func(s *reviewScene) {
			s.focused = false
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.cursor = rowWith(t, v, "review.toggle") }
		})},
	}
}

// TestReviewGolden draws each case once in colour, in the default theme with icons.
// TestReviewText covers icons off and TestScreensTakeTheirColoursFromTheTheme the other theme.
// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestReviewGolden(t *testing.T) {
	for _, c := range reviewCases() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			a := c.scene("catppuccin-mocha", true).build(t)
			golden.RequireEqual(t, []byte(a.View().Content))
		})
	}
}

// TestReviewText is the same frames without styling, so the layout reads.
func TestReviewText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, c := range reviewCases() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, c.name), func(t *testing.T) {
				t.Parallel()
				a := c.scene("catppuccin-mocha", icons).build(t)
				golden.RequireEqual(t, []byte(ansi.Strip(a.View().Content)))
			})
		}
	}
}

func TestDrawerCoversPanes(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	st := a.store.State()

	r, ok := a.drawerRect(st)
	if !ok {
		t.Fatal("no drawer rect")
	}

	with := strings.Split(ansi.Strip(a.View().Content), "\n")
	drawer := a.drawerLines(st, r)

	openIn(t, a, core.CloseReview{})

	without := strings.Split(ansi.Strip(a.View().Content), "\n")

	if len(with) != len(without) {
		t.Fatalf("frame heights differ: %d with the drawer, %d without", len(with), len(without))
	}

	rects := a.layoutRects()
	master := rects["p1"]

	if master.X+master.W <= r.X {
		t.Fatalf("the master %+v lies clear of the drawer %+v; the scene must put it partly under", master, r)
	}

	if rects["p2"].X < r.X || rects["p3"].X < r.X {
		t.Fatalf("workers %+v, %+v are not under the drawer %+v", rects["p2"], rects["p3"], r)
	}

	for y := range with {
		inDrawer := y >= r.Y && y < r.Y+r.H

		left := ansi.Cut(with[y], 0, r.X)
		if inDrawer && left != ansi.Cut(without[y], 0, r.X) {
			t.Errorf("row %d: cells left of the drawer changed:\n got %q\nwant %q", y, left, ansi.Cut(without[y], 0, r.X))
		}

		if !inDrawer {
			if with[y] != without[y] {
				t.Errorf("row %d outside the drawer changed", y)
			}

			continue
		}

		if got, want := ansi.Cut(with[y], r.X, r.X+r.W), ansi.Strip(drawer[y-r.Y]); got != want {
			t.Errorf("row %d: the drawer's cells hold pane text:\n got %q\nwant %q", y, got, want)
		}
	}
}

func TestDrawerRect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		w, h int
		rail bool
		want core.Rect
		ok   bool
	}{
		{"half of the body", 200, 52, false, core.Rect{X: 100, Y: 2, W: 100, H: 50}, true},
		{"odd body takes the larger half", 121, 30, false, core.Rect{X: 60, Y: 2, W: 61, H: 28}, true},
		{"at least 60 columns", 100, 30, false, core.Rect{X: 40, Y: 2, W: 60, H: 28}, true},
		{"a body under 60 is covered whole", 50, 30, false, core.Rect{X: 0, Y: 2, W: 50, H: 28}, true},
		{"no room for a frame", 2, 30, false, core.Rect{}, false},
		{"no rows for a frame", 100, 4, false, core.Rect{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newThemedApp(t, "catppuccin-mocha")
			sized(t, a, tt.w, tt.h)
			openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/p1", Split: core.SplitVertical})

			if tt.ok {
				openIn(t, a, core.OpenReview{Pane: "p1", Mode: core.ReviewTurn})
			}

			got, ok := a.drawerRect(a.store.State())
			if ok != tt.ok || got != tt.want {
				t.Errorf("drawerRect = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDrawerRectWithRail(t *testing.T) {
	t.Parallel()
	a := drawApp(t, "catppuccin-mocha", true, 200, 40, railFixtureAgents())
	openIn(t, a, core.FocusPane{ID: "p5"}, core.OpenReview{Pane: "p5", Mode: core.ReviewBranch})

	st := a.store.State()
	body := a.bodyAreaIn(st.Rail)

	r, ok := a.drawerRect(st)
	if !ok {
		t.Fatal("no drawer rect")
	}

	if r.X < body.X || r.X+r.W != body.X+body.W || r.W != body.W-body.W/2 {
		t.Errorf("drawerRect = %+v in a body %+v, want the right half of it", r, body)
	}
}

func TestDrawerRectSlides(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4}
	a := s.build(t)
	clk := a.clock.(*fakeClock)
	st := a.store.State()

	a.drawer.slideFrom = t0

	width := func(d time.Duration) int {
		clk.Set(t0.Add(d))

		r, ok := a.drawerRect(st)
		if !ok {
			return 0
		}

		if r.X+r.W != 200 {
			t.Errorf("at %v the drawer %+v does not end at the right edge", d, r)
		}

		return r.W
	}

	if got := width(0); got != 0 {
		t.Errorf("at 0 ms the drawer is %d wide, want it out of sight", got)
	}

	// ease-out cubic: 1 - (1 - 0.5)^3 = 0.875 of 100 columns
	if got := width(60 * time.Millisecond); got != 88 {
		t.Errorf("at 60 ms the drawer is %d wide, want 88", got)
	}

	if got := width(120 * time.Millisecond); got != 100 {
		t.Errorf("at 120 ms the drawer is %d wide, want 100", got)
	}

	if got := width(time.Second); got != 100 {
		t.Errorf("after the slide the drawer is %d wide, want 100", got)
	}

	a.drawer.closing = true

	if got := width(60 * time.Millisecond); got != 13 {
		t.Errorf("closing at 60 ms the drawer is %d wide, want 13", got)
	}

	if got := width(120 * time.Millisecond); got != 0 {
		t.Errorf("closing at 120 ms the drawer is %d wide, want 0", got)
	}

	// A slide frame draws the left part of the full-width drawer: the content slides, it does not squeeze.
	a.drawer.closing = false

	clk.Set(t0.Add(60 * time.Millisecond))

	r, _ := a.drawerRect(st)
	full, _ := a.drawerFull(st)
	got := a.drawerLines(st, r)

	if len(got) != r.H {
		t.Fatalf("slide frame has %d rows, want %d", len(got), r.H)
	}

	fullLines := a.drawerLines(st, full)
	if ansi.Strip(got[1]) != ansi.Strip(ansi.Truncate(fullLines[1], r.W, "")) {
		t.Errorf("slide row = %q, want the full row cut to %d cells", ansi.Strip(got[1]), r.W)
	}
}

type rowWidthVariant struct {
	name string
	tune func(t *testing.T, a *App, v *reviewView, long longLines)
}

func rowWidthVariants() []rowWidthVariant {
	return []rowWidthVariant{
		{"unified", func(t *testing.T, a *App, v *reviewView, long longLines) {}},
		{"split", func(t *testing.T, a *App, v *reviewView, long longLines) { v.split = true }},
		{"full file", func(t *testing.T, a *App, v *reviewView, long longLines) {
			v.full = map[string]fullText{}
			for _, f := range v.res.Files {
				v.full[f.Path] = hostileFullOf(long)
			}

			v.rebuild(v.split)
		}},
		{"tree open", func(t *testing.T, a *App, v *reviewView, long longLines) {
			openIn(t, a, core.SetReviewTree{Open: true})
			a.drawer.tree = true
		}},
		{"search", func(t *testing.T, a *App, v *reviewView, long longLines) { v.search = ptr("e\x1bv") }},
	}
}

func rowWidths(icons bool) []int {
	step := 3
	if !icons {
		step = 11
	}

	var widths []int
	for w := 40; w <= 120; w += step {
		widths = append(widths, w)
	}

	return widths
}

// rowWidthApp builds the scene once at the first width; the views are then
// drawn at every width, so a timing measures the resizes and frames, not the setup.
func rowWidthApp(t *testing.T, icons bool, vr rowWidthVariant, long longLines) *App {
	t.Helper()

	s := reviewScene{theme: "catppuccin-mocha", icons: icons, w: rowWidths(icons)[0], h: 30, res: hostileResultOf(long), turn: 2, turns: 3, focused: true}
	s.tune = func(t *testing.T, a *App, v *reviewView) {
		vr.tune(t, a, v, long)
		v.cursor = min(4, len(v.rows)-1)
	}

	return s.build(t)
}

// resizeDrawer is one step of the drawer: the update and both frames.
func resizeDrawer(t *testing.T, a *App, w int) (r core.Rect, lines []string, frame string, took time.Duration) {
	t.Helper()

	start := time.Now()

	sized(t, a, w, 30)
	settleDrawer(t, a)

	st := a.store.State()

	r, ok := a.drawerRect(st)
	if !ok {
		t.Fatal("no drawer")
	}

	lines = a.drawerLines(st, r)
	frame = a.View().Content

	return r, lines, frame, time.Since(start)
}

func TestDrawerRowsAreExactWidth(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, vr := range rowWidthVariants() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, vr.name), func(t *testing.T) {
				t.Parallel()

				a := rowWidthApp(t, icons, vr, megabyteLines())

				for _, w := range rowWidths(icons) {
					t.Run(fmt.Sprintf("width=%d", w), func(t *testing.T) {
						r, lines, frame, _ := resizeDrawer(t, a, w)

						if len(lines) != r.H {
							t.Fatalf("%d lines, want %d", len(lines), r.H)
						}

						for i, l := range lines {
							if got := ansi.StringWidth(l); got != r.W {
								t.Errorf("row %d is %d cells, want %d: %q", i, got, r.W, ansi.Strip(l))
							}

							for _, c := range ansi.Strip(l) {
								if c < 0x20 || (c >= 0x7f && c <= 0x9f) || invisible.Rune(c) {
									t.Errorf("row %d carries %U: %q", i, c, ansi.Strip(l))
								}
							}
						}

						for i, l := range strings.Split(frame, "\n") {
							if got := ansi.StringWidth(l); got != w {
								t.Errorf("frame row %d is %d cells, want %d", i, got, w)
							}
						}
					})
				}
			})
		}
	}
}

func TestHighlightCapPerFrame(t *testing.T) {
	t.Parallel()

	res, full := bigResult(10000)

	for _, whole := range []bool{false, true} {
		t.Run(fmt.Sprintf("full=%v", whole), func(t *testing.T) {
			s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 1000, res: res, turn: 1, turns: 1, focused: true}
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				if whole {
					v.full = map[string]fullText{"big.go": full}
					v.rebuild(false)
				}
			}

			a := newThemedApp(t, s.theme)
			a.icons = s.icons
			a.clock = &fakeClock{t: t0}

			sized(t, a, s.w, s.h)
			openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/p1", Split: core.SplitVertical}, core.OpenReview{Pane: "p1", Mode: core.ReviewTurn})

			v := a.reviewViewOf(a.store.State().Tab().ID)
			v.res, v.gen, v.turns, v.src = res, 1, 1, git.TurnSource{Turn: 1}
			v.rebuild(false)
			s.tune(t, a, v)

			passes, total, more := 0, 0, true

			for more && passes < 100 {
				more = a.drawerAfterUpdate() != nil
				passes++

				if got := a.drawer.tokenised; got > 2000 {
					t.Fatalf("pass %d tokenised %d lines, want at most 2000", passes, got)
				}

				total += a.drawer.tokenised
			}

			if passes < 2 {
				t.Errorf("the whole range was tokenised in %d pass; the cap never applied", passes)
			}

			if total < 2000 || total > len(full.new) {
				t.Errorf("tokenised %d lines in all", total)
			}

			if more {
				t.Fatal("highlighting still asks for a redraw after 100 passes")
			}

			// Nothing is left: the next update must not tokenise or ask again.
			if cmd := a.drawerAfterUpdate(); cmd != nil || a.drawer.tokenised != 0 {
				t.Errorf("an idle pass tokenised %d lines and asked for a redraw: %v", a.drawer.tokenised, cmd != nil)
			}
		})
	}
}

// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestHighlightColoursLines(t *testing.T) {
	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	st := a.store.State()
	r, _ := a.drawerRect(st)
	got := strings.Join(a.drawerLines(st, r), "\n")

	// The keyword "type" of the added struct and the string literal both take their palette colours.
	mocha := a.theme.Palette
	for name, hex := range map[string]string{"keyword": string(mocha.Mauve), "string": string(mocha.Green)} {
		r, g, b := rgbOf(hex)
		if !strings.Contains(got, fmt.Sprintf("38;2;%d;%d;%d", r, g, b)) {
			t.Errorf("no %s colour %s in the drawer", name, hex)
		}
	}
}

func rgbOf(hex string) (r, g, b int) {
	_, _ = fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)

	return r, g, b
}

func TestFreshRowsFlash(t *testing.T) {
	t.Parallel()

	build := func(until time.Duration) string {
		s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
		s.tune = func(t *testing.T, a *App, v *reviewView) {
			v.fresh = map[int]bool{rowWith(t, v, "review.toggle"): true}
			v.flashUntil = t0.Add(until)
		}

		return s.build(t).View().Content
	}

	flashing, quiet := build(600*time.Millisecond), build(-time.Millisecond)
	if flashing == quiet {
		t.Error("a fresh row inside its flash window draws like any other")
	}
}

func TestDrawerTitleAndTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		icons bool
		scene func(s *reviewScene)
		want  []string
		not   []string
	}{
		{"turn tag", false, func(s *reviewScene) {}, []string{"╭─ muxreveal · feat/review-pane ", " turn 4/4 ─╮"}, []string{" split ─"}},
		{"icons", true, func(s *reviewScene) {}, []string{"\U000F02DA turn 4/4 ", "\uF4D2 muxreveal"}, nil},
		{"past turn", false, func(s *reviewScene) { s.turn = 2 }, []string{" turn 2/4 ─╮", "turn 2 of 4"}, []string{", live"}},
		{"latest turn is live", false, func(s *reviewScene) {}, []string{"turn 4 of 4, live"}, nil},
		{"branch", false, func(s *reviewScene) { s.branch = true }, []string{" branch ─╮", "branch vs origin/main"}, []string{"turn 4/4"}},
		{"branch icon", true, func(s *reviewScene) { s.branch = true }, []string{"\U000F062C branch ", "\U000F062C branch vs origin/main"}, nil},
		{"split tag", false, func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.split = true }
		}, []string{" split ─ turn 4/4 ─╮"}, nil},
		{"split tag icon", true, func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.split = true }
		}, []string{"\U000F08DF split ─ \U000F02DA turn 4/4"}, nil},
		{"no snapshot", false, func(s *reviewScene) { s.res, s.turn, s.noSnap = git.Result{}, 1, true }, []string{"turn 1 of 4, no snapshot", "turn 1 has no snapshot", ", and . step through the turns; b shows the branch"}, nil},
		{"empty branch", false, func(s *reviewScene) { s.res, s.branch = git.Result{}, true }, []string{"no changes against origin/main", "0 files"}, nil},
		{"empty turn", false, func(s *reviewScene) { s.res, s.turn = git.Result{Turn: 3}, 3 }, []string{"turn 3 changed nothing"}, nil},
		{"totals", false, func(s *reviewScene) {}, []string{"4 files  +25 -4"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := reviewScene{theme: "catppuccin-mocha", icons: tt.icons, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true, tree: true}
			tt.scene(&s)

			a := s.build(t)
			text := ansi.Strip(a.View().Content)

			for _, w := range tt.want {
				if !strings.Contains(text, w) {
					t.Errorf("the frame lacks %q:\n%s", w, text)
				}
			}

			for _, n := range tt.not {
				if strings.Contains(text, n) {
					t.Errorf("the frame holds %q", n)
				}
			}
		})
	}
}

// Without the tree the mode shows only in the tag, and the diff takes the whole width.
func TestDrawerModeShowsOnlyInTheTagWithoutTheTree(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: false, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	text := ansi.Strip(s.build(t).View().Content)

	for _, want := range []string{" turn 4/4 ─╮"} {
		if !strings.Contains(text, want) {
			t.Errorf("the frame lacks %q:\n%s", want, text)
		}
	}

	for _, not := range []string{"turn 4 of 4, live", "4 files"} {
		if strings.Contains(text, not) {
			t.Errorf("the frame holds %q although the tree is hidden:\n%s", not, text)
		}
	}
}

func TestDrawerKeyHints(t *testing.T) {
	t.Parallel()

	const (
		diffHints = "j/k line   ]/[ hunk   }/{ file   tab tree   w worktree   f full   u split   b/t mode   ,/. turn   e edit   / search"
		treeHints = "j/k file   enter jump   tab hide   esc back"
	)

	build := func(h int, tree, keys bool) string {
		s := reviewScene{theme: "catppuccin-mocha", icons: false, w: 240, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true, tree: tree}
		s.tune = func(t *testing.T, a *App, v *reviewView) { a.drawer.tree = keys }

		return ansi.Strip(s.build(t).View().Content)
	}

	if got := build(52, false, false); !strings.Contains(got, diffHints) {
		t.Errorf("diff hints missing with the tree hidden:\n%s", got)
	}

	if got := build(52, true, false); !strings.Contains(got, diffHints) {
		t.Errorf("diff hints missing with the tree shown and the keyboard in the diff:\n%s", got)
	}

	if got := build(52, true, true); !strings.Contains(got, treeHints) || strings.Contains(got, "hunk") || strings.Contains(got, "tab diff") {
		t.Errorf("tree hints wrong:\n%s", got)
	}

	// An inner height under 12 shows no hints: 12 rows of body plus the frame is 14, plus the bar and the rule 16.
	if got := build(15, false, false); strings.Contains(got, "j/k line") {
		t.Errorf("hints shown with an inner height of 11:\n%s", got)
	}

	if got := build(16, false, false); !strings.Contains(got, "j/k line") {
		t.Errorf("hints missing with an inner height of 12:\n%s", got)
	}
}

// The turn hint shows only while the drawer shows a turn, as the mode tag does:
// a drawer that shows the branch leaves it out so the row holds the hints that
// work, whatever mode core keeps.
func TestDrawerTurnHintShowsOnlyForTurns(t *testing.T) {
	t.Parallel()

	const (
		head     = "j/k line   ]/[ hunk   }/{ file   tab tree   w worktree   f full   u split   b/t mode"
		viaTurn  = head + "   ,/. turn"
		viaEdit  = head + "   e edit"
		wideTurn = viaTurn + "   e edit   / search"
		wideEdit = viaEdit + "   / search"
	)

	// A turn-mode drawer that shows the branch while core keeps turn mode.
	branchInTurnMode := func(session string, turns int, foreign bool) func(s *reviewScene) {
		return func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.src, v.base, v.session, v.turns, v.foreign = git.BranchSource{Worktree: "/w/p1"}, "origin/main", session, turns, foreign
			}
		}
	}

	tests := []struct {
		name  string
		scene func(s *reviewScene)
		turn  bool // the row holds the turn hint
	}{
		{"branch mode", func(s *reviewScene) { s.branch = true }, false},
		{"turn mode on the agent's worktree", func(s *reviewScene) {}, true},
		{"turn mode on another worktree", branchInTurnMode("s", 0, true), false},
		{"turn mode with no session", branchInTurnMode("", 0, false), false},
		{"turn mode with no turns yet", branchInTurnMode("s", 0, false), false},
	}

	widths := []struct {
		name       string
		w          int
		turn, edit string // the end of the row with and without the turn hint
	}{
		{"wide", 240, wideTurn, wideEdit},
		{"usual width", 200, viaTurn, viaEdit},
	}

	for _, tt := range tests {
		for _, wd := range widths {
			t.Run(tt.name+", "+wd.name, func(t *testing.T) {
				t.Parallel()

				s := reviewScene{theme: "catppuccin-mocha", icons: false, w: wd.w, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
				tt.scene(&s)

				var row string

				for l := range strings.SplitSeq(ansi.Strip(s.build(t).View().Content), "\n") {
					if strings.Contains(l, "j/k line") {
						row = strings.TrimRight(strings.TrimRight(l, " │"), " ")
					}
				}

				want := wd.edit
				if tt.turn {
					want = wd.turn
				}

				if !strings.HasSuffix(row, want) {
					t.Errorf("the hint row is\n%q\nwant it to end with\n%q", row, want)
				}
			})
		}
	}
}

func TestDrawerSplitFallsBackWhenNarrow(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: false, w: 124, h: 40, res: turn4Result(), turn: 4, turns: 4, focused: true}
	s.tune = func(t *testing.T, a *App, v *reviewView) { v.split = true }
	a := s.build(t)

	v := a.reviewViewOf(a.store.State().Tab().ID)
	if v.rowsSplit {
		t.Error("rows are side by side in a drawer too narrow for 24 columns of code per side")
	}

	if strings.Contains(ansi.Strip(a.View().Content), "split ─") {
		t.Error("the split tag shows although unified is drawn")
	}

	if !v.split {
		t.Error("the choice of split was dropped; a wider window must bring it back")
	}

	sized(t, a, 200, 40)
	settleDrawer(t, a)

	if !v.rowsSplit {
		t.Error("rows stayed unified after the window widened")
	}
}

func TestDrawerFocusedPaneDrawsUnfocused(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4}
	s.focused = false

	keysOnPane := s.build(t)

	s.focused = true
	keysOnDrawer := s.build(t)

	rects := keysOnPane.layoutRects()
	row := func(a *App) string {
		lines := strings.Split(a.View().Content, "\n")

		return ansi.Cut(lines[rects["p1"].Y], 0, rects["p1"].X+rects["p1"].W/2)
	}

	if row(keysOnPane) == row(keysOnDrawer) {
		t.Error("the master's top border draws the same with the keyboard on it and on the drawer")
	}
}

func TestDrawerHidesPaneCursor(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	if cur := a.View().Cursor; cur != nil {
		t.Errorf("cursor %+v shown while the drawer has the keyboard", cur)
	}
}

func TestDrawerGhostsAndFolds(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	s.tune = func(t *testing.T, a *App, v *reviewView) {
		v.full = map[string]fullText{appGoPath: appGoFull()}
		v.folded = map[string]bool{foldKey(appGoPath, 1): true}
		v.rebuild(false)
	}

	text := ansi.Strip(s.build(t).View().Content)

	for _, want := range []string{"\U000F165D 2 removed lines", "enter shows them", "\U000F09EE full", "notice  string"} {
		if !strings.Contains(text, want) {
			t.Errorf("the full view lacks %q:\n%s", want, text)
		}
	}

	if strings.Contains(text, "return nil") {
		t.Errorf("a folded block still shows its lines:\n%s", text)
	}
}

func TestDrawerGhostLinesCarryNoNumber(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	s.tune = func(t *testing.T, a *App, v *reviewView) {
		v.full = map[string]fullText{appGoPath: appGoFull()}
		v.rebuild(false)
	}

	lines := strings.Split(ansi.Strip(s.build(t).View().Content), "\n")

	// before is the screen row that holds code, up to where the code starts.
	before := func(code string) string {
		t.Helper()

		var found []string

		for _, l := range lines {
			if i := strings.Index(l, code); i >= 0 {
				found = append(found, l[:i])
			}
		}

		if len(found) != 1 {
			t.Fatalf("%d screen rows hold %q, want 1:\n%s", len(found), code, strings.Join(lines, "\n"))
		}

		return found[0]
	}

	isNumber := func(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

	// The removed line "notice  string" is old line 6; the kept line "rail    rail" is new line 8.
	ghost, kept := before("notice  string"), before("rail    rail")

	fields := strings.Fields(ghost)
	if n := len(fields); n < 2 || fields[n-1] != "-" || isNumber(fields[n-2]) {
		t.Errorf("a ghost row reads %q before its code, want no number before the sign", fields[max(len(fields)-2, 0):])
	}

	if gw, kw := ansi.StringWidth(ghost), ansi.StringWidth(kept); gw != kw {
		t.Errorf("a ghost row's code starts at column %d, a kept row's at %d, want them aligned", gw, kw)
	}

	if fields := strings.Fields(kept); len(fields) == 0 || fields[len(fields)-1] != "8" {
		t.Errorf("the line after the removed one reads %q before its code, want its new number 8", fields[max(len(fields)-1, 0):])
	}
}

func TestDrawerTreeLayout(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: false, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true, tree: true}
	text := ansi.Strip(s.build(t).View().Content)

	for _, want := range []string{"internal/", "core/", "state.go", "A review_test.go", "R keymap.go", "! +11 -0"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tree lacks %q:\n%s", want, text)
		}
	}
}

func crResult(specs ...string) git.Result {
	f := fileSpec("eol.txt", git.Modified, hunkSpec(1, 1, "", specs...))

	return git.Result{Turn: 4, Files: []git.FileDiff{f}}
}

func TestLineEndingCR(t *testing.T) {
	t.Parallel()

	const tag = "\u27e8U+000D\u27e9"

	tests := []struct {
		name    string
		specs   []string
		split   bool
		tags    int
		flagged bool
	}{
		{"crlf content change shows nothing", []string{"-old\r", "+new\r", " same\r"}, false, 0, false},
		{"crlf content change in split", []string{"-old\r", "+new\r"}, true, 0, false},
		{"lf to crlf is tagged", []string{"-same", "+same\r"}, false, 1, false},
		{"crlf to lf is tagged", []string{"-same\r", "+same"}, false, 1, false},
		{"lf to crlf in split", []string{"-same", "+same\r"}, true, 1, false},
		{"mid-line cr is tagged and flags", []string{"+a\rb"}, false, 1, true},
		{"cr and an edit is not an ending change", []string{"-a\r", "+b"}, false, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := crResult(tt.specs...)
			s := reviewScene{theme: "catppuccin-mocha", w: 200, h: 52, res: res, turn: 4, turns: 4, focused: true}
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.split = tt.split }

			text := ansi.Strip(s.build(t).View().Content)
			if got := strings.Count(text, tag); got != tt.tags {
				t.Errorf("%d CR tags drawn, want %d:\n%s", got, tt.tags, text)
			}

			if got := fileHidden(res.Files[0]); got != tt.flagged {
				t.Errorf("fileHidden = %v, want %v", got, tt.flagged)
			}
		})
	}
}

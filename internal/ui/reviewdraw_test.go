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
			s.w, s.h, s.rail = 160, 48, true
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.treeOpen, v.treeSel = true, 1
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
		{"unfocused", with(func(s *reviewScene) {
			s.focused = false
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.cursor = rowWith(t, v, "review.toggle") }
		})},
	}
}

func TestReviewGolden(t *testing.T) {
	for _, th := range chromeThemes() {
		for _, icons := range []bool{true, false} {
			for _, c := range reviewCases() {
				t.Run(fmt.Sprintf("%s/icons=%v/%s", th, icons, c.name), func(t *testing.T) {
					a := c.scene(th, icons).build(t)
					golden.RequireEqual(t, []byte(a.View().Content))
				})
			}
		}
	}
}

// TestReviewText is the same frames without styling, so the layout reads.
func TestReviewText(t *testing.T) {
	for _, icons := range []bool{true, false} {
		for _, c := range reviewCases() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, c.name), func(t *testing.T) {
				a := c.scene("catppuccin-mocha", icons).build(t)
				golden.RequireEqual(t, []byte(ansi.Strip(a.View().Content)))
			})
		}
	}
}

func TestDrawerCoversPanes(t *testing.T) {
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

func TestDrawerRowsAreExactWidth(t *testing.T) {
	type variant struct {
		name string
		tune func(a *App, v *reviewView)
	}

	variants := []variant{
		{"unified", func(a *App, v *reviewView) {}},
		{"split", func(a *App, v *reviewView) { v.split = true }},
		{"full file", func(a *App, v *reviewView) {
			v.full = map[string]fullText{}
			for _, f := range v.res.Files {
				v.full[f.Path] = hostileFull()
			}

			v.rebuild(v.split)
		}},
		{"tree open", func(a *App, v *reviewView) { v.treeOpen = true; a.drawer.tree = true }},
		{"search", func(a *App, v *reviewView) { v.search = ptr("e\x1bv") }},
	}

	for _, icons := range []bool{true, false} {
		for _, vr := range variants {
			step := 3
			if !icons {
				step = 11
			}

			for w := 40; w <= 120; w += step {
				name := fmt.Sprintf("icons=%v/%s/width=%d", icons, vr.name, w)
				t.Run(name, func(t *testing.T) {
					s := reviewScene{theme: "catppuccin-mocha", icons: icons, w: w, h: 30, res: hostileResult(), turn: 2, turns: 3, focused: true}
					s.tune = func(t *testing.T, a *App, v *reviewView) {
						vr.tune(a, v)
						v.cursor = min(4, len(v.rows)-1)
					}
					a := s.build(t)

					st := a.store.State()

					r, ok := a.drawerRect(st)
					if !ok {
						t.Fatal("no drawer")
					}

					lines := a.drawerLines(st, r)
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

					for i, l := range strings.Split(a.View().Content, "\n") {
						if got := ansi.StringWidth(l); got != w {
							t.Errorf("frame row %d is %d cells, want %d", i, got, w)
						}
					}
				})
			}
		}
	}
}

func TestHighlightCapPerFrame(t *testing.T) {
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
		{"range of turns", false, func(s *reviewScene) { s.turn, s.through = 3, 4 }, []string{"turns 3-4 of 4"}, nil},
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
			s := reviewScene{theme: "catppuccin-mocha", icons: tt.icons, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
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

func TestDrawerKeyHints(t *testing.T) {
	build := func(h int, tree bool) string {
		s := reviewScene{theme: "catppuccin-mocha", icons: false, w: 240, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true}
		s.tune = func(t *testing.T, a *App, v *reviewView) { a.drawer.tree = tree }

		return ansi.Strip(s.build(t).View().Content)
	}

	if got := build(52, false); !strings.Contains(got, "j/k line   ]/[ hunk   }/{ file   tab tree   f full   u split   b/t mode   ,/. turn   e edit   / search") {
		t.Errorf("diff hints missing:\n%s", got)
	}

	if got := build(52, true); !strings.Contains(got, "j/k file   enter open   tab diff") || strings.Contains(got, "hunk") {
		t.Errorf("tree hints wrong:\n%s", got)
	}

	// An inner height under 12 shows no hints: 12 rows of body plus the frame is 14, plus the bar and the rule 16.
	if got := build(15, false); strings.Contains(got, "j/k line") {
		t.Errorf("hints shown with an inner height of 11:\n%s", got)
	}

	if got := build(16, false); !strings.Contains(got, "j/k line") {
		t.Errorf("hints missing with an inner height of 12:\n%s", got)
	}
}

func TestDrawerSplitFallsBackWhenNarrow(t *testing.T) {
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
	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	if cur := a.View().Cursor; cur != nil {
		t.Errorf("cursor %+v shown while the drawer has the keyboard", cur)
	}
}

func TestDrawerGhostsAndFolds(t *testing.T) {
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

func TestDrawerTreeLayout(t *testing.T) {
	s := reviewScene{theme: "catppuccin-mocha", icons: false, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	text := ansi.Strip(s.build(t).View().Content)

	for _, want := range []string{"internal/", "core/", "state.go", "A review_test.go", "R keymap.go", "! +11 -0"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tree lacks %q:\n%s", want, text)
		}
	}
}

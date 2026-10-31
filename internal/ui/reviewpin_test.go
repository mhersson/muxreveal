package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
)

// topLine is the drawer's top border as drawn, with its styling.
func topLine(t *testing.T, a *App) string {
	t.Helper()

	st := a.store.State()

	return a.drawerLines(st, drawerRectOf(t, a, 0))[0]
}

func TestPinnedTagStyleAndOrder(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true, pinned: true}
	s.tune = func(t *testing.T, a *App, v *reviewView) {
		v.split = true
		v.rebuild(true)
	}
	a := s.build(t)

	line := topLine(t, a)
	plain := ansi.Strip(line)

	split, pinned, mode := strings.Index(plain, "split"), strings.Index(plain, "pinned"), strings.Index(plain, "turn 4/4")
	if split < 0 || pinned < split || mode < pinned {
		t.Fatalf("tags read %q, want split, pinned, then the mode tag from the left", plain)
	}

	ui := a.current().theme.UI
	vt := lineVT(t, line, ansi.StringWidth(line))
	x := ansi.StringWidth(plain[:pinned])

	assertColours(t, vt, x-2, 0, iconPin, ui.Accent, ui.Bg, true)
	assertColours(t, vt, x, 0, "p", ui.Accent, ui.Bg, true)
	assertColours(t, vt, x+5, 0, "d", ui.Accent, ui.Bg, true)
}

func TestPinnedTagIconsOff(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true, pinned: true}
	plain := ansi.Strip(topLine(t, s.build(t)))

	if !strings.Contains(plain, " pinned ") || strings.Contains(plain, iconPin) {
		t.Errorf("top border %q, want the pinned tag without its icon", plain)
	}
}

// No tag shows for a drawer that follows, and a pin on another pane is not this drawer's.
func TestPinnedTagOnlyWhenPinned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pin  func(t *testing.T, a *App)
		want bool
	}{
		{"following", func(t *testing.T, a *App) {}, false},
		{"pinned", func(t *testing.T, a *App) { openIn(t, a, core.PinReview{Root: "/w/p1"}) }, true},
		{"unpinned again", func(t *testing.T, a *App) {
			openIn(t, a, core.PinReview{Root: "/w/p1"}, core.PinReview{})
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
			a := s.build(t)

			// Draw once so a stale cached frame would show.
			topLine(t, a)
			tt.pin(t, a)

			if got := strings.Contains(ansi.Strip(topLine(t, a)), "pinned"); got != tt.want {
				t.Errorf("pinned tag shown = %v, want %v", got, tt.want)
			}
		})
	}
}

// The split tag drops first when room runs short, then the pinned tag; the mode
// tag stays, and the title shows while it has room.
func TestTopBorderTagDrops(t *testing.T) {
	t.Parallel()

	tests := []struct {
		width                       int
		split, pinned, mode, titled bool
	}{
		{40, true, true, true, true},
		{36, true, true, true, true},
		{35, false, true, true, true},
		{28, false, true, true, true},
		{27, false, false, true, true},
		{19, false, false, true, true},
		{18, false, false, true, false},
	}

	s := reviewScene{theme: "catppuccin-mocha", w: 200, h: 52, branch: true, focused: true, pinned: true}
	a := s.build(t)

	for _, tt := range tests {
		d, _, ok := a.drawCtxFor(a.store.State())
		if !ok {
			t.Fatal("no drawer to draw")
		}

		// Side by side is on whatever the width, so the drop order is what runs.
		d.lay.split, d.lay.box.W, d.v.rowsSplit = true, tt.width, true

		line := ansi.Strip(d.topBorder())
		got := []bool{
			strings.Contains(line, "split"), strings.Contains(line, "pinned"),
			strings.Contains(line, "branch"), strings.Contains(line, "mux"),
		}

		if want := []bool{tt.split, tt.pinned, tt.mode, tt.titled}; !equalBools(got, want) {
			t.Errorf("width %d: %q shows split, pinned, mode, title = %v, want %v", tt.width, line, got, want)
		}

		if w := ansi.StringWidth(line); w != tt.width {
			t.Errorf("width %d: the border is %d cells", tt.width, w)
		}
	}
}

func equalBools(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func TestWorktreeRemovedRow(t *testing.T) {
	t.Parallel()

	const root = "/home/me/code/muxreveal/.worktrees/b"

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, branch: true, focused: true, pinned: true}
	s.tune = func(t *testing.T, a *App, v *reviewView) {
		a.getenv = func(string) string { return "/home/me" }
		v.err = errWorktreeRemoved{root: root}
		v.res = git.Result{}
		v.rebuild(false)
	}
	a := s.build(t)

	lines := a.drawerLines(a.store.State(), drawerRectOf(t, a, 0))
	want := "worktree removed: ~/code/muxreveal/.worktrees/b"

	for _, line := range lines {
		plain := ansi.Strip(line)

		before, _, ok := strings.Cut(plain, want)
		if !ok {
			continue
		}

		ui := a.current().theme.UI
		vt := lineVT(t, line, ansi.StringWidth(line))
		x := ansi.StringWidth(before)

		assertColours(t, vt, x, 0, "w", ui.Muted, ui.Bg, false)
		assertColours(t, vt, x+len(want)-1, 0, "b", ui.Muted, ui.Bg, false)

		if title := ansi.Strip(lines[0]); !strings.Contains(title, "muxreveal · feat/review-pane") {
			t.Errorf("title %q lost the project and branch", title)
		}

		return
	}

	t.Fatalf("no row reads %q:\n%s", want, ansi.Strip(strings.Join(lines, "\n")))
}

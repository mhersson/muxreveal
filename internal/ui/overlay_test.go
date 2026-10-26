package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/ui/palette"
)

var (
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
)

// fastApp is a test app whose which-key tick fires at once, so run does not wait on it.
func fastApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	a.keymap.delay = 0
	return a
}

// run executes cmd and feeds every message it produces back into a, so a
// palette's follow-up messages (highlight, chosen, closed) take effect.
func run(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			run(a, c)
		}
	case tea.QuitMsg:
	default:
		_, next := a.Update(msg)
		run(a, next)
	}
}

// pressRun sends keys one by one, running the resulting commands.
func pressRun(a *App, keys ...tea.KeyPressMsg) {
	for _, k := range keys {
		_, cmd := a.Update(k)
		run(a, cmd)
	}
}

func typeText(a *App, s string) {
	for _, r := range s {
		pressRun(a, keyRune(r))
	}
}

func TestPaletteListsEveryCommand(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	if a.overlay == nil {
		t.Fatal("overlay not open after leader :")
	}
	var want []string
	for _, c := range a.reg.all() {
		want = append(want, c.id)
	}
	view := ansi.Strip(a.View().Content)
	// The list scrolls past one page; the first page shows in registration order.
	for _, c := range a.reg.all()[:min(len(want), palette.MaxRows)] {
		if !strings.Contains(view, c.title) {
			t.Errorf("palette view lacks command %q (%s)", c.title, c.id)
		}
	}
	var got []string
	for _, it := range a.overlay.items {
		got = append(got, it.ID)
	}
	if !slices.Equal(got, want) {
		t.Errorf("palette ids = %v, want %v", got, want)
	}
	if !strings.Contains(view, "ctrl+space n") {
		t.Errorf("palette lacks the new agent hint:\n%s", view)
	}
}

func TestPaletteRunsCommand(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	typeText(a, "macchiato")
	pressRun(a, keyEnter)
	if a.overlay != nil {
		t.Error("overlay still open after enter")
	}
	if got := a.store.State().Theme; got != "catppuccin-macchiato" {
		t.Errorf("theme = %q, want catppuccin-macchiato", got)
	}
}

func TestPaletteOpensThemePicker(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	typeText(a, "pick")
	pressRun(a, keyEnter)
	if a.overlay == nil || a.overlay.kind != overlayThemes {
		t.Fatalf("overlay = %+v, want the theme picker", a.overlay)
	}
}

func TestThemePickerCommits(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune('t'), keyDown, keyEnter)
	latte := mustTheme(t, "catppuccin-latte")
	if got := a.store.State().Theme; got != latte.Name {
		t.Errorf("theme = %q, want %q", got, latte.Name)
	}
	if a.overlay != nil || a.preview != nil {
		t.Error("overlay or preview still set after enter")
	}
	if got := a.View().BackgroundColor; got != latte.UI.Bg {
		t.Errorf("background = %v, want %v", got, latte.UI.Bg)
	}
}

func TestThemePickerEscRestores(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	mocha := mustTheme(t, "catppuccin-mocha")
	pressRun(a, keyLeader, keyRune('t'))
	for _, name := range []string{"catppuccin-latte", "catppuccin-frappe", "catppuccin-macchiato"} {
		pressRun(a, keyDown)
		th := mustTheme(t, name)
		if got := a.View().BackgroundColor; got != th.UI.Bg {
			t.Errorf("after moving to %s: background = %v, want %v", name, got, th.UI.Bg)
		}
		if got := a.store.State().Theme; got != mocha.Name {
			t.Errorf("after moving to %s: store theme = %q, want mocha", name, got)
		}
		if !strings.Contains(ansi.Strip(a.View().Content), mocha.Title) {
			t.Errorf("after moving to %s: status bar lacks the committed title %q", name, mocha.Title)
		}
	}
	pressRun(a, keyEsc)
	if a.overlay != nil || a.preview != nil {
		t.Error("overlay or preview still set after esc")
	}
	v := a.View()
	if v.BackgroundColor != mocha.UI.Bg {
		t.Errorf("background after esc = %v, want %v", v.BackgroundColor, mocha.UI.Bg)
	}
	fresh := fastApp(t)
	sized(t, fresh, 100, 30)
	if v.Content != fresh.View().Content {
		t.Error("view after esc differs from a never-previewed shell")
	}
}

func TestOverlayOwnsKeys(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	cmd := press(a, keyLeader, keyRune('q'))
	if a.pending {
		t.Error("leader started while the overlay is open")
	}
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Error("q quit the app instead of typing into the query")
		}
	}
	if view := ansi.Strip(a.View().Content); !strings.Contains(view, "> q") {
		t.Errorf("query does not show q:\n%s", view)
	}
}

func TestOverlayEndsLeaderPopup(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	press(a, keyLeader)
	seq := a.seq
	pressRun(a, keyRune(':'))
	a.Update(whichKeyMsg{seq: seq})
	if a.popup {
		t.Error("stale which-key tick opened the popup over the overlay")
	}
}

func TestOverlaySizes(t *testing.T) {
	for _, key := range []rune{':', 't'} {
		for _, sz := range [][2]int{{100, 30}, {30, 8}, {5, 3}, {1, 1}, {2, 2}} {
			w, h := sz[0], sz[1]
			t.Run(fmt.Sprintf("%c/%dx%d", key, w, h), func(t *testing.T) {
				a := fastApp(t)
				sized(t, a, w, h)
				pressRun(a, keyLeader, keyRune(key))
				lines := strings.Split(a.View().Content, "\n")
				if len(lines) != h {
					t.Fatalf("view has %d lines, want %d", len(lines), h)
				}
				for i, l := range lines {
					if got := lipgloss.Width(l); got != w {
						t.Errorf("line %d width = %d, want %d", i, got, w)
					}
				}
			})
		}
	}
}

func TestPaletteOverlayGolden(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	golden.RequireEqual(t, []byte(a.View().Content))
}

func TestThemePickerGolden(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune('t'), keyDown)
	golden.RequireEqual(t, []byte(a.View().Content))
}

func TestLateHighlightIgnored(t *testing.T) {
	latte := palette.HighlightMsg{ID: "catppuccin-latte"}
	mocha := mustTheme(t, "catppuccin-mocha")
	frappe := mustTheme(t, "catppuccin-frappe")

	t.Run("after esc", func(t *testing.T) {
		a := fastApp(t)
		sized(t, a, 100, 30)
		pressRun(a, keyLeader, keyRune('t'), keyEsc)
		a.Update(latte)
		if got := a.View().BackgroundColor; got != mocha.UI.Bg {
			t.Errorf("background = %v, want mocha %v", got, mocha.UI.Bg)
		}
	})
	t.Run("after enter", func(t *testing.T) {
		a := fastApp(t)
		sized(t, a, 100, 30)
		pressRun(a, keyLeader, keyRune('t'), keyDown, keyDown, keyEnter)
		a.Update(latte)
		if got := a.View().BackgroundColor; got != frappe.UI.Bg {
			t.Errorf("background = %v, want frappe %v", got, frappe.UI.Bg)
		}
	})
	t.Run("stale id while open", func(t *testing.T) {
		a := fastApp(t)
		sized(t, a, 100, 30)
		pressRun(a, keyLeader, keyRune('t'), keyDown, keyDown)
		a.Update(latte)
		if got := a.View().BackgroundColor; got != frappe.UI.Bg {
			t.Errorf("background = %v, want frappe %v", got, frappe.UI.Bg)
		}
	})
}

func TestPasteReachesPalette(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	_, cmd := a.Update(tea.PasteMsg{Content: "quit"})
	run(a, cmd)
	view := ansi.Strip(a.View().Content)
	if !strings.Contains(view, "> quit") || strings.Contains(view, "Theme: Catppuccin") {
		t.Errorf("paste did not filter the palette:\n%s", view)
	}
}

func TestLateChosenIgnored(t *testing.T) {
	a := fastApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	chosen := palette.ChosenMsg{ID: "theme.pick"}
	_, cmd := a.Update(chosen)
	run(a, cmd)
	if a.overlay == nil || a.overlay.kind != overlayThemes {
		t.Fatalf("overlay = %+v, want the theme picker", a.overlay)
	}
	_, cmd = a.Update(chosen)
	run(a, cmd)
	if a.overlay == nil || a.overlay.kind != overlayThemes {
		t.Errorf("late chosen closed or replaced the picker: %+v", a.overlay)
	}
	if got := a.store.State().Theme; got != "catppuccin-mocha" {
		t.Errorf("store theme = %q, want catppuccin-mocha", got)
	}
}

func TestOverlayBeforeFirstSize(t *testing.T) {
	a := fastApp(t)
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})
	pressRun(a, keyRune(':'))
	v := a.View()
	if v.Content != "" || !v.AltScreen {
		t.Errorf("unsized view content = %q, AltScreen = %v, want empty and true", v.Content, v.AltScreen)
	}
	sized(t, a, 100, 30)
	if view := ansi.Strip(a.View().Content); !strings.Contains(view, "Commands") {
		t.Errorf("palette not visible after sizing:\n%s", view)
	}
}

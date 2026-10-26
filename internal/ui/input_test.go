package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
)

// twoPanes returns an app with p1 and p2 open; p2 is focused.
func twoPanes(t *testing.T) (*App, *fakeProc, *fakeProc) {
	t.Helper()

	a, procs := framedApp(t, 100, 30, shellSpec("/a", ""), shellSpec("/b", ""))

	return a, procs["p1"], procs["p2"]
}

// settled sends a marker key straight to the pane and returns everything the
// process received so far, without markers, so earlier async writes are accounted for.
func settled(t *testing.T, a *App, id core.PaneID, fp *fakeProc) string {
	t.Helper()

	// A pane in Kitty mode encodes the marker as CSI u.
	markers := func() int { return strings.Count(fp.sent(), "\x01") + strings.Count(fp.sent(), "\x1b[97;5u") }
	want := markers() + 1

	a.panes[id].sendKey(uv.KeyPressEvent{Code: 'a', Mod: uv.ModCtrl})
	eventually(t, "marker", func() bool { return markers() == want })

	return strings.NewReplacer("\x01", "", "\x1b[97;5u", "").Replace(fp.sent())
}

func TestKeysReachFocusedPane(t *testing.T) {
	tests := []struct {
		name  string
		setup string
		key   tea.KeyPressMsg
		want  string
	}{
		{"rune", "", keyRune('a'), "a"},
		{"q", "", keyRune('q'), "q"},
		{"ctrl+c", "", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "\x03"},
		{"up", "", tea.KeyPressMsg{Code: tea.KeyUp}, "\x1b[A"},
		{"up application cursor", "\x1b[?1h", tea.KeyPressMsg{Code: tea.KeyUp}, "\x1bOA"},
		{"shift+enter kitty", "\x1b[>1u", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, "\x1b[13;2u"},
		{"repeat", "", tea.KeyPressMsg{Code: tea.KeyUp, IsRepeat: true}, "\x1b[A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, fp1, fp2 := twoPanes(t)
			if tt.setup != "" {
				feedAndWait(t, a.panes["p2"], fp2, tt.setup)
			}

			press(a, tt.key)

			if got := settled(t, a, "p2", fp2); got != tt.want {
				t.Errorf("focused pane got %q, want %q", got, tt.want)
			}
			if got := settled(t, a, "p1", fp1); got != "" {
				t.Errorf("other pane got %q, want nothing", got)
			}
		})
	}
}

func TestKeyReleaseIgnored(t *testing.T) {
	a, _, fp2 := twoPanes(t)

	a.Update(tea.KeyReleaseMsg{Code: 'a', Text: "a"})

	if got := settled(t, a, "p2", fp2); got != "" {
		t.Errorf("release reached the pane: %q", got)
	}
}

func TestKeysWithoutPanes(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)

	if cmd := press(a, keyRune('a')); cmd != nil {
		t.Error("idle key with no panes returned a command")
	}
}

func TestLeaderTwiceSendsCtrlSpace(t *testing.T) {
	tests := []struct {
		name  string
		setup string
		want  string
	}{
		{"legacy", "", "\x00"},
		{"kitty", "\x1b[>1u", "\x1b[32;5u"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, fp2 := twoPanes(t)
			if tt.setup != "" {
				feedAndWait(t, a.panes["p2"], fp2, tt.setup)
			}

			if cmd := press(a, keyLeader, keyLeader); cmd != nil {
				t.Error("leader twice returned a command")
			}
			if a.pending {
				t.Error("leader still pending")
			}
			if got := settled(t, a, "p2", fp2); got != tt.want {
				t.Errorf("pane got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLeaderStillRuns(t *testing.T) {
	a, fp1, fp2 := twoPanes(t)

	press(a, keyLeader, keyRune('t'))

	if a.overlay == nil || a.overlay.kind != overlayThemes {
		t.Fatal("ctrl+space t did not open the theme picker")
	}
	a.closeOverlay()

	press(a, keyLeader, keyEsc)

	for id, fp := range map[core.PaneID]*fakeProc{"p1": fp1, "p2": fp2} {
		if got := settled(t, a, id, fp); got != "" {
			t.Errorf("%s got %q, want nothing", id, got)
		}
	}
}

func TestOverlayFirst(t *testing.T) {
	a, fp1, fp2 := twoPanes(t)
	a.keymap.delay = 0
	pressRun(a, keyLeader, keyRune(':'), keyRune('a'))
	_, cmd := a.Update(tea.PasteMsg{Content: "quit"})
	run(a, cmd)

	if view := ansi.Strip(a.View().Content); !strings.Contains(view, "> aquit") {
		t.Errorf("query lacks the typed and pasted text:\n%s", view)
	}
	for id, fp := range map[core.PaneID]*fakeProc{"p1": fp1, "p2": fp2} {
		if got := settled(t, a, id, fp); got != "" {
			t.Errorf("%s got %q, want nothing", id, got)
		}
	}
}

func TestPasteToPane(t *testing.T) {
	a, _, fp2 := twoPanes(t)

	a.Update(tea.PasteMsg{Content: "one"})
	if got := settled(t, a, "p2", fp2); got != "one" {
		t.Fatalf("plain paste = %q, want %q", got, "one")
	}

	feedAndWait(t, a.panes["p2"], fp2, "\x1b[?2004h")
	a.Update(tea.PasteMsg{Content: "two"})
	if got := settled(t, a, "p2", fp2); got != "one\x1b[200~two\x1b[201~" {
		t.Errorf("bracketed paste = %q", got)
	}
}

func TestFocusReports(t *testing.T) {
	a, fp1, fp2 := twoPanes(t)
	feedAndWait(t, a.panes["p1"], fp1, "\x1b[?1004h")
	feedAndWait(t, a.panes["p2"], fp2, "\x1b[?1004h")

	a.Update(tea.BlurMsg{})
	a.Update(tea.FocusMsg{})
	if got := settled(t, a, "p2", fp2); got != "\x1b[O\x1b[I" {
		t.Errorf("outer focus reports = %q", got)
	}

	if err := a.dispatch(core.FocusPane{ID: "p1"}); err != nil {
		t.Fatal(err)
	}
	if got := settled(t, a, "p2", fp2); got != "\x1b[O\x1b[I\x1b[O" {
		t.Errorf("old pane got %q, want focus-out", got)
	}
	if got := settled(t, a, "p1", fp1); got != "\x1b[I" {
		t.Errorf("new pane got %q, want focus-in", got)
	}

	a.Update(tea.BlurMsg{})
	if err := a.dispatch(core.FocusPane{ID: "p2"}); err != nil {
		t.Fatal(err)
	}
	if got := settled(t, a, "p2", fp2); got != "\x1b[O\x1b[I\x1b[O" {
		t.Errorf("blurred terminal still got a report: %q", got)
	}
}

func TestViewRequestsFocusReports(t *testing.T) {
	a := newTestApp(t)
	v := a.View()

	if !v.ReportFocus {
		t.Error("ReportFocus = false")
	}
	if v.KeyboardEnhancements.ReportEventTypes {
		t.Error("key releases requested")
	}
}

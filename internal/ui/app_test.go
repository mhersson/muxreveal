package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/term"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newThemedApp(t *testing.T, name string) *App {
	t.Helper()
	app, err := New(Options{Logger: testLogger(), Store: core.NewStore(core.State{Theme: name})})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// M0 tests assert the empty-layout shell, so the first size message must not open a pane.
	app.started = true
	app.start = func(context.Context, term.Command, int, int) (proc, error) {
		return nil, errors.New("test app does not start processes")
	}
	return app
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	return newThemedApp(t, "catppuccin-mocha")
}

func TestNewRequiresLogger(t *testing.T) {
	store := core.NewStore(core.State{Theme: "catppuccin-mocha"})
	if _, err := New(Options{Store: store}); err == nil {
		t.Error("New() without a logger succeeded, want an error")
	}
}

func TestNewRequiresStore(t *testing.T) {
	if _, err := New(Options{Logger: testLogger()}); err == nil {
		t.Error("New() without a store succeeded, want an error")
	}
}

func TestAppViewSizes(t *testing.T) {
	t.Run("before any size", func(t *testing.T) {
		v := newTestApp(t).View()
		if !v.AltScreen {
			t.Error("AltScreen = false, want true")
		}
	})

	tests := []struct {
		w, h     int
		wantWord bool
	}{{100, 30, true}, {20, 5, true}, {1, 1, false}}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			app := newTestApp(t)
			app.Update(tea.WindowSizeMsg{Width: tt.w, Height: tt.h})
			v := app.View()
			if !v.AltScreen {
				t.Error("AltScreen = false, want true")
			}
			if got := strings.Contains(v.Content, "MuxReveal"); got != tt.wantWord {
				t.Errorf("view contains MuxReveal = %v, want %v", got, tt.wantWord)
			}
			lines := strings.Split(v.Content, "\n")
			if len(lines) != tt.h {
				t.Fatalf("view has %d lines, want %d:\n%s", len(lines), tt.h, v.Content)
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got != tt.w {
					t.Errorf("line %d width = %d, want %d: %q", i, got, tt.w, line)
				}
			}
		})
	}
}

var (
	keyLeader = tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl}
	keyEsc    = tea.KeyPressMsg{Code: tea.KeyEscape}
)

func keyRune(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func press(a *App, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = a.Update(k)
	}
	return cmd
}

func TestLeader(t *testing.T) {
	tests := []struct {
		name        string
		keys        []tea.KeyPressMsg
		wantQuit    bool
		wantCmd     bool
		wantPending bool
		wantNotice  string
	}{
		{"leader then q quits", []tea.KeyPressMsg{keyLeader, keyRune('q')}, true, true, false, ""},
		{"leader then esc", []tea.KeyPressMsg{keyLeader, keyEsc}, false, false, false, ""},
		{"leader twice runs no command", []tea.KeyPressMsg{keyLeader, keyLeader}, false, false, false, ""},
		{"leader then unbound", []tea.KeyPressMsg{keyLeader, keyRune('w')}, false, false, false, "ctrl+space w is not bound"},
		{"leader alone", []tea.KeyPressMsg{keyLeader}, false, true, true, ""},
		{"idle ctrl+c is not an app command", []tea.KeyPressMsg{{Code: 'c', Mod: tea.ModCtrl}}, false, false, false, ""},
		{"ctrl+c while pending", []tea.KeyPressMsg{keyLeader, {Code: 'c', Mod: tea.ModCtrl}}, false, false, false, "ctrl+space ctrl+c is not bound"},
		{"idle q is not an app command", []tea.KeyPressMsg{keyRune('q')}, false, false, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			sized(t, a, 100, 30)
			cmd := press(a, tt.keys...)
			if (cmd != nil) != tt.wantCmd {
				t.Fatalf("last cmd non-nil = %v, want %v", cmd != nil, tt.wantCmd)
			}
			if tt.wantQuit {
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Errorf("cmd() is not tea.QuitMsg")
				}
			}
			if a.pending != tt.wantPending {
				t.Errorf("pending = %v, want %v", a.pending, tt.wantPending)
			}
			if a.notice != tt.wantNotice {
				t.Errorf("notice = %q, want %q", a.notice, tt.wantNotice)
			}
			plain := ansi.Strip(a.View().Content)
			if got := strings.Contains(plain, tt.wantNotice); tt.wantNotice != "" && !got {
				t.Errorf("view lacks notice %q:\n%s", tt.wantNotice, plain)
			}
		})
	}
}

func TestNoticeClearsOnNextKey(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	press(a, keyLeader, keyRune('w'))
	if a.notice == "" {
		t.Fatal("no notice after an unbound key")
	}
	press(a, keyRune('y'))
	if a.notice != "" {
		t.Errorf("notice = %q after the next key, want empty", a.notice)
	}
	if strings.Contains(ansi.Strip(a.View().Content), "is not bound") {
		t.Error("view still shows the notice")
	}
}

func hintCount(a *App) int {
	return strings.Count(ansi.Strip(a.View().Content), "Quit MuxReveal")
}

func TestWhichKey(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	before := hintCount(a)
	press(a, keyLeader)
	if got := hintCount(a); got != before {
		t.Errorf("popup visible before the delay: %d hints, want %d", got, before)
	}
	a.Update(whichKeyMsg{seq: a.seq})
	plain := ansi.Strip(a.View().Content)
	if got := strings.Count(plain, "Quit MuxReveal"); got != before+1 {
		t.Errorf("hints = %d, want %d:\n%s", got, before+1, plain)
	}
	if !strings.Contains(plain, "╭─ ctrl+space") || !strings.Contains(plain, "q  Quit MuxReveal") {
		t.Errorf("popup lacks its title or row:\n%s", plain)
	}
	press(a, keyEsc)
	if hintCount(a) != before {
		t.Error("popup still visible after esc")
	}
}

func TestStaleWhichKey(t *testing.T) {
	tests := []struct {
		name string
		keys []tea.KeyPressMsg
	}{
		{"esc", []tea.KeyPressMsg{keyEsc}},
		{"bound key", []tea.KeyPressMsg{keyRune('q')}},
		{"second leader press after esc", []tea.KeyPressMsg{keyEsc, keyLeader}},
		{"second leader press", []tea.KeyPressMsg{keyLeader}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			sized(t, a, 100, 30)
			press(a, keyLeader)
			first := a.seq
			before := hintCount(a)
			press(a, tt.keys...)
			a.Update(whichKeyMsg{seq: first})
			if got := hintCount(a); got != before {
				t.Errorf("stale tick opened the popup: %d hints, want %d", got, before)
			}
			if a.popup {
				t.Error("popup = true after a stale tick")
			}
		})
	}
}

func TestWhichKeySizes(t *testing.T) {
	for _, sz := range [][2]int{{100, 30}, {40, 12}, {20, 5}, {1, 1}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			a := newTestApp(t)
			sized(t, a, sz[0], sz[1])
			press(a, keyLeader)
			a.Update(whichKeyMsg{seq: a.seq})
			content := a.View().Content
			lines := strings.Split(content, "\n")
			if len(lines) != sz[1] {
				t.Fatalf("view has %d lines, want %d", len(lines), sz[1])
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got != sz[0] {
					t.Errorf("line %d width = %d, want %d: %q", i, got, sz[0], line)
				}
			}
			plain := ansi.Strip(content)
			switch sz[0] {
			case 100:
				if !strings.Contains(plain, "╭─ ctrl+space") || !strings.Contains(plain, "│ q  Quit MuxReveal       │") {
					t.Errorf("popup not drawn:\n%s", plain)
				}
			case 40:
				// The popup is taller than the body: its top rows show, the rest is clipped.
				if !strings.Contains(plain, "╭─ ctrl+space") || !strings.Contains(plain, "│ :  Open command palette │") {
					t.Errorf("popup top not drawn:\n%s", plain)
				}
			case 20:
				if !strings.Contains(plain, "╭─ ctrl+space") || strings.Contains(plain, "╮") {
					t.Errorf("popup not clipped on the right:\n%s", plain)
				}
			}
		})
	}
}

func TestWhichKeyGolden(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})
	golden.RequireEqual(t, []byte(a.View().Content))
}

func TestLeaderPendingGolden(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	press(a, keyLeader)
	golden.RequireEqual(t, []byte(a.View().Content))
}

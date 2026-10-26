package ui

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mhersson/muxreveal/internal/core"
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

func TestAppQuitsOnCtrlC(t *testing.T) {
	tests := []struct {
		name     string
		key      tea.KeyPressMsg
		wantQuit bool
	}{
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, true},
		{"plain c", tea.KeyPressMsg{Code: 'c', Text: "c"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cmd := newTestApp(t).Update(tt.key)
			var msg tea.Msg
			if cmd != nil {
				msg = cmd()
			}
			if _, quit := msg.(tea.QuitMsg); quit != tt.wantQuit {
				t.Errorf("Update(%s) cmd msg = %#v, want quit %v", tt.key, msg, tt.wantQuit)
			}
		})
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

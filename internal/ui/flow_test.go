package ui

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/exp/teatest/v2"
)

func TestQuitFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, newTestApp(t), teatest.WithInitialTermSize(100, 30))
	tm.Send(keyLeader)
	tm.Send(keyRune('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// runFlow sends keys to a teatest program, waits until the output sets the
// wanted theme background, quits and returns the final app.
func runFlow(t *testing.T, app *App, want string, keys ...tea.KeyPressMsg) *App {
	t.Helper()
	tm := teatest.NewTestModel(t, app, teatest.WithInitialTermSize(100, 30))
	for _, k := range keys {
		tm.Send(k)
	}
	r, g, b, _ := mustTheme(t, want).UI.Bg.RGBA()
	// Bubble Tea reports View.BackgroundColor to the terminal as OSC 11.
	bg := fmt.Sprintf("]11;#%02x%02x%02x", r>>8, g>>8, b>>8)
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte(bg)) },
		teatest.WithDuration(3*time.Second))
	if err := tm.Quit(); err != nil {
		t.Fatalf("Quit() error = %v", err)
	}
	return tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(*App)
}

func TestPaletteFlow(t *testing.T) {
	keys := []tea.KeyPressMsg{keyLeader, keyRune(':')}
	for _, r := range "macchiato" {
		keys = append(keys, keyRune(r))
	}
	final := runFlow(t, newTestApp(t), "catppuccin-macchiato", append(keys, keyEnter)...)
	if got := final.store.State().Theme; got != "catppuccin-macchiato" {
		t.Errorf("theme = %q, want catppuccin-macchiato", got)
	}
}

// TestThemePickerFlow waits for a card row hidden under the open picker, so
// only the frame drawn after the commit can satisfy it (the preview already sets Latte's OSC 11).
func TestThemePickerFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, newTestApp(t), teatest.WithInitialTermSize(100, 30))
	for _, k := range []tea.KeyPressMsg{keyLeader, keyRune('t'), keyDown} {
		tm.Send(k)
	}
	r, g, b, _ := mustTheme(t, "catppuccin-latte").UI.Bg.RGBA()
	preview := fmt.Sprintf("]11;#%02x%02x%02x", r>>8, g>>8, b>>8)
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte(preview)) },
		teatest.WithDuration(3*time.Second))
	tm.Send(keyEnter)
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte("Open command palette")) },
		teatest.WithDuration(3*time.Second))
	if err := tm.Quit(); err != nil {
		t.Fatalf("Quit() error = %v", err)
	}
	final := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(*App)
	if got := final.store.State().Theme; got != "catppuccin-latte" {
		t.Errorf("theme = %q, want catppuccin-latte", got)
	}
	if got, want := final.View().BackgroundColor, mustTheme(t, "catppuccin-latte").UI.Bg; got != want {
		t.Errorf("background = %v, want %v", got, want)
	}
}

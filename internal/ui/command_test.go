package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/ui/theme"
)

func TestRegistry(t *testing.T) {
	noop := func(*App) tea.Cmd { return nil }
	tests := []struct {
		name    string
		cmds    []command
		wantErr bool
	}{
		{"ok", []command{{id: "a", title: "A", run: noop}, {id: "b", title: "B", run: noop}}, false},
		{"empty", nil, false},
		{"empty id", []command{{id: "", title: "A", run: noop}}, true},
		{"duplicate id", []command{{id: "a", run: noop}, {id: "a", run: noop}}, true},
		{"nil run", []command{{id: "a"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := newRegistry(tt.cmds)
			if (err != nil) != tt.wantErr {
				t.Fatalf("newRegistry() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			got := r.all()
			if len(got) != len(tt.cmds) {
				t.Fatalf("all() has %d commands, want %d", len(got), len(tt.cmds))
			}
			for i, c := range tt.cmds {
				if got[i].id != c.id {
					t.Errorf("all()[%d].id = %q, want %q", i, got[i].id, c.id)
				}
				if byID, ok := r.get(c.id); !ok || byID.title != c.title {
					t.Errorf("get(%q) = %+v, %v", c.id, byID, ok)
				}
			}
			if _, ok := r.get("missing"); ok {
				t.Error("get(missing) found a command")
			}
		})
	}
}

func TestDefaultCommands(t *testing.T) {
	cmds := defaultCommands()
	fixed := []struct{ id, title string }{
		{"palette.open", "Open command palette"},
		{"theme.pick", "Pick theme"},
		{"agent.new", "New agent pane"},
		{"pane.split.down", "Split: shell below"},
		{"pane.split.right", "Split: shell right"},
		{"pane.focus.left", "Focus left"},
		{"pane.focus.down", "Focus down"},
		{"pane.focus.up", "Focus up"},
		{"pane.focus.right", "Focus right"},
		{"pane.resize.left", "Resize left"},
		{"pane.resize.down", "Resize down"},
		{"pane.resize.up", "Resize up"},
		{"pane.resize.right", "Resize right"},
		{"pane.zoom", "Zoom pane"},
		{"pane.close", "Close pane"},
		{"app.quit", "Quit MuxReveal"},
	}
	if len(cmds) != len(fixed)+len(theme.All()) {
		t.Fatalf("defaultCommands() has %d commands, want %d", len(cmds), len(fixed)+len(theme.All()))
	}
	for i, f := range fixed {
		if cmds[i].id != f.id || cmds[i].title != f.title {
			t.Errorf("command %d = %q %q, want %s %s", i, cmds[i].id, cmds[i].title, f.id, f.title)
		}
	}
	for i, th := range theme.All() {
		c := cmds[len(fixed)+i]
		if c.id != "theme.set."+th.Name || c.title != "Theme: "+th.Title {
			t.Errorf("command %d = %q %q, want theme.set.%s Theme: %s", len(fixed)+i, c.id, c.title, th.Name, th.Title)
		}
	}
}

func TestThemeCommands(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	c, ok := a.reg.get("theme.set.catppuccin-latte")
	if !ok {
		t.Fatal("theme.set.catppuccin-latte is not registered")
	}
	if cmd := c.run(a); cmd != nil {
		t.Errorf("run() cmd = %v, want nil", cmd)
	}
	if got := a.store.State().Theme; got != "catppuccin-latte" {
		t.Errorf("store theme = %q, want catppuccin-latte", got)
	}
	v := a.View()
	if want := mustTheme(t, "catppuccin-latte").UI.Bg; v.BackgroundColor != want {
		t.Errorf("BackgroundColor = %v, want %v", v.BackgroundColor, want)
	}
	if !strings.Contains(ansi.Strip(v.Content), "Catppuccin Latte") {
		t.Error("view lacks the new theme title")
	}
}

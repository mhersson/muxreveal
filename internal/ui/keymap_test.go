package ui

import "testing"

func TestEveryBindingResolves(t *testing.T) {
	reg, err := newRegistry(defaultCommands())
	if err != nil {
		t.Fatal(err)
	}
	km := defaultKeymap()
	seen := map[string]bool{}
	for _, b := range km.bindings {
		if _, ok := reg.get(b.command); !ok {
			t.Errorf("binding %q names unknown command %q", b.key, b.command)
		}
		if seen[b.key] {
			t.Errorf("key %q is bound twice", b.key)
		}
		seen[b.key] = true
	}
}

func TestDefaultKeymap(t *testing.T) {
	km := defaultKeymap()
	if km.leader != "ctrl+space" {
		t.Errorf("leader = %q, want ctrl+space", km.leader)
	}
	if km.delay.Milliseconds() != 300 {
		t.Errorf("delay = %v, want 300ms", km.delay)
	}
}

func TestKeymapLookup(t *testing.T) {
	km := defaultKeymap()
	tests := []struct {
		key     string
		wantCmd string
		wantOK  bool
	}{
		{"q", "app.quit", true},
		{":", "palette.open", true},
		{"t", "theme.pick", true},
		{"x", "pane.close", true},
		{"H", "pane.resize.left", true},
		{"w", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, ok := km.lookup(tt.key)
			if got != tt.wantCmd || ok != tt.wantOK {
				t.Errorf("lookup(%q) = %q, %v, want %q, %v", tt.key, got, ok, tt.wantCmd, tt.wantOK)
			}
		})
	}
}

func TestKeymapKeyFor(t *testing.T) {
	km := defaultKeymap()
	tests := []struct {
		command string
		want    string
		wantOK  bool
	}{
		{"app.quit", "ctrl+space q", true},
		{"palette.open", "ctrl+space :", true},
		{"theme.pick", "ctrl+space t", true},
		{"pane.split.right", "ctrl+space v", true},
		{"theme.set.catppuccin-latte", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got, ok := km.keyFor(tt.command)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("keyFor(%q) = %q, %v, want %q, %v", tt.command, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

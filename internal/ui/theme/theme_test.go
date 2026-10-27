package theme

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAllAndByName(t *testing.T) {
	names := []string{"catppuccin-latte", "catppuccin-frappe", "catppuccin-macchiato", "catppuccin-mocha"}
	if got := Names(); !slices.Equal(got, names) {
		t.Errorf("Names() = %q, want %q", got, names)
	}
	var allNames []string
	for _, th := range All() {
		allNames = append(allNames, th.Name)
	}
	if !slices.Equal(allNames, names) {
		t.Errorf("All() names = %q, want %q", allNames, names)
	}

	tests := []struct {
		name, title string
		dark        bool
	}{
		{"catppuccin-latte", "Catppuccin Latte", false},
		{"catppuccin-frappe", "Catppuccin Frappé", true},
		{"catppuccin-macchiato", "Catppuccin Macchiato", true},
		{"catppuccin-mocha", "Catppuccin Mocha", true},
	}
	for _, tt := range tests {
		th, err := ByName(tt.name)
		if err != nil {
			t.Errorf("ByName(%q): %v", tt.name, err)
			continue
		}
		if th.Name != tt.name || th.Title != tt.title || th.Dark != tt.dark {
			t.Errorf("ByName(%q) = {Name: %q, Title: %q, Dark: %v}, want {%q, %q, %v}",
				tt.name, th.Name, th.Title, th.Dark, tt.name, tt.title, tt.dark)
		}
	}

	for _, name := range []string{"nope", ""} {
		_, err := ByName(name)
		if !errors.Is(err, ErrUnknownTheme) {
			t.Errorf("ByName(%q) error = %v, want ErrUnknownTheme", name, err)
			continue
		}
		for _, n := range names {
			if !strings.Contains(err.Error(), n) {
				t.Errorf("ByName(%q) error %q does not list %q", name, err, n)
			}
		}
	}
}

func TestUIRoles(t *testing.T) {
	mocha, err := ByName("catppuccin-mocha")
	if err != nil {
		t.Fatal(err)
	}
	spot := []struct {
		role      string
		got, want Hex
	}{
		{"Bg", mocha.UI.Bg, "#1e1e2e"},
		{"Accent", mocha.UI.Accent, "#cba6f7"},
		{"BorderFocus", mocha.UI.BorderFocus, "#b4befe"},
	}
	latte, err := ByName("catppuccin-latte")
	if err != nil {
		t.Fatal(err)
	}
	for _, th := range []Theme{mocha, latte} {
		p := th.Palette
		for _, r := range []struct {
			role      string
			got, want Hex
		}{
			{"Agent", th.UI.Agent, p.Mauve},
			{"Shell", th.UI.Shell, p.Blue},
			{"Bar", th.UI.Bar, p.Crust},
			{"Dim", th.UI.Dim, p.Subtext0},
			{"Clock", th.UI.Clock, p.Lavender},
			{"Git", th.UI.Git, p.Flamingo},
			{"Load", th.UI.Load, p.Teal},
		} {
			if r.got != r.want || r.got == "" {
				t.Errorf("%s UI.%s = %q, want %q", th.Name, r.role, r.got, r.want)
			}
		}
	}
	for _, s := range spot {
		if s.got != s.want {
			t.Errorf("mocha UI.%s = %q, want %q", s.role, s.got, s.want)
		}
	}

	for _, th := range All() {
		inPalette := map[Hex]bool{}
		for _, h := range paletteFields(th.Palette) {
			inPalette[h] = true
		}
		v := reflect.ValueOf(th.UI)
		for i := range v.NumField() {
			role, h := v.Type().Field(i).Name, Hex(v.Field(i).String())
			if h == "" {
				t.Errorf("%s: UI.%s is empty", th.Name, role)
			} else if !inPalette[h] {
				t.Errorf("%s: UI.%s = %q is not a palette colour", th.Name, role, h)
			}
		}
	}
}

// Package theme holds the Catppuccin flavors MuxReveal ships: each flavor's
// palette, the semantic UI roles widgets colour from, and a matching Chroma
// syntax style.
package theme

import (
	"errors"
	"fmt"
	"strings"
)

// UI maps semantic roles to palette colours.
type UI struct {
	Bg, Fg, Muted, Subtle, Panel, Surface, Border, BorderFocus, Accent, OnAccent,
	Key, Selection, Match, Error, Warning, Success, Info Hex
}

// Theme is one Catppuccin flavor.
type Theme struct {
	Name    string // ByName key, e.g. "catppuccin-mocha"
	Title   string // display name, e.g. "Catppuccin Mocha"
	Dark    bool
	Palette Palette
	UI      UI
}

// ErrUnknownTheme is returned by ByName for a name no theme has.
var ErrUnknownTheme = errors.New("unknown theme")

// All returns every theme, light first: latte, frappe, macchiato, mocha.
func All() []Theme {
	return []Theme{
		newTheme("catppuccin-latte", "Catppuccin Latte", false, lattePalette()),
		newTheme("catppuccin-frappe", "Catppuccin Frappé", true, frappePalette()),
		newTheme("catppuccin-macchiato", "Catppuccin Macchiato", true, macchiatoPalette()),
		newTheme("catppuccin-mocha", "Catppuccin Mocha", true, mochaPalette()),
	}
}

// Names returns the theme names in the order of All.
func Names() []string {
	all := All()
	names := make([]string, len(all))
	for i, t := range all {
		names[i] = t.Name
	}

	return names
}

// ByName returns the theme called name.
func ByName(name string) (Theme, error) {
	for _, t := range All() {
		if t.Name == name {
			return t, nil
		}
	}

	return Theme{}, fmt.Errorf("%w %q (known: %s)", ErrUnknownTheme, name, strings.Join(Names(), ", "))
}

func newTheme(name, title string, dark bool, p Palette) Theme {
	return Theme{Name: name, Title: title, Dark: dark, Palette: p, UI: uiRoles(p)}
}

func uiRoles(p Palette) UI {
	return UI{
		Bg:          p.Base,
		Fg:          p.Text,
		Muted:       p.Overlay1,
		Subtle:      p.Overlay0,
		Panel:       p.Mantle,
		Surface:     p.Surface0,
		Border:      p.Surface1,
		BorderFocus: p.Lavender,
		Accent:      p.Mauve,
		OnAccent:    p.Crust,
		Key:         p.Blue,
		Selection:   p.Surface1,
		Match:       p.Peach,
		Error:       p.Red,
		Warning:     p.Yellow,
		Success:     p.Green,
		Info:        p.Blue,
	}
}

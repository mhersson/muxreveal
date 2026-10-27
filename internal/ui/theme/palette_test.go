package theme

import (
	"reflect"
	"regexp"
	"testing"
)

// paletteFields returns every field of p keyed by field name.
func paletteFields(p Palette) map[string]Hex {
	v := reflect.ValueOf(p)

	fields := make(map[string]Hex, v.NumField())
	for i := range v.NumField() {
		fields[v.Type().Field(i).Name] = Hex(v.Field(i).String())
	}

	return fields
}

func TestPaletteHex(t *testing.T) {
	hexRE := regexp.MustCompile(`^#[0-9a-f]{6}$`)

	for _, th := range All() {
		fields := paletteFields(th.Palette)
		if len(fields) != 26 {
			t.Errorf("%s: Palette has %d fields, want 26", th.Name, len(fields))
		}

		for name, h := range fields {
			if !hexRE.MatchString(string(h)) {
				t.Errorf("%s: Palette.%s = %q, want #rrggbb", th.Name, name, h)
			}
		}
	}

	tests := []struct {
		hex        Hex
		r, g, b, a uint32
	}{
		{"#cba6f7", 0xcbcb, 0xa6a6, 0xf7f7, 0xffff},
		{"#000000", 0, 0, 0, 0xffff},
		{"#ffffff", 0xffff, 0xffff, 0xffff, 0xffff},
		{"#CBA6F7", 0xcbcb, 0xa6a6, 0xf7f7, 0xffff},
		{"", 0, 0, 0, 0xffff},
		{"cba6f7", 0, 0, 0, 0xffff},
		{"#cba6f", 0, 0, 0, 0xffff},
		{"#cba6f7a", 0, 0, 0, 0xffff},
		{"#cba6fg", 0, 0, 0, 0xffff},
	}
	for _, tt := range tests {
		r, g, b, a := tt.hex.RGBA()
		if r != tt.r || g != tt.g || b != tt.b || a != tt.a {
			t.Errorf("Hex(%q).RGBA() = (%#x, %#x, %#x, %#x), want (%#x, %#x, %#x, %#x)",
				tt.hex, r, g, b, a, tt.r, tt.g, tt.b, tt.a)
		}
	}
}

func TestPaletteSpotValues(t *testing.T) {
	tests := []struct {
		theme, field string
		want         Hex
	}{
		{"catppuccin-latte", "Base", "#eff1f5"},
		{"catppuccin-latte", "Mauve", "#8839ef"},
		{"catppuccin-frappe", "Base", "#303446"},
		{"catppuccin-frappe", "Crust", "#232634"},
		{"catppuccin-macchiato", "Base", "#24273a"},
		{"catppuccin-macchiato", "Sapphire", "#7dc4e4"},
		{"catppuccin-mocha", "Base", "#1e1e2e"},
		{"catppuccin-mocha", "Text", "#cdd6f4"},
		{"catppuccin-mocha", "Lavender", "#b4befe"},
	}
	for _, tt := range tests {
		th, err := ByName(tt.theme)
		if err != nil {
			t.Fatalf("ByName(%q): %v", tt.theme, err)
		}

		if got := paletteFields(th.Palette)[tt.field]; got != tt.want {
			t.Errorf("%s Palette.%s = %q, want %q", tt.theme, tt.field, got, tt.want)
		}
	}
}

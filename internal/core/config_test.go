package core

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestIconsOn(t *testing.T) {
	if !DefaultConfig().IconsOn() || !(Config{Icons: ptr(true)}).IconsOn() || (Config{Icons: ptr(false)}).IconsOn() {
		t.Error("IconsOn: want true for nil and true, false for false")
	}
}

func TestClipboardDefaultsOn(t *testing.T) {
	d := DefaultConfig()
	if !d.Clipboard.OSC52On() || !d.Clipboard.NativeOn() {
		t.Error("clipboard: want OSC 52 and native on by default")
	}
	if d.Mouse.FocusFollowsMouse || d.Mouse.CopyOnSelect {
		t.Error("mouse: want both options off by default")
	}
	off := Clipboard{OSC52: ptr(false), Native: ptr(false)}
	if off.OSC52On() || off.NativeOn() {
		t.Error("clipboard: want false to turn both off")
	}
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		missing    bool
		unreadable bool
		want       Config
		wantErr    bool
		errHas     []string // substrings besides the path
	}{
		{name: "missing file", missing: true, want: DefaultConfig()},
		{name: "empty file", content: "", want: DefaultConfig()},
		{name: "comments only", content: "# theme = \"x\"\n\n# more\n", want: DefaultConfig()},
		{name: "theme set", content: "theme = \"catppuccin-latte\"\n", want: Config{Theme: "catppuccin-latte"}},
		{name: "theme name not validated", content: "theme = \"no-such-theme\"\n", want: Config{Theme: "no-such-theme"}},
		{name: "icons off", content: "icons = false\n", want: Config{Theme: DefaultTheme, Icons: ptr(false)}},
		{name: "icons on", content: "icons = true\n", want: Config{Theme: DefaultTheme, Icons: ptr(true)}},
		{name: "icons wrong type", content: "icons = \"no\"\n", wantErr: true, errHas: []string{"line 1", "icons"}},
		{name: "mouse options", content: "[mouse]\nfocus_follows_mouse = true\ncopy_on_select = true\n",
			want: Config{Theme: DefaultTheme, Mouse: Mouse{FocusFollowsMouse: true, CopyOnSelect: true}}},
		{name: "mouse partly set", content: "[mouse]\ncopy_on_select = true\n",
			want: Config{Theme: DefaultTheme, Mouse: Mouse{CopyOnSelect: true}}},
		{name: "clipboard off", content: "[clipboard]\nosc52 = false\nnative = false\n",
			want: Config{Theme: DefaultTheme, Clipboard: Clipboard{OSC52: ptr(false), Native: ptr(false)}}},
		{name: "clipboard native only off", content: "[clipboard]\nnative = false\n",
			want: Config{Theme: DefaultTheme, Clipboard: Clipboard{Native: ptr(false)}}},
		{name: "unknown mouse key", content: "[mouse]\nfocus = true\n", wantErr: true, errHas: []string{"unknown key mouse.focus"}},
		{name: "unknown clipboard key", content: "[clipboard]\nosc = true\n", wantErr: true, errHas: []string{"unknown key clipboard.osc"}},
		{name: "mouse wrong type", content: "[mouse]\ncopy_on_select = \"yes\"\n", wantErr: true, errHas: []string{"copy_on_select"}},
		{name: "invalid toml", content: "theme = \"x", wantErr: true, errHas: []string{"line 1"}},
		{name: "unknown key", content: "them = \"x\"\n", wantErr: true, errHas: []string{"unknown key them"}},
		{name: "wrong type", content: "theme = 3\n", wantErr: true, errHas: []string{"line 1", "theme"}},
		{name: "unreadable", content: "theme = \"catppuccin-latte\"\n", unreadable: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.unreadable && os.Geteuid() == 0 {
				t.Skip("root can read a mode 0o000 file")
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if !tt.missing {
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.unreadable {
				if err := os.Chmod(path, 0o000); err != nil {
					t.Fatal(err)
				}
			}

			got, err := LoadConfig(path)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("LoadConfig() error = %v", err)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("LoadConfig() = %+v, want %+v", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("LoadConfig() = %+v, want an error", got)
			}
			for _, s := range append([]string{path}, tt.errHas...) {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q does not contain %q", err, s)
				}
			}
		})
	}
}

package core

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mhersson/muxreveal/internal/core/tiling"
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
		{name: "layout section", content: "[layout]\ndefault_mode = \"scrolling\"\nmaster_count = 3\n",
			want: func() Config {
				c := DefaultConfig()
				c.Layout.DefaultMode, c.Layout.MasterCount = "scrolling", 3
				return c
			}()},
		{name: "unknown layout key", content: "[layout]\nmaster = 3\n", wantErr: true, errHas: []string{"unknown key layout.master"}},
		{name: "layout wrong type", content: "[layout]\nmaster_ratio = \"half\"\n", wantErr: true, errHas: []string{"master_ratio"}},
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

			// A want that leaves Layout empty means the layout defaults.
			if tt.want.Layout == (LayoutConfig{}) {
				tt.want.Layout = DefaultConfig().Layout
			}
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

func TestLayoutDefaults(t *testing.T) {
	got := DefaultConfig().Layout
	want := LayoutConfig{
		DefaultMode: "bsp", AutoScheme: "spiral", MasterRatio: 50, MasterPosition: "left",
		MasterCount: 1, MasterGrid: ptr(true), ColumnWidth: 50,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default layout = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("default layout: Validate() = %v", err)
	}
}

func TestLayoutZeroValueMeansDefaults(t *testing.T) {
	var z LayoutConfig
	if z.Mode() != ModeBSP || z.Scheme() != tiling.SchemeSpiral || z.ratio() != 0.5 || z.count() != 1 ||
		z.position() != "left" || !z.grid() || z.columnWidth() != 0.5 {
		t.Errorf("zero layout accessors: %v %v %v %v %v %v %v",
			z.Mode(), z.Scheme(), z.ratio(), z.count(), z.position(), z.grid(), z.columnWidth())
	}
	set := LayoutConfig{DefaultMode: "master-stack", AutoScheme: "alternate", MasterRatio: 70, MasterCount: 2,
		MasterPosition: "top", MasterGrid: ptr(false), ColumnWidth: 25}
	if set.Mode() != ModeMaster || set.Scheme() != tiling.SchemeAlternate || set.ratio() != 0.7 || set.count() != 2 ||
		set.position() != "top" || set.grid() || set.columnWidth() != 0.25 {
		t.Errorf("set layout accessors: %v %v %v %v %v %v %v",
			set.Mode(), set.Scheme(), set.ratio(), set.count(), set.position(), set.grid(), set.columnWidth())
	}
}

func TestLayoutValidate(t *testing.T) {
	with := func(f func(*LayoutConfig)) LayoutConfig {
		c := DefaultConfig().Layout
		f(&c)
		return c
	}
	good := []struct {
		name string
		c    LayoutConfig
	}{
		{"ratio low", with(func(c *LayoutConfig) { c.MasterRatio = 10 })},
		{"ratio high", with(func(c *LayoutConfig) { c.MasterRatio = 90 })},
		{"count low", with(func(c *LayoutConfig) { c.MasterCount = 1 })},
		{"count high", with(func(c *LayoutConfig) { c.MasterCount = 9 })},
		{"width low", with(func(c *LayoutConfig) { c.ColumnWidth = 10 })},
		{"width high", with(func(c *LayoutConfig) { c.ColumnWidth = 100 })},
		{"master-stack", with(func(c *LayoutConfig) { c.DefaultMode = "master-stack" })},
		{"scrolling", with(func(c *LayoutConfig) { c.DefaultMode = "scrolling" })},
		{"longest_side", with(func(c *LayoutConfig) { c.AutoScheme = "longest_side" })},
		{"alternate", with(func(c *LayoutConfig) { c.AutoScheme = "alternate" })},
		{"smart_split", with(func(c *LayoutConfig) { c.AutoScheme = "smart_split" })},
		{"right", with(func(c *LayoutConfig) { c.MasterPosition = "right" })},
		{"top", with(func(c *LayoutConfig) { c.MasterPosition = "top" })},
		{"bottom", with(func(c *LayoutConfig) { c.MasterPosition = "bottom" })},
		{"center", with(func(c *LayoutConfig) { c.MasterPosition = "center" })},
		{"grid off", with(func(c *LayoutConfig) { c.MasterGrid = ptr(false) })},
	}
	for _, tt := range good {
		if err := tt.c.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v", tt.name, err)
		}
	}
	bad := []struct {
		name string
		c    LayoutConfig
		want string
	}{
		{"ratio high", with(func(c *LayoutConfig) { c.MasterRatio = 95 }), "layout.master_ratio: 95 is out of range 10-90"},
		{"ratio low", with(func(c *LayoutConfig) { c.MasterRatio = 9 }), "layout.master_ratio"},
		{"count zero", with(func(c *LayoutConfig) { c.MasterCount = 0 }), "layout.master_count: 0 is out of range 1-9"},
		{"count ten", with(func(c *LayoutConfig) { c.MasterCount = 10 }), "layout.master_count"},
		{"width small", with(func(c *LayoutConfig) { c.ColumnWidth = 5 }), "layout.column_width: 5 is out of range 10-100"},
		{"width big", with(func(c *LayoutConfig) { c.ColumnWidth = 101 }), "layout.column_width"},
		{"mode", with(func(c *LayoutConfig) { c.DefaultMode = "tiled" }), "layout.default_mode"},
		{"scheme", with(func(c *LayoutConfig) { c.AutoScheme = "x" }), "layout.auto_scheme"},
		{"position", with(func(c *LayoutConfig) { c.MasterPosition = "middle" }), "layout.master_position"},
	}
	for _, tt := range bad {
		err := tt.c.Validate()
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Validate() = %v, want it to contain %q", tt.name, err, tt.want)
		}
	}
}

func TestLoadConfigLayoutOutOfRangeFailsValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[layout]\nmaster_count = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Layout.Validate(); err == nil || !strings.Contains(err.Error(), "layout.master_count") {
		t.Errorf("Validate() = %v, want an error naming layout.master_count", err)
	}
}

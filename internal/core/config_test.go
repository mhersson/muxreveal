package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
				if got != tt.want {
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

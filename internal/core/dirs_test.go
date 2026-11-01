package core

import "testing"

func testEnv(goos string, vars map[string]string) Env {
	return Env{
		GOOS:   goos,
		Home:   "/home/u",
		Getenv: func(k string) string { return vars[k] },
	}
}

func TestConfigPath(t *testing.T) {
	tests := []struct {
		name string
		env  Env
		want string
	}{
		{"linux xdg", testEnv("linux", map[string]string{"XDG_CONFIG_HOME": "/x"}), "/x/aim/config.toml"},
		{"linux unset", testEnv("linux", nil), "/home/u/.config/aim/config.toml"},
		{"linux relative xdg ignored", testEnv("linux", map[string]string{"XDG_CONFIG_HOME": "rel/x"}), "/home/u/.config/aim/config.toml"},
		{"darwin unset", testEnv("darwin", nil), "/home/u/.config/aim/config.toml"},
		{"darwin xdg", testEnv("darwin", map[string]string{"XDG_CONFIG_HOME": "/x"}), "/x/aim/config.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConfigPath(tt.env); got != tt.want {
				t.Errorf("ConfigPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStateDir(t *testing.T) {
	tests := []struct {
		name string
		env  Env
		want string
	}{
		{"linux xdg", testEnv("linux", map[string]string{"XDG_STATE_HOME": "/s"}), "/s/aim"},
		{"linux unset", testEnv("linux", nil), "/home/u/.local/state/aim"},
		{"linux relative xdg ignored", testEnv("linux", map[string]string{"XDG_STATE_HOME": "rel/s"}), "/home/u/.local/state/aim"},
		{"darwin unset", testEnv("darwin", nil), "/home/u/Library/Application Support/aim"},
		{"darwin xdg ignored", testEnv("darwin", map[string]string{"XDG_STATE_HOME": "/s"}), "/home/u/Library/Application Support/aim"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StateDir(tt.env); got != tt.want {
				t.Errorf("StateDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

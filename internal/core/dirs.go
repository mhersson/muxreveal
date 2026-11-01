package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const appName = "aim"

// Env is the part of the process environment that decides MuxReveal's paths.
type Env struct {
	GOOS   string
	Home   string
	Getenv func(string) string
}

// DefaultEnv returns the environment of the running process.
func DefaultEnv() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, fmt.Errorf("home dir: %w", err)
	}

	return Env{GOOS: runtime.GOOS, Home: home, Getenv: os.Getenv}, nil
}

// ConfigPath returns the default config file path, the same on Linux and macOS.
func ConfigPath(env Env) string {
	return filepath.Join(xdgDir(env, "XDG_CONFIG_HOME", ".config"), appName, "config.toml")
}

// StateDir returns the platform state dir that holds the log and persisted state.
func StateDir(env Env) string {
	if env.GOOS == "darwin" {
		return filepath.Join(env.Home, "Library", "Application Support", appName)
	}

	return filepath.Join(xdgDir(env, "XDG_STATE_HOME", filepath.Join(".local", "state")), appName)
}

// xdgDir returns $key, or home/fallback when it is unset or relative, which
// the XDG Base Directory spec says to ignore.
func xdgDir(env Env, key, fallback string) string {
	if dir := env.Getenv(key); filepath.IsAbs(dir) {
		return dir
	}

	return filepath.Join(env.Home, fallback)
}

package core

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// DefaultTheme is the theme used when config.toml does not set one.
const DefaultTheme = "catppuccin-mocha"

// Config is the user's config.toml.
type Config struct {
	Theme string `toml:"theme"`
	// Icons is a pointer so an absent key (nil) can mean true.
	Icons     *bool     `toml:"icons"`
	Mouse     Mouse     `toml:"mouse"`
	Clipboard Clipboard `toml:"clipboard"`
}

// Mouse is the [mouse] section; both switches are off by default.
type Mouse struct {
	FocusFollowsMouse bool `toml:"focus_follows_mouse"`
	CopyOnSelect      bool `toml:"copy_on_select"`
}

// Clipboard is the [clipboard] section. The pointers let an absent key (nil)
// mean true.
type Clipboard struct {
	OSC52  *bool `toml:"osc52"`
	Native *bool `toml:"native"`
}

// OSC52On reports whether copies are sent as OSC 52; the default is on.
func (c Clipboard) OSC52On() bool { return c.OSC52 == nil || *c.OSC52 }

// NativeOn reports whether copies also go to native clipboard tools on a
// local session; the default is on.
func (c Clipboard) NativeOn() bool { return c.Native == nil || *c.Native }

// IconsOn reports whether Nerd Font glyphs are drawn; the default is on.
func (c Config) IconsOn() bool {
	return c.Icons == nil || *c.Icons
}

// DefaultConfig returns the config used for keys the file does not set.
func DefaultConfig() Config {
	return Config{Theme: DefaultTheme}
}

// LoadConfig reads the config file at path over the defaults. A missing file
// gives the defaults; unknown keys are errors, so a typo never goes unnoticed.
// Values such as theme names are not validated here.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}

	cfg := DefaultConfig()
	err = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&cfg)

	// StrictMissingError unwraps to DecodeErrors, so it is matched first.
	var strictErr *toml.StrictMissingError
	var decodeErr *toml.DecodeError
	switch {
	case err == nil:
		return cfg, nil
	case errors.As(err, &strictErr):
		keys := make([]string, len(strictErr.Errors))
		for i := range strictErr.Errors {
			keys[i] = strings.Join(strictErr.Errors[i].Key(), ".")
		}
		return Config{}, fmt.Errorf("config %s: unknown key %s", path, strings.Join(keys, ", "))
	case errors.As(err, &decodeErr):
		line, _ := decodeErr.Position()
		msg := strings.TrimPrefix(decodeErr.Error(), "toml: ")
		if key := decodeErr.Key(); len(key) > 0 {
			msg = strings.Join(key, ".") + ": " + msg
		}
		return Config{}, fmt.Errorf("config %s: line %d: %s", path, line, msg)
	default:
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
}

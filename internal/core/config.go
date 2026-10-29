package core

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/mhersson/muxreveal/internal/core/tiling"
	"github.com/pelletier/go-toml/v2"
)

// DefaultTheme is the theme used when config.toml does not set one.
const DefaultTheme = "catppuccin-mocha"

// Config is the user's config.toml.
type Config struct {
	Theme string `toml:"theme"`
	// Icons is a pointer so an absent key (nil) can mean true.
	Icons     *bool           `toml:"icons"`
	Mouse     Mouse           `toml:"mouse"`
	Clipboard Clipboard       `toml:"clipboard"`
	Layout    LayoutConfig    `toml:"layout"`
	Diff      DiffConfig      `toml:"diff"`
	Worktrees WorktreesConfig `toml:"worktrees"`
}

// LayoutConfig is the [layout] section. Validate rejects an out-of-range value
// from a file; the accessors read a zero field as its default so a
// hand-built LayoutConfig behaves as the defaults.
type LayoutConfig struct {
	DefaultMode    string `toml:"default_mode"`    // bsp | master-stack | scrolling
	AutoScheme     string `toml:"auto_scheme"`     // spiral | longest_side | alternate | smart_split
	MasterRatio    int    `toml:"master_ratio"`    // percent, 10-90
	MasterPosition string `toml:"master_position"` // left | right | top | bottom | center
	MasterCount    int    `toml:"master_count"`    // 1-9
	MasterGrid     *bool  `toml:"master_grid"`     // nil means true
	ColumnWidth    int    `toml:"column_width"`    // percent, 10-100
}

// Bounds of the numeric [layout] keys.
const (
	minMasterRatio, maxMasterRatio = 10, 90
	minMasterCount, maxMasterCount = 1, 9
	minColumnWidth, maxColumnWidth = 10, 100
)

var layoutSchemes = []string{"spiral", "longest_side", "alternate", "smart_split"}

// Validate names the first key that is out of range or unknown.
func (c LayoutConfig) Validate() error {
	if !slices.Contains([]string{string(ModeBSP), string(ModeMaster), string(ModeScrolling)}, c.DefaultMode) {
		return fmt.Errorf("layout.default_mode: %q is not bsp, master-stack or scrolling", c.DefaultMode)
	}

	if !slices.Contains(layoutSchemes, c.AutoScheme) {
		return fmt.Errorf("layout.auto_scheme: %q is not %s", c.AutoScheme, strings.Join(layoutSchemes, ", "))
	}

	if err := inRange("layout.master_ratio", c.MasterRatio, minMasterRatio, maxMasterRatio); err != nil {
		return err
	}

	if !slices.Contains(tiling.MasterPositions(), c.MasterPosition) {
		return fmt.Errorf("layout.master_position: %q is not %s", c.MasterPosition, strings.Join(tiling.MasterPositions(), ", "))
	}

	if err := inRange("layout.master_count", c.MasterCount, minMasterCount, maxMasterCount); err != nil {
		return err
	}

	return inRange("layout.column_width", c.ColumnWidth, minColumnWidth, maxColumnWidth)
}

func inRange(key string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s: %d is out of range %d-%d", key, v, lo, hi)
	}

	return nil
}

// Mode is the mode new tabs start in.
func (c LayoutConfig) Mode() LayoutMode {
	if c.DefaultMode == "" {
		return ModeBSP
	}

	return LayoutMode(c.DefaultMode)
}

// Scheme is the auto split scheme for BSP.
func (c LayoutConfig) Scheme() tiling.AutoScheme {
	if c.AutoScheme == "" {
		return tiling.SchemeSpiral
	}

	return tiling.ParseAutoScheme(c.AutoScheme)
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}

	return v
}

// ratio is the masters' share of the body, 0.1-0.9.
func (c LayoutConfig) ratio() float64 {
	return float64(min(max(orDefault(c.MasterRatio, 50), minMasterRatio), maxMasterRatio)) / 100
}

func (c LayoutConfig) count() int { return orDefault(c.MasterCount, 1) }

func (c LayoutConfig) position() string {
	if c.MasterPosition == "" {
		return tiling.MasterPositionLeft
	}

	return c.MasterPosition
}

func (c LayoutConfig) grid() bool { return c.MasterGrid == nil || *c.MasterGrid }

// columnWidth is a new scrolling column's share of the body, 0.1-1.0.
func (c LayoutConfig) columnWidth() float64 {
	return float64(min(max(orDefault(c.ColumnWidth, 50), minColumnWidth), maxColumnWidth)) / 100
}

// DiffConfig is the [diff] section. Validate rejects an unknown name or a
// non-positive limit from a file. A hand-built DiffConfig reads a zero Layout
// as unified and a zero Mode as turn first; DefaultConfig sets branch.
type DiffConfig struct {
	Layout   string `toml:"layout"`    // unified | split: the layout a drawer starts in
	Mode     string `toml:"mode"`      // turn | branch: what a new drawer opens in
	MaxFiles int    `toml:"max_files"` // the engine's limits
	MaxBytes int    `toml:"max_bytes"`
	MaxLines int    `toml:"max_lines"`
}

// Validate names the first key that is unknown or not positive.
func (c DiffConfig) Validate() error {
	if c.Layout != "unified" && c.Layout != "split" {
		return fmt.Errorf("diff.layout: %q is not unified or split", c.Layout)
	}

	if c.Mode != string(ReviewTurn) && c.Mode != string(ReviewBranch) {
		return fmt.Errorf("diff.mode: %q is not turn or branch", c.Mode)
	}

	for _, l := range []struct {
		key string
		n   int
	}{{"diff.max_files", c.MaxFiles}, {"diff.max_bytes", c.MaxBytes}, {"diff.max_lines", c.MaxLines}} {
		if l.n <= 0 {
			return fmt.Errorf("%s: %d is not a positive number", l.key, l.n)
		}
	}

	return nil
}

// Split reports whether a new drawer starts side by side.
func (c DiffConfig) Split() bool { return c.Layout == "split" }

// BranchFirst reports whether a new drawer opens in branch mode even when the
// pane has turns.
func (c DiffConfig) BranchFirst() bool { return c.Mode == string(ReviewBranch) }

// WorktreesConfig is the [worktrees] section. Validate rejects an unknown root
// from a file; Sibling reads a zero field as the default, so a hand-built
// WorktreesConfig behaves as the defaults.
type WorktreesConfig struct {
	Root string `toml:"root"` // inside | sibling: where a new worktree goes
}

// Validate names the key when the root is unknown.
func (c WorktreesConfig) Validate() error {
	if c.Root != "inside" && c.Root != "sibling" {
		return fmt.Errorf("worktrees.root: %q is not inside or sibling", c.Root)
	}

	return nil
}

// Sibling reports whether a new worktree goes beside the main checkout rather
// than inside it. internal/git owns the layouts, so the UI maps this to its
// root style.
func (c WorktreesConfig) Sibling() bool { return c.Root == "sibling" }

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
	return Config{
		Theme: DefaultTheme,
		Layout: LayoutConfig{
			DefaultMode: string(ModeBSP), AutoScheme: "spiral", MasterRatio: 50, MasterPosition: tiling.MasterPositionLeft,
			MasterCount: 1, MasterGrid: new(true), ColumnWidth: 50,
		},
		Diff:      DiffConfig{Layout: "unified", Mode: string(ReviewBranch), MaxFiles: 400, MaxBytes: 2 << 20, MaxLines: 5000},
		Worktrees: WorktreesConfig{Root: "inside"},
	}
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
	var (
		strictErr *toml.StrictMissingError
		decodeErr *toml.DecodeError
	)
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

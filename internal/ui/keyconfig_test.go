package ui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
)

// leaderKeys builds a [keys] config with the leader entries in the given order.
func leaderKeys(pairs ...string) core.KeysConfig {
	cfg := core.KeysConfig{Leader: map[string]string{}, Order: map[string][]string{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		cfg.Leader[pairs[i]] = pairs[i+1]
		cfg.Order["leader"] = append(cfg.Order["leader"], pairs[i])
	}

	return cfg
}

func testRegistry(t *testing.T) *registry {
	t.Helper()

	reg, err := newRegistry(defaultCommands())
	if err != nil {
		t.Fatal(err)
	}

	return reg
}

func TestResolveLeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   core.KeysConfig
		check func(t *testing.T, km keymap)
	}{
		{
			"moved key", leaderKeys("w", "pr.list", "g l", "none"),
			func(t *testing.T, km keymap) {
				if got, _ := km.lookup("w"); got != "pr.list" {
					t.Errorf("lookup(w) = %q, want pr.list", got)
				}

				if got, ok := km.lookup("g l"); ok {
					t.Errorf("lookup(g l) = %q, want it unbound", got)
				}

				if got, _ := km.keyFor("pr.list"); got != "ctrl+space w" {
					t.Errorf("keyFor = %q, want ctrl+space w", got)
				}

				if got := km.groupOf("pr.list"); got != "Git" {
					t.Errorf("group = %q, want Git", got)
				}
			},
		},
		{
			"added key", leaderKeys("w", "pr.list"),
			func(t *testing.T, km keymap) {
				if got, _ := km.keyFor("pr.list"); got != "ctrl+space w" {
					t.Errorf("keyFor = %q, want ctrl+space w", got)
				}

				if want := []string{"w", "g l"}; !slices.Equal(km.keysOf("pr.list"), want) {
					t.Errorf("keysOf = %q, want %q", km.keysOf("pr.list"), want)
				}

				for _, key := range []string{"w", "g l"} {
					if got, _ := km.lookup(key); got != "pr.list" {
						t.Errorf("lookup(%s) = %q, want pr.list", key, got)
					}
				}
			},
		},
		{
			"key taken over", leaderKeys("z", "tab.new"),
			func(t *testing.T, km keymap) {
				if keys := km.keysOf("pane.zoom"); len(keys) != 0 {
					t.Errorf("pane.zoom keys = %q, want none", keys)
				}

				if got, _ := km.lookup("z"); got != "tab.new" {
					t.Errorf("lookup(z) = %q, want tab.new", got)
				}
			},
		},
		{
			"group by prefix", leaderKeys("b", "layout.set.bsp"),
			func(t *testing.T, km keymap) {
				if got := groupFor(km, "layout.set.bsp"); got != "Layout" {
					t.Errorf("group = %q, want Layout", got)
				}

				if got := km.groupOf("layout.set.bsp"); got != "Layout" {
					t.Errorf("binding group = %q, want Layout", got)
				}
			},
		},
		{
			"file order is kept", leaderKeys("w", "pr.list", "e", "pr.list"),
			func(t *testing.T, km keymap) {
				if want := []string{"w", "e", "g l"}; !slices.Equal(km.keysOf("pr.list"), want) {
					t.Errorf("keysOf = %q, want %q", km.keysOf("pr.list"), want)
				}
			},
		},
		{
			"leader key",
			core.KeysConfig{LeaderKey: "ctrl+a"},
			func(t *testing.T, km keymap) {
				if km.leader != "ctrl+a" {
					t.Errorf("leader = %q, want ctrl+a", km.leader)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			km, err := resolveLeader(tt.cfg, testRegistry(t))
			if err != nil {
				t.Fatal(err)
			}

			tt.check(t, km)
		})
	}
}

func TestCheckKeysErrors(t *testing.T) {
	t.Parallel()

	noGit := []string{"g l", "none", "g p", "none", "g o", "none", "g m", "none"}

	tests := []struct {
		name string
		cfg  core.KeysConfig
		want string
	}{
		{"unknown command", leaderKeys("w", "pr.lst"), `keys.leader: "w" = "pr.lst": no such command`},
		{"prefix over a key", leaderKeys("g", "pr.list"), `keys.leader: "g" is bound and starts "g l"`},
		{"key under a bound key", leaderKeys("q x", "pr.list"), `keys.leader: "q" is bound and starts "q x"`},
		{"prefix freed", leaderKeys(append([]string{"g", "pr.list"}, noGit...)...), ""},
		{
			"leader under leader",
			func() core.KeysConfig {
				c := leaderKeys("ctrl+a", "pr.list")
				c.LeaderKey = "ctrl+a"

				return c
			}(),
			`keys.leader_key: "ctrl+a" is also bound under keys.leader`,
		},
		{
			"sequence starting with the leader",
			func() core.KeysConfig {
				c := leaderKeys("ctrl+a x", "pr.list")
				c.LeaderKey = "ctrl+a"

				return c
			}(),
			`keys.leader_key: "ctrl+a" is also bound under keys.leader`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := CheckKeys(tt.cfg)
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("CheckKeys() = %v, want nil", err)
			case tt.want != "" && (err == nil || err.Error() != tt.want):
				t.Errorf("CheckKeys() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDefaultKeysUnchanged(t *testing.T) {
	t.Parallel()

	km, err := resolveLeader(core.KeysConfig{}, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(km, defaultKeymap()) {
		t.Error("an empty config changed the default keymap")
	}
}

var keyCtrlA = tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}

func TestLeaderRemapScreens(t *testing.T) {
	t.Parallel()

	cfg := leaderKeys("w", "pr.list", "g l", "none")
	cfg.LeaderKey = "ctrl+a"

	app := func(t *testing.T) *App {
		t.Helper()

		a := newKeyedApp(t, "catppuccin-mocha", cfg)
		sized(t, a, 120, 40)

		return a
	}
	text := func(a *App) string { return ansi.Strip(a.View().Content) }

	t.Run("which-key", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyCtrlA)
		a.Update(whichKeyMsg{seq: a.seq})

		plain := text(a)
		if !strings.Contains(plain, " ctrl+a ") || strings.Contains(plain, "ctrl+space") {
			t.Errorf("title lacks ctrl+a:\n%s", plain)
		}

		var git []hint

		for _, h := range a.hints() {
			if h.group == "Git" {
				git = append(git, h)
			}
		}

		want := []hint{{"g", "Push and pull requests", "Git"}, {"w", "Pull requests", "Git"}}
		if !slices.Equal(git, want) {
			t.Errorf("Git hints = %v, want %v", git, want)
		}
	})

	t.Run("sequence popup", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyCtrlA, keyRune('g'))

		var got []string
		for _, h := range a.sequenceRows("g") {
			got = append(got, h.key+"  "+h.title)
		}

		want := []string{"p  Push", "o  Open a pull request", "m  Merge pull request", "esc  Cancel"}
		if !slices.Equal(got, want) {
			t.Errorf("rows = %q, want %q", got, want)
		}

		if plain := text(a); !strings.Contains(plain, " ctrl+a g ") {
			t.Errorf("popup title lacks ctrl+a g:\n%s", plain)
		}
	})

	t.Run("moved key runs", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyCtrlA, keyRune('w'))

		if a.notice != noticePRsOff {
			t.Errorf("notice = %q, want %q", a.notice, noticePRsOff)
		}
	})

	t.Run("old leader is a plain key", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyLeader)

		if a.pending {
			t.Error("ctrl+space left a leader pending")
		}
	})

	t.Run("unbound key", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyCtrlA, keyRune('Z'))

		if a.notice != "ctrl+a Z is not bound" {
			t.Errorf("notice = %q", a.notice)
		}
	})

	t.Run("freed sequence key", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyCtrlA, keyRune('g'), keyRune('l'))

		if a.notice != "ctrl+a g l is not bound" || a.prList != nil {
			t.Errorf("notice = %q, prList = %v, want g l unbound", a.notice, a.prList)
		}
	})

	t.Run("palette", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		a.openCommandPalette()

		for _, it := range a.overlay.items {
			if it.ID == "pr.list" && it.Hint != "ctrl+a w" {
				t.Errorf("Pull requests hint = %q, want ctrl+a w", it.Hint)
			}
		}
	})

	t.Run("welcome card", func(t *testing.T) {
		t.Parallel()

		if plain := text(app(t)); !strings.Contains(plain, "ctrl+a :") {
			t.Errorf("welcome card lacks ctrl+a ::\n%s", plain)
		}
	})
}

func TestSequenceFromBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		cfg    core.KeysConfig
		prefix string
		want   []string
	}{
		{
			"preselect clear moved", leaderKeys("; x", "pane.preselect.clear", "; esc", "none"), ";",
			[]string{"h j k l  Preselect side", "x  Clear preselection", "esc  Cancel"},
		},
		{
			"one tab key freed", leaderKeys("> 1", "none"), ">",
			[]string{
				"2  Move pane to tab 2", "3  Move pane to tab 3", "4  Move pane to tab 4", "5  Move pane to tab 5",
				"6  Move pane to tab 6", "7  Move pane to tab 7", "8  Move pane to tab 8", "9  Move pane to tab 9", "esc  Cancel",
			},
		},
		{"defaults", core.KeysConfig{}, ">", []string{"1-9  Move pane to tab N", "esc  Cancel"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newKeyedApp(t, "catppuccin-mocha", tt.cfg)

			var got []string
			for _, h := range a.sequenceRows(tt.prefix) {
				got = append(got, h.key+"  "+h.title)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("rows = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("which-key stops collapsing", func(t *testing.T) {
		t.Parallel()

		a := newKeyedApp(t, "catppuccin-mocha", leaderKeys("> 1", "none"))
		for _, h := range a.hints() {
			if h.key == "> 1-9" {
				t.Errorf("hint %v collapses a family with an unbound key", h)
			}
		}
	})

	t.Run("preselect needs a BSP tab", func(t *testing.T) {
		t.Parallel()

		a := newKeyedApp(t, "catppuccin-mocha", leaderKeys("p", "pane.preselect.left", "; h", "none"))
		sized(t, a, 120, 40)

		if !a.keymap.preselectPrefix(";") || a.keymap.preselectPrefix(">") {
			t.Error("preselectPrefix wrong for ; and >")
		}

		if err := a.dispatch(core.SetLayoutMode{Mode: core.ModeMaster, Area: a.bodyArea()}); err != nil {
			t.Fatal(err)
		}

		press(a, keyLeader, keyRune(';'))

		if a.notice != noticePreselectBSP || a.prefix != "" {
			t.Errorf("notice = %q, prefix = %q, want the BSP notice and no prefix", a.notice, a.prefix)
		}
	})
}

func TestWhichKeyDelayConfig(t *testing.T) {
	t.Parallel()

	zero := 0

	if got := newKeyedApp(t, "catppuccin-mocha", core.KeysConfig{WhichKeyDelay: &zero}).keymap.delay; got != 0 {
		t.Errorf("delay = %v, want 0", got)
	}

	if got := newTestApp(t).keymap.delay.Milliseconds(); got != 300 {
		t.Errorf("default delay = %dms, want 300", got)
	}
}

func TestPrefixRowKeepsItsGroup(t *testing.T) {
	t.Parallel()

	a := newKeyedApp(t, "catppuccin-mocha", leaderKeys("g z", "tab.new"))

	for _, h := range a.hints() {
		if h.key == "g" && h.group != "Git" {
			t.Errorf("g row group = %q, want Git", h.group)
		}
	}
}

package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mhersson/muxreveal/internal/core"
)

// CheckKeys reports the first mistake in the [keys] config that the UI can
// find: a command that does not exist, or a key that is both a key and the
// start of a sequence. main calls it before the TUI starts.
func CheckKeys(cfg core.KeysConfig) error {
	reg, err := newRegistry(defaultCommands())
	if err != nil {
		return fmt.Errorf("build command registry: %w", err)
	}

	_, err = resolveLeader(cfg, reg)

	return err
}

// resolveLeader lays the config over the built-in leader keymap. A config key
// replaces a default binding of the same key, and goes right before its
// command's first remaining default binding, else at the end of the command's
// group, so the command's first key is the first the config lists.
func resolveLeader(cfg core.KeysConfig, reg *registry) (keymap, error) {
	defaults := defaultKeymap()
	km := defaults
	km.leader = cfg.LeaderName()
	km.delay = cfg.Delay()
	km.bindings = slices.Clone(defaults.bindings)

	binds := cfg.Binds("leader")

	for _, b := range binds {
		if first, _, _ := strings.Cut(b.Key, " "); first == km.leader {
			return keymap{}, fmt.Errorf("keys.leader_key: %q is also bound under keys.leader", km.leader)
		}

		if b.Command == core.KeyNone {
			continue
		}

		if _, ok := reg.get(b.Command); !ok {
			return keymap{}, fmt.Errorf("keys.leader: %q = %q: no such command", b.Key, b.Command)
		}
	}

	km.bindings = slices.DeleteFunc(km.bindings, func(d binding) bool {
		return slices.ContainsFunc(binds, func(b core.KeyBind) bool { return b.Key == d.key })
	})

	added := make([]bool, len(km.bindings))

	for _, b := range binds {
		if b.Command == core.KeyNone {
			continue
		}

		nb := binding{key: b.Key, command: b.Command, group: groupFor(defaults, b.Command)}
		at := insertAt(km.bindings, added, nb)
		km.bindings = slices.Insert(km.bindings, at, nb)
		added = slices.Insert(added, at, true)
	}

	for _, b := range km.bindings {
		first, _, ok := strings.Cut(b.key, " ")
		if !ok {
			continue
		}

		if _, bound := km.lookup(first); bound {
			return keymap{}, fmt.Errorf("keys.leader: %q is bound and starts %q", first, b.key)
		}
	}

	return km, nil
}

// insertAt is where a config binding goes in bindings; added marks the config's
// own entries. It goes before the command's first default binding, else after
// the config's earlier keys for the command, else after its group.
func insertAt(bindings []binding, added []bool, nb binding) int {
	for i, b := range bindings {
		if !added[i] && b.command == nb.command {
			return i
		}
	}

	if i := slices.IndexFunc(bindings, func(b binding) bool { return b.command == nb.command }); i >= 0 {
		for i < len(bindings) && bindings[i].command == nb.command {
			i++
		}

		return i
	}

	for i := len(bindings) - 1; i >= 0; i-- {
		if bindings[i].group == nb.group {
			return i + 1
		}
	}

	return len(bindings)
}

// groupFor is the which-key group of a command: the group of its binding in
// km, else the group its id prefix names.
func groupFor(km keymap, id string) string {
	if g := km.groupOf(id); g != "" {
		return g
	}

	switch {
	case strings.HasPrefix(id, "layout."):
		return "Layout"
	case strings.HasPrefix(id, "tab."):
		return "Tabs"
	case strings.HasPrefix(id, "pr."), strings.HasPrefix(id, "git."), strings.HasPrefix(id, "worktree."):
		return "Git"
	}

	for _, p := range []string{"pane.", "agent.", "copy.", "rail.", "review.", "md.", "plan."} {
		if strings.HasPrefix(id, p) {
			return "Panes"
		}
	}

	return "App"
}

// keysOf returns every key a command is bound to after the leader, in order.
func (k keymap) keysOf(id string) []string {
	var keys []string

	for _, b := range k.bindings {
		if b.command == id {
			keys = append(keys, b.key)
		}
	}

	return keys
}

// preselectPrefix reports whether key opens a sequence of preselect commands,
// which work only in a BSP tab.
func (k keymap) preselectPrefix(key string) bool {
	found := false

	for _, b := range k.bindings {
		if !strings.HasPrefix(b.key, key+" ") {
			continue
		}

		if !strings.HasPrefix(b.command, "pane.preselect.") {
			return false
		}

		found = true
	}

	return found
}

package ui

import "time"

// binding maps a key pressed after the leader to a command id; group names
// the which-key section it is listed under.
type binding struct{ key, command, group string }

// keymap binds keys pressed after the leader key to commands.
type keymap struct {
	leader   string
	delay    time.Duration // leader wait before the which-key popup opens
	bindings []binding
}

// defaultKeymap returns the built-in bindings.
func defaultKeymap() keymap {
	return keymap{
		leader: "ctrl+space",
		delay:  300 * time.Millisecond,
		bindings: []binding{
			{key: ":", command: "palette.open", group: "App"},
			{key: "t", command: "theme.pick", group: "App"},
			{key: "n", command: "agent.new", group: "Panes"},
			{key: "s", command: "pane.split.down", group: "Panes"},
			{key: "v", command: "pane.split.right", group: "Panes"},
			{key: "h", command: "pane.focus.left", group: "Panes"},
			{key: "j", command: "pane.focus.down", group: "Panes"},
			{key: "k", command: "pane.focus.up", group: "Panes"},
			{key: "l", command: "pane.focus.right", group: "Panes"},
			{key: "H", command: "pane.resize.left", group: "Panes"},
			{key: "J", command: "pane.resize.down", group: "Panes"},
			{key: "K", command: "pane.resize.up", group: "Panes"},
			{key: "L", command: "pane.resize.right", group: "Panes"},
			{key: "ctrl+h", command: "pane.swap.left", group: "Panes"},
			{key: "ctrl+j", command: "pane.swap.down", group: "Panes"},
			{key: "ctrl+k", command: "pane.swap.up", group: "Panes"},
			{key: "ctrl+l", command: "pane.swap.right", group: "Panes"},
			{key: "z", command: "pane.zoom", group: "Panes"},
			{key: "x", command: "pane.close", group: "Panes"},
			{key: "c", command: "tab.new", group: "Tabs"},
			{key: ",", command: "tab.rename", group: "Tabs"},
			{key: "X", command: "tab.close", group: "Tabs"},
			{key: "tab", command: "tab.next", group: "Tabs"},
			{key: "shift+tab", command: "tab.prev", group: "Tabs"},
			{key: "1", command: "tab.jump.1", group: "Tabs"},
			{key: "2", command: "tab.jump.2", group: "Tabs"},
			{key: "3", command: "tab.jump.3", group: "Tabs"},
			{key: "4", command: "tab.jump.4", group: "Tabs"},
			{key: "5", command: "tab.jump.5", group: "Tabs"},
			{key: "6", command: "tab.jump.6", group: "Tabs"},
			{key: "7", command: "tab.jump.7", group: "Tabs"},
			{key: "8", command: "tab.jump.8", group: "Tabs"},
			{key: "9", command: "tab.jump.9", group: "Tabs"},
			{key: "q", command: "app.quit", group: "App"},
		},
	}
}

func (k keymap) lookup(key string) (string, bool) {
	for _, b := range k.bindings {
		if b.key == key {
			return b.command, true
		}
	}
	return "", false
}

// keyOf returns the key a command is bound to after the leader, e.g. "q".
func (k keymap) keyOf(command string) (string, bool) {
	for _, b := range k.bindings {
		if b.command == command {
			return b.key, true
		}
	}
	return "", false
}

// keyFor returns the full key sequence for a command, e.g. "ctrl+space q".
func (k keymap) keyFor(command string) (string, bool) {
	key, ok := k.keyOf(command)
	if !ok {
		return "", false
	}
	return k.leader + " " + key, true
}

// groupOf returns the which-key group of the binding for a command.
func (k keymap) groupOf(command string) string {
	for _, b := range k.bindings {
		if b.command == command {
			return b.group
		}
	}
	return ""
}

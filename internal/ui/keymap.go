package ui

import "time"

// binding maps a key pressed after the leader to a command id.
type binding struct{ key, command string }

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
			{key: ":", command: "palette.open"},
			{key: "t", command: "theme.pick"},
			{key: "n", command: "agent.new"},
			{key: "s", command: "pane.split.down"},
			{key: "v", command: "pane.split.right"},
			{key: "h", command: "pane.focus.left"},
			{key: "j", command: "pane.focus.down"},
			{key: "k", command: "pane.focus.up"},
			{key: "l", command: "pane.focus.right"},
			{key: "H", command: "pane.resize.left"},
			{key: "J", command: "pane.resize.down"},
			{key: "K", command: "pane.resize.up"},
			{key: "L", command: "pane.resize.right"},
			{key: "ctrl+h", command: "pane.swap.left"},
			{key: "ctrl+j", command: "pane.swap.down"},
			{key: "ctrl+k", command: "pane.swap.up"},
			{key: "ctrl+l", command: "pane.swap.right"},
			{key: "z", command: "pane.zoom"},
			{key: "x", command: "pane.close"},
			{key: "q", command: "app.quit"},
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

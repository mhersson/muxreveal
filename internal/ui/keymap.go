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
		leader:   "ctrl+space",
		delay:    300 * time.Millisecond,
		bindings: []binding{{key: "q", command: "app.quit"}},
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

// keyFor returns the full key sequence for a command, e.g. "ctrl+space q".
func (k keymap) keyFor(command string) (string, bool) {
	for _, b := range k.bindings {
		if b.command == command {
			return k.leader + " " + b.key, true
		}
	}
	return "", false
}

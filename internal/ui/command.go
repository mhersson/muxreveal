package ui

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// command is a named user action. Every action is a command so the keymap and
// the command palette reach it the same way.
type command struct {
	id, title string
	run       func(a *App) tea.Cmd
}

// registry holds commands by id and in registration order.
type registry struct {
	byID  map[string]command
	order []command
}

// newRegistry fails on an empty or duplicate id, or a command without run.
func newRegistry(cmds []command) (*registry, error) {
	r := &registry{byID: make(map[string]command, len(cmds)), order: make([]command, 0, len(cmds))}
	for _, c := range cmds {
		if c.id == "" {
			return nil, errors.New("ui: command with empty id")
		}
		if c.run == nil {
			return nil, fmt.Errorf("ui: command %q has no run function", c.id)
		}
		if _, dup := r.byID[c.id]; dup {
			return nil, fmt.Errorf("ui: duplicate command id %q", c.id)
		}
		r.byID[c.id] = c
		r.order = append(r.order, c)
	}
	return r, nil
}

func (r *registry) get(id string) (command, bool) {
	c, ok := r.byID[id]
	return c, ok
}

// all returns the commands in registration order.
func (r *registry) all() []command {
	return append([]command(nil), r.order...)
}

func defaultCommands() []command {
	cmds := []command{{
		id:    "app.quit",
		title: "Quit MuxReveal",
		run:   func(*App) tea.Cmd { return tea.Quit },
	}}
	for _, th := range theme.All() {
		cmds = append(cmds, command{
			id:    "theme.set." + th.Name,
			title: "Theme: " + th.Title,
			run: func(a *App) tea.Cmd {
				// dispatch logs its own failures.
				_ = a.dispatch(core.SetTheme{Name: th.Name})
				return nil
			},
		})
	}
	return cmds
}

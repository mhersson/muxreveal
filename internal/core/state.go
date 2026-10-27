package core

import (
	"errors"
	"maps"
)

// State is the application state; only a Store changes it. A State with no
// tabs behaves as one empty tab.
type State struct {
	Theme     string
	Tabs      []Tab // at least one once an action has run
	Active    int   // index into Tabs
	Panes     map[PaneID]Pane
	NextID    int // last pane number handed out
	NextTabID int // last tab number handed out
}

// InitialState returns the state of a fresh session: one empty tab.
func InitialState(cfg Config) State {
	return State{
		Theme:     cfg.Theme,
		Tabs:      []Tab{{ID: "t1"}},
		Panes:     map[PaneID]Pane{},
		NextTabID: 1,
	}
}

func (s State) clone() State {
	c := s
	c.Tabs = make([]Tab, len(s.Tabs))
	for i, t := range s.Tabs {
		t.Layout = t.Layout.clone()
		c.Tabs[i] = t
	}
	c.Panes = make(map[PaneID]Pane, len(s.Panes))
	maps.Copy(c.Panes, s.Panes)
	return c
}

// Action is a state change applied through Store.Dispatch.
type Action interface {
	apply(*State) error
}

// SetTheme switches the theme by name. Whether the name is a known theme is
// the caller's check, since core cannot import the theme package.
type SetTheme struct {
	Name string
}

func (a SetTheme) apply(s *State) error {
	if a.Name == "" {
		return errors.New("core: SetTheme with an empty name")
	}
	s.Theme = a.Name
	return nil
}

// Store holds the State. It is used only on the Bubble Tea goroutine, so it
// has no lock.
type Store struct {
	state State
}

// NewStore returns a Store holding initial.
func NewStore(initial State) *Store {
	return &Store{state: initial.clone()}
}

// Dispatch applies a. On error the state is unchanged.
func (s *Store) Dispatch(a Action) error {
	next := s.state.clone()
	if err := a.apply(&next); err != nil {
		return err
	}
	s.state = next
	return nil
}

// State returns a deep copy of the current state.
func (s *Store) State() State {
	return s.state.clone()
}

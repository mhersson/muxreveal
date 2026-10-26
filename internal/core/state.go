package core

import "errors"

// State is the application state; only a Store changes it.
type State struct {
	Theme string
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
	return &Store{state: initial}
}

// Dispatch applies a. On error the state is unchanged.
func (s *Store) Dispatch(a Action) error {
	next := s.state
	if err := a.apply(&next); err != nil {
		return err
	}
	s.state = next
	return nil
}

// State returns a copy of the current state.
func (s *Store) State() State {
	return s.state
}

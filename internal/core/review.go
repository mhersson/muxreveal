package core

import (
	"errors"
	"fmt"
	"slices"
)

// ReviewMode is what a tab's review drawer shows.
type ReviewMode string

// Review modes.
const (
	ReviewTurn   ReviewMode = "turn"
	ReviewBranch ReviewMode = "branch"
)

// Valid reports whether m is a known mode.
func (m ReviewMode) Valid() bool { return m == ReviewTurn || m == ReviewBranch }

// Review is a tab's open review drawer: the pane it reviews and its mode.
type Review struct {
	Pane PaneID
	Mode ReviewMode
}

// errNoReview is returned by SetReviewMode when the active tab has no drawer.
var errNoReview = errors.New("core: no review drawer is open")

// dropReview closes the drawer when it reviews id, which is leaving the tab.
func (t *Tab) dropReview(id PaneID) {
	if t.Review != nil && t.Review.Pane == id {
		t.Review = nil
	}
}

// OpenReview opens the active tab's drawer on a pane of that tab, replacing
// any open one.
type OpenReview struct {
	Pane PaneID
	Mode ReviewMode
}

func (a OpenReview) apply(s *State) error {
	t := s.cur()
	if !slices.Contains(t.Panes, a.Pane) {
		return fmt.Errorf("core: no pane %q in the active tab", a.Pane)
	}

	if !a.Mode.Valid() {
		return fmt.Errorf("core: unknown review mode %q", a.Mode)
	}

	t.Review = &Review{Pane: a.Pane, Mode: a.Mode}

	return nil
}

// CloseReview closes the active tab's drawer; with none open it does nothing.
type CloseReview struct{}

func (CloseReview) apply(s *State) error {
	s.cur().Review = nil

	return nil
}

// SetReviewMode changes the open drawer's mode.
type SetReviewMode struct {
	Mode ReviewMode
}

func (a SetReviewMode) apply(s *State) error {
	t := s.cur()
	if t.Review == nil {
		return errNoReview
	}

	if !a.Mode.Valid() {
		return fmt.Errorf("core: unknown review mode %q", a.Mode)
	}

	t.Review = &Review{Pane: t.Review.Pane, Mode: a.Mode}

	return nil
}

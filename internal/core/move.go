package core

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// Errors MovePane and BreakPane return for a move that cannot or need not
// happen; the state is unchanged.
var (
	ErrSameTab = errors.New("core: the pane is already in that tab")
	ErrNoTab   = errors.New("core: there is no such tab")
	ErrAlone   = errors.New("core: the pane is alone in its tab")
)

// maxMoveTab is the highest tab index a move can target (tab 9).
const maxMoveTab = 8

// MovePane moves a pane to the tab at a 0-based index, or to a new tab at the
// end when Tab is the tab count. It lands per the target's mode (BSP: at the
// preselection, else beside the focused pane; master-stack: appended;
// scrolling: a new column after the focused one) and becomes the target's
// focused pane. The source loses it as ClosePane would, and an emptied source
// tab is removed. Follow makes the target active; otherwise the previously
// active tab stays active, unless it was the removed source tab.
type MovePane struct {
	ID     PaneID
	Tab    int
	Follow bool
	Area   Rect
}

func (a MovePane) apply(s *State) error {
	s.normalize()
	src, ok := s.TabOf(a.ID)
	if !ok {
		return fmt.Errorf("core: no pane %q", a.ID)
	}
	if a.Tab < 0 || a.Tab > len(s.Tabs) || a.Tab > maxMoveTab {
		return ErrNoTab
	}
	if a.Tab == src {
		return ErrSameTab
	}
	lone := len(s.Tabs[src].Panes) == 1
	if lone && a.Tab == len(s.Tabs) {
		return ErrAlone
	}

	dst := a.Tab
	if dst == len(s.Tabs) {
		s.NextTabID++
		s.Tabs = append(s.Tabs, Tab{ID: TabID("t" + strconv.Itoa(s.NextTabID))})
	}
	if err := s.Tabs[dst].place(a.ID, "", a.Area, s.Layout); err != nil {
		return err
	}
	s.Tabs[dst].setFocus(a.ID)
	s.Tabs[dst].Zoomed = false

	activeID := s.Tabs[s.Active].ID
	dstID := s.Tabs[dst].ID
	if lone {
		s.Tabs = slices.Delete(s.Tabs, src, src+1)
	} else {
		t := &s.Tabs[src]
		next, err := t.remove(a.ID)
		if err != nil {
			return err
		}
		if t.Focus == a.ID {
			t.Zoomed = false
		}
		t.setFocus(next)
	}

	// Tab indices shift when the source is removed, so look them up by id.
	target, _ := s.tabIndex(dstID)
	switch active, kept := s.tabIndex(activeID); {
	case a.Follow || !kept:
		s.Active = target
	default:
		s.Active = active
	}
	return nil
}

// BreakPane moves a pane into a new tab of its own and activates it.
type BreakPane struct {
	ID   PaneID
	Area Rect
}

func (a BreakPane) apply(s *State) error {
	s.normalize()
	return MovePane{ID: a.ID, Tab: len(s.Tabs), Follow: true, Area: a.Area}.apply(s)
}

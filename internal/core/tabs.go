package core

import (
	"fmt"
	"strconv"
)

// TabID names a tab for the session: "t1", "t2", ... and never reused.
type TabID string

// Tab is one workspace: its own split layout, focus and zoom.
type Tab struct {
	ID     TabID
	Name   string // "" for the automatic name
	Layout *Node  // nil when the tab has no pane
	Focus  PaneID // "" when the tab has no pane
	Zoomed bool   // the focused pane fills the body
}

// normalize gives a State with no tabs its one empty tab.
func (s *State) normalize() {
	if len(s.Tabs) == 0 {
		s.Tabs = []Tab{{ID: "t1"}}
		s.Active = 0
		s.NextTabID = max(s.NextTabID, 1)
	}
	if s.Panes == nil {
		s.Panes = map[PaneID]Pane{}
	}
}

// cur returns the active tab for in-place change.
func (s *State) cur() *Tab {
	s.normalize()
	return &s.Tabs[s.Active]
}

// Tab returns the active tab.
func (s State) Tab() Tab {
	if len(s.Tabs) == 0 {
		return Tab{ID: "t1"}
	}
	return s.Tabs[s.Active]
}

// TabOf returns the index of the tab holding a pane.
func (s State) TabOf(id PaneID) (int, bool) {
	for i, t := range s.Tabs {
		if t.Layout.path(id) != nil {
			return i, true
		}
	}
	return 0, false
}

// TabHasAgents reports whether tab i holds an agent pane.
func (s State) TabHasAgents(i int) bool {
	if i < 0 || i >= len(s.Tabs) {
		return false
	}
	var found bool
	var walk func(*Node)
	walk = func(n *Node) {
		switch {
		case n == nil || found:
		case n.leaf():
			found = s.Panes[n.Pane].Kind == KindAgent
		default:
			walk(n.A)
			walk(n.B)
		}
	}
	walk(s.Tabs[i].Layout)
	return found
}

func (s State) tabIndex(id TabID) (int, bool) {
	for i, t := range s.Tabs {
		if t.ID == id {
			return i, true
		}
	}
	return 0, false
}

// removeTab drops tab i and its panes' metadata. The only tab stays, emptied;
// keepName keeps its custom name (a closed pane, not a closed tab).
// The active tab stays the same one, or moves left when it was removed.
func (s *State) removeTab(i int, keepName bool) {
	var drop func(*Node)
	drop = func(n *Node) {
		if n == nil {
			return
		}
		if n.leaf() {
			delete(s.Panes, n.Pane)
			return
		}
		drop(n.A)
		drop(n.B)
	}
	drop(s.Tabs[i].Layout)
	name := s.Tabs[i].Name
	if len(s.Tabs) == 1 {
		s.Tabs[0] = Tab{ID: s.Tabs[0].ID}
		if keepName {
			s.Tabs[0].Name = name
		}
		return
	}
	s.Tabs = append(s.Tabs[:i], s.Tabs[i+1:]...)
	if i <= s.Active && s.Active > 0 {
		s.Active--
	}
}

// NewTab appends a tab whose first pane is a new pane and activates it. An
// empty active tab (only possible as the only tab) is filled instead, keeping
// its ID and name.
type NewTab struct {
	Kind PaneKind
	Cwd  string
}

func (a NewTab) apply(s *State) error {
	if a.Kind != KindShell && a.Kind != KindAgent {
		return fmt.Errorf("core: NewTab with unknown kind %q", a.Kind)
	}
	s.normalize()
	id := s.NewPaneID()
	s.NextID++
	if t := s.cur(); t.Layout == nil {
		t.Layout = &Node{Pane: id}
		t.Focus = id
		t.Zoomed = false
	} else {
		s.NextTabID++
		s.Tabs = append(s.Tabs, Tab{
			ID:     TabID("t" + strconv.Itoa(s.NextTabID)),
			Layout: &Node{Pane: id},
			Focus:  id,
		})
		s.Active = len(s.Tabs) - 1
	}
	// A conversion (S1016) ties NewTab's fields to Pane's.
	s.Panes[id] = Pane(a)
	return nil
}

// CloseTab removes a tab and its panes' metadata. Closing the active tab
// activates the tab to its left, or the new first tab. The only tab stays,
// emptied.
type CloseTab struct {
	ID TabID
}

func (a CloseTab) apply(s *State) error {
	s.normalize()
	i, ok := s.tabIndex(a.ID)
	if !ok {
		return fmt.Errorf("core: no tab %q", a.ID)
	}
	s.removeTab(i, false)
	return nil
}

// RenameTab sets a tab's name; "" restores the automatic name.
type RenameTab struct {
	ID   TabID
	Name string
}

func (a RenameTab) apply(s *State) error {
	s.normalize()
	i, ok := s.tabIndex(a.ID)
	if !ok {
		return fmt.Errorf("core: no tab %q", a.ID)
	}
	s.Tabs[i].Name = a.Name
	return nil
}

// SelectTab activates the tab at a 0-based index; out of range does nothing.
type SelectTab struct {
	Index int
}

func (a SelectTab) apply(s *State) error {
	s.normalize()
	if a.Index >= 0 && a.Index < len(s.Tabs) {
		s.Active = a.Index
	}
	return nil
}

// CycleTab activates the next (+1) or previous (-1) tab, wrapping.
type CycleTab struct {
	Delta int
}

func (a CycleTab) apply(s *State) error {
	s.normalize()
	n := len(s.Tabs)
	s.Active = ((s.Active+a.Delta)%n + n) % n
	return nil
}

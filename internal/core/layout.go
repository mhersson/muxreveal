package core

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
)

// PaneID names a pane for the session: "p1", "p2", ... and never reused.
type PaneID string

// PaneKind is what a pane runs.
type PaneKind string

// Pane kinds.
const (
	KindShell PaneKind = "shell"
	KindAgent PaneKind = "agent"
)

// Split is the orientation of a split node.
type Split string

// Split orientations: vertical puts the children side by side, horizontal
// stacks them.
const (
	SplitVertical   Split = "vertical"
	SplitHorizontal Split = "horizontal"
)

// Resize bounds and step for a split's Ratio.
const (
	minRatio  = 0.1
	maxRatio  = 0.9
	ratioStep = 0.05
)

// Node is a layout tree node: a leaf when Pane is set, otherwise a split with
// children A (left or top) and B (right or bottom), A taking Ratio of the space.
type Node struct {
	Pane  PaneID
	Split Split
	Ratio float64
	A, B  *Node
}

func (n *Node) leaf() bool { return n.Pane != "" }

func (n *Node) clone() *Node {
	if n == nil {
		return nil
	}
	c := *n
	c.A, c.B = n.A.clone(), n.B.clone()
	return &c
}

func (n *Node) firstLeaf() PaneID {
	for !n.leaf() {
		n = n.A
	}
	return n.Pane
}

// path returns the nodes from n down to the leaf for id, or nil.
func (n *Node) path(id PaneID) []*Node {
	if n == nil {
		return nil
	}
	if n.leaf() {
		if n.Pane == id {
			return []*Node{n}
		}
		return nil
	}
	for _, c := range []*Node{n.A, n.B} {
		if p := c.path(id); p != nil {
			return append([]*Node{n}, p...)
		}
	}
	return nil
}

// Pane is the metadata of one pane.
type Pane struct {
	Kind       PaneKind
	Cwd        string
	Harness    string     // "claude-code" for agents; "" for shells
	Session    SessionID  // the latest session the agent reported; "" for none
	Transcript string     // that session's transcript file, as the harness reported it
	State      AgentState // not persisted
}

// Dir is a direction for focus, resize and preselection. The zero value is
// NoDir, so a Tab literal has no preselection.
type Dir int

// Directions, in the order of the h j k l keys.
const (
	NoDir Dir = iota
	Left
	Down
	Up
	Right
)

// NewPaneID returns the ID the next OpenPane or NewTab will use.
func (s State) NewPaneID() PaneID {
	return PaneID("p" + strconv.Itoa(s.NextID+1))
}

// OpenPane puts a new pane beside the active tab's focused pane and focuses
// it; in BSP a split divides the focused pane with the new pane as child B. The
// first pane of a tab ignores Split. Area is the inner rect the UI lays out in;
// a zero Area decides auto splits as on a wide screen. Harness is kept only
// for an agent.
type OpenPane struct {
	Kind    PaneKind
	Cwd     string
	Split   Split
	Harness string
	Area    Rect
}

func (a OpenPane) apply(s *State) error {
	if len(s.Panes) >= MaxPanes {
		return ErrTooManyPanes
	}
	if a.Kind != KindShell && a.Kind != KindAgent {
		return fmt.Errorf("core: OpenPane with unknown kind %q", a.Kind)
	}
	id := s.NewPaneID()
	t := s.cur()
	if !t.Empty() && a.Split != SplitVertical && a.Split != SplitHorizontal {
		return fmt.Errorf("core: OpenPane with unknown split %q", a.Split)
	}
	if err := t.place(id, a.Split, a.Area, s.Layout); err != nil {
		return err
	}
	s.NextID++
	p := Pane{Kind: a.Kind, Cwd: a.Cwd}
	if a.Kind == KindAgent {
		p.Harness = a.Harness
	}
	s.Panes[id] = p
	t.setFocus(id)
	t.Zoomed = false
	return nil
}

// ClosePane removes a pane from its own tab, giving its space to the rest.
// Closing the focused pane focuses the pane that took its place. The last
// pane of a tab removes the tab unless it is the only one.
type ClosePane struct {
	ID PaneID
}

func (a ClosePane) apply(s *State) error {
	s.normalize()
	i, ok := s.TabOf(a.ID)
	if !ok {
		return fmt.Errorf("core: no pane %q", a.ID)
	}
	t := &s.Tabs[i]
	if len(t.Panes) == 1 {
		s.removeTab(i, true)
		return nil
	}
	next, err := t.remove(a.ID)
	if err != nil {
		return err
	}
	delete(s.Panes, a.ID)
	t.Zoomed = false
	t.setFocus(next)
	return nil
}

// FocusPane focuses a pane.
type FocusPane struct {
	ID PaneID
}

func (a FocusPane) apply(s *State) error {
	t := s.cur()
	if !slices.Contains(t.Panes, a.ID) {
		return fmt.Errorf("core: no pane %q", a.ID)
	}
	if a.ID != t.Focus {
		t.setFocus(a.ID)
		t.Zoomed = false
	}
	return nil
}

// FocusDir focuses the pane Neighbour picks on the tiles Arrange lays out in
// Area; a zero Area uses a fixed large rect. It does nothing at an edge.
type FocusDir struct {
	Dir  Dir
	Area Rect
}

func (a FocusDir) apply(s *State) error {
	t := s.cur()
	if id, ok := neighbour(*t, a.Dir, a.Area, s.Layout); ok {
		t.setFocus(id)
		t.Zoomed = false
	}
	return nil
}

// SwapDir exchanges the focused pane with its FocusDir neighbour. In BSP the
// tree keeps its shape and ratios, so each position keeps its size; focus
// stays on the moved pane. It does nothing at an edge.
type SwapDir struct {
	Dir  Dir
	Area Rect
}

func (a SwapDir) apply(s *State) error {
	t := s.cur()
	other, ok := neighbour(*t, a.Dir, a.Area, s.Layout)
	if !ok {
		return nil
	}
	if err := t.swap(t.Focus, other); err != nil {
		return err
	}
	t.Zoomed = false
	t.keepVisible()
	return nil
}

// ResizeDir moves the nearest BSP divider on the direction's axis by one step:
// Right and Down grow the first child, Left and Up shrink it. It does nothing
// when the focused pane has no split on that axis. Master-stack moves the
// master ratio and scrolling the focused column's width.
type ResizeDir struct {
	Dir Dir
}

func (a ResizeDir) apply(s *State) error {
	axis, delta := SplitHorizontal, ratioStep
	switch a.Dir {
	case Left, Right:
		axis = SplitVertical
	}
	switch a.Dir {
	case Left, Up:
		delta = -ratioStep
	}
	t := s.cur()
	switch t.Mode {
	case ModeMaster:
		t.resizeMaster(a.Dir, s.Layout)
		return nil
	case ModeScrolling:
		t.resizeColumn(a.Dir)
		return nil
	}
	p := t.Layout.path(t.Focus)
	for i := len(p) - 2; i >= 0; i-- {
		if p[i].Split == axis {
			r := math.Round((p[i].Ratio+delta)*100) / 100
			p[i].Ratio = min(max(r, minRatio), maxRatio)
			return nil
		}
	}
	return nil
}

// Errors the layout actions return when the tab's mode or shape does not fit.
var (
	ErrNeedsBSP        = errors.New("core: needs a BSP layout")
	ErrNeedsMaster     = errors.New("core: needs a master-stack layout")
	ErrNothingToRotate = errors.New("core: nothing to rotate")
)

func (t Tab) isBSP() bool { return t.Mode == ModeBSP || t.Mode == "" }

// Preselect makes the next pane opened in the active BSP tab go to the given
// side of the focused pane at ratio 0.5. Focus moves keep it; a mode switch
// and the pane it places clear it.
type Preselect struct {
	Dir Dir
}

func (a Preselect) apply(s *State) error {
	t := s.cur()
	if !t.isBSP() {
		return ErrNeedsBSP
	}
	if _, ok := sideOf(a.Dir); !ok {
		return fmt.Errorf("core: Preselect with no direction")
	}
	t.Preselect = a.Dir
	return nil
}

// ClearPreselect drops the active BSP tab's preselection, set or not.
type ClearPreselect struct{}

func (ClearPreselect) apply(s *State) error {
	t := s.cur()
	if !t.isBSP() {
		return ErrNeedsBSP
	}
	t.Preselect = NoDir
	return nil
}

// RotateSplit flips the axis of the focused pane's parent split in BSP and
// keeps its ratio. A lone pane has nothing to rotate.
type RotateSplit struct{}

func (RotateSplit) apply(s *State) error {
	t := s.cur()
	if !t.isBSP() {
		return ErrNeedsBSP
	}
	p := t.Layout.path(t.Focus)
	if len(p) < 2 {
		return ErrNothingToRotate
	}
	parent := p[len(p)-2]
	if parent.Split == SplitVertical {
		parent.Split = SplitHorizontal
	} else {
		parent.Split = SplitVertical
	}
	t.Zoomed = false
	return nil
}

// Equalize evens out the active tab's layout: BSP panes become equal along
// each axis, master-stack goes back to master_ratio and every scrolling column
// to column_width. It changes nothing for fewer than two panes.
type Equalize struct{}

func (Equalize) apply(s *State) error {
	t := s.cur()
	if len(t.Panes) < 2 {
		return nil
	}
	switch t.Mode {
	case ModeMaster:
		t.Master = s.Layout.ratio()
	case ModeScrolling:
		for i := range t.Columns {
			t.Columns[i].Width = s.Layout.columnWidth()
		}
		t.keepVisible()
	default:
		equalizeNode(t.Layout)
	}
	t.Zoomed = false
	return nil
}

// Promote swaps the focused pane with the first pane of a master-stack tab, or
// with the second when it is first, and focuses the pane now first.
type Promote struct{}

func (Promote) apply(s *State) error {
	t := s.cur()
	if t.Mode != ModeMaster {
		return ErrNeedsMaster
	}
	if len(t.Panes) < 2 {
		return nil
	}
	i := slices.Index(t.Panes, t.Focus)
	if i < 0 {
		return fmt.Errorf("core: focused pane %q is not in the layout", t.Focus)
	}
	other := t.Panes[0]
	if i == 0 {
		other = t.Panes[1]
	}
	if err := t.swapMaster(t.Focus, other); err != nil {
		return err
	}
	t.setFocus(t.Panes[0])
	t.Zoomed = false
	return nil
}

// ToggleZoom zooms or unzooms the active tab's focused pane.
type ToggleZoom struct{}

func (ToggleZoom) apply(s *State) error {
	t := s.cur()
	if t.Focus == "" {
		return nil
	}
	t.Zoomed = !t.Zoomed
	return nil
}

// SetPaneCwd records a pane's working directory.
type SetPaneCwd struct {
	ID  PaneID
	Cwd string
}

func (a SetPaneCwd) apply(s *State) error {
	p, ok := s.Panes[a.ID]
	if !ok {
		return fmt.Errorf("core: no pane %q", a.ID)
	}
	p.Cwd = a.Cwd
	s.Panes[a.ID] = p
	return nil
}

package core

import (
	"fmt"
	"math"
	"sort"
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

// Dir is a direction for focus and resize.
type Dir int

// Directions, in the order of the h j k l keys.
const (
	Left Dir = iota
	Down
	Up
	Right
)

// NewPaneID returns the ID the next OpenPane or NewTab will use.
func (s State) NewPaneID() PaneID {
	return PaneID("p" + strconv.Itoa(s.NextID+1))
}

// OpenPane splits the active tab's focused pane: A keeps it, B is the new
// pane. The first pane of a tab becomes the root and ignores Split. The new
// pane gets focus. Harness is kept only for an agent.
type OpenPane struct {
	Kind    PaneKind
	Cwd     string
	Split   Split
	Harness string
}

func (a OpenPane) apply(s *State) error {
	if a.Kind != KindShell && a.Kind != KindAgent {
		return fmt.Errorf("core: OpenPane with unknown kind %q", a.Kind)
	}
	id := s.NewPaneID()
	fresh := &Node{Pane: id}
	t := s.cur()
	if t.Layout == nil {
		t.Layout = fresh
	} else {
		if a.Split != SplitVertical && a.Split != SplitHorizontal {
			return fmt.Errorf("core: OpenPane with unknown split %q", a.Split)
		}
		p := t.Layout.path(t.Focus)
		if p == nil {
			return fmt.Errorf("core: focused pane %q is not in the layout", t.Focus)
		}
		target := p[len(p)-1]
		*target = Node{Split: a.Split, Ratio: 0.5, A: &Node{Pane: target.Pane}, B: fresh}
	}
	s.NextID++
	p := Pane{Kind: a.Kind, Cwd: a.Cwd}
	if a.Kind == KindAgent {
		p.Harness = a.Harness
	}
	s.Panes[id] = p
	t.Focus = id
	t.Zoomed = false
	return nil
}

// ClosePane removes a pane from its own tab; its sibling takes the parent's
// place. Closing the focused pane focuses the first leaf of the sibling. The
// last pane of a tab removes the tab unless it is the only one.
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
	p := t.Layout.path(a.ID)
	if len(p) == 1 {
		s.removeTab(i, true)
		return nil
	}
	delete(s.Panes, a.ID)
	t.Zoomed = false
	parent := p[len(p)-2]
	sibling := parent.A
	if sibling == p[len(p)-1] {
		sibling = parent.B
	}
	*parent = *sibling
	if t.Focus == a.ID {
		t.Focus = parent.firstLeaf()
	}
	return nil
}

// FocusPane focuses a pane.
type FocusPane struct {
	ID PaneID
}

func (a FocusPane) apply(s *State) error {
	t := s.cur()
	if t.Layout.path(a.ID) == nil {
		return fmt.Errorf("core: no pane %q", a.ID)
	}
	if a.ID != t.Focus {
		t.Focus = a.ID
		t.Zoomed = false
	}
	return nil
}

// FocusDir focuses the nearest pane in a direction: least edge distance, then
// most perpendicular overlap, then top-most or left-most. It does nothing at
// an edge.
type FocusDir struct {
	Dir Dir
}

func (a FocusDir) apply(s *State) error {
	t := s.cur()
	if id, ok := neighbour(*t, a.Dir); ok {
		t.Focus = id
		t.Zoomed = false
	}
	return nil
}

// neighbour finds the pane nearest the tab's focused one in a direction.
func neighbour(t Tab, d Dir) (PaneID, bool) {
	rects := Rects(t.Layout, Rect{0, 0, 10000, 10000})
	cur, ok := rects[t.Focus]
	if !ok {
		return "", false
	}
	type cand struct {
		id            PaneID
		dist, overlap int
		pos           int
	}
	var cands []cand
	for id, r := range rects {
		if id == t.Focus {
			continue
		}
		var dist, overlap, pos int
		switch d {
		case Left:
			dist, overlap, pos = cur.X-(r.X+r.W), span(cur.Y, cur.H, r.Y, r.H), r.Y
		case Right:
			dist, overlap, pos = r.X-(cur.X+cur.W), span(cur.Y, cur.H, r.Y, r.H), r.Y
		case Up:
			dist, overlap, pos = cur.Y-(r.Y+r.H), span(cur.X, cur.W, r.X, r.W), r.X
		case Down:
			dist, overlap, pos = r.Y-(cur.Y+cur.H), span(cur.X, cur.W, r.X, r.W), r.X
		}
		if dist >= 0 && overlap > 0 {
			cands = append(cands, cand{id, dist, overlap, pos})
		}
	}
	if len(cands) == 0 {
		return "", false
	}
	sort.Slice(cands, func(i, j int) bool {
		x, y := cands[i], cands[j]
		switch {
		case x.dist != y.dist:
			return x.dist < y.dist
		case x.overlap != y.overlap:
			return x.overlap > y.overlap
		case x.pos != y.pos:
			return x.pos < y.pos
		}
		return x.id < y.id
	})
	return cands[0].id, true
}

// SwapDir exchanges the focused pane with its FocusDir neighbour. The tree
// keeps its shape and ratios, so each position keeps its size; focus stays on
// the moved pane. It does nothing at an edge.
type SwapDir struct {
	Dir Dir
}

func (a SwapDir) apply(s *State) error {
	t := s.cur()
	other, ok := neighbour(*t, a.Dir)
	if !ok {
		return nil
	}
	from, to := t.Layout.path(t.Focus), t.Layout.path(other)
	if from == nil || to == nil {
		return fmt.Errorf("core: swap between %q and %q: pane not in the layout", t.Focus, other)
	}
	from[len(from)-1].Pane, to[len(to)-1].Pane = other, t.Focus
	t.Zoomed = false
	return nil
}

// span is the overlap of [a, a+al) and [b, b+bl).
func span(a, al, b, bl int) int {
	return max(min(a+al, b+bl)-max(a, b), 0)
}

// ResizeDir moves the nearest divider on the direction's axis by one step:
// Right and Down grow the first child, Left and Up shrink it. It does nothing
// when the focused pane has no split on that axis.
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

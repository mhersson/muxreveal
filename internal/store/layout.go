package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/mhersson/muxreveal/internal/core"
)

// maxLayoutDepth bounds the recursion on a hostile tree; a real tab needs one
// level per pane.
const maxLayoutDepth = 1024

// Ratio bounds, as core keeps them for resizing.
const (
	minRatio = 0.1
	maxRatio = 0.9
)

// layoutJSON is the file form of a layout node: a leaf sets Pane, a split sets
// Split, Ratio and two Children. It is the only place that knows this shape.
type layoutJSON struct {
	Pane     string        `json:"pane,omitempty"`
	Split    string        `json:"split,omitempty"`
	Ratio    *float64      `json:"ratio,omitempty"`
	Children []*layoutJSON `json:"children,omitempty"`
}

// normRatio clamps a split ratio and rounds it to 0.01.
func normRatio(r float64) float64 {
	return math.Round(min(max(r, minRatio), maxRatio)*100) / 100
}

func validSplit(s core.Split) bool {
	return s == core.SplitVertical || s == core.SplitHorizontal
}

// encodeLayout returns the JSON of a layout tree, or null for none.
func encodeLayout(n *core.Node) (json.RawMessage, error) {
	if n == nil {
		return json.RawMessage("null"), nil
	}
	j, err := nodeToJSON(n, 0)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return nil, fmt.Errorf("encoding layout: %w", err)
	}
	return raw, nil
}

func nodeToJSON(n *core.Node, depth int) (*layoutJSON, error) {
	if depth > maxLayoutDepth {
		return nil, errors.New("layout is nested too deeply")
	}
	if n == nil {
		return nil, errors.New("layout node is missing")
	}
	if n.Pane != "" {
		return &layoutJSON{Pane: string(n.Pane)}, nil
	}
	if !validSplit(n.Split) {
		return nil, fmt.Errorf("layout split %q is not vertical or horizontal", n.Split)
	}
	if math.IsNaN(n.Ratio) || math.IsInf(n.Ratio, 0) {
		return nil, errors.New("layout ratio is not a finite number")
	}
	a, err := nodeToJSON(n.A, depth+1)
	if err != nil {
		return nil, err
	}
	b, err := nodeToJSON(n.B, depth+1)
	if err != nil {
		return nil, err
	}
	r := normRatio(n.Ratio)
	return &layoutJSON{Split: string(n.Split), Ratio: &r, Children: []*layoutJSON{a, b}}, nil
}

// decodeLayout reads a layout tree whose leaves are exactly panes, each once.
// An absent or null layout is valid only for no panes.
func decodeLayout(raw json.RawMessage, panes []core.PaneID) (*core.Node, error) {
	want := make(map[core.PaneID]bool, len(panes))
	for _, id := range panes {
		if want[id] {
			return nil, fmt.Errorf("pane %q listed twice", id)
		}
		want[id] = true
	}
	if t := bytes.TrimSpace(raw); len(t) == 0 || bytes.Equal(t, []byte("null")) {
		if len(panes) > 0 {
			return nil, errors.New("tab has panes but no layout")
		}
		return nil, nil
	}
	var j layoutJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("parsing layout: %w", err)
	}
	seen := make(map[core.PaneID]bool, len(panes))
	root, err := nodeFromJSON(&j, 0, want, seen)
	if err != nil {
		return nil, err
	}
	if len(seen) != len(want) {
		return nil, fmt.Errorf("layout has %d panes, tab has %d", len(seen), len(want))
	}
	return root, nil
}

func nodeFromJSON(j *layoutJSON, depth int, want, seen map[core.PaneID]bool) (*core.Node, error) {
	if depth > maxLayoutDepth {
		return nil, errors.New("layout is nested too deeply")
	}
	if j == nil {
		return nil, errors.New("layout node is null")
	}
	if j.Pane != "" {
		if j.Split != "" || j.Ratio != nil || len(j.Children) > 0 {
			return nil, fmt.Errorf("layout leaf %q has split fields", j.Pane)
		}
		id := core.PaneID(j.Pane)
		if !want[id] {
			return nil, fmt.Errorf("layout names pane %q outside the tab", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("layout names pane %q twice", id)
		}
		seen[id] = true
		return &core.Node{Pane: id}, nil
	}
	split := core.Split(j.Split)
	if !validSplit(split) {
		return nil, fmt.Errorf("layout split %q is not vertical or horizontal", j.Split)
	}
	if j.Ratio == nil || math.IsNaN(*j.Ratio) || math.IsInf(*j.Ratio, 0) {
		return nil, errors.New("layout split has no finite ratio")
	}
	if len(j.Children) != 2 {
		return nil, fmt.Errorf("layout split has %d children, want 2", len(j.Children))
	}
	a, err := nodeFromJSON(j.Children[0], depth+1, want, seen)
	if err != nil {
		return nil, err
	}
	b, err := nodeFromJSON(j.Children[1], depth+1, want, seen)
	if err != nil {
		return nil, err
	}
	return &core.Node{Split: split, Ratio: normRatio(*j.Ratio), A: a, B: b}, nil
}

// defaultLayout puts panes side by side in a chain of vertical splits where
// pane i takes 1/(n-i) of what is left. Ratios stay within [0.1, 0.9], so the
// columns are equal up to 10 panes and each split keeps at least 0.1.
func defaultLayout(panes []core.PaneID) *core.Node {
	switch len(panes) {
	case 0:
		return nil
	case 1:
		return &core.Node{Pane: panes[0]}
	}
	return &core.Node{
		Split: core.SplitVertical,
		Ratio: normRatio(1 / float64(len(panes))),
		A:     &core.Node{Pane: panes[0]},
		B:     defaultLayout(panes[1:]),
	}
}

// leafOrder lists a tree's panes, first leaf first.
func leafOrder(n *core.Node) []core.PaneID {
	switch {
	case n == nil:
		return nil
	case n.Pane != "":
		return []core.PaneID{n.Pane}
	}
	return append(leafOrder(n.A), leafOrder(n.B)...)
}

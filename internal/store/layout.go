package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"

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

// columnJSON is the file form of a scrolling column. Width stays raw so a
// value that is not a number defaults only its tab.
type columnJSON struct {
	Panes []string        `json:"panes"`
	Width json.RawMessage `json:"width"`
}

type columnOut struct {
	Panes []string `json:"panes"`
	Width float64  `json:"width"`
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// normWidth clamps a column width to 0.1-1.0 and rounds it to 0.01.
func normWidth(w float64) float64 {
	return math.Round(min(max(w, minRatio), 1)*100) / 100
}

// finiteNumber reads raw as a finite JSON number; ok is false for anything
// else, including null and strings.
func finiteNumber(raw json.RawMessage) (float64, bool) {
	var v float64
	if json.Unmarshal(raw, &v) != nil || !finite(v) || isNull(raw) {
		return 0, false
	}

	return v, true
}

func encodeColumns(cols []core.Column) ([]columnOut, error) {
	out := make([]columnOut, 0, len(cols))
	for _, c := range cols {
		if !finite(c.Width) {
			return nil, errors.New("column width is not a finite number")
		}

		ids := make([]string, len(c.Panes))
		for i, id := range c.Panes {
			ids[i] = string(id)
		}

		out = append(out, columnOut{Panes: ids, Width: normWidth(c.Width)})
	}

	return out, nil
}

// decodeColumns reads scrolling columns that hold exactly panes, in order.
func decodeColumns(raw json.RawMessage, panes []core.PaneID) ([]core.Column, error) {
	var in []columnJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("parsing columns: %w", err)
	}

	cols := make([]core.Column, 0, len(in))

	var flat []core.PaneID

	for i, c := range in {
		if len(c.Panes) == 0 {
			return nil, fmt.Errorf("column %d is empty", i)
		}

		w, ok := finiteNumber(c.Width)
		if !ok {
			return nil, fmt.Errorf("column %d width is not a finite number", i)
		}

		col := core.Column{Width: normWidth(w)}
		for _, s := range c.Panes {
			col.Panes = append(col.Panes, core.PaneID(s))
		}

		flat = append(flat, col.Panes...)
		cols = append(cols, col)
	}

	if !slices.Equal(flat, panes) {
		return nil, errors.New("columns do not hold the tab's panes in order")
	}

	return cols, nil
}

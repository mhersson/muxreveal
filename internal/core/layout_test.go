package core

import (
	"maps"
	"testing"
)

func leaf(id PaneID) *Node { return &Node{Pane: id} }

func vsplit(a, b *Node) *Node {
	return &Node{Split: SplitVertical, Ratio: 0.5, A: a, B: b}
}

func hsplit(a, b *Node) *Node {
	return &Node{Split: SplitHorizontal, Ratio: 0.5, A: a, B: b}
}

// show renders a tree compactly: p1, v(p1,p2), h(p1,v(p2,p3)).
func show(n *Node) string {
	switch {
	case n == nil:
		return "-"
	case n.leaf():
		return string(n.Pane)
	case n.Split == SplitVertical:
		return "v(" + show(n.A) + "," + show(n.B) + ")"
	}
	return "h(" + show(n.A) + "," + show(n.B) + ")"
}

func leaves(n *Node, f func(PaneID)) {
	if n == nil {
		return
	}
	if n.leaf() {
		f(n.Pane)
		return
	}
	leaves(n.A, f)
	leaves(n.B, f)
}

func stateOf(layout *Node, focus PaneID) State {
	s := InitialState(DefaultConfig())
	s.Layout, s.Focus = layout, focus
	leaves(layout, func(id PaneID) {
		s.Panes[id] = Pane{Kind: KindShell}
		s.NextID++
	})
	return s
}

func dispatch(t *testing.T, s State, a Action) State {
	t.Helper()
	st := NewStore(s)
	if err := st.Dispatch(a); err != nil {
		t.Fatalf("Dispatch(%T) error = %v", a, err)
	}
	return st.State()
}

func TestInitialState(t *testing.T) {
	s := InitialState(Config{Theme: "catppuccin-latte"})
	if s.Theme != "catppuccin-latte" {
		t.Errorf("Theme = %q", s.Theme)
	}
	if s.Layout != nil || s.Focus != "" || len(s.Panes) != 0 || s.Zoomed {
		t.Errorf("state not empty: %+v", s)
	}
}

func TestOpenPane(t *testing.T) {
	tests := []struct {
		name      string
		from      State
		action    OpenPane
		wantTree  string
		wantFocus PaneID
	}{
		{"empty layout", stateOf(nil, ""), OpenPane{Kind: KindShell, Cwd: "/w"}, "p1", "p1"},
		{"side by side", stateOf(leaf("p1"), "p1"), OpenPane{Kind: KindShell, Split: SplitVertical}, "v(p1,p2)", "p2"},
		{"stacked", stateOf(leaf("p1"), "p1"), OpenPane{Kind: KindAgent, Split: SplitHorizontal}, "h(p1,p2)", "p2"},
		{"splits the focused pane", stateOf(vsplit(leaf("p1"), leaf("p2")), "p2"), OpenPane{Kind: KindShell, Split: SplitHorizontal}, "v(p1,h(p2,p3))", "p3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.from.Zoomed = tt.from.Layout != nil
			got := dispatch(t, tt.from, tt.action)
			if show(got.Layout) != tt.wantTree {
				t.Errorf("tree = %s, want %s", show(got.Layout), tt.wantTree)
			}
			if got.Focus != tt.wantFocus {
				t.Errorf("Focus = %q, want %q", got.Focus, tt.wantFocus)
			}
			if got.Zoomed {
				t.Error("zoom not cleared")
			}
			if p := got.Panes[tt.wantFocus]; p.Kind != tt.action.Kind || p.Cwd != tt.action.Cwd {
				t.Errorf("pane = %+v", p)
			}
			if got.NextID != tt.from.NextID+1 {
				t.Errorf("NextID = %d, want %d", got.NextID, tt.from.NextID+1)
			}
			if n := got.Layout.path(tt.wantFocus); len(n) > 1 {
				if r := n[len(n)-2].Ratio; r != 0.5 {
					t.Errorf("ratio = %v, want 0.5", r)
				}
			}
		})
	}
}

func TestOpenPaneErrors(t *testing.T) {
	tests := []struct {
		name   string
		from   State
		action OpenPane
	}{
		{"unknown kind", stateOf(nil, ""), OpenPane{Kind: "x"}},
		{"missing split", stateOf(leaf("p1"), "p1"), OpenPane{Kind: KindShell}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := NewStore(tt.from).Dispatch(tt.action); err == nil {
				t.Error("want error")
			}
		})
	}
}

func TestNewPaneIDNeverReused(t *testing.T) {
	s := dispatch(t, stateOf(nil, ""), OpenPane{Kind: KindShell})
	s = dispatch(t, s, ClosePane{ID: "p1"})
	if got := s.NewPaneID(); got != "p2" {
		t.Errorf("NewPaneID() = %q, want p2", got)
	}
}

func TestClosePane(t *testing.T) {
	tree := func() *Node { return vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))) }
	tests := []struct {
		name      string
		from      State
		id        PaneID
		wantTree  string
		wantFocus PaneID
	}{
		{"focused pane focuses the sibling", stateOf(tree(), "p2"), "p2", "v(p1,p3)", "p3"},
		{"sibling subtree focuses its first leaf", stateOf(tree(), "p1"), "p1", "h(p2,p3)", "p2"},
		{"unfocused pane keeps focus", stateOf(tree(), "p1"), "p3", "v(p1,p2)", "p1"},
		{"unfocused pane keeps focus in the sibling", stateOf(tree(), "p3"), "p1", "h(p2,p3)", "p3"},
		{"last pane", stateOf(leaf("p1"), "p1"), "p1", "-", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.from.Zoomed = true
			got := dispatch(t, tt.from, ClosePane{ID: tt.id})
			if show(got.Layout) != tt.wantTree {
				t.Errorf("tree = %s, want %s", show(got.Layout), tt.wantTree)
			}
			if got.Focus != tt.wantFocus {
				t.Errorf("Focus = %q, want %q", got.Focus, tt.wantFocus)
			}
			if _, ok := got.Panes[tt.id]; ok {
				t.Error("pane metadata not removed")
			}
			if got.Zoomed {
				t.Error("zoom not cleared")
			}
		})
	}
}

func TestDispatchKeepsStateOnError(t *testing.T) {
	st := NewStore(stateOf(vsplit(leaf("p1"), leaf("p2")), "p1"))
	for _, a := range []Action{
		ClosePane{ID: "p9"}, FocusPane{ID: "p9"}, SetPaneCwd{ID: "p9"}, OpenPane{Kind: "x"},
	} {
		if err := st.Dispatch(a); err == nil {
			t.Errorf("Dispatch(%T) want error", a)
		}
	}
	got := st.State()
	if show(got.Layout) != "v(p1,p2)" || got.Focus != "p1" || len(got.Panes) != 2 || got.NextID != 2 {
		t.Errorf("state changed: %s %+v", show(got.Layout), got)
	}
}

func TestStateIsCopied(t *testing.T) {
	st := NewStore(stateOf(vsplit(leaf("p1"), leaf("p2")), "p1"))
	got := st.State()
	got.Panes["p1"] = Pane{Kind: KindAgent, Cwd: "/x"}
	delete(got.Panes, "p2")
	got.Layout.Ratio = 0.9
	got.Layout.A.Pane = "zz"
	again := st.State()
	if len(again.Panes) != 2 || again.Panes["p1"].Kind != KindShell {
		t.Errorf("Panes leaked: %+v", again.Panes)
	}
	if again.Layout.Ratio != 0.5 || again.Layout.A.Pane != "p1" {
		t.Errorf("Layout leaked: %s", show(again.Layout))
	}
}

func TestSetThemeStillWorks(t *testing.T) {
	st := NewStore(State{Theme: "a"})
	if err := st.Dispatch(SetTheme{Name: "b"}); err != nil {
		t.Fatal(err)
	}
	if st.State().Theme != "b" {
		t.Error("theme not set")
	}
}

func TestFocusDir(t *testing.T) {
	tree := func() *Node { return vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))) }
	tests := []struct {
		name  string
		focus PaneID
		dir   Dir
		want  PaneID
	}{
		{"right takes the top-most tie", "p1", Right, "p2"},
		{"left", "p3", Left, "p1"},
		{"up", "p3", Up, "p2"},
		{"down from the bottom", "p3", Down, "p3"},
		{"right from the edge", "p3", Right, "p3"},
		{"left from the edge", "p1", Left, "p1"},
		{"down", "p2", Down, "p3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dispatch(t, stateOf(tree(), tt.focus), FocusDir{Dir: tt.dir})
			if got.Focus != tt.want {
				t.Errorf("Focus = %q, want %q", got.Focus, tt.want)
			}
		})
	}
}

func TestFocusDirPrefersOverlap(t *testing.T) {
	// p1 is wide on top; below sit p2 (narrow, left) and p3 (wide, right).
	tree := hsplit(leaf("p1"), &Node{Split: SplitVertical, Ratio: 0.2, A: leaf("p2"), B: leaf("p3")})
	tree.Ratio = 0.5
	s := stateOf(tree, "p1")
	if got := dispatch(t, s, FocusDir{Dir: Down}); got.Focus != "p3" {
		t.Errorf("Focus = %q, want p3 (most overlap)", got.Focus)
	}
}

func TestResizeDir(t *testing.T) {
	tests := []struct {
		name      string
		tree      *Node
		focus     PaneID
		dirs      []Dir
		wantRatio float64
	}{
		{"right grows the first child", vsplit(leaf("p1"), leaf("p2")), "p1", []Dir{Right}, 0.55},
		{"right from the second child", vsplit(leaf("p1"), leaf("p2")), "p2", []Dir{Right}, 0.55},
		{"left clamps", vsplit(leaf("p1"), leaf("p2")), "p1", []Dir{Left, Left, Left, Left, Left, Left, Left, Left, Left}, 0.1},
		{"right clamps", vsplit(leaf("p1"), leaf("p2")), "p1", []Dir{Right, Right, Right, Right, Right, Right, Right, Right, Right}, 0.9},
		{"down without a horizontal ancestor", vsplit(leaf("p1"), leaf("p2")), "p1", []Dir{Down}, 0.5},
		{"down on a stack", hsplit(leaf("p1"), leaf("p2")), "p1", []Dir{Down, Down}, 0.6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stateOf(tt.tree, tt.focus)
			for _, d := range tt.dirs {
				s = dispatch(t, s, ResizeDir{Dir: d})
			}
			if s.Layout.Ratio != tt.wantRatio {
				t.Errorf("Ratio = %v, want %v", s.Layout.Ratio, tt.wantRatio)
			}
		})
	}
}

func TestResizeDirUsesNearestAncestor(t *testing.T) {
	tree := vsplit(leaf("p1"), vsplit(leaf("p2"), leaf("p3")))
	s := dispatch(t, stateOf(tree, "p3"), ResizeDir{Dir: Right})
	if s.Layout.Ratio != 0.5 || s.Layout.B.Ratio != 0.55 {
		t.Errorf("ratios = %v, %v", s.Layout.Ratio, s.Layout.B.Ratio)
	}
}

func TestToggleZoom(t *testing.T) {
	s := stateOf(vsplit(leaf("p1"), leaf("p2")), "p1")
	s = dispatch(t, s, ToggleZoom{})
	if !s.Zoomed {
		t.Fatal("not zoomed")
	}
	if s = dispatch(t, s, ToggleZoom{}); s.Zoomed {
		t.Error("still zoomed")
	}
	if s = dispatch(t, stateOf(nil, ""), ToggleZoom{}); s.Zoomed {
		t.Error("zoomed with no pane")
	}
}

func TestZoomClearedBy(t *testing.T) {
	zoomed := func(focus PaneID) State {
		s := stateOf(vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))), focus)
		s.Zoomed = true
		return s
	}
	tests := []struct {
		name   string
		from   State
		action Action
		want   bool
	}{
		{"open", zoomed("p1"), OpenPane{Kind: KindShell, Split: SplitVertical}, false},
		{"close", zoomed("p1"), ClosePane{ID: "p3"}, false},
		{"focus pane", zoomed("p1"), FocusPane{ID: "p2"}, false},
		{"focus pane on the same pane", zoomed("p1"), FocusPane{ID: "p1"}, true},
		{"focus dir that moves", zoomed("p1"), FocusDir{Dir: Right}, false},
		{"focus dir at an edge", zoomed("p1"), FocusDir{Dir: Left}, true},
		{"resize", zoomed("p1"), ResizeDir{Dir: Right}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dispatch(t, tt.from, tt.action); got.Zoomed != tt.want {
				t.Errorf("Zoomed = %v, want %v", got.Zoomed, tt.want)
			}
		})
	}
}

func TestSetPaneCwd(t *testing.T) {
	s := dispatch(t, stateOf(leaf("p1"), "p1"), SetPaneCwd{ID: "p1", Cwd: "/tmp"})
	if s.Panes["p1"].Cwd != "/tmp" || s.Panes["p1"].Kind != KindShell {
		t.Errorf("pane = %+v", s.Panes["p1"])
	}
}

func TestSwapDir(t *testing.T) {
	ratioed := func() *Node {
		return &Node{Split: SplitVertical, Ratio: 0.7, A: leaf("p1"), B: leaf("p2")}
	}
	tree := func() *Node { return vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))) }
	tests := []struct {
		name      string
		layout    *Node
		focus     PaneID
		dir       Dir
		want      string
		wantRatio float64
		zoomed    bool
		wantZoom  bool
	}{
		{"keeps the ratio of the position", ratioed(), "p2", Left, "v(p2,p1)", 0.7, false, false},
		{"right takes the top-most tie", tree(), "p1", Right, "v(p2,h(p1,p3))", 0.5, false, false},
		{"up", tree(), "p3", Up, "v(p1,h(p3,p2))", 0.5, false, false},
		{"no neighbour keeps the tree and zoom", tree(), "p1", Left, "v(p1,h(p2,p3))", 0.5, true, true},
		{"swap clears zoom", tree(), "p1", Right, "v(p2,h(p1,p3))", 0.5, true, false},
		{"single pane", leaf("p1"), "p1", Right, "p1", 0, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stateOf(tt.layout, tt.focus)
			s.Zoomed = tt.zoomed
			before := maps.Clone(s.Panes)
			got := dispatch(t, s, SwapDir{Dir: tt.dir})
			if g := show(got.Layout); g != tt.want {
				t.Errorf("layout = %s, want %s", g, tt.want)
			}
			if got.Focus != tt.focus {
				t.Errorf("Focus = %q, want %q", got.Focus, tt.focus)
			}
			if got.Zoomed != tt.wantZoom {
				t.Errorf("Zoomed = %v, want %v", got.Zoomed, tt.wantZoom)
			}
			if tt.wantRatio != 0 && got.Layout.Ratio != tt.wantRatio {
				t.Errorf("root ratio = %v, want %v", got.Layout.Ratio, tt.wantRatio)
			}
			if !maps.Equal(got.Panes, before) {
				t.Errorf("Panes = %v, want %v", got.Panes, before)
			}
		})
	}
}

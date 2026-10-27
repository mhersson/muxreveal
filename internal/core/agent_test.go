package core

import "testing"

func agentState(panes map[PaneID]Pane, layout *Node, focus PaneID) State {
	s := stateOf(layout, focus)
	for id, p := range panes {
		s.Panes[id] = p
	}

	return s
}

func TestAgentStateString(t *testing.T) {
	tests := []struct {
		s    AgentState
		want string
	}{
		{AgentUnknown, ""},
		{AgentIdle, "idle"},
		{AgentWorking, "working"},
		{AgentBlocked, "blocked"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("AgentState(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}

	if AgentUnknown != 0 {
		t.Error("AgentUnknown must be the zero value")
	}
}

func TestSetPaneKind(t *testing.T) {
	full := Pane{Kind: KindAgent, Cwd: "/w", Harness: "claude-code", Session: "s1", Transcript: "/t", State: AgentWorking, Action: "Edit a.go"}

	tests := []struct {
		name string
		from Pane
		act  SetPaneKind
		want Pane
	}{
		{"shell to agent", Pane{Kind: KindShell, Cwd: "/w"}, SetPaneKind{ID: "p1", Kind: KindAgent, Harness: "claude-code"}, Pane{Kind: KindAgent, Cwd: "/w", Harness: "claude-code"}},
		{"agent to shell clears agent fields", full, SetPaneKind{ID: "p1", Kind: KindShell}, Pane{Kind: KindShell, Cwd: "/w"}},
		{"agent to shell ignores harness", full, SetPaneKind{ID: "p1", Kind: KindShell, Harness: "x"}, Pane{Kind: KindShell, Cwd: "/w"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dispatch(t, agentState(map[PaneID]Pane{"p1": tt.from}, leaf("p1"), "p1"), tt.act)
			if got.Panes["p1"] != tt.want {
				t.Errorf("pane = %+v, want %+v", got.Panes["p1"], tt.want)
			}
		})
	}
}

func TestAgentActionErrors(t *testing.T) {
	base := func() State {
		return agentState(map[PaneID]Pane{
			"p1": {Kind: KindShell},
			"p2": {Kind: KindAgent, Harness: "claude-code"},
		}, vsplit(leaf("p1"), leaf("p2")), "p1")
	}

	tests := []struct {
		name string
		act  Action
	}{
		{"kind of unknown pane", SetPaneKind{ID: "p9", Kind: KindShell}},
		{"agent without harness", SetPaneKind{ID: "p1", Kind: KindAgent}},
		{"unknown kind", SetPaneKind{ID: "p1", Kind: "x"}},
		{"session of unknown pane", SetAgentSession{ID: "p9", Session: "s"}},
		{"session of shell pane", SetAgentSession{ID: "p1", Session: "s"}},
		{"state of unknown pane", SetAgentState{ID: "p9", State: AgentIdle}},
		{"state out of range", SetAgentState{ID: "p2", State: AgentState(99)}},
		{"state negative", SetAgentState{ID: "p2", State: AgentState(-1)}},
		{"state of shell pane", SetAgentState{ID: "p1", State: AgentIdle}},
		{"action of unknown pane", SetAgentAction{ID: "p9", Action: "x"}},
		{"action of shell pane", SetAgentAction{ID: "p1", Action: "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := NewStore(base())
			if err := st.Dispatch(tt.act); err == nil {
				t.Fatal("want error")
			}

			want := base()
			for id, p := range want.Panes {
				if st.State().Panes[id] != p {
					t.Errorf("pane %s changed: %+v", id, st.State().Panes[id])
				}
			}
		})
	}
}

func TestSetAgentAction(t *testing.T) {
	s := agentState(map[PaneID]Pane{"p1": {Kind: KindAgent, Harness: "claude-code"}}, leaf("p1"), "p1")

	s = dispatch(t, s, SetAgentAction{ID: "p1", Action: "Edit a.go"})
	if got := s.Panes["p1"].Action; got != "Edit a.go" {
		t.Fatalf("action = %q", got)
	}

	s = dispatch(t, s, SetAgentState{ID: "p1", State: AgentWorking})
	if got := s.Panes["p1"].Action; got != "Edit a.go" {
		t.Errorf("a state change cleared the action: %q", got)
	}

	s = dispatch(t, s, SetAgentAction{ID: "p1"})
	if got := s.Panes["p1"].Action; got != "" {
		t.Errorf("empty action did not clear: %q", got)
	}

	s = dispatch(t, s, SetAgentAction{ID: "p1", Action: "Bash ls"})
	s = dispatch(t, s, SetPaneKind{ID: "p1", Kind: KindShell})

	if got := s.Panes["p1"].Action; got != "" {
		t.Errorf("a shell pane kept the action: %q", got)
	}
}

func TestSetAgentSessionAndState(t *testing.T) {
	s := agentState(map[PaneID]Pane{"p1": {Kind: KindAgent, Harness: "claude-code"}}, leaf("p1"), "p1")
	s = dispatch(t, s, SetAgentSession{ID: "p1", Session: "s1", Transcript: "/t.jsonl"})

	s = dispatch(t, s, SetAgentState{ID: "p1", State: AgentBlocked})
	if p := s.Panes["p1"]; p.Session != "s1" || p.Transcript != "/t.jsonl" || p.State != AgentBlocked {
		t.Fatalf("pane = %+v", p)
	}

	s = dispatch(t, s, SetAgentState{ID: "p1", State: AgentUnknown})
	if s.Panes["p1"].State != AgentUnknown {
		t.Error("AgentUnknown did not clear the state")
	}

	s = dispatch(t, s, SetAgentSession{ID: "p1"})
	if p := s.Panes["p1"]; p.Session != "" || p.Transcript != "" {
		t.Errorf("empty session did not clear: %+v", p)
	}
}

func TestTabAgentState(t *testing.T) {
	ag := func(st AgentState) Pane { return Pane{Kind: KindAgent, Harness: "claude-code", State: st} }

	tests := []struct {
		name   string
		panes  map[PaneID]Pane
		layout *Node
		want   AgentState
	}{
		{"no agents", map[PaneID]Pane{"p1": {Kind: KindShell}}, leaf("p1"), AgentUnknown},
		{"idle", map[PaneID]Pane{"p1": ag(AgentIdle), "p2": {Kind: KindShell}}, vsplit(leaf("p1"), leaf("p2")), AgentIdle},
		{"working over idle", map[PaneID]Pane{"p1": ag(AgentIdle), "p2": ag(AgentWorking)}, vsplit(leaf("p1"), leaf("p2")), AgentWorking},
		{"blocked over working", map[PaneID]Pane{"p1": ag(AgentWorking), "p2": ag(AgentBlocked), "p3": ag(AgentIdle)}, vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))), AgentBlocked},
		{"unknown agent", map[PaneID]Pane{"p1": ag(AgentUnknown)}, leaf("p1"), AgentUnknown},
		{"shell state ignored", map[PaneID]Pane{"p1": {Kind: KindShell, State: AgentBlocked}}, leaf("p1"), AgentUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := agentState(tt.panes, tt.layout, "p1")
			if got := s.TabAgentState(0); got != tt.want {
				t.Errorf("TabAgentState(0) = %v, want %v", got, tt.want)
			}
		})
	}

	s := agentState(map[PaneID]Pane{"p1": ag(AgentBlocked)}, leaf("p1"), "p1")
	if got := s.TabAgentState(5); got != AgentUnknown {
		t.Errorf("out of range = %v", got)
	}

	if got := s.TabAgentState(-1); got != AgentUnknown {
		t.Errorf("negative = %v", got)
	}
}

func TestAgentPanes(t *testing.T) {
	s := agentState(map[PaneID]Pane{
		"p1": {Kind: KindShell},
		"p2": {Kind: KindAgent, Harness: "claude-code"},
		"p3": {Kind: KindAgent, Harness: "claude-code"},
		"p4": {Kind: KindAgent, Harness: "claude-code"},
		"p5": {Kind: KindShell},
	}, vsplit(hsplit(leaf("p3"), leaf("p1")), leaf("p2")), "p3")
	t2 := Tab{ID: "t2", Focus: "p4"}
	setLayout(&t2, hsplit(leaf("p5"), leaf("p4")))
	s.Tabs = append(s.Tabs, t2)
	got := s.AgentPanes()

	want := []PaneID{"p3", "p2", "p4"}
	if len(got) != len(want) {
		t.Fatalf("AgentPanes() = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AgentPanes() = %v, want %v", got, want)
		}
	}
}

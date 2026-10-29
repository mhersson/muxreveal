package core

import (
	"math"
	"testing"
)

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
	full := Pane{Kind: KindAgent, Cwd: "/w", Harness: "claude-code", Session: "s1", Transcript: "/t", State: AgentWorking, Action: "Edit a.go", AgentCwd: "/w/sub", Model: "Opus 4.8", Context: 42, ContextKnown: true, StatusSession: "s1"}

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
		{"cwd of unknown pane", SetAgentCwd{ID: "p9", Cwd: "/x"}},
		{"cwd of shell pane", SetAgentCwd{ID: "p1", Cwd: "/x"}},
		{"status of unknown pane", SetAgentStatus{ID: "p9", Session: "s", Model: "Opus 4.8"}},
		{"status of shell pane", SetAgentStatus{ID: "p1", Session: "s", Model: "Opus 4.8"}},
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

func TestSetAgentCwd(t *testing.T) {
	s := agentState(map[PaneID]Pane{"p1": {Kind: KindAgent, Cwd: "/w", Harness: "claude-code"}}, leaf("p1"), "p1")

	tests := []struct {
		name    string
		cwd     string
		want    string
		wantDir string
	}{
		{"set", "/w/sub", "/w/sub", "/w/sub"},
		{"overwrite", "/other", "/other", "/other"},
		{"clear", "", "", "/w"},
	}
	for _, tt := range tests {
		s = dispatch(t, s, SetAgentCwd{ID: "p1", Cwd: tt.cwd})

		p := s.Panes["p1"]
		if p.AgentCwd != tt.want || p.Cwd != "/w" || p.WorkDir() != tt.wantDir {
			t.Errorf("%s: pane = %+v, WorkDir = %q; want AgentCwd %q, Cwd /w, WorkDir %q", tt.name, p, p.WorkDir(), tt.want, tt.wantDir)
		}
	}
}

func TestPaneWorkDir(t *testing.T) {
	tests := []struct {
		name string
		pane Pane
		want string
	}{
		{"cwd alone", Pane{Cwd: "/w"}, "/w"},
		{"agent cwd wins", Pane{Cwd: "/w", AgentCwd: "/w/sub"}, "/w/sub"},
		{"agent cwd without cwd", Pane{AgentCwd: "/w/sub"}, "/w/sub"},
		{"neither", Pane{}, ""},
	}
	for _, tt := range tests {
		if got := tt.pane.WorkDir(); got != tt.want {
			t.Errorf("%s: WorkDir() = %q, want %q", tt.name, got, tt.want)
		}
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

func TestSetAgentStatus(t *testing.T) {
	reported := Pane{Kind: KindAgent, Harness: "claude-code", Session: "s1", Transcript: "/t", State: AgentWorking, Action: "Edit a.go", Model: "Opus 4.8", Context: 55, ContextKnown: true, StatusSession: "s1"}

	tests := []struct {
		name    string
		from    Pane
		act     SetAgentStatus
		want    Pane
		wantErr bool
	}{
		{
			name: "set",
			from: Pane{Kind: KindAgent, Harness: "claude-code"},
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 42, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 42, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name: "69.9 floors to 69",
			from: Pane{Kind: KindAgent, Harness: "claude-code"},
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 69.9, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 69, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name: "zero percent is known",
			from: Pane{Kind: KindAgent, Harness: "claude-code"},
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 0.4, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 0, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name: "over 100 clamps to 100",
			from: Pane{Kind: KindAgent, Harness: "claude-code"},
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 130.5, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 100, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name: "negative clamps to 0",
			from: Pane{Kind: KindAgent, Harness: "claude-code"},
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: -3.2, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 0, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name: "a float far past the int range clamps to 100",
			from: Pane{Kind: KindAgent, Harness: "claude-code"},
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 1e300, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 100, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name: "a float far below the int range clamps to 0",
			from: Pane{Kind: KindAgent, Harness: "claude-code"},
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: -1e300, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 0, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name: "not a number reads as 0",
			from: Pane{Kind: KindAgent, Harness: "claude-code"},
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: math.NaN(), ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 0, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name: "a null percentage keeps the model and clears the meter",
			from: reported,
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 77},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Session: "s1", Transcript: "/t", State: AgentWorking, Action: "Edit a.go", Model: "Opus 4.8", StatusSession: "s1"},
		},
		{
			name: "a new model and percentage replace the old",
			from: reported,
			act:  SetAgentStatus{Session: "s1", Model: "Sonnet 5.5", Context: 91.2, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Session: "s1", Transcript: "/t", State: AgentWorking, Action: "Edit a.go", Model: "Sonnet 5.5", Context: 91, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name:    "a shell pane errors",
			from:    Pane{Kind: KindShell, Cwd: "/w"},
			act:     SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 42, ContextKnown: true},
			want:    Pane{Kind: KindShell, Cwd: "/w"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.act.ID = "p1"
			st := NewStore(agentState(map[PaneID]Pane{"p1": tt.from}, leaf("p1"), "p1"))

			err := st.Dispatch(tt.act)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Dispatch() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got := st.State().Panes["p1"]; got != tt.want {
				t.Errorf("pane = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSessionChangeClearsStatus(t *testing.T) {
	reported := func(session SessionID) Pane {
		return Pane{Kind: KindAgent, Harness: "claude-code", Session: session, Transcript: "/t", Model: "Opus 4.8", Context: 40, ContextKnown: true, StatusSession: "s1"}
	}

	tests := []struct {
		name string
		from Pane
		act  SetAgentSession
		want Pane
	}{
		{
			name: "a new session clears it",
			from: reported("s1"),
			act:  SetAgentSession{Session: "s2", Transcript: "/t2"},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Session: "s2", Transcript: "/t2"},
		},
		{
			name: "the same session keeps it",
			from: reported("s1"),
			act:  SetAgentSession{Session: "s1", Transcript: "/t"},
			want: reported("s1"),
		},
		{
			name: "an end clears it",
			from: reported("s1"),
			act:  SetAgentSession{},
			want: Pane{Kind: KindAgent, Harness: "claude-code"},
		},
		{
			name: "an end clears a status that came before any session",
			from: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 40, ContextKnown: true, StatusSession: "s1"},
			act:  SetAgentSession{},
			want: Pane{Kind: KindAgent, Harness: "claude-code"},
		},
		{
			name: "a status that already carries the new session survives its SessionStart",
			from: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 40, ContextKnown: true, StatusSession: "s1"},
			act:  SetAgentSession{Session: "s1", Transcript: "/t"},
			want: reported("s1"),
		},
		{
			name: "a status from another session does not survive",
			from: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 40, ContextKnown: true, StatusSession: "s9"},
			act:  SetAgentSession{Session: "s1", Transcript: "/t"},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Session: "s1", Transcript: "/t"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.act.ID = "p1"

			got := dispatch(t, agentState(map[PaneID]Pane{"p1": tt.from}, leaf("p1"), "p1"), tt.act)
			if got.Panes["p1"] != tt.want {
				t.Errorf("pane = %+v, want %+v", got.Panes["p1"], tt.want)
			}
		})
	}
}

func TestSetAgentStatusMatches(t *testing.T) {
	p := Pane{Kind: KindAgent, Model: "Opus 4.8", Context: 69, ContextKnown: true, StatusSession: "s1"}

	tests := []struct {
		name string
		pane Pane
		act  SetAgentStatus
		want bool
	}{
		{"identical", p, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 69, ContextKnown: true}, true},
		{"same floor", p, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 69.9, ContextKnown: true}, true},
		{"other floor", p, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 70, ContextKnown: true}, false},
		{"same clamp", Pane{Kind: KindAgent, Model: "Opus 4.8", Context: 100, ContextKnown: true, StatusSession: "s1"}, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 130, ContextKnown: true}, true},
		{"same clamp below", Pane{Kind: KindAgent, Model: "Opus 4.8", Context: 0, ContextKnown: true, StatusSession: "s1"}, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: -5, ContextKnown: true}, true},
		{"other model", p, SetAgentStatus{Session: "s1", Model: "Sonnet 5.5", Context: 69, ContextKnown: true}, false},
		{"other session", p, SetAgentStatus{Session: "s2", Model: "Opus 4.8", Context: 69, ContextKnown: true}, false},
		{"meter cleared", p, SetAgentStatus{Session: "s1", Model: "Opus 4.8"}, false},
		{"a null percentage ignores the number", Pane{Kind: KindAgent, Model: "Opus 4.8", StatusSession: "s1"}, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 33}, true},
		{"nothing yet", Pane{Kind: KindAgent}, SetAgentStatus{Session: "s1", Model: "Opus 4.8"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.act.Matches(tt.pane); got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

package core

import (
	"fmt"
	"slices"
)

// SessionID names a harness session, e.g. a Claude Code session UUID.
type SessionID string

// AgentState is what an agent pane is doing, as its harness reports it.
type AgentState int

// Agent states, in increasing urgency; AgentUnknown shows no badge.
const (
	AgentUnknown AgentState = iota
	AgentIdle
	AgentWorking
	AgentBlocked
)

func (s AgentState) String() string {
	switch s {
	case AgentIdle:
		return "idle"
	case AgentWorking:
		return "working"
	case AgentBlocked:
		return "blocked"
	}
	return ""
}

// SetPaneKind changes what a pane is. An agent names its harness; becoming a
// shell clears Harness, Session, Transcript and State.
type SetPaneKind struct {
	ID      PaneID
	Kind    PaneKind
	Harness string
}

func (a SetPaneKind) apply(s *State) error {
	p, ok := s.Panes[a.ID]
	if !ok {
		return fmt.Errorf("core: no pane %q", a.ID)
	}
	switch a.Kind {
	case KindShell:
		p = Pane{Kind: KindShell, Cwd: p.Cwd}
	case KindAgent:
		if a.Harness == "" {
			return fmt.Errorf("core: SetPaneKind agent for pane %q without a harness", a.ID)
		}
		p.Kind, p.Harness = KindAgent, a.Harness
	default:
		return fmt.Errorf("core: SetPaneKind with unknown kind %q", a.Kind)
	}
	s.Panes[a.ID] = p
	return nil
}

// agentPane returns the agent pane for id, or an error for an unknown or shell pane.
func (s *State) agentPane(id PaneID) (Pane, error) {
	p, ok := s.Panes[id]
	if !ok {
		return Pane{}, fmt.Errorf("core: no pane %q", id)
	}
	if p.Kind != KindAgent {
		return Pane{}, fmt.Errorf("core: pane %q is not an agent", id)
	}
	return p, nil
}

// SetAgentSession records the session an agent reported and its transcript
// file. An empty Session clears both.
type SetAgentSession struct {
	ID         PaneID
	Session    SessionID
	Transcript string
}

func (a SetAgentSession) apply(s *State) error {
	p, err := s.agentPane(a.ID)
	if err != nil {
		return err
	}
	p.Session, p.Transcript = a.Session, a.Transcript
	if a.Session == "" {
		p.Transcript = ""
	}
	s.Panes[a.ID] = p
	return nil
}

// SetAgentState records what an agent is doing; AgentUnknown clears the badge.
type SetAgentState struct {
	ID    PaneID
	State AgentState
}

func (a SetAgentState) apply(s *State) error {
	if a.State < AgentUnknown || a.State > AgentBlocked {
		return fmt.Errorf("core: SetAgentState with unknown state %d", int(a.State))
	}
	p, err := s.agentPane(a.ID)
	if err != nil {
		return err
	}
	p.State = a.State
	s.Panes[a.ID] = p
	return nil
}

// TabAgentState is the most urgent state among tab i's agent panes: blocked,
// working, idle, else AgentUnknown.
func (s State) TabAgentState(i int) AgentState {
	if i < 0 || i >= len(s.Tabs) {
		return AgentUnknown
	}
	best := AgentUnknown
	for _, id := range s.tabLeaves(i) {
		if p := s.Panes[id]; p.Kind == KindAgent && p.State > best {
			best = p.State
		}
	}
	return best
}

// AgentPanes lists the agent panes by tab, then layout order.
func (s State) AgentPanes() []PaneID {
	var out []PaneID
	for i := range s.Tabs {
		for _, id := range s.tabLeaves(i) {
			if s.Panes[id].Kind == KindAgent {
				out = append(out, id)
			}
		}
	}
	return out
}

// tabLeaves lists tab i's panes in layout order. The copy keeps callers from
// editing the tab.
func (s State) tabLeaves(i int) []PaneID {
	return slices.Clone(s.Tabs[i].Panes)
}

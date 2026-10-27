package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/harness"
)

const (
	// agentQuiet is how long a working agent may print nothing before it counts as idle.
	agentQuiet = 3 * time.Second
	// spinEvery is the badge spinner's frame time and the quiet rule's resolution.
	spinEvery = 125 * time.Millisecond
)

// hookMsg carries one hook event from the socket server.
type hookMsg struct{ ev harness.Event }

// agentTickMsg advances the spinner and applies the quiet rule while an agent works.
type agentTickMsg struct{}

// waitEvent reads the next hook event, or returns nothing once the app's
// context ends or the channel closes. The update loop re-arms it after each event.
func (a *App) waitEvent() tea.Cmd {
	if a.events == nil {
		return nil
	}

	return func() tea.Msg {
		select {
		case ev, ok := <-a.events:
			if !ok {
				return nil
			}

			return hookMsg{ev: ev}
		case <-a.ctx.Done():
			return nil
		}
	}
}

// onHook applies a hook event. An event for a pane that is not an open agent is
// ignored, so a late or misaddressed event never disturbs the state.
func (a *App) onHook(ev harness.Event) tea.Cmd {
	m, ok := a.store.State().Panes[ev.Pane]
	if !ok || m.Kind != core.KindAgent {
		a.log.Debug("hook event ignored", "pane", ev.Pane, "event", ev.Name)

		return nil
	}

	a.railEntry(ev.Pane).hookAt = a.clock.Now()
	a.watchRetry(ev.Pane)
	probes := a.railHook(ev.Pane, ev.Start, ev.State == core.AgentIdle)

	if ev.State == core.AgentIdle {
		a.markUnseen(ev.Pane)
	}

	switch {
	case ev.Start:
		if p, ok := a.panes[ev.Pane]; ok {
			p.resuming = false
		}

		// dispatch logs its own failures.
		_ = a.dispatch(core.SetAgentSession{ID: ev.Pane, Session: ev.Session, Transcript: ev.Transcript})
	case ev.End:
		_ = a.dispatch(core.SetAgentSession{ID: ev.Pane})
		_ = a.changeAgentState(ev.Pane, core.AgentUnknown)
	}

	if ev.ActionSet {
		_ = a.dispatch(core.SetAgentAction{ID: ev.Pane, Action: ev.Action})
	}

	if ev.State == core.AgentUnknown {
		return probes
	}

	return tea.Batch(probes, a.setAgentState(ev.Pane, ev.State))
}

// setAgentState records a state. Entering or staying in working restarts the
// quiet timer and makes sure the agent tick runs.
func (a *App) setAgentState(id core.PaneID, s core.AgentState) tea.Cmd {
	if err := a.changeAgentState(id, s); err != nil || s != core.AgentWorking {
		return nil
	}

	if p, ok := a.panes[id]; ok {
		p.workingAt = a.clock.Now()
	}

	return a.agentTickCmd()
}

// agentTickCmd starts the agent tick unless one is outstanding or nothing works.
func (a *App) agentTickCmd() tea.Cmd {
	if a.agentTicking || !a.anyWorking() {
		return nil
	}

	a.agentTicking = true

	return a.tick(spinEvery, func(time.Time) tea.Msg { return agentTickMsg{} })
}

// onAgentTick advances the spinner, idles agents that went quiet and re-arms
// itself only while some agent still works.
func (a *App) onAgentTick() tea.Cmd {
	a.agentTicking = false
	a.spin++

	now := a.clock.Now()

	for id, m := range a.store.State().Panes {
		p, ok := a.panes[id]
		if !ok || m.State != core.AgentWorking {
			continue
		}

		quietSince := p.lastOut
		if p.workingAt.After(quietSince) {
			quietSince = p.workingAt
		}

		if now.Sub(quietSince) >= agentQuiet {
			// dispatch logs its own failures.
			_ = a.changeAgentState(id, core.AgentIdle)
		}
	}

	return a.agentTickCmd()
}

func (a *App) anyWorking() bool {
	for _, m := range a.store.State().Panes {
		if m.State == core.AgentWorking {
			return true
		}
	}

	return false
}

func anyBlocked(st core.State) bool {
	for _, m := range st.Panes {
		if m.Kind == core.KindAgent && m.State == core.AgentBlocked {
			return true
		}
	}

	return false
}

// dialogKey reports whether key answers a permission or question dialog.
func dialogKey(key string) bool {
	switch key {
	case "enter", "esc", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return true
	}

	return false
}

// answerDialog makes a blocked agent work again when a key answers its dialog;
// no hook fires for a denied permission or an interrupt.
func (a *App) answerDialog(p *pane, key string) {
	if !dialogKey(key) || a.store.State().Panes[p.id].State != core.AgentBlocked {
		return
	}

	if cmd := a.setAgentState(p.id, core.AgentWorking); cmd != nil {
		a.later = append(a.later, cmd)
	}
}

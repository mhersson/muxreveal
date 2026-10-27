package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/harness"
)

// tickLog records the scheduled ticks and their messages.
type tickLog struct{ msgs []tea.Msg }

func (l *tickLog) tick(_ time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	msg := fn(t0)
	l.msgs = append(l.msgs, msg)

	return func() tea.Msg { return msg }
}

func (l *tickLog) agentTicks() int {
	n := 0

	for _, m := range l.msgs {
		if _, ok := m.(agentTickMsg); ok {
			n++
		}
	}

	return n
}

// agentApp is a sized app whose pane p2 is an agent and has the focus.
func agentApp(t *testing.T) (*App, *tickLog, *fakeClock) {
	t.Helper()

	a, _, clk := kindApp(t)
	chord(a, 'n')

	tl := &tickLog{}
	a.tick = tl.tick

	if kindOf(a, "p2") != core.KindAgent {
		t.Fatal("p2 is not an agent")
	}

	return a, tl, clk
}

func hook(a *App, ev harness.Event) tea.Cmd {
	_, cmd := a.Update(hookMsg{ev: ev})

	return cmd
}

func agentState(a *App, id core.PaneID) core.AgentState { return a.store.State().Panes[id].State }

// tickAt moves the clock to t0+d and delivers one agent tick.
func tickAt(a *App, clk *fakeClock, d time.Duration) {
	clk.Set(t0.Add(d))
	a.Update(agentTickMsg{})
}

func TestHookEvents(t *testing.T) {
	a, _, _ := agentApp(t)

	steps := []struct {
		name    string
		ev      harness.Event
		state   core.AgentState
		session core.SessionID
		trans   string
	}{
		{"session start", harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Transcript: "/t/1.jsonl", Start: true, State: core.AgentIdle}, core.AgentIdle, "s1", "/t/1.jsonl"},
		{"prompt", harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking}, core.AgentWorking, "s1", "/t/1.jsonl"},
		{"permission", harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked}, core.AgentBlocked, "s1", "/t/1.jsonl"},
		{"tool done", harness.Event{Pane: "p2", Name: "PostToolUse", State: core.AgentWorking}, core.AgentWorking, "s1", "/t/1.jsonl"},
		{"stop", harness.Event{Pane: "p2", Name: "Stop", State: core.AgentIdle}, core.AgentIdle, "s1", "/t/1.jsonl"},
		{"clear starts a new session", harness.Event{Pane: "p2", Name: "SessionStart", Session: "s2", Transcript: "/t/2.jsonl", Start: true, State: core.AgentIdle}, core.AgentIdle, "s2", "/t/2.jsonl"},
		{"compact keeps the state", harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking}, core.AgentWorking, "s2", "/t/2.jsonl"},
		{"compact start", harness.Event{Pane: "p2", Name: "SessionStart", Session: "s2", Transcript: "/t/2.jsonl", Start: true}, core.AgentWorking, "s2", "/t/2.jsonl"},
		{"session end", harness.Event{Pane: "p2", Name: "SessionEnd", End: true}, core.AgentUnknown, "", ""},
		{"stop after end", harness.Event{Pane: "p2", Name: "Stop", State: core.AgentIdle}, core.AgentIdle, "", ""},
		{"start without an id", harness.Event{Pane: "p2", Name: "SessionStart", State: core.AgentIdle}, core.AgentIdle, "", ""},
	}

	for _, s := range steps {
		hook(a, s.ev)

		m := a.store.State().Panes["p2"]
		if m.State != s.state || m.Session != s.session || m.Transcript != s.trans {
			t.Errorf("%s: pane = state %v session %q transcript %q, want %v %q %q", s.name, m.State, m.Session, m.Transcript, s.state, s.session, s.trans)
		}
	}
}

func TestHookEventsThatDoNotFit(t *testing.T) {
	a, _, _ := agentApp(t)

	hook(a, harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Start: true, State: core.AgentIdle})

	before := a.store.State()

	tests := []struct {
		name string
		ev   harness.Event
	}{
		{"unknown pane", harness.Event{Pane: "p9", Name: "Stop", State: core.AgentIdle}},
		{"empty pane", harness.Event{Name: "Stop", State: core.AgentIdle}},
		{"shell pane", harness.Event{Pane: "p1", Name: "SessionStart", Session: "s9", Start: true, State: core.AgentWorking}},
		{"out of range state", harness.Event{Pane: "p2", Name: "Stop", State: core.AgentState(99)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if cmd := hook(a, tt.ev); tt.name != "out of range state" && cmd != nil {
				// Only the re-armed wait may remain, and there is no channel here.
				t.Errorf("command = %v, want none", cmd)
			}

			if got := a.store.State(); got.Panes["p1"] != before.Panes["p1"] || got.Panes["p2"] != before.Panes["p2"] {
				t.Errorf("panes changed: %+v", got.Panes)
			}
		})
	}

	t.Run("closed pane", func(t *testing.T) {
		a.closePane("p2")

		if _, ok := a.store.State().Panes["p2"]; ok {
			t.Fatal("p2 is still open")
		}

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})

		if _, ok := a.store.State().Panes["p2"]; ok {
			t.Error("an event reopened a closed pane")
		}
	})
}

func TestQuietRule(t *testing.T) {
	t.Run("silence idles a working pane", func(t *testing.T) {
		a, _, clk := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})

		tickAt(a, clk, agentQuiet-spinEvery)

		if agentState(a, "p2") != core.AgentWorking {
			t.Fatal("idle before agentQuiet passed")
		}

		tickAt(a, clk, agentQuiet)

		if agentState(a, "p2") != core.AgentIdle {
			t.Errorf("state = %v after agentQuiet of silence, want idle", agentState(a, "p2"))
		}
	})

	t.Run("output keeps it working", func(t *testing.T) {
		a, _, clk := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})

		clk.Set(t0.Add(2 * time.Second))
		a.Update(paneDirtyMsg{id: "p2"})

		tickAt(a, clk, 4*time.Second)

		if agentState(a, "p2") != core.AgentWorking {
			t.Fatal("idle although output arrived 2s ago")
		}

		tickAt(a, clk, 5*time.Second)

		if agentState(a, "p2") != core.AgentIdle {
			t.Error("still working 3s after the last output")
		}
	})

	t.Run("silence before the prompt does not count", func(t *testing.T) {
		a, _, clk := agentApp(t)

		clk.Set(t0.Add(time.Minute))
		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})
		tickAt(a, clk, time.Minute+spinEvery)

		if agentState(a, "p2") != core.AgentWorking {
			t.Error("a pane silent before the prompt went idle at the first tick")
		}
	})

	t.Run("a hook beats the rule", func(t *testing.T) {
		a, _, clk := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})
		hook(a, harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked})
		tickAt(a, clk, time.Minute)

		if agentState(a, "p2") != core.AgentBlocked {
			t.Errorf("state = %v, want blocked to survive the quiet rule", agentState(a, "p2"))
		}
	})
}

func TestDialogKeys(t *testing.T) {
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}

	for _, k := range []tea.KeyPressMsg{enter, keyEsc, keyRune('1'), keyRune('9')} {
		t.Run(k.String(), func(t *testing.T) {
			a, tl, clk := agentApp(t)

			hook(a, harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked})

			clk.Set(t0.Add(time.Minute))
			press(a, k)

			if agentState(a, "p2") != core.AgentWorking {
				t.Fatalf("state = %v after %s, want working", agentState(a, "p2"), k)
			}

			if tl.agentTicks() != 1 {
				t.Errorf("agent ticks = %d, want 1", tl.agentTicks())
			}

			tickAt(a, clk, time.Minute+agentQuiet-spinEvery)

			if agentState(a, "p2") != core.AgentWorking {
				t.Fatal("the quiet timer did not restart at the key")
			}

			tickAt(a, clk, time.Minute+agentQuiet)

			if agentState(a, "p2") != core.AgentIdle {
				t.Error("still working 3s after the key")
			}
		})
	}

	t.Run("other keys change nothing", func(t *testing.T) {
		a, tl, _ := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked})
		press(a, keyRune('k'))

		if agentState(a, "p2") != core.AgentBlocked || tl.agentTicks() != 0 {
			t.Errorf("state = %v, ticks = %d, want blocked and none", agentState(a, "p2"), tl.agentTicks())
		}
	})

	t.Run("an idle pane ignores dialog keys", func(t *testing.T) {
		a, _, _ := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "Stop", State: core.AgentIdle})
		press(a, enter)

		if agentState(a, "p2") != core.AgentIdle {
			t.Errorf("state = %v, want idle", agentState(a, "p2"))
		}
	})
}

func TestAgentTick(t *testing.T) {
	t.Run("one tick at a time and none once nothing works", func(t *testing.T) {
		a, tl, clk := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})
		hook(a, harness.Event{Pane: "p2", Name: "PostToolUse", State: core.AgentWorking})

		if tl.agentTicks() != 1 {
			t.Fatalf("agent ticks = %d after two working events, want 1", tl.agentTicks())
		}

		spin := a.spin

		tickAt(a, clk, spinEvery)

		if a.spin != spin+1 || tl.agentTicks() != 2 {
			t.Errorf("spin = %d, ticks = %d, want the spinner advanced and the tick re-armed", a.spin, tl.agentTicks())
		}

		tickAt(a, clk, agentQuiet+spinEvery)

		if agentState(a, "p2") != core.AgentIdle || tl.agentTicks() != 2 {
			t.Errorf("state = %v, ticks = %d, want idle and no re-arm", agentState(a, "p2"), tl.agentTicks())
		}

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})

		if tl.agentTicks() != 3 {
			t.Errorf("agent ticks = %d, want a new tick when work starts again", tl.agentTicks())
		}
	})

	t.Run("idle and blocked events start no tick", func(t *testing.T) {
		a, tl, _ := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked})
		hook(a, harness.Event{Pane: "p2", Name: "Stop", State: core.AgentIdle})

		if tl.agentTicks() != 0 {
			t.Errorf("agent ticks = %d, want none", tl.agentTicks())
		}
	})
}

func TestWaitEvent(t *testing.T) {
	t.Run("nil channel starts no wait", func(t *testing.T) {
		a, _ := lifeApp(t)

		if a.waitEvent() != nil {
			t.Error("waitEvent returned a command without a channel")
		}
	})

	t.Run("delivers and re-arms", func(t *testing.T) {
		ch := make(chan harness.Event, 2)
		a, _, _ := kindApp(t, func(o *Options) { o.Events = ch })

		ch <- harness.Event{Pane: "p1", Name: "Stop"}

		msg, ok := a.waitEvent()().(hookMsg)
		if !ok || msg.ev.Name != "Stop" {
			t.Fatalf("message = %#v, want the Stop event", msg)
		}

		_, cmd := a.Update(msg)
		if cmd == nil {
			t.Fatal("the update did not re-arm the wait")
		}

		ch <- harness.Event{Name: "Second"}

		if got, ok := collectHook(cmd); !ok || got.ev.Name != "Second" {
			t.Errorf("re-armed wait gave %#v, want the second event", got)
		}
	})

	t.Run("ends with the app context", func(t *testing.T) {
		ch := make(chan harness.Event)
		a, _, _ := kindApp(t, func(o *Options) { o.Events = ch })

		done := make(chan tea.Msg, 1)
		cmd := a.waitEvent()

		go func() { done <- cmd() }()

		a.cancel()

		select {
		case m := <-done:
			if m != nil {
				t.Errorf("message = %#v, want none", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waitEvent kept waiting after the context ended")
		}
	})

	t.Run("a closed channel ends the wait", func(t *testing.T) {
		ch := make(chan harness.Event)
		close(ch)

		a, _, _ := kindApp(t, func(o *Options) { o.Events = ch })

		if m := a.waitEvent()(); m != nil {
			t.Errorf("message = %#v, want none", m)
		}
	})
}

// collectHook runs the commands of cmd and returns the hook message among them.
func collectHook(cmd tea.Cmd) (hookMsg, bool) {
	for _, m := range collect(cmd) {
		if h, ok := m.(hookMsg); ok {
			return h, true
		}
	}

	return hookMsg{}, false
}

func TestStartNotice(t *testing.T) {
	a, _, _ := kindApp(t, func(o *Options) { o.Notice = "agent hooks are off: boom" })

	if a.notice != "agent hooks are off: boom" {
		t.Errorf("notice = %q, want the start notice", a.notice)
	}
}

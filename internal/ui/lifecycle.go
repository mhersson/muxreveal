package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/term"
)

// cwdProbeEvery bounds how often a pane's working directory is looked up.
const cwdProbeEvery = 500 * time.Millisecond

// paneCwdMsg carries a pane's looked-up working directory; empty when unknown.
type paneCwdMsg struct {
	id  core.PaneID
	cwd string
}

// paneCwdDueMsg ends the wait before a throttled cwd lookup.
type paneCwdDueMsg struct{ id core.PaneID }

// termStart adapts term.Start to the proc interface; on error it returns a nil
// interface, not a nil *term.Process.
func termStart(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
	p, err := term.Start(ctx, c, cols, rows)
	if err != nil {
		return nil, err
	}

	return p, nil
}

// contentSize is the PTY size for a pane rect: its content area, at least 1x1.
func contentSize(r core.Rect) (cols, rows int) {
	c := contentRect(r)

	return max(c.W, 1), max(c.H, 1)
}

// openPane starts a pane of kind next to the focused one, in its cwd, and
// focuses it. Nothing changes when the process does not start.
func (a *App) openPane(kind core.PaneKind, split core.Split) tea.Cmd {
	// Without a size there is no rect to start at.
	if !a.started {
		return nil
	}

	st := a.store.State()

	cwd := a.cwd
	if m, ok := st.Panes[st.Tab().Focus]; ok && m.Cwd != "" {
		cwd = m.Cwd
	}

	act := core.OpenPane{Kind: kind, Cwd: cwd, Split: split}
	id := st.NewPaneID()

	// The pane starts at its final size, found by applying the action to a copy.
	cols, rows := 1, 1

	sim := core.NewStore(st)
	if err := sim.Dispatch(act); err == nil {
		area := innerRect(bodyRect(a.width, a.height))
		if r, ok := core.Rects(sim.State().Tab().Layout, area)[id]; ok {
			cols, rows = contentSize(r)
		}
	}

	c := term.ShellCommand(a.getenv, cwd)
	if kind == core.KindAgent {
		c = term.AgentCommand(a.getenv, cwd)
	}

	// On the update loop: user-initiated, fork/exec takes a millisecond or two, and State and the registry stay in step.
	began := time.Now()

	p, err := a.start(a.ctx, c, cols, rows)
	if err != nil && cwd != a.cwd && !usableDir(cwd) {
		// The focused pane's directory is gone; the launch dir always works.
		a.log.Warn("pane cwd unusable, using launch dir", "pane", id, "cwd", cwd, "err", err)
		a.notice = fmt.Sprintf("%s is not available, started in %s", cwd, a.cwd)

		c.Dir = a.cwd
		act.Cwd = a.cwd
		p, err = a.start(a.ctx, c, cols, rows)
	}

	if err != nil {
		a.log.Error("start pane", "kind", kind, "err", err)
		a.notice = fmt.Sprintf("cannot start %s: %v", kind, err)

		return nil
	}

	a.log.Debug("pane started", "pane", id, "kind", kind, "took", time.Since(began))

	ui := a.current().theme.UI
	opts := []paneOption{withColors(ui.Fg, ui.Bg)}

	if a.recordDir != "" {
		tp, err := openTap(a.log, a.recordDir, id, cols, rows, a.clock.Now)
		if err != nil {
			// A recording problem never blocks the pane.
			a.log.Warn("recording disabled for pane", "pane", id, "err", err)
			a.notice = fmt.Sprintf("recording is off for %s: %v", id, err)
		} else {
			opts = append(opts, withTap(tp))
		}
	}

	pn := newPane(id, p, cols, rows, a.clock, opts...)
	a.panes[id] = pn

	if err := a.dispatch(act); err != nil {
		delete(a.panes, id)
		a.closeAsync(pn)
		a.notice = fmt.Sprintf("cannot open %s: %v", kind, err)

		return nil
	}

	return tea.Batch(pn.wait(), a.relayout())
}

// usableDir reports whether dir is an existing directory; exec reports a
// missing one as a failed fork/exec, so the start error cannot say.
func usableDir(dir string) bool {
	fi, err := os.Stat(dir)

	return err == nil && fi.IsDir()
}

// closePane removes a pane from the state and the registry at once and closes
// its process in the background. An open confirm about it closes.
func (a *App) closePane(id core.PaneID) tea.Cmd {
	p := a.panes[id]
	delete(a.panes, id)

	if a.confirm != nil && a.confirm.pane == id {
		a.confirm = nil
	}

	if _, ok := a.store.State().Panes[id]; ok {
		// dispatch logs its own failures.
		_ = a.dispatch(core.ClosePane{ID: id})
	}

	if p == nil {
		return nil
	}

	a.closeAsync(p)

	return a.relayout()
}

// closeAsync closes a pane that has left the registry. Pane.close can take a
// second, so it never runs on the update loop; Close waits for it.
func (a *App) closeAsync(p *pane) {
	a.closing.Go(func() {
		if err := p.close(); err != nil {
			a.log.Warn("close pane", "pane", p.id, "err", err)
		}
	})
}

// act dispatches a layout action and refits the panes to the new geometry.
func (a *App) act(act core.Action) tea.Cmd {
	// dispatch logs its own failures.
	_ = a.dispatch(act)

	return a.relayout()
}

// relayout resizes every pane to its content rect (at least 1x1; panes the
// layout drops get 1x1) and renders the resized ones at once. Panes hidden by
// zoom keep their size.
func (a *App) relayout() tea.Cmd {
	st := a.store.State()
	t := st.Tab()
	_, focused := st.Panes[t.Focus]
	zoomed := t.Zoomed && focused
	rects := a.geometry().rects

	var cmds []tea.Cmd

	for id, p := range a.panes {
		cols, rows := 1, 1

		if r, ok := rects[id]; ok {
			cols, rows = contentSize(r)
		} else if zoomed {
			continue
		}

		if p.resize(cols, rows) {
			cmds = append(cmds, a.renderPane(p))
		}
	}

	return tea.Batch(cmds...)
}

// renderPane renders p and, when a sync hold keeps it from drawing, arms one
// tick for the hold's end.
func (a *App) renderPane(p *pane) tea.Cmd {
	_, held := p.render()
	if held.IsZero() || p.holdArmed {
		return nil
	}

	p.holdArmed = true

	return p.holdTick(held)
}

// onPaneDirty renders a pane with output and waits for its next message.
func (a *App) onPaneDirty(id core.PaneID) tea.Cmd {
	p, ok := a.panes[id]
	if !ok {
		return nil
	}

	return tea.Batch(a.renderPane(p), p.wait(), a.probeCwd(p))
}

func (a *App) onHoldExpired(id core.PaneID) tea.Cmd {
	p, ok := a.panes[id]
	if !ok {
		return nil
	}

	p.holdArmed = false

	return a.renderPane(p)
}

// onPaneExited closes the pane without asking. It never re-arms wait: the exit
// signal stays closed and a re-arm would spin.
func (a *App) onPaneExited(id core.PaneID) tea.Cmd {
	if _, ok := a.panes[id]; !ok {
		return nil
	}

	return a.closePane(id)
}

// probeCwd looks up a pane's working directory off the update loop, at most
// once per cwdProbeEvery; a lookup that arrives early is deferred to the end of
// the window so the last change is not missed.
func (a *App) probeCwd(p *pane) tea.Cmd {
	if p.cwdBusy || p.cwdWait {
		return nil
	}

	if since := a.clock.Now().Sub(p.cwdAt); !p.cwdAt.IsZero() && since < cwdProbeEvery {
		p.cwdWait = true

		return tea.Tick(cwdProbeEvery-since, func(time.Time) tea.Msg { return paneCwdDueMsg{id: p.id} })
	}

	return a.runProbe(p)
}

func (a *App) runProbe(p *pane) tea.Cmd {
	p.cwdBusy = true
	p.cwdAt = a.clock.Now()

	return func() tea.Msg { return paneCwdMsg{id: p.id, cwd: p.workingDir()} }
}

func (a *App) onCwdDue(id core.PaneID) tea.Cmd {
	p, ok := a.panes[id]
	if !ok {
		return nil
	}

	p.cwdWait = false

	if p.cwdBusy {
		return nil
	}

	return a.runProbe(p)
}

func (a *App) onCwd(msg paneCwdMsg) {
	p, ok := a.panes[msg.id]
	if !ok {
		return
	}

	p.cwdBusy = false

	if msg.cwd == "" || a.store.State().Panes[msg.id].Cwd == msg.cwd {
		return
	}

	// dispatch logs its own failures.
	_ = a.dispatch(core.SetPaneCwd{ID: msg.id, Cwd: msg.cwd})
}

// hasAgents reports whether any open pane runs an agent.
func (a *App) hasAgents() bool {
	for _, m := range a.store.State().Panes {
		if m.Kind == core.KindAgent {
			return true
		}
	}

	return false
}

// closeFocused implements pane.close: agents ask first.
func (a *App) closeFocused() tea.Cmd {
	st := a.store.State()

	id := st.Tab().Focus
	m, ok := st.Panes[id]

	switch {
	case !ok:
		return nil
	case m.Kind != core.KindAgent:
		return a.closePane(id)
	}

	a.openConfirm(confirm{
		prompt: "Close this agent pane?",
		pane:   id,
		yes:    func(a *App) tea.Cmd { return a.closePane(id) },
	})

	return nil
}

// quit implements app.quit: open agents ask first.
func (a *App) quit() tea.Cmd {
	if !a.hasAgents() {
		return tea.Quit
	}

	a.openConfirm(confirm{
		prompt: "Agent panes are running. Quit MuxReveal?",
		yes:    func(*App) tea.Cmd { return tea.Quit },
	})

	return nil
}

// Close closes every pane, concurrently, and waits for closes already running.
// ctx bounds the wait; when it ends first Close cancels the app context, and
// exec's context watcher then SIGKILLs each remaining child's process group and
// its terminal's foreground group, asynchronously after Close returns.
// It is safe to call twice, and only after the program stopped running Update.
func (a *App) Close(ctx context.Context) error {
	for id, p := range a.panes {
		delete(a.panes, id)
		a.closeAsync(p)
	}

	done := make(chan struct{})

	go func() {
		a.closing.Wait()
		close(done)
	}()

	defer a.cancel()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("close panes: %w", ctx.Err())
	}
}

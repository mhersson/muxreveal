package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
)

// The plan overlay's notices.
const (
	noticeNoPlan   = "no plan from %s yet"
	noticeNotAgent = "the focused pane is not an agent"
)

// planView is the open plan overlay: what an agent's plan file read as. It is
// UI state and is never saved. root is where the plan reads and its links
// follow; base is what its relative links resolve against.
type planView struct {
	pane       core.PaneID
	agent      string
	plan       core.Plan
	root, base string
	view       mdView
}

// planReadMsg answers the read and parse of a plan; seq is the serial of the ask.
type planReadMsg struct {
	seq        int
	pane       core.PaneID
	plan       core.Plan
	root, base string
	doc        *mdDoc
	err        error
}

// planReadRoot is the folder a plan file is read through: a plan file's root,
// else the folder of a plan-mode plan, which lies outside any worktree.
func planReadRoot(p core.Plan) string {
	if p.Kind == core.PlanFile {
		return p.Root
	}

	return filepath.Dir(p.Path)
}

// planCommand is plan.open: the focused pane's plan.
func (a *App) planCommand() tea.Cmd { return a.openPlan(a.store.State().Tab().Focus) }

// openPlan reads the latest plan of agent id off the update loop; the overlay
// shows when the answer lands. A plan-mode plan's links resolve against and
// follow inside the agent's working directory, since Claude writes them
// relative to the project.
func (a *App) openPlan(id core.PaneID) tea.Cmd {
	a.endLeader()

	st := a.store.State()

	m, ok := st.Panes[id]
	switch {
	case !ok || m.Kind != core.KindAgent:
		a.notice = noticeNotAgent

		return nil
	case m.Plan.Path == "":
		a.notice = fmt.Sprintf(noticeNoPlan, a.cardName(st, id))

		return nil
	}

	root, base := m.Plan.Root, filepath.Dir(m.Plan.Path)
	if m.Plan.Kind == core.PlanMode {
		root = cmp.Or(m.WorkDir(), base)
		base = root
	}

	a.planSeq++

	return a.readPlanCmd(planReadMsg{seq: a.planSeq, pane: id, plan: m.Plan, root: root, base: base})
}

// readPlanCmd reads and parses the plan msg names and answers with msg filled in.
func (a *App) readPlanCmd(msg planReadMsg) tea.Cmd {
	parent, tok := a.ctx, a.drawer.hl.HighlightLang

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, mdReadTimeout)
		defer cancel()

		src, err := readLocalMarkdown(ctx, planReadRoot(msg.plan), msg.plan.Path)
		if err != nil {
			msg.err = err

			return msg
		}

		msg.doc = parseMarkdown(src, msg.base, tok)

		return msg
	}
}

// onPlanRead shows a plan once it was read. An answer for an ask a newer one or
// an esc superseded, one that lands under another modal, or one for a pane that
// is gone or no longer an agent is dropped.
func (a *App) onPlanRead(msg planReadMsg) {
	m, ok := a.store.State().Panes[msg.pane]
	if msg.seq != a.planSeq || a.modalOpen() || !ok || m.Kind != core.KindAgent {
		return
	}

	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			a.notice = markdownNotice(planReadRoot(msg.plan), msg.plan.Path, a.getenv("HOME"), msg.err)
		}

		return
	}

	a.endLeader()
	a.plan = &planView{
		pane: msg.pane, agent: printable(a.cardName(a.store.State(), msg.pane)),
		plan: msg.plan, root: msg.root, base: msg.base, view: mdView{doc: msg.doc},
	}
	a.sizePlan()

	// dispatch logs its own failures.
	_ = a.dispatch(core.ReadPlan{ID: msg.pane})
}

// closePlan closes the overlay and drops any read still on its way.
func (a *App) closePlan() {
	a.plan = nil
	a.planSeq++
}

// sizePlan sets the cells the view draws in, keeping the cursor on its source
// line when the width changes. A box too small to draw leaves the view as it was.
func (a *App) sizePlan() {
	v := a.plan
	if v == nil {
		return
	}

	_, _, w, h, ok := planBox(a.width, a.height)
	if !ok {
		return
	}

	rows, _, _ := planRows(h)
	vw := min(w-4, planMaxView)

	if v.view.w != 0 && (v.view.w != vw || v.view.icons != a.icons) {
		line := v.view.sourceLine()
		v.view.size(vw, rows, a.icons)
		v.view.goSource(line)

		return
	}

	v.view.size(vw, rows, a.icons)
}

// handlePlanKey answers the open plan overlay; any other key is dropped. A box
// too small to draw answers esc alone.
func (a *App) handlePlanKey(msg tea.KeyPressMsg) tea.Cmd {
	a.notice = ""
	key := bindingKey(msg)

	if key == "esc" {
		a.closePlan()

		return nil
	}

	if _, _, _, _, ok := planBox(a.width, a.height); !ok {
		return nil
	}

	v := &a.plan.view

	switch key {
	case "j", "down":
		v.move(1)
	case "k", "up":
		v.move(-1)
	case "ctrl+d":
		v.page(1)
	case "ctrl+u":
		v.page(-1)
	case "ctrl+f", "pgdown":
		v.pageFull(1)
	case "ctrl+b", "pgup":
		v.pageFull(-1)
	case "g":
		v.end(false)
	case "G":
		v.end(true)
	case "]":
		v.heading(1)
	case "[":
		v.heading(-1)
	case "enter":
		v.fold()
	}

	return nil
}

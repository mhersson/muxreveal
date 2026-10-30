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

	// pages are the markdown files followed from the plan, the last one shown;
	// pageSeq is the serial of the newest page ask.
	pages   []*planPage
	pageSeq int
	chooser *linkChooser

	// searching is the prompt being open, typing into query; last is the query
	// n and N repeat. query stays painted after enter.
	searching   bool
	query, last string
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

	views := []*mdView{&v.view}
	for _, p := range v.pages {
		views = append(views, &p.view)
	}

	for _, mv := range views {
		if mv.w != 0 && (mv.w != vw || mv.icons != a.icons) {
			line := mv.sourceLine()
			mv.size(vw, rows, a.icons)
			mv.goSource(line)

			continue
		}

		mv.size(vw, rows, a.icons)
	}
}

// handlePlanKey answers the open plan overlay; any other key is dropped. A box
// too small to draw answers esc alone.
func (a *App) handlePlanKey(msg tea.KeyPressMsg) tea.Cmd {
	a.notice = ""
	key := bindingKey(msg)

	pv := a.plan

	if key == "esc" {
		switch {
		case pv.chooser != nil:
			pv.chooser = nil
		case pv.searching:
			pv.searching, pv.query = false, ""
		case len(pv.pages) > 0:
			a.planPop()
		default:
			a.closePlan()
		}

		return nil
	}

	if _, _, _, _, ok := planBox(a.width, a.height); !ok {
		return nil
	}

	if pv.chooser != nil {
		return a.planChooserKey(msg)
	}

	if pv.searching {
		a.planSearchKey(msg)

		return nil
	}

	v := pv.shown()

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
		return a.planEnter()
	case "/":
		pv.searching, pv.query = true, ""
	case "n":
		a.planFind(pv.last, false)
	case "N":
		a.planFind(pv.last, true)
	}

	return nil
}

// planSearchKey types into the open search prompt.
func (a *App) planSearchKey(msg tea.KeyPressMsg) {
	pv := a.plan

	switch key := msg.String(); {
	case key == "enter":
		pv.searching = false
		a.planFind(cmp.Or(pv.query, pv.last), false)
	case key == "backspace":
		if r := []rune(pv.query); len(r) > 0 {
			pv.query = string(r[:len(r)-1])
		}
	case msg.Text != "":
		pv.query += printable(msg.Text)
	}
}

// planPaste extends the search query while the prompt is open; any other paste
// is dropped.
func (a *App) planPaste(text string) {
	if pv := a.plan; pv.searching {
		pv.query += printable(text)
	}
}

// planFind keeps q and moves to its next (or previous) match; a miss says so.
func (a *App) planFind(q string, back bool) {
	if q == "" {
		return
	}

	pv := a.plan

	pv.last, pv.query = q, q

	v := pv.shown()

	find := v.find
	if back {
		find = v.findPrev
	}

	if !find(q) {
		a.notice = noticeNotFound + q
	}
}

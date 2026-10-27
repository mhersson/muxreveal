package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"
	"unicode"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
)

// idleAction is the third card line of an idle agent.
const idleAction = "waiting for a prompt"

// railCard is one agent on the rail.
type railCard struct {
	pane       core.PaneID
	tab        int // 0-based
	tabName    string
	state      core.AgentState
	project    string
	branch     string
	action     string // the card's third line; empty for an agent without hooks
	detached   bool   // branch holds the first 7 characters of the commit
	stats      git.Stats
	statsKnown bool
	active     time.Time // the later of its last output and last hook event
	unseen     bool
}

// railGroup is the cards of one state, in tab and pane order.
type railGroup struct {
	state core.AgentState
	cards []railCard
}

// railGroupOrder is the display order, most urgent first.
var railGroupOrder = [...]core.AgentState{core.AgentBlocked, core.AgentWorking, core.AgentIdle, core.AgentUnknown}

// railField names where a search matched.
type railField int

const (
	railFieldProject railField = iota
	railFieldBranch
	railFieldAction
	railFieldTabNumber
	railFieldTabName
)

// railHit is a match in one field, as rune offsets [start, end).
type railHit struct {
	field      railField
	start, end int
}

// railProbe is the result of one pane's probes.
type railProbe struct {
	project, branch string
	detached        bool
	repo            bool // the cwd is inside a repository
	known           bool // a head probe has succeeded
	headCwd         string
	stats           git.Stats
	statsKnown      bool
	statsCwd        string
}

// railPane is the rail's per-pane memory; it lives until the pane closes.
type railPane struct {
	hookAt time.Time
	unseen bool
	probe  railProbe
	head   probeSlot
	stats  probeSlot
}

// railState is the rail's UI state; core.State only says whether it is open.
type railState struct {
	focused   bool        // the rail has the keyboard
	sel       core.PaneID // the selected agent
	selIdx    int         // its index in the shown list when last seen
	searching bool        // the search row is open
	query     string
	top       int // first list row shown; the drawing clamps it

	panes  map[core.PaneID]*railPane
	ctx    context.Context //nolint:containedctx // the rail's lifetime, set while the rail is open; probes derive from it
	cancel context.CancelFunc
	gen    int // bumps on every open, so late messages of an earlier open are dropped
}

func (a *App) railIsOpen() bool { return a.rail.ctx != nil }

// railEntry returns the pane's memory, creating it.
func (a *App) railEntry(id core.PaneID) *railPane {
	if a.rail.panes == nil {
		a.rail.panes = map[core.PaneID]*railPane{}
	}

	rp, ok := a.rail.panes[id]
	if !ok {
		rp = &railPane{}
		a.rail.panes[id] = rp
	}

	return rp
}

// railCards lists the agents matching the search, grouped by state.
func (a *App) railCards(st core.State) []railGroup {
	tabs := a.tabInfos()
	byState := map[core.AgentState][]railCard{}

	for _, id := range st.AgentPanes() {
		m := st.Panes[id]
		ti, _ := st.TabOf(id)

		c := railCard{pane: id, tab: ti, state: m.State, action: cardAction(m)}
		if ti < len(tabs) {
			c.tabName = tabs[ti].name
		}

		rp := a.rail.panes[id]
		c.project, c.branch = filepath.Base(m.Cwd), ""

		if rp != nil {
			c.unseen, c.active = rp.unseen, rp.hookAt
			if rp.probe.known {
				c.project, c.branch, c.detached = rp.probe.project, rp.probe.branch, rp.probe.detached
			}

			if rp.probe.statsKnown {
				c.stats, c.statsKnown = rp.probe.stats, true
			}
		}

		if p, ok := a.panes[id]; ok && p.lastOut.After(c.active) {
			c.active = p.lastOut
		}

		if _, ok := c.matches(a.rail.query); ok {
			byState[c.state] = append(byState[c.state], c)
		}
	}

	var out []railGroup

	for _, s := range railGroupOrder {
		if cs := byState[s]; len(cs) > 0 {
			out = append(out, railGroup{state: s, cards: cs})
		}
	}

	return out
}

func cardAction(m core.Pane) string {
	switch m.State {
	case core.AgentBlocked, core.AgentWorking:
		return m.Action
	case core.AgentIdle:
		return idleAction
	}

	return ""
}

// matches reports where query occurs in the card, case-insensitively: project,
// branch, action, "tab N" and the tab's name. An empty query matches with no hits.
func (c railCard) matches(query string) ([]railHit, bool) {
	if query == "" {
		return nil, true
	}

	q := foldRunes(query)

	var hits []railHit

	for _, f := range [...]struct {
		field railField
		text  string
	}{
		{railFieldProject, c.project},
		{railFieldBranch, c.branch},
		{railFieldAction, c.action},
		{railFieldTabNumber, fmt.Sprintf("tab %d", c.tab+1)},
		{railFieldTabName, c.tabName},
	} {
		if i := indexRunes(foldRunes(f.text), q); i >= 0 {
			hits = append(hits, railHit{field: f.field, start: i, end: i + len(q)})
		}
	}

	return hits, len(hits) > 0
}

// foldRunes lowercases rune by rune, so offsets in the result are offsets in s.
func foldRunes(s string) []rune {
	r := []rune(s)
	for i, c := range r {
		r[i] = unicode.ToLower(c)
	}

	return r
}

func indexRunes(s, sub []rune) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}

	return -1
}

// railShown flattens the groups into the order the list shows.
func (a *App) railShown(st core.State) []railCard {
	var out []railCard
	for _, g := range a.railCards(st) {
		out = append(out, g.cards...)
	}

	return out
}

// railNormalize keeps the selection on a shown card: it follows its agent, else
// takes the card now at its old index, else the last.
func (a *App) railNormalize(st core.State) {
	shown := a.railShown(st)
	if len(shown) == 0 {
		a.rail.sel, a.rail.selIdx = "", 0

		return
	}

	if i := slices.IndexFunc(shown, func(c railCard) bool { return c.pane == a.rail.sel }); i >= 0 {
		a.rail.selIdx = i

		return
	}

	i := min(max(a.rail.selIdx, 0), len(shown)-1)
	a.rail.sel, a.rail.selIdx = shown[i].pane, i
}

// railSelected is the selected agent, if any card is shown.
func (a *App) railSelected() (core.PaneID, bool) {
	return a.rail.sel, a.rail.sel != ""
}

// railMove moves the selection by delta cards, stopping at the ends.
func (a *App) railMove(delta int) {
	st := a.store.State()
	a.railNormalize(st)

	shown := a.railShown(st)
	if len(shown) == 0 {
		return
	}

	i := min(max(a.rail.selIdx+delta, 0), len(shown)-1)
	a.rail.sel, a.rail.selIdx = shown[i].pane, i
}

// railSelect selects an agent if it is shown.
func (a *App) railSelect(id core.PaneID) {
	for i, c := range a.railShown(a.store.State()) {
		if c.pane == id {
			a.rail.sel, a.rail.selIdx = id, i

			return
		}
	}
}

// railSetSearch opens the search row (or closes it with open false) and sets its text.
func (a *App) railSetSearch(open bool, text string) {
	a.rail.searching, a.rail.query = open, text
	a.railNormalize(a.store.State())
}

// markUnseen flags an agent that changed while it is not the focused pane of the active tab.
func (a *App) markUnseen(id core.PaneID) {
	if a.store.State().Tab().Focus != id {
		a.railEntry(id).unseen = true
	}
}

// changeAgentState dispatches a state and marks the pane unseen when it changed.
func (a *App) changeAgentState(id core.PaneID, s core.AgentState) error {
	prev := a.store.State().Panes[id].State
	if err := a.dispatch(core.SetAgentState{ID: id, State: s}); err != nil {
		return err
	}

	if prev != s {
		a.markUnseen(id)
	}

	return nil
}

// railAfterUpdate runs after every update: it clears the mark of the focused
// pane, forgets closed panes, keeps the selection valid and starts the probes
// that panes new to the rail or with a new cwd need.
func (a *App) railAfterUpdate() {
	if len(a.rail.panes) == 0 && !a.railIsOpen() {
		return
	}

	st := a.store.State()

	if rp, ok := a.rail.panes[st.Tab().Focus]; ok {
		rp.unseen = false
	}

	for id := range a.rail.panes {
		if _, ok := st.Panes[id]; !ok {
			delete(a.rail.panes, id)
		}
	}

	if !a.railIsOpen() {
		return
	}

	a.railNormalize(st)

	for _, id := range st.AgentPanes() {
		if cmd := a.railSync(id, st.Panes[id].Cwd); cmd != nil {
			a.later = append(a.later, cmd)
		}
	}
}

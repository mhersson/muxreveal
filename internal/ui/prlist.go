package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

// noticePRListNone is what the list says when no agent's branch has an open
// pull request.
const noticePRListNone = "no open pull requests on agent branches"

// noticePRNoCheckout is what a row of the other tabs says when no pane is in
// its repository; it takes prRepoName of the repository.
const noticePRNoCheckout = "no checkout of %s here; O opens it in the browser"

// prListKey identifies a pull request in the list: its repository keyed
// "host/owner/name" and its number.
type prListKey struct {
	repo   string
	number int
}

// prListView is the open pull request list: the shown tab and, per tab, the
// selection. A selection is kept by key so a poll that reorders the rows keeps
// it; idx is where it last stood, for the nearest row when the pull request
// leaves the list.
type prListView struct {
	tab prListTab
	sel [prTabCount]prListKey
	idx [prTabCount]int
}

// prListRow is one listed pull request and the agent the keys act through.
type prListRow struct {
	key  prListKey
	pr   core.PRInfo
	pane core.PaneID
	root string // the agent's working directory, where the pull request is read from
}

// prListRows lists the open pull requests of the agents' branches, one row per
// pull request however many agents share its branch, with the first of them in
// rail order as its agent. Repositories come by name and each one's rows newest
// update first, so the rows of a repository are adjacent.
func (a *App) prListRows(st core.State) []prListRow {
	panes := st.AgentPanes()
	rank := func(id core.PaneID) int {
		return slices.Index(railGroupOrder[:], st.Panes[id].State)
	}

	// A state the rail has no group for comes last, as an unknown one does.
	slices.SortStableFunc(panes, func(x, y core.PaneID) int {
		rx, ry := rank(x), rank(y)
		if rx < 0 {
			rx = len(railGroupOrder)
		}

		if ry < 0 {
			ry = len(railGroupOrder)
		}

		return rx - ry
	})

	seen := map[prListKey]bool{}

	var rows []prListRow

	for _, id := range panes {
		repo, ok := a.prRepoOf(id)
		if !ok {
			continue
		}

		pr, ok := a.prOfPane(st, id)
		if !ok || pr.State != prOpenState {
			continue
		}

		key := prListKey{repo: repo.String(), number: pr.Number}
		if seen[key] {
			continue
		}

		seen[key] = true

		rows = append(rows, prListRow{key: key, pr: pr, pane: id, root: a.rail.panes[id].probe.headCwd})
	}

	sortPRListRows(rows)

	return rows
}

// sortPRListRows puts the repositories by name and each one's rows newest
// update first, so the rows of a repository are adjacent.
func sortPRListRows(rows []prListRow) {
	slices.SortStableFunc(rows, func(x, y prListRow) int {
		if c := strings.Compare(prRepoName(x.key.repo), prRepoName(y.key.repo)); c != 0 {
			return c
		}

		if c := strings.Compare(x.key.repo, y.key.repo); c != 0 {
			return c
		}

		if c := y.pr.UpdatedAt.Compare(x.pr.UpdatedAt); c != 0 {
			return c
		}

		return y.key.number - x.key.number
	})
}

// prRepoName is a repository as the list names it: owner/name on github.com,
// host/owner/name elsewhere.
func prRepoName(key string) string {
	return printable(strings.TrimPrefix(key, "github.com/"))
}

// selected is the index of the selected row, or -1 for an empty list. A pull
// request that left the list moves the selection to the row now where it stood.
func (v *prListView) selected(rows []prListRow) int {
	t := v.tab
	if len(rows) == 0 {
		v.idx[t] = 0

		return -1
	}

	for i, r := range rows {
		if r.key == v.sel[t] {
			v.idx[t] = i

			return i
		}
	}

	v.idx[t] = min(v.idx[t], len(rows)-1)
	v.sel[t] = rows[v.idx[t]].key

	return v.idx[t]
}

// move selects the row delta away, stopping at the ends.
func (v *prListView) move(rows []prListRow, delta int) {
	i := v.selected(rows)
	if i < 0 {
		return
	}

	v.idx[v.tab] = min(max(i+delta, 0), len(rows)-1)
	v.sel[v.tab] = rows[v.idx[v.tab]].key
}

// prListCommand is `leader g l`.
func (a *App) prListCommand() tea.Cmd {
	a.endLeader()

	if a.prs.src == nil {
		a.notice = noticePRsOff

		return nil
	}

	a.prList = &prListView{}

	return a.prSearchOpen()
}

// prListShow shows tab. A searched tab needs the reads; without them the list
// stays where it is.
func (a *App) prListShow(tab prListTab) {
	if tab != prTabAgents && a.reads == nil {
		a.notice = noticePRsOff

		return
	}

	a.prList.tab = tab
}

// prListStep shows the next tab (forward) or the previous one, wrapping.
func (a *App) prListStep(forward bool) {
	t := a.prList.tab

	switch {
	case forward && t == prTabCount-1:
		t = 0
	case forward:
		t++
	case t == 0:
		t = prTabCount - 1
	default:
		t--
	}

	a.prListShow(t)
}

// handlePRListKey answers the open list; any other key is swallowed.
func (a *App) handlePRListKey(msg tea.KeyPressMsg) tea.Cmd {
	return a.runKey(a.keys.prList, bindingKey(msg))
}

// prListRefresh asks the poller for each listed repository.
func (a *App) prListRefresh(rows []prListRow) {
	if a.prs.src == nil {
		return
	}

	done := map[string]bool{}

	for _, r := range rows {
		if done[r.key.repo] {
			continue
		}

		done[r.key.repo] = true

		if repo, ok := repoOfKey(r.key.repo); ok {
			a.prs.src.Refresh(repo)
		}
	}
}

// prCheckout is the directory a pull request of repo is read from when no agent
// has its branch: a pane resolved to the repository, the active tab's focused
// pane when it is one, else the lowest pane id.
func (a *App) prCheckout(repo string) (dir string, ok bool) {
	focus := a.store.State().Tab().Focus

	var (
		best  core.PaneID
		found bool
	)

	for id, pp := range a.prs.panes {
		if !pp.ok || pp.repo.String() != repo || pp.answered.cwd == "" {
			continue
		}

		if id == focus {
			return pp.answered.cwd, true
		}

		if !found || paneIDLess(id, best) {
			best, found = id, true
		}
	}

	if !found {
		return "", false
	}

	return a.prs.panes[best].answered.cwd, true
}

// paneIDLess orders pane ids "p2" before "p10".
func paneIDLess(x, y core.PaneID) bool {
	if len(x) != len(y) {
		return len(x) < len(y)
	}

	return x < y
}

// prListOpen shows the row's pull request and runs then once its diff has
// loaded. A row with an agent switches to the agent's tab, as the rail's jump
// does; any other opens the active tab's drawer on the focused pane, through a
// checkout of the repository, or says there is none.
func (a *App) prListOpen(row prListRow, then prThen) tea.Cmd {
	st := a.store.State()

	if row.pane == "" {
		return a.prListOpenCheckout(st, row, then)
	}

	a.prList = nil

	ti, ok := st.TabOf(row.pane)
	if !ok {
		return nil
	}

	var cmds []tea.Cmd

	if ti != st.Active {
		cmds = append(cmds, a.act(core.SelectTab{Index: ti}))
	}

	cmds = append(cmds, a.focusPane(row.pane))
	a.railRelease()

	want := &prWant{repo: row.key.repo, number: row.key.number, root: row.root, base: row.pr.BaseBranch}
	cmds = append(cmds, a.openWithPR(row.pane, want, then))

	return tea.Batch(cmds...)
}

// prListOpenCheckout opens the drawer on the row's checkout, or says there is
// none.
func (a *App) prListOpenCheckout(st core.State, row prListRow, then prThen) tea.Cmd {
	dir, ok := a.prCheckout(row.key.repo)
	if !ok || st.Tab().Focus == "" {
		a.notice = fmt.Sprintf(noticePRNoCheckout, prRepoName(row.key.repo))

		return nil
	}

	a.prList = nil

	want := &prWant{repo: row.key.repo, number: row.key.number, root: dir, base: row.pr.BaseBranch}

	return a.openWithPR(st.Tab().Focus, want, then)
}

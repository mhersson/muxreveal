package ui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
)

// noticePRListNone is what the list says when no agent's branch has an open
// pull request.
const noticePRListNone = "no open pull requests on agent branches"

// prListKey identifies a pull request in the list: its repository keyed
// "host/owner/name" and its number.
type prListKey struct {
	repo   string
	number int
}

// prListView is the open pull request list. The selection is kept by key so a
// poll that reorders the rows keeps it; idx is where it last stood, for the
// nearest row when the pull request leaves the list.
type prListView struct {
	sel prListKey
	idx int
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

	return rows
}

// prRepoName is a repository as the list names it: owner/name on github.com,
// host/owner/name elsewhere.
func prRepoName(key string) string {
	return printable(strings.TrimPrefix(key, "github.com/"))
}

// selected is the index of the selected row, or -1 for an empty list. A pull
// request that left the list moves the selection to the row now where it stood.
func (v *prListView) selected(rows []prListRow) int {
	if len(rows) == 0 {
		v.idx = 0

		return -1
	}

	for i, r := range rows {
		if r.key == v.sel {
			v.idx = i

			return i
		}
	}

	v.idx = min(v.idx, len(rows)-1)
	v.sel = rows[v.idx].key

	return v.idx
}

// move selects the row delta away, stopping at the ends.
func (v *prListView) move(rows []prListRow, delta int) {
	i := v.selected(rows)
	if i < 0 {
		return
	}

	v.idx = min(max(i+delta, 0), len(rows)-1)
	v.sel = rows[v.idx].key
}

// prListCommand is `leader g`.
func (a *App) prListCommand() tea.Cmd {
	a.endLeader()

	if a.prs.src == nil {
		a.notice = noticePRsOff

		return nil
	}

	a.prList = &prListView{}

	return nil
}

// handlePRListKey answers the open list; any other key is swallowed.
func (a *App) handlePRListKey(msg tea.KeyPressMsg) tea.Cmd {
	v := a.prList
	rows := a.prListRows(a.store.State())
	i := v.selected(rows)

	key := msg.String()

	switch key {
	case "esc":
		a.prList = nil
	case "j", "down":
		v.move(rows, 1)
	case "k", "up":
		v.move(rows, -1)
	case "r":
		a.prListRefresh(rows)
	}

	if i < 0 {
		return nil
	}

	row := rows[i]

	switch key {
	case "enter":
		a.prList = nil

		return a.prListOpen(row, thenNothing)
	case "c":
		// The checks stack over the list, which stays under them.
		return a.openChecks(row.key.repo, row.key.number)
	case "O":
		return a.openURL(a.prLink(core.ReviewPR{Repo: row.key.repo, Number: row.key.number}))
	case "a":
		if a.actions == nil {
			a.notice = noticePRsOff

			return nil
		}

		if row.pr.ViewerAuthored {
			a.notice = noticeApproveOwn

			return nil
		}

		a.prList = nil

		return a.prListOpen(row, thenApprove)
	case "M":
		if a.actions == nil {
			a.notice = noticePRsOff

			return nil
		}

		a.prList = nil

		return a.prListOpen(row, thenMerge)
	}

	return nil
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

// prListOpen switches to the row's agent, as the rail's jump does, and shows
// the pull request in that tab's drawer, then runs then once its diff has loaded.
func (a *App) prListOpen(row prListRow, then prThen) tea.Cmd {
	st := a.store.State()

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

	want := &prWant{repo: row.key.repo, number: row.key.number, root: row.root}
	cmds = append(cmds, a.openWithPR(row.pane, want, then))

	return tea.Batch(cmds...)
}

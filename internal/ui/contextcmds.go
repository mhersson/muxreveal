package ui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
)

// The default keys of the agents rail, the PR list and the checks overlay;
// [keys.rail], [keys.pr_list] and [keys.checks] are laid over them.
var (
	railKeys = []reviewKey{
		{"j", "rail.next"},
		{"down", "rail.next"},
		{"k", "rail.prev"},
		{"up", "rail.prev"},
		{"/", "rail.search"},
		{"enter", "rail.jump"},
		{"p", "rail.plan"},
		{"esc", "rail.back"},
	}
	prListKeys = []reviewKey{
		{"j", "prlist.next"},
		{"down", "prlist.next"},
		{"k", "prlist.prev"},
		{"up", "prlist.prev"},
		{"tab", "prlist.tab.next"},
		{"shift+tab", "prlist.tab.prev"},
		{"1", "prlist.tab.agents"},
		{"2", "prlist.tab.requested"},
		{"3", "prlist.tab.all"},
		{"enter", "prlist.show"},
		{"c", "prlist.checks"},
		{"a", "prlist.approve"},
		{"M", "prlist.merge"},
		{"O", "prlist.browser"},
		{"r", "prlist.refresh"},
		{"esc", "prlist.close"},
	}
	checksKeys = []reviewKey{
		{"j", "checks.next"},
		{"down", "checks.next"},
		{"k", "checks.prev"},
		{"up", "checks.prev"},
		{"enter", "checks.browser"},
		{"r", "checks.refresh"},
		{"esc", "checks.close"},
	}
)

// scopeNoun is how an error names the view a scoped command belongs to.
func scopeNoun(scope string) string {
	if scope == "pr_list" {
		return "PR list"
	}

	return scope
}

// scoped makes the commands of a view: registered so its [keys] table can name
// them, but never in the palette or under [keys.leader].
func scoped(scope string, cmds []command) []command {
	for i := range cmds {
		cmds[i].scope = scope
	}

	return cmds
}

// runKey runs the command a context's table binds to key; a key without one is dropped.
func (a *App) runKey(t keyTable, key string) tea.Cmd {
	id, ok := t.lookup(key)
	if !ok {
		return nil
	}

	c, ok := a.reg.get(id)
	if !ok {
		a.log.Error("key names a missing command", "key", key, "command", id)

		return nil
	}

	return c.run(a)
}

// railOpen reports whether the rail has the keyboard and is not taking search text.
func (a *App) railOpen() bool { return a.rail.focused && !a.rail.searching }

func railCommands() []command {
	cmd := func(id, title string, run func(*App) tea.Cmd) command {
		return command{id: id, title: "Agents rail: " + title, run: func(a *App) tea.Cmd {
			if !a.railOpen() {
				return nil
			}

			return run(a)
		}}
	}

	return scoped("rail", []command{
		cmd("rail.next", "next card", func(a *App) tea.Cmd {
			a.railMove(1)

			return nil
		}),
		cmd("rail.prev", "previous card", func(a *App) tea.Cmd {
			a.railMove(-1)

			return nil
		}),
		cmd("rail.search", "search", func(a *App) tea.Cmd {
			a.railSetSearch(true, a.rail.query)

			return nil
		}),
		cmd("rail.jump", "jump to the agent", func(a *App) tea.Cmd { return a.railJump(a.rail.sel) }),
		cmd("rail.plan", "open the agent's plan", func(a *App) tea.Cmd {
			if a.rail.sel == "" {
				return nil
			}

			return a.openPlan(a.rail.sel)
		}),
		cmd("rail.back", "end the search or go back to the panes", func(a *App) tea.Cmd {
			if a.rail.query != "" {
				a.railSetSearch(false, "")
			} else {
				a.railRelease()
			}

			return nil
		}),
	})
}

func prListCommands() []command {
	// cmd runs on the open list; row says the command needs the selected row.
	cmd := func(id, title string, row bool, run func(a *App, rows []prListRow, i int) tea.Cmd) command {
		return command{id: id, title: "Pull requests: " + title, run: func(a *App) tea.Cmd {
			if a.prList == nil {
				return nil
			}

			rows := a.prListRowsOf(a.store.State(), a.prList.tab)

			i := a.prList.selected(rows)
			if row && i < 0 {
				return nil
			}

			return run(a, rows, i)
		}}
	}

	return scoped("pr_list", []command{
		cmd("prlist.tab.next", "next tab", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prListStep(true)

			return nil
		}),
		cmd("prlist.tab.prev", "previous tab", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prListStep(false)

			return nil
		}),
		cmd("prlist.tab.agents", "agents tab", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prListShow(prTabAgents)

			return nil
		}),
		cmd("prlist.tab.requested", "review requested tab", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prListShow(prTabRequested)

			return nil
		}),
		cmd("prlist.tab.all", "all open tab", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prListShow(prTabAll)

			return nil
		}),
		cmd("prlist.next", "next", false, func(a *App, rows []prListRow, _ int) tea.Cmd {
			a.prList.move(rows, 1)

			return nil
		}),
		cmd("prlist.prev", "previous", false, func(a *App, rows []prListRow, _ int) tea.Cmd {
			a.prList.move(rows, -1)

			return nil
		}),
		cmd("prlist.show", "show the diff", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			if rows[i].pane == "" {
				return nil
			}

			a.prList = nil

			return a.prListOpen(rows[i], thenNothing)
		}),
		// The checks stack over the list, which stays under them.
		cmd("prlist.checks", "show the checks", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			return a.openChecks(rows[i].key.repo, rows[i].key.number)
		}),
		cmd("prlist.approve", "approve", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			if a.actions == nil {
				a.notice = noticePRsOff

				return nil
			}

			if rows[i].pr.ViewerAuthored {
				a.notice = noticeApproveOwn

				return nil
			}

			if rows[i].pane == "" {
				return nil
			}

			a.prList = nil

			return a.prListOpen(rows[i], thenApprove)
		}),
		cmd("prlist.merge", "merge", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			if a.actions == nil {
				a.notice = noticePRsOff

				return nil
			}

			if rows[i].pane == "" {
				return nil
			}

			a.prList = nil

			return a.prListOpen(rows[i], thenMerge)
		}),
		cmd("prlist.browser", "open in the browser", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			return a.openURL(a.prListRowURL(rows[i]))
		}),
		cmd("prlist.refresh", "refresh", false, func(a *App, rows []prListRow, _ int) tea.Cmd {
			a.prListRefresh(rows)

			return a.prSearchCmd()
		}),
		cmd("prlist.close", "close", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prList = nil

			return nil
		}),
	})
}

func checksCommands() []command {
	// cmd runs on the open overlay with its rows and the clamped selection.
	cmd := func(id, title string, run func(a *App, v *checksView, rows []checkRow) tea.Cmd) command {
		return command{id: id, title: "Checks: " + title, run: func(a *App) tea.Cmd {
			v := a.checks
			if v == nil {
				return nil
			}

			rows := checkRows(a.checksOf(a.store.State(), v), a.clock.Now())
			v.sel = min(max(v.sel, 0), max(len(rows)-1, 0))

			return run(a, v, rows)
		}}
	}

	return scoped("checks", []command{
		cmd("checks.next", "next check", func(_ *App, v *checksView, rows []checkRow) tea.Cmd {
			v.sel = min(v.sel+1, max(len(rows)-1, 0))

			return nil
		}),
		cmd("checks.prev", "previous check", func(_ *App, v *checksView, _ []checkRow) tea.Cmd {
			v.sel = max(v.sel-1, 0)

			return nil
		}),
		cmd("checks.browser", "open in the browser", func(a *App, v *checksView, rows []checkRow) tea.Cmd {
			if v.sel >= len(rows) {
				return nil
			}

			if rows[v.sel].url == "" {
				a.notice = noticeCheckNoPage

				return nil
			}

			return a.openURL(rows[v.sel].url)
		}),
		cmd("checks.refresh", "refresh", func(a *App, v *checksView, _ []checkRow) tea.Cmd {
			if repo, ok := repoOfKey(v.repo); ok && a.prs.src != nil {
				a.prs.src.Refresh(repo)
			}

			return nil
		}),
		cmd("checks.close", "close", func(a *App, _ *checksView, _ []checkRow) tea.Cmd {
			a.closeChecks()

			return nil
		}),
	})
}

// hintPairs drops the pairs whose command has no key.
func hintPairs(pairs [][2]string) [][2]string {
	return slices.DeleteFunc(pairs, func(p [2]string) bool { return p[0] == "" })
}

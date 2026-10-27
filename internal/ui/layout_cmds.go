package ui

import (
	"errors"
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
)

const noticePreselectBSP = "preselection works in BSP layouts"

// expectedError reports an error the user causes by pressing a key where it
// does nothing; it is shown as a notice and not logged as a failure.
func expectedError(err error) bool {
	for _, e := range []error{
		core.ErrNeedsBSP, core.ErrNeedsMaster, core.ErrNothingToRotate,
		core.ErrSameTab, core.ErrNoTab, core.ErrAlone,
	} {
		if errors.Is(err, e) {
			return true
		}
	}

	return false
}

func (a *App) tabIsBSP() bool {
	m := a.store.State().Tab().Mode

	return m == core.ModeBSP || m == ""
}

// layoutAct applies a layout action, relayouts, and shows the notice for a
// refusal; what names the action for notices that depend on it.
func (a *App) layoutAct(act core.Action, what string) tea.Cmd {
	err := a.dispatch(act)
	if err != nil {
		a.notice = layoutNotice(err, what)
	}

	return a.relayout()
}

func layoutNotice(err error, what string) string {
	switch {
	case errors.Is(err, core.ErrNeedsBSP) && what == "rotate":
		return "rotate works in BSP layouts"
	case errors.Is(err, core.ErrNeedsBSP):
		return noticePreselectBSP
	case errors.Is(err, core.ErrNeedsMaster):
		return "promote works in master-stack layouts"
	case errors.Is(err, core.ErrNothingToRotate):
		return "nothing to rotate"
	}

	return "cannot " + what + ": " + err.Error()
}

// switchLayout applies a mode change and shows the resulting mode, also when
// the tab already had it.
func (a *App) switchLayout(act core.Action, what string) tea.Cmd {
	if err := a.dispatch(act); err != nil {
		a.notice = layoutNotice(err, what)

		return nil
	}

	m := a.store.State().Tab().Mode
	if m == "" {
		m = core.ModeBSP
	}

	a.notice = "layout: " + string(m)

	return a.relayout()
}

func (a *App) cycleLayout() tea.Cmd {
	return a.switchLayout(core.CycleLayoutMode{Area: a.innerArea()}, "cycle layout")
}

func (a *App) setLayout(mode core.LayoutMode) tea.Cmd {
	return a.switchLayout(core.SetLayoutMode{Mode: mode, Area: a.innerArea()}, "set layout")
}

// movePane moves the focused pane to the tab with the 1-based number n; the
// pane keeps its process, PTY and history and only changes size.
func (a *App) movePane(n int, follow bool) tea.Cmd {
	id := a.store.State().Tab().Focus
	if id == "" {
		return nil
	}

	return a.moved(id, n, core.MovePane{ID: id, Tab: n - 1, Follow: follow, Area: a.innerArea()})
}

func (a *App) breakPane() tea.Cmd {
	id := a.store.State().Tab().Focus
	if id == "" {
		return nil
	}

	return a.moved(id, 0, core.BreakPane{ID: id, Area: a.innerArea()})
}

// moved dispatches a move or break of pane id; n is the 1-based tab number
// the user asked for, used in notices.
func (a *App) moved(id core.PaneID, n int, act core.Action) tea.Cmd {
	if err := a.dispatch(act); err != nil {
		a.notice = moveNotice(err, n)

		return nil
	}

	a.endCopyOf(id)
	a.clearSelectionOf(id)

	return a.relayout()
}

func moveNotice(err error, n int) string {
	switch {
	case errors.Is(err, core.ErrSameTab):
		return fmt.Sprintf("the pane is already in tab %d", n)
	case errors.Is(err, core.ErrNoTab):
		return fmt.Sprintf("there is no tab %d", n)
	case errors.Is(err, core.ErrAlone):
		return "the pane is alone in its tab"
	}

	return "cannot move the pane: " + err.Error()
}

func layoutCommands() []command {
	var cmds []command

	for n := 1; n <= 9; n++ {
		cmds = append(cmds, command{
			id:    "pane.move.tab." + strconv.Itoa(n),
			icon:  iconMove,
			title: "Move pane to tab " + strconv.Itoa(n),
			run:   func(a *App) tea.Cmd { return a.movePane(n, true) },
		})
	}

	for n := 1; n <= 9; n++ {
		cmds = append(cmds, command{
			id:    "pane.send.tab." + strconv.Itoa(n),
			icon:  iconMove,
			title: "Send pane to tab " + strconv.Itoa(n),
			run:   func(a *App) tea.Cmd { return a.movePane(n, false) },
		})
	}

	cmds = append(cmds, command{
		id:    "pane.break",
		icon:  iconBreak,
		title: "Break pane into new tab",
		run:   (*App).breakPane,
	}, command{
		id:    "layout.cycle",
		icon:  iconLayoutBSP,
		title: "Cycle layout",
		run:   (*App).cycleLayout,
	})

	for _, m := range []struct {
		mode  core.LayoutMode
		title string
		icon  string
	}{
		{core.ModeBSP, "BSP", iconLayoutBSP},
		{core.ModeMaster, "master-stack", iconLayoutMaster},
		{core.ModeScrolling, "scrolling", iconLayoutScrolling},
	} {
		cmds = append(cmds, command{
			id:    "layout.set." + string(m.mode),
			icon:  m.icon,
			title: "Layout: " + m.title,
			run:   func(a *App) tea.Cmd { return a.setLayout(m.mode) },
		})
	}

	for _, d := range []struct {
		name string
		dir  core.Dir
		icon string
	}{
		{"left", core.Left, iconArrowLeft}, {"down", core.Down, iconArrowDown},
		{"up", core.Up, iconArrowUp}, {"right", core.Right, iconArrowRight},
	} {
		cmds = append(cmds, command{
			id:    "pane.preselect." + d.name,
			icon:  d.icon,
			title: "Preselect " + d.name,
			run:   func(a *App) tea.Cmd { return a.layoutAct(core.Preselect{Dir: d.dir}, "preselect") },
		})
	}

	return append(cmds, command{
		id:    "pane.preselect.clear",
		icon:  iconClose,
		title: "Clear preselection",
		run:   func(a *App) tea.Cmd { return a.layoutAct(core.ClearPreselect{}, "clear preselection") },
	}, command{
		id:    "layout.rotate",
		icon:  iconRotate,
		title: "Rotate split",
		run:   func(a *App) tea.Cmd { return a.layoutAct(core.RotateSplit{}, "rotate") },
	}, command{
		id:    "layout.equalize",
		icon:  iconEqualize,
		title: "Equalize",
		run:   func(a *App) tea.Cmd { return a.layoutAct(core.Equalize{}, "equalize") },
	}, command{
		id:    "layout.promote",
		icon:  iconPromote,
		title: "Promote to master",
		run:   func(a *App) tea.Cmd { return a.layoutAct(core.Promote{}, "promote") },
	})
}

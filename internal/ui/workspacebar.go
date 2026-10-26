package ui

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// Tray module IDs, left to right as the tray shows them.
const (
	modPR     = "pr"
	modAgents = "agents"
	modGit    = "git"
	modLoad   = "load"
	modClock  = "clock"
)

// trayGap is the least number of bar cells between the tabs and the tray.
const trayGap = 2

// trayModule is one tray entry. render returns its styled cells, exactly width
// wide: ` icon value ` on Surface, or the clock's own Clock segment.
type trayModule struct {
	id     string
	width  int
	render func() string
}

// tabInfo is what the bar needs to know about a tab; name is already resolved.
type tabInfo struct {
	n     int // 1-based position
	name  string
	agent bool
}

// tabLevel is how much of a tab segment shows.
type tabLevel int

const (
	levelFull   tabLevel = iota // ` N icon name `
	levelIcon                   // ` N icon `
	levelNumber                 // ` N `
)

// planTab is a visible tab segment.
type planTab struct {
	idx   int
	level tabLevel
}

// barPlan is what the workspace bar shows: the visible tab segments, the tray
// modules that stayed, and the cells each takes.
type barPlan struct {
	tabs         []planTab
	tray         []trayModule
	tabsW, trayW int
}

// segWidth is the cells of a tab segment, its edge excluded.
func segWidth(t tabInfo, level tabLevel, icons bool) int {
	d := len(strconv.Itoa(t.n))

	switch {
	case level == levelNumber:
		return d + 2
	case !icons:
		return d + ansi.StringWidth(t.name) + 3
	case level == levelIcon:
		return d + 4
	}

	return d + ansi.StringWidth(t.name) + 5
}

// tabsWidth is the cells of the visible segments with their edges: one edge
// cell after each with icons, one bar cell between them without.
func tabsWidth(tabs []tabInfo, vis []planTab, icons bool) int {
	w := 0
	for _, v := range vis {
		w += segWidth(tabs[v.idx], v.level, icons)
	}

	if icons {
		return w + len(vis)
	}

	return w + max(len(vis)-1, 0)
}

// trayWidth is the cells of the tray: the opening edge, the modules with one
// divider cell between them, and the clock's own edge. Without icons there are
// no edges.
func trayWidth(mods []trayModule, icons bool) int {
	if len(mods) == 0 {
		return 0
	}

	w := 0
	for _, m := range mods {
		w += m.width
	}

	w += len(mods) - 1

	if !icons {
		return w
	}

	// Each module after the first follows a divider or the clock's edge; the
	// first follows the opening edge.
	return w + 1
}

// planBar decides what the bar shows in width cells. As the width shrinks it
// gives way in DESIGN's order: load, git and PR leave the tray; inactive tab
// names shrink to the icon and then the number, longest names first; the clock
// leaves; the active name shrinks the same way; the agents leave; finally the
// segments farthest from the active tab are left out.
func planBar(tabs []tabInfo, active int, mods []trayModule, width int, icons bool) barPlan {
	active = min(max(active, 0), len(tabs)-1)
	tray := slices.Clone(mods)
	vis := make([]planTab, len(tabs))

	for i := range vis {
		vis[i] = planTab{idx: i}
	}

	measure := func() (int, int) { return tabsWidth(tabs, vis, icons), trayWidth(tray, icons) }
	fits := func() bool {
		tw, rw := measure()
		if rw > 0 {
			tw += trayGap + rw
		}

		return tw <= width
	}
	drop := func(id string) func() bool {
		return func() bool {
			i := slices.IndexFunc(tray, func(m trayModule) bool { return m.id == id })
			if i < 0 {
				return false
			}

			tray = slices.Delete(tray, i, i+1)

			return true
		}
	}
	// shrink lowers the longest name among the segments selected by active.
	shrink := func(to tabLevel, isActive bool) func() bool {
		return func() bool {
			best := -1

			for i, v := range vis {
				if (v.idx == active) != isActive || v.level >= to {
					continue
				}

				if best < 0 || ansi.StringWidth(tabs[v.idx].name) > ansi.StringWidth(tabs[vis[best].idx].name) {
					best = i
				}
			}

			if best < 0 {
				return false
			}

			vis[best].level = to

			return true
		}
	}
	// omit leaves out the farthest segment; on equal distance the right-hand one goes first.
	omit := func() bool {
		best, far := -1, 0

		for i, v := range vis {
			if d := abs(v.idx - active); v.idx != active && d >= far {
				best, far = i, d
			}
		}

		if best < 0 {
			return false
		}

		vis = slices.Delete(vis, best, best+1)

		return true
	}

	stages := []func() bool{drop(modLoad), drop(modGit), drop(modPR)}
	if icons {
		stages = append(stages, shrink(levelIcon, false))
	}

	stages = append(stages, shrink(levelNumber, false), drop(modClock))
	if icons {
		stages = append(stages, shrink(levelIcon, true))
	}

	stages = append(stages, shrink(levelNumber, true), drop(modAgents), omit)

	for _, stage := range stages {
		for !fits() && stage() {
		}
	}

	tw, rw := measure()

	return barPlan{tabs: vis, tray: tray, tabsW: tw, trayW: rw}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}

	return n
}

// tabInfos lists the tabs with their resolved names.
func (a *App) tabInfos() []tabInfo {
	st := a.store.State()
	if len(st.Tabs) == 0 {
		return []tabInfo{{n: 1, name: "shell"}}
	}

	out := make([]tabInfo, len(st.Tabs))

	for i, t := range st.Tabs {
		name := t.Name
		if name == "" {
			if cwd := st.Panes[t.Focus].Cwd; cwd != "" {
				name = printable(filepath.Base(cwd))
			}
		}

		if name == "" {
			name = "shell"
		}

		out[i] = tabInfo{n: i + 1, name: name, agent: st.TabHasAgents(i)}
	}

	return out
}

// trayModules lists the modules the tray offers, left to right.
func (a *App) trayModules() []trayModule { return nil }

// workspaceBar draws row 0: the tab segments and the tray, exactly width cells.
func (a *App) workspaceBar(width int) string {
	return renderBar(a.tabInfos(), a.store.State().Active, a.trayModules(), width, a.icons, a.current().theme.UI)
}

func paint(fg, bg theme.Hex, bold bool) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(fg).Background(bg).Bold(bold)
}

// renderBar plans the bar for width and draws it.
func renderBar(tabs []tabInfo, active int, mods []trayModule, width int, icons bool, ui theme.UI) string {
	if width <= 0 {
		return ""
	}

	p := planBar(tabs, active, mods, width, icons)
	left := renderTabs(p, tabs, active, icons, ui)

	right := ""
	if p.trayW > 0 && p.tabsW+trayGap+p.trayW <= width {
		right = renderTray(p.tray, icons, ui)
	}

	ground := lipgloss.NewStyle().Background(ui.Bar)
	gap := width - p.tabsW - p.trayW

	if right == "" {
		gap = width - p.tabsW
	}

	if gap < 0 {
		return ansi.Truncate(left, width, "")
	}

	return left + ground.Render(strings.Repeat(" ", gap)) + right
}

func renderTabs(p barPlan, tabs []tabInfo, active int, icons bool, ui theme.UI) string {
	ground := lipgloss.NewStyle().Background(ui.Bar)
	active = min(max(active, 0), len(tabs)-1)

	var sb strings.Builder

	for i, v := range p.tabs {
		info := tabs[v.idx]
		isActive := v.idx == active
		bg := ui.Surface

		if isActive {
			bg = ui.Accent
		}

		sb.WriteString(renderSegment(info, v.level, isActive, icons, ui))

		if i == len(p.tabs)-1 {
			if icons {
				sb.WriteString(paint(bg, ui.Bar, false).Render(edgeSolid))
			}

			continue
		}

		if !icons {
			sb.WriteString(ground.Render(" "))
			continue
		}

		nextActive := p.tabs[i+1].idx == active

		switch {
		case !isActive && !nextActive:
			sb.WriteString(paint(ui.Bar, ui.Surface, false).Render(edgeThin))
		case nextActive:
			sb.WriteString(paint(bg, ui.Accent, false).Render(edgeSolid))
		default:
			sb.WriteString(paint(bg, ui.Surface, false).Render(edgeSolid))
		}
	}

	return sb.String()
}

func renderSegment(t tabInfo, level tabLevel, isActive, icons bool, ui theme.UI) string {
	icon := iconShell
	if t.agent {
		icon = iconAgent
	}

	if isActive {
		text := " " + strconv.Itoa(t.n) + " "
		if icons && level != levelNumber {
			text += icon + " "
		}

		if level == levelFull {
			text += t.name + " "
		}

		return paint(ui.OnAccent, ui.Accent, true).Render(text)
	}

	dim := paint(ui.Dim, ui.Surface, false)
	out := dim.Render(" ") + paint(ui.Muted, ui.Surface, true).Render(strconv.Itoa(t.n)) + dim.Render(" ")

	if icons && level != levelNumber {
		out += dim.Render(icon + " ")
	}

	if level == levelFull {
		out += dim.Render(t.name + " ")
	}

	return out
}

// renderTray draws the tray: one Surface region opened by an edge, the modules
// split by thin dividers, and the clock on its own segment.
func renderTray(mods []trayModule, icons bool, ui theme.UI) string {
	var sb strings.Builder

	for i, m := range mods {
		isClock := m.id == modClock

		switch {
		case !icons && i > 0:
			sb.WriteString(paint(ui.Bar, ui.Surface, false).Render("│"))
		case !icons:
		case isClock && i > 0:
			sb.WriteString(paint(ui.Clock, ui.Surface, false).Render(trayOpen))
		case isClock:
			sb.WriteString(paint(ui.Clock, ui.Bar, false).Render(trayOpen))
		case i == 0:
			sb.WriteString(paint(ui.Surface, ui.Bar, false).Render(trayOpen))
		default:
			sb.WriteString(paint(ui.Bar, ui.Surface, false).Render(edgeThin))
		}

		sb.WriteString(m.render())
	}

	return sb.String()
}

package ui

import (
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/harness"
	"github.com/mhersson/muxreveal/internal/md"
)

// planFileRoot reports whether path is a markdown file under one of the folders
// in paths, matched on the path alone so ignored folders count, and the
// directory above the last such folder in it.
func planFileRoot(paths []string, path string) (root string, ok bool) {
	if !filepath.IsAbs(path) || !md.IsMarkdown(filepath.Base(path)) {
		return "", false
	}

	comps := strings.Split(path, string(filepath.Separator))
	best := -1

	for _, p := range paths {
		folder := strings.Split(filepath.Clean(p), string(filepath.Separator))
		// The last component is the file name, so the folder must end before it.
		for i := len(comps) - len(folder) - 1; i >= 1; i-- {
			if slices.Equal(comps[i:i+len(folder)], folder) {
				best = max(best, i)

				break
			}
		}
	}

	if best < 0 {
		return "", false
	}

	return string(filepath.Separator) + filepath.Join(comps[1:best]...), true
}

// notePlans records the plans an event reports, in event order so the last is
// the latest. A path equal to the latest plan's own is a rewrite of it; one the
// overlay shows is recorded read and reloaded, which the returned command does.
func (a *App) notePlans(m core.Pane, id core.PaneID, ev harness.Event) tea.Cmd {
	cur := m.Plan

	var reloads []tea.Cmd

	note := func(p core.Plan) {
		p.At = a.clock.Now()
		shown := p.Path == cur.Path && a.planShowing(id, p.Path)
		p.Unread = !shown

		if a.dispatch(core.NotePlan{ID: id, Plan: p}) != nil {
			return
		}

		cur = p

		if shown {
			reloads = append(reloads, a.reloadPlan(p))
		}
	}

	if ev.Plan != "" && md.IsMarkdown(filepath.Base(ev.Plan)) {
		note(core.Plan{Path: ev.Plan, Kind: core.PlanMode})
	}

	paths := ev.Changed
	if ev.Edit != "" && !ev.Before {
		paths = append([]string{ev.Edit}, paths...)
	}

	for _, p := range paths {
		if cur.Path != "" && p == cur.Path {
			note(core.Plan{Path: p, Root: cur.Root, Kind: cur.Kind})
		} else if root, ok := planFileRoot(a.plans.Paths, p); ok {
			note(core.Plan{Path: p, Root: root, Kind: core.PlanFile})
		}
	}

	return tea.Batch(reloads...)
}

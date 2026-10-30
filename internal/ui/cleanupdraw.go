package ui

import (
	"fmt"
	"strings"

	"github.com/mhersson/muxreveal/internal/core"
)

// cleanupBox draws the cleanup confirm w cells wide with room for at most
// room lines (0 for no limit). It drops the blank rows and then the hint row
// before it clips anything. Text from git and GitHub is drawn without control
// and invisible characters.
func cleanupBox(t cleanupTarget, w, room int, icons bool, st styles) []string {
	title := " Clean up after " + prNumberText(t.number) + " "
	if icons {
		title = " " + iconCleanup + title
	}

	top, row, bottom := boxFrame(w, title, boxStyle{border: st.popupBorder, title: st.mergeTitle, cut: "…"}, st)
	cw := w - 4
	sha := shortOID(t.head)

	check := func(on bool) string {
		if on {
			return st.popupAccent.Render("[x]")
		}

		return st.popup.Render("[ ]")
	}

	lines := []mergeLine{
		{blank: true},
		{text: st.confirmQuestion.Render(prNumberText(t.number) + " " + printable(t.title))},
		{text: st.popupMuted.Render("is merged at " + sha + ". Clean up its worktree?")},
		{blank: true},
		{text: check(t.remove) + st.popup.Render(" remove the worktree  ") + st.popupDim.Render(t.path())},
		{text: st.popupMuted.Render("    " + cleanupPanesNote(t.panes))},
	}

	branch := printable(t.found.branch)

	if t.canDelete() {
		note := "at " + sha + ", the merged head"
		if t.found.tip != t.head {
			note = "behind the merged head " + sha
		}

		on := t.remove && t.del
		if !t.remove {
			note = "needs the worktree removed"
		}

		lines = append(lines,
			mergeLine{text: check(on) + st.popup.Render(" delete the local branch  ") + st.popupDim.Render(branch)},
			mergeLine{text: st.popupMuted.Render("    " + note)})
	} else {
		lines = append(lines,
			mergeLine{text: st.popupMuted.Render("[-] delete the local branch  " + branch)},
			mergeLine{text: st.popupWarn.Render("    kept " + branch + ": " + t.keptReason())})
	}

	pairs := [][2]string{{"enter", "clean up"}, {"w", "worktree"}}
	if t.canDelete() {
		pairs = append(pairs, [2]string{"b", "branch"})
	}

	pairs = append(pairs, [2]string{"esc", "keep"})

	if hints := hintLine(pairs, cw, st); hints != "" {
		lines = append(lines, mergeLine{blank: true}, mergeLine{text: hints, hint: true})
	}

	// The frame takes two lines beyond these.
	for over := len(lines) + 2 - room; room > 0 && over > 0; {
		i := lastBlank(lines)
		if i < 0 {
			break
		}

		lines = append(lines[:i], lines[i+1:]...)
		over--
	}

	if over := len(lines) + 2 - room; room > 0 && over > 0 && len(lines) > 0 && lines[len(lines)-1].hint {
		lines = lines[:len(lines)-1]
	}

	out := []string{top}

	for _, l := range lines {
		out = append(out, row(l.text))
	}

	return append(out, bottom)
}

// cleanupPanesNote is the line under the worktree row: what closes with it.
func cleanupPanesNote(panes []cleanupPane) string {
	var parts []string

	shells := 0

	for _, p := range panes {
		if !p.agent {
			shells++

			continue
		}

		state := "no hooks"
		if p.state == core.AgentIdle {
			state = "idle"
		}

		parts = append(parts, fmt.Sprintf("%s (%s)", printable(p.name), state))
	}

	if shells > 0 {
		parts = append(parts, plural(shells, "shell"))
	}

	switch len(parts) {
	case 0:
		return "clean"
	case 1:
		return "clean; closes " + parts[0]
	}

	return "clean; closes " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

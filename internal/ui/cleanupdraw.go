package ui

// noteNeedsWorktree is under the branch row while the worktree stays.
const noteNeedsWorktree = "needs the worktree removed"

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
	}

	if t.removable() {
		lines = append(lines,
			mergeLine{text: check(t.remove) + st.popup.Render(" remove the worktree  ") + st.popupDim.Render(t.path())},
			mergeLine{text: st.popupMuted.Render("    clean")})
	} else {
		lines = append(lines,
			mergeLine{text: st.popupMuted.Render("[-] remove the worktree  " + t.path())},
			mergeLine{text: st.popupWarn.Render("    " + inIt(t.inUse))})
	}

	branch := printable(t.found.branch)

	switch {
	case !t.canDelete():
		lines = append(lines,
			mergeLine{text: st.popupMuted.Render("[-] delete the local branch  " + branch)},
			mergeLine{text: st.popupWarn.Render("    kept " + branch + ": " + t.keptReason())})
	case !t.removable():
		lines = append(lines,
			mergeLine{text: st.popupMuted.Render("[-] delete the local branch  " + branch)},
			mergeLine{text: st.popupMuted.Render("    " + noteNeedsWorktree)})
	default:
		note := "at " + sha + ", the merged head"
		if t.found.tip != t.head {
			note = "behind the merged head " + sha
		}

		on := t.remove && t.del
		if !t.remove {
			note = noteNeedsWorktree
		}

		lines = append(lines,
			mergeLine{text: check(on) + st.popup.Render(" delete the local branch  ") + st.popupDim.Render(branch)},
			mergeLine{text: st.popupMuted.Render("    " + note)})
	}

	var pairs [][2]string

	if t.removable() {
		pairs = append(pairs, [2]string{"enter", "clean up"}, [2]string{"w", "worktree"})
		if t.canDelete() {
			pairs = append(pairs, [2]string{"b", "branch"})
		}
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

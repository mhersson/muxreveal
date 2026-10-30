package ui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// prListWidth is the list's width: the body less 40 cells, at least 60 where
// the body allows it.
func prListWidth(width int) int {
	return min(max(width-40, min(60, width-4)), width)
}

// prListReview is the review column of a row: its words and colour.
func prListReview(pr core.PRInfo, ui theme.UI) (string, theme.Hex) {
	switch {
	case pr.Draft:
		return "draft", ui.Muted
	case pr.ReviewDecision == "APPROVED":
		return "approved", ui.Success
	case pr.ReviewDecision == "CHANGES_REQUESTED":
		return "changes", ui.Warning
	}

	return "no review yet", ui.Subtle
}

// prListBody draws the groups' headers and rows cw cells wide and gives the
// line the selected row is on.
func (a *App) prListBody(rows []prListRow, sel, cw int, lk look) (lines []string, selLine int) {
	ui := lk.theme.UI
	panel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	prev := ""

	for i, r := range rows {
		if r.key.repo != prev {
			if prev != "" {
				lines = append(lines, "")
			}

			n := 0

			for _, o := range rows[i:] {
				if o.key.repo != r.key.repo {
					break
				}

				n++
			}

			lines = append(lines, prListHeader(prRepoName(r.key.repo), n, cw, panel, ui))
			prev = r.key.repo
		}

		if i == sel {
			selLine = len(lines)
		}

		lines = append(lines, a.prListRowLine(r, i == sel, cw, panel, ui))
	}

	return lines, selLine
}

// prListHeader is a repository's name, a rule and its count at the right.
func prListHeader(name string, count, cw int, panel lipgloss.Style, ui theme.UI) string {
	num := strconv.Itoa(count)
	name = ansi.Truncate(name, max(cw-ansi.StringWidth(num)-4, 1), "…")
	rule := max(cw-ansi.StringWidth(name)-ansi.StringWidth(num)-2, 0)

	return panel.Foreground(ui.Muted).Bold(true).Render(name) + panel.Render(" ") +
		panel.Foreground(ui.Border).Render(strings.Repeat("─", max(rule-1, 0))) + panel.Render(" ") +
		panel.Foreground(ui.Muted).Render(num)
}

// prListRowLine is one row: the number, title and branch at the left and the
// checks, review and age at the right, which give way to the title as the box
// narrows.
func (a *App) prListRowLine(r prListRow, selected bool, cw int, panel lipgloss.Style, ui theme.UI) string {
	bg := ui.Panel
	if selected {
		bg = ui.Selection
	}

	with := func(fg theme.Hex, bold bool) lipgloss.Style { return panel.Background(bg).Foreground(fg).Bold(bold) }

	marker := with(ui.Accent, true).Render(" ")
	if selected {
		marker = with(ui.Accent, true).Render("▎")
	}

	// The right end, widest first: each piece after the checks goes before the title is cut.
	type piece struct {
		text string
		fg   theme.Hex
		bold bool
	}

	var pieces []piece

	glyph, counts := prChecksOf(r.pr, a.icons, ui)
	if glyph.text != "" {
		pieces = append(pieces, piece{glyph.text + " " + counts.text, glyph.fg, glyph.bold})
	}

	words, fg := prListReview(r.pr, ui)
	pieces = append(pieces, piece{words, fg, false})

	if age := railAge(r.pr.UpdatedAt, a.clock.Now()); age != "" {
		pieces = append(pieces, piece{age, ui.Subtle, false})
	}

	num := "#" + strconv.Itoa(r.key.number) + " "
	title := printable(r.pr.Title)
	branch := printable(r.pr.HeadBranch)

	// The title and its number keep at least 12 cells before a piece is dropped.
	right := func() int {
		w := 0

		for i, p := range pieces {
			w += ansi.StringWidth(p.text)
			if i > 0 {
				w += 2
			}
		}

		return w
	}

	for len(pieces) > 0 && cw-1-right()-2 < min(12, ansi.StringWidth(num+title)) {
		pieces = pieces[:len(pieces)-1]
	}

	leftW := cw - 1
	if len(pieces) > 0 {
		leftW -= right() + 2
	}

	leftW = max(leftW, 0)

	// The branch shows whole when both fit, else it takes at most a third of
	// the room, and goes when that is too little to read.
	branchW := ansi.StringWidth(branch)
	if ansi.StringWidth(num+title)+2+branchW > leftW {
		branchW = min(branchW, leftW/3)
	}

	if branchW < 4 && branchW < ansi.StringWidth(branch) {
		branchW = 0
	}

	titleW := leftW - ansi.StringWidth(num)
	if branchW > 0 {
		titleW -= branchW + 2
	}

	title = ansi.Truncate(title, max(titleW, 0), "…")

	left := with(ui.Fg, true).Render(num) + with(ui.Fg, false).Render(title)
	if branchW > 0 {
		left += with(ui.Fg, false).Render("  ") + with(ui.Dim, false).Render(ansi.Truncate(branch, branchW, "…"))
	}

	var tail string

	for i, p := range pieces {
		if i > 0 {
			tail += with(ui.Fg, false).Render("  ")
		}

		tail += with(p.fg, p.bold).Render(p.text)
	}

	gap := max(cw-1-ansi.StringWidth(left)-ansi.StringWidth(tail), 0)

	return marker + left + with(ui.Fg, false).Render(strings.Repeat(" ", gap)) + tail
}

// prListBox draws the list w cells wide with room for at most room lines. A
// list taller than that scrolls; the blank rows and then the hint row go before
// a pull request does.
func (a *App) prListBox(lk look, w, room int) []string {
	ui, st := lk.theme.UI, lk.styles
	panel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	on := func(fg theme.Hex) lipgloss.Style { return panel.Foreground(fg) }

	v := a.prList
	rows := a.prListRows(a.store.State())
	sel := v.selected(rows)

	title := " Pull requests "
	if a.icons {
		title = " " + iconPR + " Pull requests "
	}

	top, row, bottom := boxFrame(w, title, boxStyle{border: st.popupBorder, title: st.pushTitle, cut: "…"}, st)
	cw := w - 5

	tabs := panel.Background(ui.Accent).Foreground(ui.OnAccent).Bold(true).Render(" Agents "+strconv.Itoa(len(rows))+" ") +
		on(ui.Subtle).Render("  Review requested  All open")

	var body []string

	selLine := 0

	if len(rows) == 0 {
		body = []string{row(on(ui.Subtle).Render(noticePRListNone))}
	} else {
		var lines []string

		lines, selLine = a.prListBody(rows, sel, cw, lk)
		for _, l := range lines {
			body = append(body, row(l))
		}
	}

	hint := func(key, label string) string { return st.popupKey.Render(key) + st.popupMuted.Render(" "+label) }
	kt := a.keys.prList
	pairs := hintPairs([][2]string{
		{kt.hint("prlist.show"), "diff"},
		{kt.hint("prlist.checks"), "checks"},
		{kt.hint("prlist.approve"), "approve"},
		{kt.hint("prlist.merge"), "merge"},
		{kt.hint("prlist.browser"), "browser"},
		{kt.hint("prlist.refresh"), "refresh"},
		{kt.hint("prlist.close"), "close"},
	})
	hints, used := "", 0

	for i, kv := range pairs {
		need := ansi.StringWidth(kv[0]) + 1 + ansi.StringWidth(kv[1])
		if i > 0 {
			need += 4
		}

		if used+need > cw {
			break
		}

		if i > 0 {
			hints += st.popup.Render("    ")
		}

		hints += hint(kv[0], kv[1])
		used += need
	}

	// The fullest frame the room holds around the whole list; else the list scrolls.
	frames := []struct{ airy, hints bool }{{true, true}, {false, true}, {false, false}}
	if hints == "" {
		frames = frames[2:]
	}

	pick := frames[len(frames)-1]
	chosen := false

	overhead := func(f struct{ airy, hints bool }) int {
		n := 4 // the frame, the tab row and the blank row under it
		if f.airy {
			n += 2 // the blank rows above the tabs and above the hints
		}

		if f.hints {
			n++
		}

		return n
	}

	for _, f := range frames {
		if overhead(f)+len(body) <= room {
			pick, chosen = f, true

			break
		}
	}

	if !chosen {
		for _, f := range frames {
			if room-overhead(f) >= 3 {
				pick = f

				break
			}
		}
	}

	if win := max(room-overhead(pick), 1); len(body) > win {
		start := min(max(selLine-win/2, 0), len(body)-win)
		body = body[start : start+win]
	}

	out := []string{top}
	if pick.airy {
		out = append(out, row(""))
	}

	out = append(out, row(tabs), row(""))
	out = append(out, body...)

	if pick.airy {
		out = append(out, row(""))
	}

	if pick.hints {
		out = append(out, row(hints))
	}

	return append(out, bottom)
}

// overlayPRList draws the list over base, centred with its top at a quarter of
// the height and ending at least two rows above the bottom.
func (a *App) overlayPRList(base string, lk look) string {
	w := prListWidth(a.width)
	if w < 12 {
		return base
	}

	top := a.height / 4
	lines := a.prListBox(lk, w, max(a.height-2-top, 0))

	return overlayLines(base, a.width, a.height, top, lines, w)
}

package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const (
	// composeBodyRows is how many rows the body box takes when there is room.
	composeBodyRows = 14
	// composeMinBody is the fewest rows it is cut to.
	composeMinBody = 3
	// composeLabelW is the width of a field's mark and label.
	composeLabelW = 10
)

// composeLine is one row of the compose box. cur is the column of the terminal
// cursor in the row, or -1.
type composeLine struct {
	text  string
	blank bool
	cur   int
}

// composeBox draws the compose box w cells wide with room for at most room
// lines. When the box is too tall the body box shrinks to three rows, then the
// blank rows go, then the hints. It returns the lines and the cursor's cell
// within them, or -1 for both when no field shows one.
func (a *App) composeBox(lk look, w, room int) (lines []string, curX, curY int) {
	ui, st := lk.theme.UI, lk.styles
	v := a.compose

	title := " Open a pull request "
	if a.icons {
		title = " " + iconPR + " Open a pull request "
	}

	top, row, bottom := boxFrame(w, title, boxStyle{border: st.popupBorder, title: st.pushTitle, cut: "…"}, st)
	cw := w - 4
	run := func(text string, fg theme.Hex, bold bool) string { return paint(fg, ui.Panel, bold).Render(text) }
	mark := func(f composeField) string {
		if v.field == f {
			return run("❯ ", ui.Accent, true)
		}

		return run("  ", ui.Fg, false)
	}
	label := func(f composeField, name string) string {
		return mark(f) + run(fmt.Sprintf("%-8s", name), ui.Muted, false)
	}
	plain := func(text string) composeLine { return composeLine{text: text, cur: -1} }
	blank := composeLine{blank: true, cur: -1}

	branch, remote := printable(v.t.branch), printable(v.t.remote)
	base := printable(v.t.bases[v.base])

	where := run(printable(repoLabel(v.t.repo)), ui.Muted, false) + run("  ", ui.Fg, false)
	if a.icons {
		where += run(iconGit+" ", ui.Git, false)
	}

	where += run(branch, ui.Dim, false) + run(" → ", ui.Muted, false) + run(base, ui.Dim, false) +
		run("   "+commitCount(v.commits), ui.Subtle, false)

	head := []composeLine{blank, plain(where)}
	if v.t.unpushed > 0 {
		head = append(head, plain(run(fmt.Sprintf("↑%d not pushed yet: creating the PR pushes %s to %s first",
			v.t.unpushed, branch, remote), ui.Info, false)))
	}

	titleView, titleCol := fieldView(v.title, max(cw-composeLabelW-1, 0), paint(ui.Fg, ui.Panel, false))
	titleLine := composeLine{text: label(composeTitle, "title") + titleView, cur: -1}

	if v.field == composeTitle {
		titleLine.cur = composeLabelW + titleCol
	}

	baseLine := plain(label(composeBase, "base") + run(base, ui.Fg, v.field == composeBase) + run("   ←/→ to change", ui.Subtle, false))

	box, fg := "[ ]", ui.Subtle
	if v.draft {
		box, fg = "[x]", ui.Accent
	}

	draftLine := plain(label(composeDraft, "draft") + run(box, fg, v.draft) + run(" open as a draft", ui.Fg, false))

	head = append(head, blank, titleLine, blank, baseLine, blank, draftLine, blank, plain(label(composeBody, "body")))

	tail := []composeLine{blank, plain(run(printable(v.status), statusColor(v, ui), false)), plain(composeHints(cw, v.preview.on, st))}

	// The frame is two lines, the body box two more around its rows.
	rows := composeBodyRowCount(v, room)
	bodyBox := a.composeBody(lk, cw-2, rows)

	all := append(append(head, bodyBox...), tail...)

	for over := len(all) + 2 - room; over > 0; over-- {
		i := lastBlank(composeAsMerge(all))
		if i < 0 {
			break
		}

		all = append(all[:i], all[i+1:]...)
	}

	if len(all)+2 > room && len(all) > 0 {
		// The hints go before anything is clipped.
		all = all[:len(all)-1]
	}

	curX, curY = -1, -1

	out := []string{top}

	for i, l := range all {
		out = append(out, row(l.text))

		if l.cur >= 0 {
			curX, curY = l.cur, i+1
		}
	}

	return append(out, bottom), curX, curY
}

func statusColor(v *composeView, ui theme.UI) theme.Hex {
	if v.statusErr {
		return ui.Error
	}

	return ui.Muted
}

// composeAsMerge adapts rows to lastBlank, which keeps the first row.
func composeAsMerge(lines []composeLine) []mergeLine {
	out := make([]mergeLine, len(lines))
	for i, l := range lines {
		out[i] = mergeLine{text: l.text, blank: l.blank}
	}

	return out
}

// composeBody draws the body's rounded box cw cells wide with rows text rows,
// its source set into the top border at the right.
func (a *App) composeBody(lk look, cw, rows int) (lines []composeLine) {
	ui := lk.theme.UI
	v := a.compose
	border := paint(ui.Border, ui.Panel, false)
	text := paint(ui.Fg, ui.Panel, false)
	inner := max(cw-2, 0)

	toggle := func(name string, shown bool) string {
		if shown {
			return paint(ui.Accent, ui.Panel, true).Render(name)
		}

		return paint(ui.Subtle, ui.Panel, false).Render(name)
	}
	label := border.Render("╭─ ") + toggle("Write", !v.preview.on) + border.Render(" │ ") + toggle("Preview", v.preview.on) + border.Render(" ")
	total := inner + 2

	src := " from " + printable(v.source) + " "
	src = ansi.Truncate(src, max(total-ansi.StringWidth(label)-2, 0), "…")
	topLine := label + border.Render(strings.Repeat("─", max(total-ansi.StringWidth(label)-ansi.StringWidth(src)-2, 0))) +
		paint(ui.Subtle, ui.Panel, false).Render(src) + border.Render("─╮")
	lines = append(lines, composeLine{text: ansi.Truncate(topLine, total, ""), cur: -1})

	bw := max(inner-2, 0)

	var body []string

	r, c := -1, 0

	if v.preview.on {
		body = a.composePreviewRows(lk, bw, rows)
	} else {
		body, r, c = v.body.View(bw, rows, text)
	}

	for i := range rows {
		cell := ""
		if i < len(body) {
			cell = body[i]
		}

		pad := text.Render(strings.Repeat(" ", max(bw-ansi.StringWidth(cell), 0)))
		l := composeLine{text: border.Render("│") + text.Render(" ") + cell + pad + text.Render(" ") + border.Render("│"), cur: -1}

		if v.field == composeBody && i == r {
			l.cur = 2 + c
		}

		lines = append(lines, l)
	}

	lines = append(lines, composeLine{text: border.Render("╰" + strings.Repeat("─", inner) + "╯"), cur: -1})

	return lines
}

// composeBodyRowCount is how many text rows the body box takes in a box with
// room lines, as composeBox lays it out.
func composeBodyRowCount(v *composeView, room int) int {
	// Ten head rows (eleven with the push note), the frame, the box's borders
	// and the three tail rows.
	fixed := 10 + 3 + 2 + 2
	if v.t.unpushed > 0 {
		fixed++
	}

	return min(composeBodyRows, max(room-fixed, composeMinBody))
}

// composePreviewRows paints the visible rows of the rendered body, w cells
// each. It only reads; composePreviewSettle keeps the scroll fitted.
func (a *App) composePreviewRows(lk look, w, rows int) []string {
	p := a.compose.preview
	if p.doc == nil {
		return nil
	}

	lay := p.doc.layout(composePreviewW(a.width), a.icons)
	top := max(0, min(p.top, len(lay.Lines)-rows))
	paintr := newMDPainter(lk.theme, lk.theme.UI.Panel)
	out := make([]string, 0, rows)

	for i := top; i < min(top+rows, len(lay.Lines)); i++ {
		out = append(out, paintr.line(lay.Lines[i], lay.Links, w))
	}

	return out
}

// composeHints are the key hints that fit cw cells; preview says the body box
// shows the preview.
func composeHints(cw int, preview bool, st styles) string {
	toggle := "preview"
	if preview {
		toggle = "write"
	}

	return hintLine([][2]string{{"tab", "next field"}, {"ctrl+e", "body in $EDITOR"}, {"ctrl+s", "create"}, {"esc", "cancel"}, {"ctrl+p", toggle}}, cw, st)
}

// overlayCompose draws the compose box over base, centred.
func (a *App) overlayCompose(base string, lk look) string {
	w := min(maxComposeWidth, a.width-2)
	if w < 4 {
		return base
	}

	lines, _, _ := a.composeBox(lk, w, a.height-2)
	top := max(1, (a.height-len(lines))/2)

	return overlayLines(base, a.width, a.height, top, lines, w)
}

// composeCursor places the terminal's cursor in the field that has it.
func (a *App) composeCursor() *tea.Cursor {
	w := min(maxComposeWidth, a.width-2)
	if w < 4 {
		return nil
	}

	lk := a.current()

	lines, x, y := a.composeBox(lk, w, a.height-2)
	if x < 0 {
		return nil
	}

	top := max(1, (a.height-len(lines))/2)
	left := (a.width - w) / 2

	// The row indents the content two cells from the border.
	cx, cy := left+1+2+x, top+y
	if cx >= a.width-1 || cy >= a.height-1 {
		return nil
	}

	return tea.NewCursor(cx, cy)
}

package ui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const (
	// planMaxBox and planMaxView cap the box and the view inside it; the text is
	// four cells narrower than the view.
	planMaxBox  = 110
	planMaxView = 104
	// planMinBox is the narrowest box that is drawn.
	planMinBox = 24
	// planMinRows is how many doc rows the box keeps before its blank rows and
	// then its hint row go.
	planMinRows = 3
	// planHintGap is the cells between two hints.
	planHintGap = 4
)

// planBox is the plan overlay's box on a width x height terminal. ok is false
// for a terminal too small to draw it; the overlay then stays open unseen.
func planBox(width, height int) (x, y, w, h int, ok bool) {
	w, h = min(planMaxBox, width-4), height-4
	if w < planMinBox || h < 3 {
		return 0, 0, 0, 0, false
	}

	return (width - w) / 2, 2, w, h, true
}

// planRows splits a box of h rows: the doc rows, and whether the two blank rows
// and the hint row fit. The doc keeps planMinRows rows while it can: the blank
// rows go first, then the hint row.
func planRows(h int) (rows int, blanks, hint bool) {
	switch {
	case h-5 >= planMinRows:
		return h - 5, true, true
	case h-3 >= planMinRows:
		return h - 3, false, true
	}

	return max(h-2, 1), false, false
}

// planIndicator says where the view is in the doc.
func planIndicator(lines, rows, top int) string {
	switch {
	case lines <= rows:
		return " all "
	case top <= 0:
		return " top "
	case top >= lines-rows:
		return " end "
	}

	span := lines - rows

	return " " + strconv.Itoa((top*200+span)/(2*span)) + "% "
}

// planHints are the key hints that fit w cells; the least needed drop first and
// esc always stays.
func planHints(w int) [][2]string {
	pairs := [][2]string{{"j/k", "line"}, {"ctrl+d/u", "half page"}, {"]/[", "heading"}, {"/", "search"}, {"enter", "open"}, {"esc", "close"}}
	width := func() int {
		n := planHintGap * (len(pairs) - 1)
		for _, p := range pairs {
			n += ansi.StringWidth(p[0]) + 1 + ansi.StringWidth(p[1])
		}

		return n
	}

	for _, drop := range []string{"/", "]/[", "ctrl+d/u", "enter"} {
		if width() > w {
			pairs = slices.DeleteFunc(pairs, func(p [2]string) bool { return p[0] == drop })
		}
	}

	return pairs
}

// planCutStart is s cut from its start with … to n cells.
func planCutStart(s string, n int) string {
	if w := ansi.StringWidth(s); w > n {
		return ansi.TruncateLeft(s, w-max(n-1, 0), "…")
	}

	return s
}

// planTop fits the title and the tag into the top border of a box w cells
// wide. A path too long for its room is cut from its start; when that would
// leave it under 12 cells the tag drops and the path takes the room, at least
// 4 cells.
func planTop(lead, path, tag string, w int) (title, tagOut string) {
	room := w - 4
	fixed := ansi.StringWidth(lead) + 1
	avail := room - ansi.StringWidth(tag) - 1

	switch {
	case fixed+ansi.StringWidth(path) <= avail:
		return lead + path + " ", tag
	case avail-fixed >= 12:
		return lead + planCutStart(path, avail-fixed) + " ", tag
	}

	return ansi.Truncate(lead+planCutStart(path, max(room-fixed, 4))+" ", room, ""), ""
}

// planLines draws the box w x h: the title and tag in the top border, the view
// centred in the inner width, the hints and the indicator.
func (a *App) planLines(lk look, w, h int) []string {
	v := a.plan
	ui := lk.theme.UI
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(ui.Panel) }
	border := on(ui.BorderFocus)
	pad := func(n int) string { return on(ui.Fg).Render(strings.Repeat(" ", max(n, 0))) }

	rows, blanks, hint := planRows(h)
	inner := w - 2
	vw := min(w-4, planMaxView)
	off := (inner - vw) / 2

	row := func(content string, cw int) string {
		return border.Render("│") + pad(off) + content + pad(inner-off-cw) + border.Render("│")
	}

	lead := " " + v.agent + " · "
	if a.icons {
		lead = " " + iconPlan + " " + v.agent + " · "
	}

	tag := " " + planKindWord(v.plan.Kind)
	if age := railAge(v.plan.At, a.clock.Now()); age != "" {
		tag += " · " + age
	}

	tag += " "

	path := v.plan.Path
	if n := len(v.pages); n > 0 {
		path, tag = v.pages[n-1].path, ""
	}

	title, tag := planTop(lead, shortPath(path, v.root, a.getenv("HOME")), tag, w)
	top := border.Render("╭─") + on(ui.Info).Bold(true).Render(title)

	if tag != "" {
		top += border.Render(strings.Repeat("─", w-4-ansi.StringWidth(title)-ansi.StringWidth(tag))) + on(ui.Muted).Render(tag) + border.Render("─╮")
	} else {
		top += border.Render(strings.Repeat("─", w-3-ansi.StringWidth(title)) + "╮")
	}

	a.sizePlan()

	shown := v.shown()
	ind := planIndicator(len(shown.layout().Lines), rows, shown.top)
	bottom := border.Render("╰"+strings.Repeat("─", w-3-ansi.StringWidth(ind))) + on(ui.Accent).Bold(true).Render(ind) + border.Render("─╯")

	out := []string{top}
	if blanks {
		out = append(out, row("", 0))
	}

	for _, l := range shown.draw(newMDPainter(lk.theme, ui.Panel).withQuery(v.query), true) {
		out = append(out, row(l, vw))
	}

	if blanks {
		out = append(out, row("", 0))
	}

	if hint {
		hints := a.planHintRow(ui, vw-4)
		out = append(out, row(pad(2)+hints, 2+ansi.StringWidth(hints)))
	}

	out = append(out, bottom)

	if v.chooser != nil {
		a.planChooserLines(ui, out)
	}

	return out
}

// planChooserLines draws the link chooser over the box's rows, two cells in from
// each side from the fourth row; a box without a row for it draws nothing.
func (a *App) planChooserLines(ui theme.UI, lines []string) {
	box := a.plan.chooser.lines(ui, ansi.StringWidth(lines[0])-2*wtInsetX, len(lines)-1-wtTopY, a.icons, a.getenv("HOME"))
	if box == nil {
		return
	}

	gap := lipgloss.NewStyle().Background(ui.Panel).Render(" ")
	side := lipgloss.NewStyle().Foreground(ui.BorderFocus).Background(ui.Panel).Render("│")

	for i, l := range box {
		y := wtTopY + i
		lines[y] = cutRow(lines[y], wtInsetX) + l + gap + side
	}
}

// planHintRow is the hints that fit w cells, a gap apart.
func (a *App) planHintRow(ui theme.UI, w int) string {
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(ui.Panel) }

	if a.plan.searching {
		return a.planPromptRow(ui, w)
	}

	var b strings.Builder

	for i, p := range planHints(w) {
		if i > 0 {
			b.WriteString(on(ui.Fg).Render(strings.Repeat(" ", planHintGap)))
		}

		b.WriteString(on(ui.Key).Bold(true).Render(p[0]) + on(ui.Muted).Render(" "+p[1]))
	}

	return ansi.Truncate(b.String(), max(w, 0), "")
}

// planPromptRow is the search prompt in w cells: the query cut from its start
// to leave the caret in view, then the prompt's hints when they fit.
func (a *App) planPromptRow(ui theme.UI, w int) string {
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(ui.Panel) }
	gap := on(ui.Fg).Render(strings.Repeat(" ", planHintGap))
	hints := on(ui.Key).Bold(true).Render("enter") + on(ui.Muted).Render(" find") + gap +
		on(ui.Key).Bold(true).Render("esc") + on(ui.Muted).Render(" cancel")

	q, _ := cleanText(a.plan.query)
	room := w - 3 // "/ " and the caret

	if tail := planHintGap + ansi.StringWidth(ansi.Strip(hints)); room-tail >= 8 {
		room -= tail
	} else {
		hints = ""
	}

	if qw := ansi.StringWidth(q); qw > room {
		q = ansi.TruncateLeft(q, qw-max(room, 0), "")
	}

	row := on(ui.Info).Bold(true).Render("/ ") + on(ui.Fg).Render(q+"▏")
	if hints != "" {
		row += gap + hints
	}

	return ansi.Truncate(row, max(w, 0), "")
}

func planKindWord(k core.PlanKind) string {
	if k == core.PlanFile {
		return "plan file"
	}

	return "plan mode"
}

// overlayPlan draws the plan overlay over base; a terminal too small for the
// box draws nothing.
func (a *App) overlayPlan(base string, lk look) string {
	x, y, w, h, ok := planBox(a.width, a.height)
	if !ok {
		return base
	}

	return overlayAt(base, a.width, a.height, x, y, a.planLines(lk, w, h))
}

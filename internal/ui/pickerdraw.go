package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/ui/palette"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// pickerBox is the box's left column, top row and width; ok is false when the
// terminal is too small to draw it.
func (a *App) pickerBox() (left, top, w int, ok bool) {
	w = min(maxOverlayWidth, a.width-4)
	if w < 2 || a.height < 2 {
		return 0, 0, 0, false
	}

	return (a.width - w) / 2, a.height / 4, w, true
}

// overlayPicker draws the picker over base, centred with its top at a quarter
// of the height and clipped to the body rows.
func (a *App) overlayPicker(base string, lk look) string {
	_, top, w, ok := a.pickerBox()
	if !ok {
		return base
	}

	p := a.picker.open
	pt := pickerPaint{ui: lk.theme.UI, inner: w - 2}

	lines := a.pickerListLines(p, pt)
	if p.page == pageNewWorktree {
		lines = a.pickerFormLines(p, pt)
	}

	return overlayLines(base, a.width, a.height, top, lines, w)
}

// pickerCursor places the terminal's cursor in the field that has the keyboard;
// nil when the box is clipped away.
func (a *App) pickerCursor() *tea.Cursor {
	p := a.picker.open

	left, top, w, ok := a.pickerBox()
	if !ok {
		return nil
	}

	inner := w - 2
	row, col := 1, 0

	if p.page == pageNewWorktree {
		in := p.form.branch
		if p.form.onBase {
			in, row = p.form.base, 2
		}

		_, c := fieldView(in, max(inner-formValueAt-1, 0), lipgloss.NewStyle())
		col = formValueAt + c
	} else {
		// A cell and the prompt come first.
		_, c := fieldView(p.query, inner-4, lipgloss.NewStyle())
		col = 3 + c
	}

	if top+row >= a.height-1 {
		return nil
	}

	return tea.NewCursor(left+1+col, top+row)
}

// fieldView draws an input's text in at most w cells, scrolled left so the
// cursor's cell stays inside, and returns the cursor's column in it.
func fieldView(in textinput.Model, w int, style lipgloss.Style) (string, int) {
	runes := []rune(in.Value())
	pos := min(in.Position(), len(runes))
	start := 0

	for start < pos && drawnWidth(string(runes[start:pos])) >= w {
		start++
	}

	return style.Render(ansi.Truncate(string(runes[start:]), max(w, 0), "")), drawnWidth(string(runes[start:pos]))
}

// pickerPaint draws the picker's box on the panel colour.
type pickerPaint struct {
	ui    theme.UI
	inner int // the cells between the side borders
}

// pickerSeg is a run of a row: text and its style.
type pickerSeg struct {
	text  string
	style lipgloss.Style
}

func (pt pickerPaint) on(fg theme.Hex) lipgloss.Style {
	return lipgloss.NewStyle().Background(pt.ui.Panel).Foreground(fg)
}

func (pt pickerPaint) border() lipgloss.Style { return pt.on(pt.ui.BorderFocus) }

// row puts content between the side borders, cut or padded to the inner width.
func (pt pickerPaint) row(content string, fill lipgloss.Style) string {
	content = ansi.Truncate(content, pt.inner, "")
	pad := fill.Render(strings.Repeat(" ", max(0, pt.inner-lipgloss.Width(content))))
	b := pt.border()

	return b.Render("│") + content + pad + b.Render("│")
}

// top is the top border with the title two cells from the corner.
func (pt pickerPaint) top(title string) string {
	b := pt.border()
	dash := min(1, pt.inner)
	title = ansi.Truncate(" "+title+" ", pt.inner-dash, "")

	return b.Render("╭"+strings.Repeat("─", dash)) + pt.on(pt.ui.Accent).Bold(true).Render(title) +
		b.Render(strings.Repeat("─", pt.inner-dash-lipgloss.Width(title))+"╮")
}

func (pt pickerPaint) divider() string {
	return pt.border().Render("├" + strings.Repeat("─", pt.inner) + "┤")
}

// bottom is the bottom border with count ending one dash before the corner;
// the count is left out when it does not fit.
func (pt pickerPaint) bottom(count string) string {
	b := pt.border()

	cw := lipgloss.Width(count)
	if count == "" || pt.inner < cw+1 {
		return b.Render("╰" + strings.Repeat("─", pt.inner) + "╯")
	}

	return b.Render("╰"+strings.Repeat("─", pt.inner-cw-1)) + pt.on(pt.ui.Muted).Render(count) + b.Render("─╯")
}

// hints is the key hint row: each key in Key bold and its label in Muted, the
// pairs three cells apart, after one cell of margin.
func (pt pickerPaint) hints(pairs [][2]string) string {
	fill := pt.on(pt.ui.Fg)

	var sb strings.Builder

	sb.WriteString(fill.Render(" "))

	for i, h := range pairs {
		if i > 0 {
			sb.WriteString(fill.Render("   "))
		}

		sb.WriteString(pt.on(pt.ui.Key).Bold(true).Render(h[0]))
		sb.WriteString(pt.on(pt.ui.Muted).Render(" " + h[1]))
	}

	return sb.String()
}

func segsWidth(segs []pickerSeg) int {
	w := 0

	for i, s := range segs {
		if i > 0 {
			w += 2
		}

		w += lipgloss.Width(s.text)
	}

	return w
}

// renderSegs joins segs two cells apart, the gap in gap's style.
func renderSegs(segs []pickerSeg, gap lipgloss.Style) string {
	var sb strings.Builder

	for i, s := range segs {
		if i > 0 {
			sb.WriteString(gap.Render("  "))
		}

		sb.WriteString(s.style.Render(s.text))
	}

	return sb.String()
}

func (a *App) pickerListLines(p *picker, pt pickerPaint) []string {
	ui := pt.ui
	fill := pt.on(ui.Fg)
	rows := len(p.matches) + 1

	query, _ := fieldView(p.query, pt.inner-4, fill)
	lines := []string{
		pt.top(a.titled(iconAgent, "New agent")),
		pt.row(fill.Render(" ")+pt.on(ui.Accent).Render("❯ ")+query, fill),
		pt.divider(),
	}

	for i := p.top; i < min(rows, p.top+palette.MaxRows); i++ {
		lines = append(lines, a.pickerRow(p, pt, i))
	}

	return append(lines,
		pt.divider(),
		pt.row(pt.hints([][2]string{{"up/down", "move"}, {"enter", "open"}, {"esc", "close"}}), fill),
		pt.bottom(" "+strconv.Itoa(p.sel+1)+" of "+strconv.Itoa(rows)+" "),
	)
}

// pickerRow draws row i: a worktree, or the new worktree row after the last.
func (a *App) pickerRow(p *picker, pt pickerPaint, i int) string {
	ui := pt.ui
	bg, selected := ui.Panel, i == p.sel

	if selected {
		bg = ui.Selection
	}

	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Background(bg).Foreground(fg) }
	base := on(ui.Fg)
	title := base

	if selected {
		title = base.Bold(true)
	}

	iconColour := ui.Muted
	if selected {
		iconColour = ui.Accent
	}

	if i == len(p.matches) {
		label, lead := "+ new worktree…", ""
		if a.icons {
			label, lead = "new worktree…", on(iconColour).Render(iconNewWorktree)+base.Render(" ")
		}

		return pt.row(base.Render(" ")+lead+title.Render(label), base)
	}

	m := p.matches[i]
	t := p.trees[m.tree]
	avail := pt.inner - 2 // a cell of margin on each side
	lead := ""

	if a.icons {
		colour := ui.Git
		if selected {
			colour = ui.Accent
		}

		lead = on(colour).Render(iconGit) + base.Render(" ")
		avail -= 2
	}

	var marks []pickerSeg

	if n := a.agentsIn(t.wt.Path); n > 0 {
		text := "● " + strconv.Itoa(n) + " agent"
		if n > 1 {
			text += "s"
		}

		marks = append(marks, pickerSeg{text, on(ui.Agent)})
	}

	if t.wt.Locked {
		marks = append(marks, pickerSeg{"locked", on(ui.Warning)})
	}

	if t.wt.Prunable {
		marks = append(marks, pickerSeg{"prunable", on(ui.Subtle)})
	}

	// The path goes first when the row is too narrow, then the label is cut.
	right := append(slices.Clone(marks), pickerSeg{t.short, on(ui.Muted)})
	if lipgloss.Width(t.label)+2+segsWidth(right) > avail {
		right = marks
	}

	room := avail
	if len(right) > 0 {
		room = max(avail-segsWidth(right)-2, 1)
	}

	branch, used := pickerLabel(t.label, room, m.hit, title, on(ui.Match).Bold(true))
	content := base.Render(" ") + lead + branch

	if len(right) > 0 {
		content += base.Render(strings.Repeat(" ", max(avail-used-segsWidth(right), 0))) + renderSegs(right, base)
	}

	return pt.row(content, base)
}

// pickerLabel draws text in room cells, cut with an ellipsis, with the runes at
// the byte offsets in hit emphasised; it returns the cells used.
func pickerLabel(text string, room int, hit map[int]bool, normal, match lipgloss.Style) (string, int) {
	cut := lipgloss.Width(text) > room
	limit := room

	if cut {
		limit = max(room-1, 0)
	}

	var sb, run strings.Builder

	used, runHit := 0, false
	flush := func() {
		if run.Len() == 0 {
			return
		}

		if runHit {
			sb.WriteString(match.Render(run.String()))
		} else {
			sb.WriteString(normal.Render(run.String()))
		}

		run.Reset()
	}

	for bi, r := range text {
		w := lipgloss.Width(string(r))
		if used+w > limit {
			break
		}

		used += w

		if hit[bi] != runHit {
			flush()

			runHit = hit[bi]
		}

		run.WriteRune(r)
	}

	flush()

	if cut && room > 0 {
		sb.WriteString(normal.Render("…"))

		used++
	}

	return sb.String(), used
}

func (a *App) pickerFormLines(p *picker, pt pickerPaint) []string {
	ui, f := pt.ui, p.form
	fill := pt.on(ui.Fg)
	valueW := max(pt.inner-formValueAt-1, 0)

	field := func(label, text string, focused bool) string {
		marker := fill.Render("  ")
		if focused {
			marker = pt.on(ui.Accent).Render("❯") + fill.Render(" ")
		}

		return pt.row(fill.Render(" ")+marker+pt.on(ui.Muted).Render(fmt.Sprintf("%-*s", formLabelW, label))+fill.Render("  ")+text, fill)
	}

	branch, _ := fieldView(f.branch, valueW, fill)
	base, _ := fieldView(f.base, valueW, fill)

	var path string

	if name := strings.TrimSpace(f.branch.Value()); name == "" {
		path = pt.on(ui.Subtle).Render("(from the branch name)")
	} else if dest, err := a.worktreePreview(p.repo, name); err != nil {
		path = pt.on(ui.Error).Render(worktreeReason(err))
	} else {
		path = pt.on(ui.Muted).Render(shortPath(dest, p.main, p.home))
	}

	var status string

	switch {
	case f.busy:
		status = pt.on(ui.Muted).Render("creating the worktree…")
	case f.err != "":
		status = pt.on(ui.Error).Render(f.err)
	}

	lines := []string{
		pt.top(a.titled(iconNewWorktree, "New worktree")),
		field("branch", branch, !f.onBase),
		field("base", base, f.onBase),
		field("path", path, false),
		pt.row(fill.Render(" ")+status, fill),
	}

	for _, t := range f.tools {
		lead, room := "", pt.inner-2 // a cell of margin on each side
		if a.icons {
			lead, room = pt.on(ui.Warning).Render(iconAlert)+fill.Render(" "), room-2
		}

		text := ansi.Truncate(t.Name+": "+t.Hint, max(room, 0), "…")
		lines = append(lines, pt.row(fill.Render(" ")+lead+pt.on(ui.Warning).Render(text), fill))
	}

	return append(lines,
		pt.divider(),
		pt.row(pt.hints([][2]string{{"tab", "next field"}, {"enter", "create"}, {"esc", "back"}}), fill),
		pt.bottom(""),
	)
}

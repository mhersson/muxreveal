package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/ui/palette"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const (
	wtInsetX  = 2  // cells between the drawer's left border and the list box
	wtTopY    = 3  // the box's top row, counted from the drawer's top border
	wtChrome  = 4  // the box's rows besides the places: top border, blank row, hint row, bottom border
	wtMinBoxW = 10 // the narrowest box drawn
	wtLeftMin = 12 // the least cells a row's left text keeps before its right part drops
	wtAgeW    = 3  // the width of the age column
)

// wtGeom is where the list box lies in the drawer's box, and how many rows it shows.
type wtGeom struct{ x, y, w, h, vis int }

// wtGeometry places the box in a drawer box of boxW by boxH cells for rows
// rows. The box ends at least one row above the drawer's bottom border and
// keeps its hint row and bottom border, so it shows fewer rows when short of
// room; ok is false when no row fits.
func wtGeometry(boxW, boxH, rows int) (g wtGeom, ok bool) {
	w := boxW - 2*wtInsetX
	h := min(rows+wtChrome, boxH-1-wtTopY)

	if w < wtMinBoxW || h-wtChrome < 1 {
		return wtGeom{}, false
	}

	return wtGeom{x: wtInsetX, y: wtTopY, w: w, h: h, vis: h - wtChrome}, true
}

// wtTop is the first row shown, moved by whole rows as far as keeping sel in
// view needs.
func wtTop(top, sel, vis, n int) int {
	top = min(max(top, 0), max(n-vis, 0))

	switch {
	case sel < top:
		return sel
	case sel >= top+vis:
		return sel - vis + 1
	}

	return top
}

// wtKey is what drawing the open list reads, for the drawer's draw key: the
// rows do not change within a generation but for their counts, and the ages
// move with the clock.
type wtKey struct {
	on                     bool
	gen, sel, top, counted int
	ages                   string
}

func (d *drawCtx) listKey() wtKey {
	l := d.list
	if l == nil {
		return wtKey{}
	}

	k := wtKey{on: true, gen: l.gen, sel: l.sel, top: l.top}

	for _, r := range l.rows {
		if r.statsKnown {
			k.counted++
		}
	}

	g, ok := wtGeometry(d.lay.box.W, d.lay.box.H, len(l.rows))
	if !ok {
		return k
	}

	k.top = wtTop(l.top, l.sel, g.vis, len(l.rows))

	var ages strings.Builder

	for _, r := range l.rows[k.top:min(k.top+g.vis, len(l.rows))] {
		ages.WriteString(railAge(r.at, d.now))
		ages.WriteByte(',')
	}

	k.ages = ages.String()

	return k
}

// overlayList draws the open list over the drawer's lines, inside its borders.
// Of each row it covers, the drawer keeps the first inner cell; the cell before
// the right border is left blank, as the box's margin.
func (d *drawCtx) overlayList(lines []string) {
	l := d.list
	if l == nil {
		return
	}

	box := d.lay.box

	g, ok := wtGeometry(box.W, box.H, len(l.rows))
	if !ok || len(lines) != box.H {
		return
	}

	gap := railPainter{bg: d.ui.Bg}.line(nil, 1)
	side := paint(d.edge(), d.ui.Bg, false).Render("│")

	for i, line := range d.listBox(g) {
		y := g.y + i
		lines[y] = cutRow(lines[y], g.x) + line + gap + side
	}
}

// listBox draws the box: g.h lines of g.w cells.
func (d *drawCtx) listBox(g wtGeom) []string {
	l, ui := d.list, d.ui
	pt := palette.NewBox(theme.Theme{UI: ui}, g.w)
	top := wtTop(l.top, l.sel, g.vis, len(l.rows))

	out := []string{pt.Top(d.withIcon(iconWorktrees, "Worktrees"))}

	for i := top; i < min(top+g.vis, len(l.rows)); i++ {
		out = append(out, pt.Row(d.listRow(l.rows[i], i == l.sel, pt.Inner()), pt.On(ui.Fg)))
	}

	hints := pt.Hints([][2]string{{"j/k", "move"}, {"enter", "show"}, {"esc", "close"}})

	return append(out, pt.Row("", pt.On(ui.Fg)), pt.Row(hints, pt.On(ui.Fg)), pt.Bottom(""))
}

// listRow draws one row of the list, inner cells wide, on the box's ground or,
// when selected, on Selection.
func (d *drawCtx) listRow(r wtRow, selected bool, inner int) string {
	ui := d.ui
	bg := ui.Panel

	if selected {
		bg = ui.Selection
	}

	p := railPainter{bg: bg}

	switch r.kind {
	case wtGap:
		return p.line(nil, inner)
	case wtHead:
		return p.line(d.headRuns(r.text, inner), inner)
	case wtFollow:
		left := []railRun{{text: "Follow the agent", fg: ui.Fg, bold: true}}
		if d.icons {
			left = append([]railRun{{text: iconFollow + " ", fg: ui.Accent, bold: true}}, left...)
		}

		if !d.pinned {
			left = append(left, railRun{text: "  on", fg: ui.Accent})
		}

		var right []railRun
		if now := printable(d.list.now); now != "" {
			right = []railRun{{text: "now " + now, fg: ui.Subtle}}
		}

		return p.line(d.listLine(selected, inner, left, right), inner)
	}

	return p.line(d.listLine(selected, inner, d.placeLeft(r), d.placeRight(r)), inner)
}

// headRuns is a header: its text, then a rule to one cell short of the inner edge.
func (d *drawCtx) headRuns(text string, inner int) []railRun {
	text = ansi.Truncate(printable(text), max(inner-3, 0), "…")
	rule := max(inner-ansi.StringWidth(text)-3, 0)

	return []railRun{
		{text: " "},
		{text: text + " ", fg: d.ui.Muted, bold: true},
		{text: strings.Repeat("─", rule), fg: d.ui.Border},
	}
}

// placeLeft is a place's repository, branch and marks.
func (d *drawCtx) placeLeft(r wtRow) []railRun {
	ui := d.ui
	runs := []railRun{{text: printable(r.repo), fg: ui.Fg, bold: true}}

	if branch := printable(r.branch); branch != "" {
		runs = append(runs, railRun{text: " · ", fg: ui.Muted}, railRun{text: branch, fg: ui.Dim})
	}

	if r.here {
		runs = append(runs, railRun{text: "  agent here", fg: ui.Subtle})
	}

	if r.latest {
		runs = append(runs, railRun{text: "  latest", fg: ui.Accent})
	}

	return runs
}

// placeRight is a place's changes, once they are known, and the age of its
// latest edit, in a column of its own.
func (d *drawCtx) placeRight(r wtRow) []railRun {
	ui := d.ui

	var runs []railRun

	switch {
	case !r.statsKnown:
	case r.stats.Files == 0:
		runs = []railRun{{text: "clean", fg: ui.Subtle}}
	default:
		runs = statsRuns(ui, r.stats)
	}

	age := fmt.Sprintf("%*s", wtAgeW, railAge(r.at, d.now))

	return append(runs, railRun{text: "  ", fg: ui.Fg}, railRun{text: age, fg: ui.Subtle})
}

// listLine lays a row out: the mark in the first cell, the left text from the
// third, the right text ending one cell before the edge. The left text is cut
// with an ellipsis; when that would leave it under wtLeftMin cells the right
// text drops instead.
func (d *drawCtx) listLine(selected bool, inner int, left, right []railRun) []railRun {
	mark := railRun{text: " ", fg: d.ui.Fg}
	if selected {
		mark = railRun{text: markGlyph, fg: d.ui.Accent}
	}

	// The mark, a blank, the text and a blank margin.
	avail := inner - 3
	room := avail - runsWidth(right) - 2

	if len(right) > 0 && room < min(runsWidth(left), wtLeftMin) {
		right = nil
	}

	if len(right) == 0 {
		room = avail
	}

	left = cutRuns(left, room)
	runs := append([]railRun{mark, {text: " ", fg: d.ui.Fg}}, left...)

	if len(right) == 0 {
		return runs
	}

	pad := avail - runsWidth(left) - runsWidth(right)

	return append(append(runs, railRun{text: strings.Repeat(" ", pad), fg: d.ui.Fg}), right...)
}

package ui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/ui/diffview"
	"github.com/mhersson/aim/internal/ui/theme"
)

// thFor is the diff theme for a row: the flash twin while the row flashes.
func (d *drawCtx) thFor(fresh bool) *diffview.Theme {
	if fresh {
		return d.flash
	}

	return d.dv
}

func diffKind(k git.LineKind) diffview.Kind {
	switch k {
	case git.Add:
		return diffview.Add
	case git.Del:
		return diffview.Delete
	}

	return diffview.Context
}

// spansAt is the tokenised spans of one line, or nil while untokenised.
func (d *drawCtx) spansAt(ref spanRef, ok bool) []diffview.Span {
	if !ok {
		return nil
	}

	ch := d.c.spans[ref.key]
	if ref.at < 0 || ref.at >= len(ch) {
		return nil
	}

	return ch[ref.at]
}

// mark is the first cell of a code row, on the gutter's ground.
func (d *drawCtx) mark(th *diffview.Theme, kind diffview.Kind, cursor bool) string {
	if !cursor {
		return th.BlankGutter(kind, false, markW)
	}

	return lipgloss.NewStyle().Foreground(d.ui.Accent).Background(th.GutterBg(kind, true)).Render(markGlyph)
}

// partner is the index of the line li is paired with, as side by side pairs a
// run of removed lines with the added ones after it; -1 for none.
func partner(ls []git.Line, li int) int {
	switch ls[li].Kind {
	case git.Del:
		d0 := li
		for d0 > 0 && ls[d0-1].Kind == git.Del {
			d0--
		}

		a0 := li
		for a0 < len(ls) && ls[a0].Kind == git.Del {
			a0++
		}

		a1 := a0
		for a1 < len(ls) && ls[a1].Kind == git.Add {
			a1++
		}

		if p := a0 + li - d0; p < a1 {
			return p
		}
	case git.Add:
		a0 := li
		for a0 > 0 && ls[a0-1].Kind == git.Add {
			a0--
		}

		d0 := a0
		for d0 > 0 && ls[d0-1].Kind == git.Del {
			d0--
		}

		if p := d0 + li - a0; p < a0 {
			return p
		}
	}

	return -1
}

// changedIn is the range of line li's cleaned text that differs from its
// partner's.
func changedIn(ls []git.Line, li int, text string) diffview.Range {
	p := partner(ls, li)
	if p < 0 {
		return diffview.Range{}
	}

	other, _ := shownLine(ls[p].Text)
	if ls[li].Kind == git.Del {
		r, _ := diffview.Changed(text, other)

		return r
	}

	_, r := diffview.Changed(other, text)

	return r
}

// unifiedRow is a line of a hunk: two number columns, the sign, the code.
func (d *drawCtx) unifiedRow(r reviewRow, f git.FileDiff, cursor, fresh bool) string {
	h := f.Hunks[r.hunk]
	l := h.Lines[r.line]
	p := partner(h.Lines, r.line)

	other := ""
	if p >= 0 {
		other = h.Lines[p].Text
	}

	text := lineShown(l.Text, other, p >= 0)
	ref, ok := lineRef(r.file, r.hunk, h, r.line)

	part, has := d.partOf(l.Text, tagsCR(l.Text, other, p >= 0), d.v.wrapW.line, r.part)
	if !has {
		part = &wrapPart{} // the row builders never ask past the last part; this only blanks the code defensively
	}

	return d.lineCells(d.thFor(fresh), diffKind(l.Kind), cursor, []int{l.Old, l.New}, text, d.spansAt(ref, ok), changedIn(h.Lines, r.line, text), d.lay.unifiedCode(2), part, r.part == 0)
}

// partOf is the cells of a raw line that row part of it draws, counted as the
// rows were built. A nil part draws the line whole, as when nothing wraps; has
// is false when the line has no such row.
func (d *drawCtx) partOf(raw string, tagCR bool, wrap, part int) (p *wrapPart, has bool) {
	if wrap <= 0 {
		return nil, true
	}

	parts := lineParts(raw, tagCR, wrap)
	if part >= len(parts) {
		return nil, false
	}

	return &parts[part], true
}

// signCells is the sign column of a code row: the sign of the line's first row
// and the wrap mark of a continuation row.
func (d *drawCtx) signCells(th *diffview.Theme, kind diffview.Kind, cursor, first bool) string {
	if first {
		return th.Sign(kind, cursor, false)
	}

	return lipgloss.NewStyle().Foreground(d.ui.Subtle).Background(th.CodeBg(kind, cursor)).Render(wrapGlyph + " ")
}

// lineCells is a line's mark, number columns, sign and code. A nil part draws
// the line whole; a continuation row (not first) has blank numbers and the wrap
// mark.
func (d *drawCtx) lineCells(th *diffview.Theme, kind diffview.Kind, cursor bool, nums []int, text string, spans []diffview.Span, changed diffview.Range, width int, part *wrapPart, first bool) string {
	var b strings.Builder

	b.WriteString(d.mark(th, kind, cursor))

	for _, n := range nums {
		if first {
			b.WriteString(th.Gutter(n, d.lay.gw, kind, cursor))
		} else {
			b.WriteString(th.BlankGutter(kind, cursor, d.lay.gw))
		}
	}

	b.WriteString(d.signCells(th, kind, cursor, first))
	b.WriteString(d.code(th, text, spans, changed, kind, cursor, width, part))

	return b.String()
}

// pairSides are the indexes of the lines a side by side row shows on its left
// and right, -1 for a side with none.
func pairSides(h git.Hunk, r reviewRow) (left, right int) {
	switch h.Lines[r.line].Kind {
	case git.Context:
		return r.line, r.line
	case git.Del:
		return r.line, r.other
	}

	return -1, r.line
}

// pairRow is a row of two lines side by side.
func (d *drawCtx) pairRow(r reviewRow, f git.FileDiff, cursor, fresh bool) string {
	th := d.thFor(fresh)
	h := f.Hunks[r.hunk]
	lay := d.lay

	left, right := pairSides(h, r)

	var lt, rt string

	paired := left >= 0 && right >= 0 && left != right

	if left >= 0 {
		lt = d.sideText(h, left, right, paired)
	}

	if right >= 0 {
		rt = d.sideText(h, right, left, paired)
	}

	var lcr, rcr bool

	if left >= 0 {
		lcr = tagsCR(h.Lines[left].Text, d.otherText(h, right, paired), paired)
	}

	if right >= 0 {
		rcr = tagsCR(h.Lines[right].Text, d.otherText(h, left, paired), paired)
	}

	var lr, rr diffview.Range
	if left >= 0 && right >= 0 && left != right {
		lr, rr = diffview.Changed(lt, rt)
	}

	avail := lay.diffW - markW - 1 - 2*(lay.gw+diffview.SignWidth)
	lw := avail / 2

	var b strings.Builder

	side := func(first bool, idx int, text string, tagCR bool, ch diffview.Range, num func(git.Line) int, width, wrap int) {
		if idx < 0 {
			if first {
				b.WriteString(d.mark(th, diffview.Missing, cursor))
			}

			b.WriteString(th.BlankGutter(diffview.Missing, cursor, lay.gw))
			b.WriteString(th.Blank(diffview.Missing, cursor, diffview.SignWidth+width))

			return
		}

		l := h.Lines[idx]
		kind := diffKind(l.Kind)
		ref, ok := lineRef(r.file, r.hunk, h, idx)

		if first {
			b.WriteString(d.mark(th, kind, cursor))
		}

		part, has := d.partOf(l.Text, tagCR, wrap, r.part)
		if !has {
			// This side wraps to fewer rows: its ground stays, blank.
			b.WriteString(th.BlankGutter(kind, cursor, lay.gw))
			b.WriteString(th.Blank(kind, cursor, diffview.SignWidth+width))

			return
		}

		own := r.part == 0

		if own {
			b.WriteString(th.Gutter(num(l), lay.gw, kind, cursor))
		} else {
			b.WriteString(th.BlankGutter(kind, cursor, lay.gw))
		}

		b.WriteString(d.signCells(th, kind, cursor, own))
		b.WriteString(d.code(th, text, d.spansAt(ref, ok), ch, kind, cursor, width, part))
	}

	side(true, left, lt, lcr, lr, func(l git.Line) int { return l.Old }, lw, d.v.wrapW.left)
	b.WriteString(paint(d.ui.Border, d.ui.Bg, false).Render("│"))
	side(false, right, rt, rcr, rr, func(l git.Line) int { return l.New }, avail-lw, d.v.wrapW.right)

	return b.String()
}

// sideText is the text of line li of a side by side pair.
func (d *drawCtx) sideText(h git.Hunk, li, other int, paired bool) string {
	return lineShown(h.Lines[li].Text, d.otherText(h, other, paired), paired)
}

// otherText is the raw text of the line a paired line is compared with.
func (d *drawCtx) otherText(h git.Hunk, other int, paired bool) string {
	if !paired {
		return ""
	}

	return h.Lines[other].Text
}

// fullRow is a line of the file shown whole, with one number column.
func (d *drawCtx) fullRow(r reviewRow, cursor, fresh bool) string {
	kind := diffview.Context
	if r.other < 0 {
		kind = diffview.Add
	}

	text, _ := shownLine(r.text)
	ref := spanRef{spanKey{r.file, -1, sideNew, r.line / hlChunk}, r.line % hlChunk}

	part, has := d.partOf(r.text, false, d.v.wrapW.full, r.part)
	if !has {
		part = &wrapPart{} // the row builders never ask past the last part; this only blanks the code defensively
	}

	return d.lineCells(d.thFor(fresh), kind, cursor, []int{r.line + 1}, text, d.spansAt(ref, true), diffview.Range{}, d.lay.unifiedCode(1), part, r.part == 0)
}

// ghostGround is the ground of a removed line in the full view.
func (d *drawCtx) ghostGround(cursor, fresh bool) theme.Hex {
	g := mixHex(d.ui.Bg, d.ui.Error, tintGhost)
	if fresh {
		g = mixHex(g, d.ui.Match, tintFlash)
	}

	return d.cursorGround(g, cursor)
}

// ghostRow is a removed line in the full view: muted text on a faint Error
// ground, with no syntax colours and no line number, as the number column
// counts the new file only.
func (d *drawCtx) ghostRow(r reviewRow, cursor, fresh bool) string {
	ui, lay := d.ui, d.lay
	g := d.ghostGround(cursor, fresh)
	gutter := mixHex(ui.Bg, ui.Fg, tintGhostNr)

	if fresh {
		gutter = mixHex(gutter, ui.Match, tintFlash)
	}

	gutter = d.cursorGround(gutter, cursor)
	text, _ := shownLine(r.text)
	runs := d.matchRuns(text, ui.Muted, false)
	sign := railRun{text: "- ", fg: ui.Error, bold: true}

	if part, has := d.partOf(r.text, false, d.v.wrapW.full, r.part); !has {
		runs = nil
	} else if part != nil {
		runs = sliceRuns(runs, part.c0, min(part.c1, part.c0+lay.unifiedCode(1)))
	}

	if r.part > 0 {
		sign = railRun{text: wrapGlyph + " ", fg: ui.Subtle}
	}

	var b strings.Builder

	b.WriteString(railPainter{bg: gutter}.line([]railRun{d.markRun(cursor)}, markW))
	b.WriteString(railPainter{bg: gutter}.line(nil, lay.gw))
	b.WriteString(railPainter{bg: g}.line([]railRun{sign}, diffview.SignWidth))
	b.WriteString(railPainter{bg: g}.line(runs, lay.unifiedCode(1)))

	return b.String()
}

// foldRow is a folded block of removed lines.
func (d *drawCtx) foldRow(r reviewRow, cursor, fresh bool) string {
	ui, lay := d.ui, d.lay
	g := d.ghostGround(cursor, fresh)

	noun := " removed lines"
	if r.other == 1 {
		noun = " removed line"
	}

	lead := markW + lay.gw + diffview.SignWidth

	runs := []railRun{d.markRun(cursor), {text: strings.Repeat(" ", lead-markW)}}
	if d.icons {
		runs = append(runs, railRun{text: iconGhost + " ", fg: ui.Muted})
	}

	runs = append(runs, railRun{text: strconv.Itoa(r.other) + noun, fg: ui.Muted})

	if key, ok := d.bind.first("review.fold"); ok {
		runs = append(runs, railRun{text: "   " + key + " shows them", fg: ui.Subtle})
	}

	return railPainter{bg: g}.line(runs, lay.diffW)
}

// paintRange is a stretch of a line's text drawn over its code.
type paintRange struct {
	diffview.Range
	fg theme.Hex
}

// tagRanges are the hidden-character tags cleanText wrote into text.
func tagRanges(text string) []diffview.Range {
	var out []diffview.Range

	const open, closeTag = "⟨U+", "⟩"

	for at := 0; at < len(text); {
		i := strings.Index(text[at:], open)
		if i < 0 {
			break
		}

		start := at + i
		body := start + len(open)

		j := strings.Index(text[body:], closeTag)
		if j < 0 {
			break
		}

		hex := text[body : body+j]
		if _, err := strconv.ParseUint(hex, 16, 32); err != nil || len(hex) < tagDigitsMin || len(hex) > 6 {
			at = start + len(open)

			continue
		}

		end := body + j + len(closeTag)
		out = append(out, diffview.Range{Start: start, End: end})
		at = end
	}

	return out
}

// code draws a line's text in width cells with the diff theme, then draws the
// hidden-character tags in Error and the search matches in Match over it. A
// nil part draws the whole line, cut with an ellipsis; else the cells of the
// part, clipped to width.
func (d *drawCtx) code(th *diffview.Theme, text string, spans []diffview.Span, changed diffview.Range, kind diffview.Kind, cursor bool, width int, part *wrapPart) string {
	from, to := 0, ansi.StringWidth(text)

	var styled string

	if part == nil {
		styled = th.Code(text, spans, changed, kind, cursor, 0, width)
	} else {
		from, to = part.c0, min(part.c1, part.c0+width)
		styled = th.CodePart(text, spans, []diffview.Range{changed}, kind, cursor, part.c0, part.c1, width)
	}

	tags := tagRanges(text)

	var over []paintRange

	for _, t := range tags {
		over = append(over, paintRange{t, d.ui.Error})
	}

	for _, m := range matchRanges(text, d.query) {
		if !slices.ContainsFunc(tags, func(t diffview.Range) bool { return m.Start < t.End && t.Start < m.End }) {
			over = append(over, paintRange{m, d.ui.Match})
		}
	}

	if len(over) == 0 || width <= 0 {
		return styled
	}

	slices.SortFunc(over, func(p, q paintRange) int { return p.Start - q.Start })

	// A line drawn whole and cut ends in an ellipsis that stays.
	if part == nil && to > width {
		to = width - 1
	}

	var b strings.Builder

	pos := 0

	for _, o := range over {
		c0 := ansi.StringWidth(text[:o.Start])
		c1 := c0 + ansi.StringWidth(text[o.Start:o.End])

		lo, hi := max(c0, from), min(c1, to)
		if lo >= hi {
			if c0 >= to {
				break
			}

			continue
		}

		word := changed.Start <= o.Start && o.Start < changed.End
		seg := ansi.Cut(text[o.Start:o.End], lo-c0, hi-c0)

		b.WriteString(ansi.Cut(styled, pos, lo-from))
		b.WriteString(lipgloss.NewStyle().Foreground(o.fg).Background(th.Bg(kind, cursor, word)).Bold(true).Render(seg))

		pos = hi - from
	}

	b.WriteString(ansi.Cut(styled, pos, width))

	return b.String()
}

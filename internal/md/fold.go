package md

import (
	"slices"
	"strconv"
	"strings"
)

// fold lays out a <details> block: the summary line, then the body when open.
func (l *layouter) fold(out *frag, n *foldNode, width int, c blockCtx) {
	if !l.foldHead(out, n, max(width-c.lead, 1)) {
		return
	}

	c.depth++
	c.lead = 0

	if c.depth > maxNest {
		l.blocks(out, n, width, c, false)

		return
	}

	start := len(out.lines)

	l.blocks(out, n, max(width-2, 1), c, false)
	hang(out, start, l.spaces(2), l.spaces(2), width, false)
}

// foldHead appends the summary lines of n, with the hint when it is folded, and
// reports whether n is open.
func (l *layouter) foldHead(out *frag, n *foldNode, width int) bool {
	open := n.Open
	if v, ok := l.open[n.ID]; ok {
		open = v
	}

	summary := strings.Join(strings.Fields(cleanProse(n.Summary)), " ")
	if summary == "" {
		summary = "Details"
	}

	sst := styled(RoleSummary)
	sst.bold = true
	mark := styled(RoleSummaryMark)
	mark.bold = true
	glyph := "▸ "

	if open {
		glyph = "▾ "
	}

	first := len(out.lines)
	out.folds = append(out.folds, Fold{ID: n.ID, Line: first, Open: open})

	for i, ln := range wrap([]seg{{text: summary, st: sst}}, max(width-2, 1)) {
		if i == 0 {
			ln = concat(Line{mark.run(glyph)}, ln)
		} else {
			ln = concat(l.spaces(2), ln)
		}

		out.lines = append(out.lines, ln)
	}

	if !open && l.foldKey != "" {
		hint := "   " + l.foldKey + " opens"

		last := len(out.lines) - 1
		if lineWidth(out.lines[last])+len(hint) <= width {
			out.lines[last] = append(out.lines[last], styled(RoleFoldHint).run(hint))
		} else {
			out.lines = append(out.lines, concat(l.spaces(2), Line{styled(RoleFoldHint).run(hint[3:])}))
		}
	}

	for i := first; i < len(out.lines); i++ {
		out.lines[i] = fit(out.lines[i], width)
	}

	return open
}

// footnotes lays out the section after the body: a rule, then each referenced
// definition in reference order.
func (l *layouter) footnotes(out *frag, width int) {
	defs, labels := l.d.notes.defs, l.d.notes.labels

	if len(labels) == 0 {
		return
	}

	order := make([]int, 0, len(labels))
	for idx := range labels {
		order = append(order, idx)
	}

	slices.Sort(order)

	numW := 2
	if last := order[len(order)-1]; len(strconv.Itoa(last)) > numW {
		numW = len(strconv.Itoa(last))
	}

	ruled := false

	for _, idx := range order {
		def := defs[labels[idx]]
		if def == nil || l.d.footnotes == nil || l.d.footnotes.FindByLabel([]byte(labels[idx])) == nil {
			continue
		}

		if !ruled {
			ruled = true

			if len(out.lines) > 0 {
				out.lines = append(out.lines, nil)
				out.markNone(len(out.lines) - 1)
			}

			out.lines = append(out.lines, l.rule(RoleFootnoteRule, min(16, width)))
			out.markNone(len(out.lines) - 1)
		}

		num := strconv.Itoa(idx)
		first := Line{styled(RoleFootnoteNumber).run(strings.Repeat(" ", numW-len(num)) + num + ". ")}

		start := len(out.lines)

		l.blocks(out, def, max(width-numW-2, 1), blockCtx{text: RoleFootnoteText}, false)

		if len(out.lines) == start {
			out.lines = append(out.lines, nil)
		}

		hang(out, start, first, l.spaces(numW+2), width, false)
		out.restamp(start, l.d.ranges[def])
	}
}

// imageLine is the one line an image takes: icon, alt text and the host or file
// name. It carries the image's link.
func (l *layouter) imageLine(sg seg) Line {
	st := sg.st
	st.role, st.bold, st.italic, st.strike = RoleImage, false, false, false

	label := "image: "
	if l.icons {
		label = "\U000F0976 "
	}

	text := label + sg.img.alt
	if sg.img.host != "" && sg.img.alt != "" {
		text += "  "
	}

	line := Line{st.run(text)}

	if sg.img.host != "" {
		st.role = RoleImageHost
		line = append(line, st.run(sg.img.host))
	}

	return line
}

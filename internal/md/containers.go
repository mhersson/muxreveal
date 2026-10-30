package md

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
)

// list lays out a list. Past maxNest nothing is indented and the marker goes on
// the item's first line only, which is narrower by it; the other lines use the
// whole width.
func (l *layouter) list(out *frag, n *ast.List, width int, c blockCtx) {
	capped := c.depth >= maxNest
	indent := 0

	if !capped && c.listDepth == 0 {
		indent = 2
	}

	inner := c
	inner.depth++
	inner.listDepth++

	items, numW := listItems(n)

	for i, item := range items {
		marker := l.marker(n, item, i, numW, c.listDepth)
		pad := indent + lineWidth(marker)

		ic := inner
		ic.done = isDone(item)

		start := len(out.lines)

		if capped {
			ic.lead = leadFor(pad, width)
			l.blocks(out, item, width, ic, true)
			attachMarker(out, start, marker, width)
			out.stamp(start, l.d.ranges[item])

			continue
		}

		l.blocks(out, item, max(width-pad, 1), ic, true)

		if len(out.lines) == start {
			out.lines = append(out.lines, nil)
		}

		hang(out, start, concat(l.spaces(indent), marker), l.spaces(pad), width, false)
		out.stamp(start, l.d.ranges[item])
	}
}

// leadFor is the first-line room a marker of pad cells reserves in width cells;
// a marker no narrower than the width reserves none, and is dropped unless the
// line has room for it.
func leadFor(pad, width int) int {
	if pad >= width {
		return 0
	}

	return pad
}

// attachMarker draws marker before the first line an item appended since start,
// when the line has room for it; a line the text fills keeps all its text.
func attachMarker(out *frag, start int, marker Line, width int) {
	if len(out.lines) == start {
		out.lines = append(out.lines, nil)
	}

	if first := out.lines[start]; len(first) == 0 || lineWidth(first)+lineWidth(marker) <= width {
		out.lines[start] = fit(concat(marker, first), width)
	}
}

// listItems returns the items of n and the digits of its largest number.
func listItems(n *ast.List) (items []ast.Node, numW int) {
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if _, ok := ch.(*ast.ListItem); ok {
			items = append(items, ch)
		}
	}

	if n.IsOrdered() {
		numW = max(len(strconv.Itoa(n.Start)), len(strconv.Itoa(n.Start+len(items)-1)))
	}

	return items, numW
}

func isDone(item ast.Node) bool {
	s, ok := extension.TaskStatusOf(item)

	return ok && s == extension.TaskStatusCompleted
}

// marker is the bullet, number or task mark of item i of list n.
func (l *layouter) marker(n *ast.List, item ast.Node, i, numW, depth int) Line {
	var text string

	if n.IsOrdered() {
		num := strconv.Itoa(n.Start + i)
		text = strings.Repeat(" ", numW-len(num)) + num + ". "
	} else {
		text = [...]string{"•", "◦", "▪"}[min(depth, 2)] + " "
	}

	if s, ok := extension.TaskStatusOf(item); ok {
		mark, glyph := styled(RoleTaskOpen), "○ "

		if s == extension.TaskStatusCompleted {
			mark, glyph = styled(RoleTaskDone), "✓ "
			mark.bold = true
		}

		return concat(Line{mark.run(glyph)}, l.spaces(utf8.RuneCountInString(text)-2))
	}

	return Line{styled(RoleBullet).run(text)}
}

// quote lays out a block quote; past maxNest it adds no more bars.
func (l *layouter) quote(out *frag, n *ast.Blockquote, width int, c blockCtx) {
	inner := c
	inner.text = RoleQuoteText
	inner.depth++

	l.barred(out, n, width, inner, styled(RoleQuoteBar), c.depth >= maxNest)
}

// barred appends n's blocks behind a bar on every line, blank ones too.
func (l *layouter) barred(out *frag, n ast.Node, width int, c blockCtx, bar style, capped bool) {
	if capped {
		l.blocks(out, n, width, c, false)

		return
	}

	start := len(out.lines)

	l.blocks(out, n, max(width-2, 1), c, false)

	b := Line{bar.run("▎ ")}
	hang(out, start, b, b, width, true)
}

var alertTitles = [...]struct {
	title, icon string
	role        Role
}{
	alertNote:      {"Note", "\U000F02FD", RoleAlertNote},
	alertTip:       {"Tip", "\U000F0336", RoleAlertTip},
	alertImportant: {"Important", "\U000F0A04", RoleAlertImportant},
	alertWarning:   {"Warning", "\U000F002A", RoleAlertWarning},
	alertCaution:   {"Caution", "\U000F0CE6", RoleAlertCaution},
}

func (l *layouter) alert(out *frag, n *alertNode, width int, c blockCtx) {
	head, st := l.alertTitle(n)
	capped := c.depth >= maxNest

	inner := c
	inner.text = RoleText
	inner.depth++

	if !capped {
		head = concat(Line{st.run("▎ ")}, head)
	}

	out.lines = append(out.lines, fit(head, max(width-c.lead, 1)))
	inner.lead = 0

	l.barred(out, n, width, inner, st, capped)
}

// alertTitle is the title line of n without its bar, and the alert's style.
func (l *layouter) alertTitle(n *alertNode) (Line, style) {
	a := alertTitles[n.Alert]

	title := a.title
	if l.icons {
		title = a.icon + " " + title
	}

	st := styled(a.role)
	head := Line{st.run(title)}
	head[0].Bold = true

	return head, st
}

package md

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
	east "github.com/yuin/goldmark/v2/extension/ast"
)

const (
	// maxWidth is the widest line a layout produces, whatever width is asked for.
	maxWidth = 100
	// maxNest is the depth past which lists and quotes add no more indentation.
	maxNest = 16
)

// Role says what a run is for. The UI maps each role to theme colours; the
// layout never carries a colour.
type Role uint8

const (
	RoleText Role = iota
	RoleHeading1
	RoleHeading2
	RoleHeading3
	RoleRule
	RoleCode
	RoleLink
	RoleKey
	RoleBullet
	RoleTaskDone
	RoleTaskDoneText
	RoleTaskOpen
	RoleCodeFrame
	RoleCodeLang
	RoleLineNumber
	RoleCodeText
	RoleHidden
	RoleQuoteBar
	RoleQuoteText
	RoleAlertNote
	RoleAlertTip
	RoleAlertImportant
	RoleAlertWarning
	RoleAlertCaution
	RoleSummaryMark
	RoleSummary
	RoleFoldHint
	RoleImage
	RoleImageHost
	RoleTableFrame
	RoleTableHeader
	RoleFootnoteRef
	RoleFootnoteRule
	RoleFootnoteNumber
	RoleFootnoteText
)

// Highlighter classifies the bytes of code lines for RoleCodeText runs; nil
// leaves code plain.
type Highlighter func(lang string, lines []string) [][]Span

// Span is a highlighted byte range of one code line.
type Span struct {
	Start, End int
	Class      uint8 // the highlighter's own class
}

// LayoutOptions configures Layout.
type LayoutOptions struct {
	Width     int          // cells; at most 100 are used
	Open      map[int]bool // by Fold.ID; a present key overrides the fold's default
	Icons     bool
	Highlight Highlighter
}

// Layout is a document laid out for one width.
type Layout struct {
	Lines  []Line
	Links  []Link
	Images []Image
	Folds  []Fold
}

// Line is one screen line, never wider than the width laid out for.
type Line []Run

// Run is a stretch of text with one style.
type Run struct {
	Text      string
	Role      Role
	Bold      bool
	Italic    bool
	Underline bool
	Strike    bool
	Class     uint8  // the highlighter's class, for RoleCodeText
	Link      int    // index into Layout.Links, -1 for none
	Path      string // a relative link resolved against a local base
}

// Link is a link target the UI may emit as OSC 8: always http, https or mailto.
type Link struct{ URL string }

// Image is an image reference on a layout line.
type Image struct {
	Line     int
	Alt, Src string
	Link     int
}

// Fold is a foldable block.
type Fold struct {
	ID, Line int
	Open     bool // the state laid out, after LayoutOptions.Open
}

// Plain returns each line's text, for search and copy.
func (l Layout) Plain() []string {
	out := make([]string, len(l.Lines))

	for i, line := range l.Lines {
		var b strings.Builder
		for _, r := range line {
			b.WriteString(r.Text)
		}

		out[i] = b.String()
	}

	return out
}

// layouter holds the state of one Layout call; the Doc stays untouched.
type layouter struct {
	d     *Doc
	width int
	open  map[int]bool
	icons bool
	out   Layout
}

// blockCtx is what a block inherits from the containers around it.
type blockCtx struct {
	text      Role // the role of RoleText runs
	depth     int  // lists and quotes entered
	listDepth int  // lists entered
	done      bool // the next paragraph is a done task's text
}

// frag collects lines with the images and folds on them. Blocks append to it
// and indexes are absolute, so nesting copies no lines.
type frag struct {
	lines  []Line
	images []Image
	folds  []Fold
}

// Layout wraps the document to o.Width cells (at most 100, at least 1).
func (d *Doc) Layout(o LayoutOptions) Layout {
	l := &layouter{d: d, width: min(max(o.Width, 1), maxWidth), open: o.Open, icons: o.Icons}

	var f frag

	l.blocks(&f, d.root, l.width, blockCtx{}, false)
	l.footnotes(&f, l.width)

	l.out.Lines, l.out.Images, l.out.Folds = f.lines, f.images, f.folds
	l.dropUnusedLinks()

	return l.out
}

// blocks appends the children of parent laid out in width cells, a blank line
// apart unless compact.
func (l *layouter) blocks(out *frag, parent ast.Node, width int, c blockCtx, compact bool) {
	start := len(out.lines)

	for ch := parent.FirstChild(); ch != nil; ch = ch.NextSibling() {
		before := len(out.lines)
		if before > start && !compact {
			out.lines = append(out.lines, nil)
		}

		l.block(out, ch, width, c)

		if len(out.lines) == before+1 && out.lines[before] == nil && before > start && !compact {
			out.lines = out.lines[:before]
		}
	}
}

// block appends one block. Code blocks and tables lay out nothing yet.
func (l *layouter) block(out *frag, n ast.Node, width int, c blockCtx) {
	text := c.text
	if c.done {
		text = RoleTaskDoneText
	}

	c.done = false

	switch n := n.(type) {
	case *ast.Paragraph:
		l.flow(out, restyle(l.inlines(n), text, false), width)
	case *ast.Heading:
		l.heading(out, n, width)
	case *ast.List:
		l.list(out, n, width, c)
	case *ast.Blockquote:
		l.quote(out, n, width, c)
	case *alertNode:
		l.alert(out, n, width, c)
	case *foldNode:
		l.fold(out, n, width, c)
	case *htmlFragment:
		l.flow(out, restyle(l.htmlSegs(n.HTML), text, false), width)
	case *ast.HTMLBlock:
		l.htmlBlock(out, n, width, text)
	case *ast.ThematicBreak:
		out.lines = append(out.lines, l.rule(RoleRule, width))
	}
}

// rule is a line of n cells of ─.
func (l *layouter) rule(role Role, n int) Line {
	return Line{styled(role).run(strings.Repeat("─", n))}
}

func styled(role Role) style { return style{role: role, link: -1} }

// restyle gives the RoleText segs the role text, and every seg bold when bold.
func restyle(segs []seg, text Role, bold bool) []seg {
	for i := range segs {
		if segs[i].img != nil || segs[i].brk {
			continue
		}

		if segs[i].st.role == RoleText {
			segs[i].st.role = text
		}

		if bold {
			segs[i].st.bold = true
		}
	}

	return segs
}

// flow wraps segs, giving each image a line of its own.
func (l *layouter) flow(out *frag, segs []seg, width int) {
	start := 0

	for i, sg := range segs {
		if sg.img == nil {
			continue
		}

		out.lines = append(out.lines, wrap(segs[start:i], width)...)
		start = i + 1

		out.images = append(out.images, Image{Line: len(out.lines), Alt: sg.img.alt, Src: sg.img.src, Link: sg.st.link})
		out.lines = append(out.lines, fit(l.imageLine(sg), width))
	}

	out.lines = append(out.lines, wrap(segs[start:], width)...)
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

func (l *layouter) heading(out *frag, h *ast.Heading, width int) {
	role := RoleHeading3

	switch h.Level {
	case 1:
		role = RoleHeading1
	case 2:
		role = RoleHeading2
	}

	start := len(out.lines)

	l.flow(out, restyle(l.inlines(h), role, true), width)

	if h.Level == 1 && len(out.lines) > start {
		out.lines = append(out.lines, l.rule(RoleRule, width))
	}
}

func (l *layouter) htmlBlock(out *frag, h *ast.HTMLBlock, width int, text Role) {
	raw := string(h.Value.Bytes(l.d.src))

	switch h.HTMLBlockKind {
	case ast.HTMLBlockKind2, ast.HTMLBlockKind3, ast.HTMLBlockKind4, ast.HTMLBlockKind5:
		return
	case ast.HTMLBlockKind1:
		if name, _, ok := tagName(strings.TrimLeft(raw, " \t\r\n")); ok && (name == "script" || name == "style") {
			return
		}
	}

	l.flow(out, restyle(l.htmlSegs(raw), text, false), width)
}

// list lays out a list. Past maxNest the marker stays on the item's first line
// but nothing is indented.
func (l *layouter) list(out *frag, n *ast.List, width int, c blockCtx) {
	capped := c.depth >= maxNest
	indent := 0

	if !capped && c.listDepth == 0 {
		indent = 2
	}

	inner := c
	inner.depth++
	inner.listDepth++

	var items []ast.Node

	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if _, ok := ch.(*ast.ListItem); ok {
			items = append(items, ch)
		}
	}

	numW := 0
	if n.IsOrdered() {
		numW = max(len(strconv.Itoa(n.Start)), len(strconv.Itoa(n.Start+len(items)-1)))
	}

	for i, item := range items {
		marker := l.marker(n, item, i, numW, c.listDepth)
		pad := indent + lineWidth(marker)

		ic := inner
		ic.done = isDone(item)

		start := len(out.lines)

		l.blocks(out, item, max(width-pad, 1), ic, true)

		if len(out.lines) == start {
			out.lines = append(out.lines, nil)
		}

		rest := l.spaces(pad)
		if capped {
			rest = nil
		}

		hang(out, start, concat(l.spaces(indent), marker), rest, width, false)
	}
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

func (l *layouter) spaces(n int) Line {
	if n <= 0 {
		return nil
	}

	return Line{plainStyle().run(strings.Repeat(" ", n))}
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
	a := alertTitles[n.Alert]
	capped := c.depth >= maxNest

	inner := c
	inner.text = RoleText
	inner.depth++

	title := a.title
	if l.icons {
		title = a.icon + " " + title
	}

	st := styled(a.role)
	head := Line{st.run(title)}
	head[0].Bold = true

	if !capped {
		head = concat(Line{st.run("▎ ")}, head)
	}

	out.lines = append(out.lines, fit(head, width))
	l.barred(out, n, width, inner, st, capped)
}

// fold lays out a <details> block: the summary line, then the body when open.
func (l *layouter) fold(out *frag, n *foldNode, width int, c blockCtx) {
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

	if !open {
		const hint = "   enter opens"

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

	if !open {
		return
	}

	c.depth++

	if c.depth > maxNest {
		l.blocks(out, n, width, c, false)

		return
	}

	start := len(out.lines)

	l.blocks(out, n, max(width-2, 1), c, false)
	hang(out, start, l.spaces(2), l.spaces(2), width, false)
}

// footnotes lays out the section after the body: a rule, then each referenced
// definition in reference order.
func (l *layouter) footnotes(out *frag, width int) {
	defs := map[string]*east.FootnoteDefinition{}
	labels := map[int]string{}

	_ = ast.Walk(l.d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		switch n := n.(type) {
		case *east.FootnoteDefinition:
			defs[n.Label.Value(l.d.src)] = n

			return ast.WalkSkipChildren, nil
		case *east.FootnoteReference:
			labels[n.Index] = n.Label.Value(l.d.src)
		}

		return ast.WalkContinue, nil
	})

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
			}

			out.lines = append(out.lines, l.rule(RoleFootnoteRule, min(16, width)))
		}

		num := strconv.Itoa(idx)
		first := Line{styled(RoleFootnoteNumber).run(strings.Repeat(" ", numW-len(num)) + num + ". ")}

		start := len(out.lines)

		l.blocks(out, def, max(width-numW-2, 1), blockCtx{text: RoleFootnoteText}, false)

		if len(out.lines) == start {
			out.lines = append(out.lines, nil)
		}

		hang(out, start, first, l.spaces(numW+2), width, false)
	}
}

// hang puts first before the first line appended since start and rest before
// the others, cutting lines at width. A later blank line gets rest only when
// blanks is set.
func hang(out *frag, start int, first, rest Line, width int, blanks bool) {
	end := len(out.lines)
	if len(rest) == 0 {
		end = min(end, start+1)
	}

	for i := start; i < end; i++ {
		prefix := rest
		if i == start {
			prefix = first
		}

		if (len(out.lines[i]) == 0 && i > start && !blanks) || len(prefix) == 0 {
			continue
		}

		out.lines[i] = fit(concat(prefix, out.lines[i]), width)
	}
}

// concat joins a and b, merging the runs at the seam when they match.
func concat(a, b Line) Line {
	if len(a) == 0 || len(b) == 0 {
		return append(slices.Clone(a), b...)
	}

	out := make(Line, 0, len(a)+len(b))
	out = append(out, a...)

	if last := &out[len(out)-1]; sameStyle(*last, b[0]) {
		last.Text += b[0].Text
		b = b[1:]
	}

	return append(out, b...)
}

func lineWidth(line Line) int {
	w := 0
	for _, r := range line {
		w += ansi.StringWidth(r.Text)
	}

	return w
}

// fit cuts line to width cells, ending in … when it had to cut.
func fit(line Line, width int) Line {
	if lineWidth(line) <= width {
		return line
	}

	out := make(Line, 0, len(line))
	left := width - 1

	for _, r := range line {
		w := ansi.StringWidth(r.Text)
		if w <= left {
			out = append(out, r)
			left -= w

			continue
		}

		if left > 0 {
			if cut := ansi.Truncate(r.Text, left, ""); cut != "" {
				r.Text = cut
				out = append(out, r)
			}
		}

		r.Text = "…"
		out = append(out, r)

		return out
	}

	return out
}

// dropUnusedLinks removes the links no run or image refers to, which an empty
// link leaves behind, and renumbers the rest.
func (l *layouter) dropUnusedLinks() {
	if len(l.out.Links) == 0 {
		return
	}

	used := make([]bool, len(l.out.Links))

	for _, line := range l.out.Lines {
		for _, r := range line {
			if r.Link >= 0 {
				used[r.Link] = true
			}
		}
	}

	for _, im := range l.out.Images {
		if im.Link >= 0 {
			used[im.Link] = true
		}
	}

	remap := make([]int, len(used))

	var kept []Link

	for i, u := range used {
		remap[i] = -1

		if u {
			remap[i] = len(kept)
			kept = append(kept, l.out.Links[i])
		}
	}

	for _, line := range l.out.Lines {
		for i := range line {
			if line[i].Link >= 0 {
				line[i].Link = remap[line[i].Link]
			}
		}
	}

	for i := range l.out.Images {
		if l.out.Images[i].Link >= 0 {
			l.out.Images[i].Link = remap[l.out.Images[i].Link]
		}
	}

	l.out.Links = kept
}

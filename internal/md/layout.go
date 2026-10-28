package md

import (
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark/v2/ast"
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
	d         *Doc
	width     int
	open      map[int]bool
	icons     bool
	highlight Highlighter
	out       Layout
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
	l := &layouter{d: d, width: min(max(o.Width, 1), maxWidth), open: o.Open, icons: o.Icons, highlight: o.Highlight}

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

// block appends one block.
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
	case *ast.CodeBlock:
		l.codeBlock(out, n, width)
	case *east.Table:
		l.table(out, n, width, text)
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

func (l *layouter) spaces(n int) Line {
	if n <= 0 {
		return nil
	}

	return Line{plainStyle().run(strings.Repeat(" ", n))}
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

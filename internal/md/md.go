// Package md parses markdown (PR bodies, plan files, repo docs) into a tree the
// layout walks. It is pure: bytes in, values out, no IO and no UI imports.
package md

import (
	"bytes"
	"html"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
	east "github.com/yuin/goldmark/v2/extension/ast"
	"github.com/yuin/goldmark/v2/parser"
)

// MaxBytes bounds the source a Doc reads; Parse cuts anything past it.
const MaxBytes = 4 << 20

// ParseOptions configures Parse.
type ParseOptions struct {
	Base string // a URL or an absolute directory for relative links and images; empty leaves them unresolved
}

// Doc is a parsed document. It is immutable after Parse, so one Doc can be laid
// out at several widths concurrently.
type Doc struct {
	src       []byte
	root      ast.Node
	folds     int
	cut       bool
	base      base
	footnotes extension.Footnotes
	notes     noteIndex
}

// noteIndex is what the footnote section needs from the tree, found once at
// parse so a layout does not walk the whole tree for it.
type noteIndex struct {
	labels map[int]string                      // the label of each referenced note, by goldmark's number
	defs   map[string]*east.FootnoteDefinition // the definition of each label
}

// Cut reports that the input was longer than MaxBytes and was cut.
func (d *Doc) Cut() bool { return d.cut }

type baseKind uint8

const (
	baseNone baseKind = iota
	baseURL
	baseLocal
)

// base is the classified ParseOptions.Base.
type base struct {
	kind baseKind
	url  *url.URL
	dir  string
}

// Parse reads src once. It never fails and never panics: any input, invalid
// UTF-8 included, yields a Doc.
func Parse(src []byte, o ParseOptions) *Doc {
	src, cut := bound(src)
	d := &Doc{src: src, cut: cut, base: classifyBase(o.Base)}
	d.parse()

	return d
}

// bound returns a private copy of src cut to MaxBytes.
func bound(src []byte) ([]byte, bool) {
	if len(src) <= MaxBytes {
		return bytes.Clone(src), false
	}

	n := bytes.LastIndexByte(src[:MaxBytes], '\n')
	if n < 0 {
		n = MaxBytes
		for n > 0 && !utf8.RuneStart(src[n]) {
			n--
		}
	}

	return bytes.Clone(src[:n]), true
}

func classifyBase(s string) base {
	if s == "" {
		return base{}
	}

	if u, err := url.Parse(s); err == nil && u.IsAbs() && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
		return base{kind: baseURL, url: u}
	}

	if filepath.IsAbs(s) {
		return base{kind: baseLocal, dir: filepath.Clean(s)}
	}

	return base{}
}

func (d *Doc) parse() {
	pc := parser.NewContext()

	defer d.recoverParse()

	p := parser.New(parser.WithExtensions(extension.NewGFMParser(), extension.NewFootnoteParser()))
	d.root = p.Parse(d.src, parser.WithContext(pc))
	d.footnotes = extension.ContextFootnotes(pc)
	d.restructure(d.root)
	d.numberFolds()
	d.indexNotes()
}

// recoverParse is deferred by parse. goldmark is not ours; a bug in it must not
// take the UI down, so a panic leaves an empty document.
func (d *Doc) recoverParse() {
	if recover() != nil {
		d.root = ast.NewDocument()
		d.folds = 0
		d.notes = noteIndex{}
		d.footnotes = extension.ContextFootnotes(parser.NewContext())
	}
}

// restructure runs the alert and fold passes over root's block descendants,
// each container after its children. It walks by parent links instead of
// recursing, so nesting depth costs no stack.
func (d *Doc) restructure(root ast.Node) {
	n, c := root, root.FirstChild()

	for {
		if c == nil {
			d.groupDetails(n)

			if n == root {
				return
			}

			n, c = n.Parent(), n.NextSibling()

			continue
		}

		if q, ok := c.(*ast.Blockquote); ok {
			c = d.convertAlert(q)
		}

		switch c.(type) {
		case *ast.Paragraph, *ast.Heading:
		default:
			if c.HasChildren() {
				n, c = c, c.FirstChild()

				continue
			}
		}

		c = c.NextSibling()
	}
}

func (d *Doc) numberFolds() {
	walkTree(d.root, func(n ast.Node) bool {
		if f, ok := n.(*foldNode); ok {
			f.ID = d.folds
			d.folds++
		}

		return true
	})
}

// indexNotes finds the footnotes to list: the references in the body, then
// those inside each definition they lead to, until no new label appears. The
// first definition of a label wins.
func (d *Doc) indexNotes() {
	d.notes = noteIndex{labels: map[int]string{}, defs: map[string]*east.FootnoteDefinition{}}

	var work []string

	ref := func(n *east.FootnoteReference) {
		label := n.Label.Value(d.src)
		d.notes.labels[n.Index] = label
		work = append(work, label)
	}

	walkTree(d.root, func(n ast.Node) bool {
		switch n := n.(type) {
		case *east.FootnoteDefinition:
			if label := n.Label.Value(d.src); d.notes.defs[label] == nil {
				d.notes.defs[label] = n
			}

			return false
		case *east.FootnoteReference:
			ref(n)
		}

		return true
	})

	read := map[string]bool{}

	for len(work) > 0 {
		label := work[len(work)-1]
		work = work[:len(work)-1]

		def := d.notes.defs[label]
		if def == nil || read[label] {
			continue
		}

		read[label] = true

		walkTree(def, func(n ast.Node) bool {
			switch n := n.(type) {
			case *east.FootnoteDefinition:
				return n == def
			case *east.FootnoteReference:
				ref(n)
			}

			return true
		})
	}
}

// alertKind is the type of a GitHub alert.
type alertKind uint8

const (
	alertNote alertKind = iota
	alertTip
	alertImportant
	alertWarning
	alertCaution
)

var alertMarkers = [...]string{
	alertNote:      "[!NOTE]",
	alertTip:       "[!TIP]",
	alertImportant: "[!IMPORTANT]",
	alertWarning:   "[!WARNING]",
	alertCaution:   "[!CAUTION]",
}

func (k alertKind) String() string {
	return strings.ToLower(alertMarkers[k][2 : len(alertMarkers[k])-1])
}

// maxMarkerLen bounds how much of a quote's first line is joined to look for
// an alert marker.
const maxMarkerLen = 32

var (
	kindFold     = ast.NewNodeKind("MDFold")
	kindAlert    = ast.NewNodeKind("MDAlert")
	kindFragment = ast.NewNodeKind("MDHTMLFragment")
)

// foldNode is a <details> block: its children are the body blocks.
type foldNode struct {
	ast.BaseBlock
	ID      int
	Summary string
	Open    bool // the opener has the open attribute

	named bool // a <summary> has set Summary
}

func newFold() *foldNode {
	n := &foldNode{Summary: "Details"}
	n.Init(n)

	return n
}

func (n *foldNode) Kind() ast.NodeKind { return kindFold }

func (n *foldNode) Dump(_ []byte) *ast.NodeDump {
	return ast.NewNodeDump(n, map[string]any{"ID": n.ID, "Summary": n.Summary})
}

// alertNode is a GitHub alert: its children are the quote's blocks without the
// marker.
type alertNode struct {
	ast.BaseBlock
	Alert alertKind
}

func newAlert(k alertKind) *alertNode {
	n := &alertNode{Alert: k}
	n.Init(n)

	return n
}

func (n *alertNode) Kind() ast.NodeKind { return kindAlert }

func (n *alertNode) Dump(_ []byte) *ast.NodeDump {
	return ast.NewNodeDump(n, map[string]any{"Alert": n.Alert.String()})
}

// htmlFragment is HTML text that sat between <details> tags; the layout reads
// it by the inline-HTML rules.
type htmlFragment struct {
	ast.BaseBlock
	HTML string
}

func newFragment(s string) *htmlFragment {
	n := &htmlFragment{HTML: s}
	n.Init(n)

	return n
}

func (n *htmlFragment) Kind() ast.NodeKind { return kindFragment }

func (n *htmlFragment) Dump(_ []byte) *ast.NodeDump {
	return ast.NewNodeDump(n, map[string]any{"HTML": n.HTML})
}

// convertAlert replaces q with an alertNode when its first line is an alert
// marker, and returns the node now in q's place.
func (d *Doc) convertAlert(q *ast.Blockquote) ast.Node {
	p, ok := q.FirstChild().(*ast.Paragraph)
	if !ok {
		return q
	}

	var (
		line   strings.Builder
		marker []*ast.Text
	)

	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		t, ok := c.(*ast.Text)
		if !ok {
			return q
		}

		marker = append(marker, t)
		line.WriteString(t.Value.Value(d.src))

		if t.SoftLineBreak() || t.HardLineBreak() {
			break
		}

		if line.Len() > maxMarkerLen {
			return q
		}
	}

	kind, ok := alertMarker(line.String())
	if !ok {
		return q
	}

	for _, t := range marker {
		p.RemoveChild(t)
	}

	if !p.HasChildren() {
		q.RemoveChild(p)
	}

	a := newAlert(kind)
	a.SetBlankPreviousLines(q.HasBlankPreviousLines())
	moveChildren(a, q)
	q.Parent().ReplaceChild(q, a)

	return a
}

func alertMarker(line string) (alertKind, bool) {
	line = strings.TrimSpace(line)
	for k, m := range alertMarkers {
		if strings.EqualFold(line, m) {
			return alertKind(k), true
		}
	}

	return 0, false
}

func moveChildren(to, from ast.Node) {
	var kids []ast.Node
	for c := from.FirstChild(); c != nil; c = c.NextSibling() {
		kids = append(kids, c)
	}

	from.RemoveChildren()

	for _, c := range kids {
		to.AppendChild(c)
	}
}

type tagKind uint8

const (
	tagDetailsOpen tagKind = iota
	tagDetailsClose
	tagSummaryOpen
	tagSummaryClose
)

type htmlTag struct {
	kind       tagKind
	start, end int
}

// scanTags finds the <details> and <summary> tags of s in order. Comments are
// skipped, and a tag with no closing '>' ends the scan since no later tag can
// close either.
func scanTags(s string) []htmlTag {
	var tags []htmlTag

	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			break
		}

		i += j
		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				break
			}

			i += 4 + end + 3

			continue
		}

		kind, ok := tagAt(s, i)
		if !ok {
			i++

			continue
		}

		end := tagEnd(s, i)
		if s[end-1] != '>' {
			break
		}

		tags = append(tags, htmlTag{kind: kind, start: i, end: end})
		i = end
	}

	return tags
}

// tagAt reports a details or summary tag opening at s[i].
func tagAt(s string, i int) (kind tagKind, ok bool) {
	p := i + 1

	closing := p < len(s) && s[p] == '/'
	if closing {
		p++
	}

	for _, name := range [...]string{"details", "summary"} {
		end := p + len(name)
		if end > len(s) || !strings.EqualFold(s[p:end], name) {
			continue
		}

		if end < len(s) {
			switch s[end] {
			case ' ', '\t', '\n', '\r', '\f', '>':
			case '/':
				if closing {
					return 0, false
				}
			default:
				return 0, false
			}
		}

		switch {
		case name == "details" && closing:
			kind = tagDetailsClose
		case name == "details":
			kind = tagDetailsOpen
		case closing:
			kind = tagSummaryClose
		default:
			kind = tagSummaryOpen
		}

		return kind, true
	}

	return 0, false
}

// groupDetails folds the HTML blocks and paragraphs of n that carry <details>
// tags into foldNodes. A fold still open at the end of n's children ends there.
func (d *Doc) groupDetails(n ast.Node) {
	found := false

	for c := n.FirstChild(); c != nil && !found; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.HTMLBlock:
			found = true
		case *ast.Paragraph:
			found = d.hasFoldTag(c)
		}
	}

	if !found {
		return
	}

	var kids []ast.Node
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		kids = append(kids, c)
	}

	n.RemoveChildren()

	g := folder{d: d, root: n}
	for _, c := range kids {
		g.add(c)
	}
}

// folder rebuilds one container's children with <details> folds.
type folder struct {
	d     *Doc
	root  ast.Node
	stack []*foldNode
	// pending is the fold opened without a summary in the previous block,
	// which the block straight after it may still name.
	pending *foldNode
}

func (g *folder) target() ast.Node {
	if len(g.stack) > 0 {
		return g.stack[len(g.stack)-1]
	}

	return g.root
}

func (g *folder) add(c ast.Node) {
	if p, ok := c.(*ast.Paragraph); ok && g.d.hasFoldTag(p) {
		g.addParagraph(p)

		return
	}

	h, ok := c.(*ast.HTMLBlock)
	if !ok || (h.HTMLBlockKind != ast.HTMLBlockKind6 && h.HTMLBlockKind != ast.HTMLBlockKind7) {
		g.pending = nil
		g.target().AppendChild(c)

		return
	}

	raw := string(h.Value.Bytes(g.d.src))
	tags := scanTags(raw)
	cand := g.pending
	g.pending = nil
	opened := false
	acted := false
	last := 0
	flush := func(upTo int) {
		if upTo > last {
			if s := strings.TrimSpace(raw[last:upTo]); s != "" {
				g.target().AppendChild(newFragment(s))
			}
		}
	}

	for i := 0; i < len(tags); i++ {
		t := tags[i]
		switch t.kind {
		case tagDetailsOpen:
			flush(t.start)

			f := newFold()
			f.Open = hasOpenAttr(raw[t.start:t.end])
			g.target().AppendChild(f)
			g.stack = append(g.stack, f)
			cand, opened, acted = f, true, true
			last = t.end
		case tagDetailsClose:
			flush(t.start)

			if len(g.stack) > 0 {
				g.stack = g.stack[:len(g.stack)-1]
			}

			acted = true
			last = t.end
		case tagSummaryOpen:
			if cand == nil || cand.named || len(g.stack) == 0 || g.stack[len(g.stack)-1] != cand {
				continue
			}

			flush(t.start)

			end := len(raw)

			next := len(tags)
			for j := i + 1; j < len(tags); j++ {
				if tags[j].kind == tagSummaryClose {
					end, next = tags[j].start, j

					break
				}
			}

			cand.Summary, cand.named = summaryText(raw[t.end:end]), true
			acted = true

			if next < len(tags) {
				last = tags[next].end
			} else {
				last = len(raw)
			}

			i = next
		}
	}

	if !acted {
		g.target().AppendChild(c)

		return
	}

	flush(len(raw))

	if opened && cand != nil && !cand.named && len(g.stack) > 0 && g.stack[len(g.stack)-1] == cand {
		g.pending = cand
	}
}

// rawTag reads the <details> or <summary> tag an inline node holds.
func (d *Doc) rawTag(n ast.Node) (tagKind, string, bool) {
	r, ok := n.(*ast.RawHTML)
	if !ok {
		return 0, "", false
	}

	raw := r.Value.Value(d.src)
	if raw == "" || raw[0] != '<' {
		return 0, "", false
	}

	kind, ok := tagAt(raw, 0)

	return kind, raw, ok
}

// hasFoldTag reports whether p holds a <details> or <summary> tag.
func (d *Doc) hasFoldTag(p *ast.Paragraph) bool {
	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		if _, _, ok := d.rawTag(c); ok {
			return true
		}
	}

	return false
}

// addParagraph splits p at its <details> and <summary> tags by the rules of an
// HTML block: the inline content before a tag stays a paragraph, the content
// after an opener is the new fold's body, and the content after a closer
// follows the fold.
func (g *folder) addParagraph(p *ast.Paragraph) {
	cand := g.pending
	g.pending = nil
	opened := false

	var kids []ast.Node
	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		kids = append(kids, c)
	}

	var cur *ast.Paragraph

	flush := func() {
		if cur != nil {
			g.target().AppendChild(cur)
			cur = nil
		}
	}

	keep := func(n ast.Node) {
		if cur == nil {
			cur = ast.NewParagraph()
		}

		cur.AppendChild(n)
	}

	for i := 0; i < len(kids); i++ {
		kind, raw, ok := g.d.rawTag(kids[i])

		switch {
		case !ok || kind == tagSummaryClose:
			keep(kids[i])
		case kind == tagDetailsOpen:
			flush()

			f := newFold()
			f.Open = hasOpenAttr(raw)
			g.target().AppendChild(f)
			g.stack = append(g.stack, f)
			cand, opened = f, true
		case kind == tagDetailsClose:
			flush()

			if len(g.stack) > 0 {
				g.stack = g.stack[:len(g.stack)-1]
			}
		case cand == nil || cand.named || len(g.stack) == 0 || g.stack[len(g.stack)-1] != cand:
			keep(kids[i])
		default:
			flush()

			end := len(kids)

			for j := i + 1; j < len(kids); j++ {
				if k, _, ok := g.d.rawTag(kids[j]); ok && k == tagSummaryClose {
					end = j

					break
				}
			}

			cand.Summary, cand.named = g.d.inlineSummary(kids[i+1:end]), true
			i = end
		}
	}

	flush()

	if opened && cand != nil && !cand.named && len(g.stack) > 0 && g.stack[len(g.stack)-1] == cand {
		g.pending = cand
	}
}

// inlineSummary is the summary text of the inline nodes between <summary> tags:
// the text of each, a space for a line break or a <br>, <p> or <div> tag, and
// whitespace collapsed. Goldmark has already decoded the entities.
func (d *Doc) inlineSummary(nodes []ast.Node) string {
	var b strings.Builder

	for _, n := range nodes {
		walkTree(n, func(c ast.Node) bool {
			switch c := c.(type) {
			case *ast.Text:
				b.WriteString(c.Value.Value(d.src))

				if c.SoftLineBreak() || c.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.CodeSpan:
				b.WriteString(c.Value.Value(d.src))

				return false
			case *ast.RawHTML:
				if raw := c.Value.Value(d.src); len(raw) > 2 && breaksLine(strings.Trim(raw, "<>")) {
					b.WriteByte(' ')
				}
			}

			return true
		})
	}

	if text := strings.Join(strings.Fields(b.String()), " "); text != "" {
		return text
	}

	return "Details"
}

// summaryText drops tags, decodes entities and collapses whitespace.
func summaryText(s string) string {
	var b strings.Builder

	for len(s) > 0 {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			b.WriteString(s)

			break
		}

		b.WriteString(s[:i])

		j := strings.IndexByte(s[i:], '>')
		if j < 0 {
			break
		}

		if breaksLine(s[i+1 : i+j]) {
			b.WriteByte(' ')
		}

		s = s[i+j+1:]
	}

	if text := strings.Join(strings.Fields(html.UnescapeString(b.String())), " "); text != "" {
		return text
	}

	return "Details"
}

// hasOpenAttr reports whether a <details ...> tag carries the open attribute.
func hasOpenAttr(tag string) bool {
	_, ok := tagAttr(tag, "open")

	return ok
}

// breaksLine reports whether the tag (without its angle brackets) separates
// words: <br>, <p> and <div>, open or closing.
func breaksLine(tag string) bool {
	tag = strings.TrimPrefix(tag, "/")

	name := tag
	if i := strings.IndexAny(tag, " \t\r\n\f/"); i >= 0 {
		name = tag[:i]
	}

	switch strings.ToLower(name) {
	case "br", "p", "div":
		return true
	}

	return false
}

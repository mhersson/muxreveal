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

	defer func() {
		// goldmark is not ours; a bug in it must not take the UI down.
		if recover() != nil {
			d.root = ast.NewDocument()
			d.folds = 0
			d.footnotes = extension.ContextFootnotes(parser.NewContext())
		}
	}()

	p := parser.New(parser.WithExtensions(extension.NewGFMParser(), extension.NewFootnoteParser()))
	d.root = p.Parse(d.src, parser.WithContext(pc))
	d.footnotes = extension.ContextFootnotes(pc)
	d.restructure(d.root)
	d.numberFolds()
}

// restructure runs the alert and fold passes over n's block descendants.
func (d *Doc) restructure(n ast.Node) {
	switch n.(type) {
	case *ast.Paragraph, *ast.Heading:
		return
	}

	for c := n.FirstChild(); c != nil; {
		next := c.NextSibling()
		if q, ok := c.(*ast.Blockquote); ok {
			c = d.convertAlert(q)
		}

		if c.HasChildren() {
			d.restructure(c)
		}

		c = next
	}

	d.groupDetails(n)
}

func (d *Doc) numberFolds() {
	_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if f, ok := n.(*foldNode); ok && entering {
			f.ID = d.folds
			d.folds++
		}

		return ast.WalkContinue, nil
	})
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

		kind, nameEnd, ok := tagAt(s, i)
		if !ok {
			i++

			continue
		}

		gt := strings.IndexByte(s[nameEnd:], '>')
		if gt < 0 {
			break
		}

		end := nameEnd + gt + 1
		tags = append(tags, htmlTag{kind: kind, start: i, end: end})
		i = end
	}

	return tags
}

// tagAt reports a details or summary tag opening at s[i] and where its name ends.
func tagAt(s string, i int) (kind tagKind, nameEnd int, ok bool) {
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
					return 0, 0, false
				}
			default:
				return 0, 0, false
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

		return kind, end, true
	}

	return 0, 0, false
}

// groupDetails folds the HTML blocks of n that carry <details> tags into
// foldNodes. A fold still open at the end of n's children ends there.
func (d *Doc) groupDetails(n ast.Node) {
	hasHTML := false

	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if _, ok := c.(*ast.HTMLBlock); ok {
			hasHTML = true

			break
		}
	}

	if !hasHTML {
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
	tag = strings.TrimSuffix(strings.TrimSuffix(tag, ">"), "/")

	i := strings.IndexAny(tag, " \t\r\n\f")
	if i < 0 {
		return false
	}

	s := tag[i:]
	for {
		s = strings.TrimLeft(s, " \t\r\n\f/")
		if s == "" {
			return false
		}

		n := strings.IndexAny(s, " \t\r\n\f/=")
		if n < 0 {
			n = len(s)
		}

		name := s[:n]
		s = s[n:]

		rest := strings.TrimLeft(s, " \t\r\n\f")
		if strings.HasPrefix(rest, "=") {
			rest = strings.TrimLeft(rest[1:], " \t\r\n\f")
			switch {
			case rest != "" && (rest[0] == '"' || rest[0] == '\''):
				end := strings.IndexByte(rest[1:], rest[0])
				if end < 0 {
					rest = ""
				} else {
					rest = rest[end+2:]
				}
			default:
				end := strings.IndexAny(rest, " \t\r\n\f")
				if end < 0 {
					end = len(rest)
				}

				rest = rest[end:]
			}

			s = rest
		}

		if strings.EqualFold(name, "open") {
			return true
		}
	}
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

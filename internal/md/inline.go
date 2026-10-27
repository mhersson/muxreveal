package md

import (
	"html"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/v2/ast"
	east "github.com/yuin/goldmark/v2/extension/ast"

	"github.com/mhersson/muxreveal/internal/git/invisible"
)

// maxInlineDepth bounds the recursion into nested emphasis and links; deeper
// content is flattened to its text.
const maxInlineDepth = 64

// style is everything a run carries besides its text.
type style struct {
	role                            Role
	bold, italic, underline, strike bool
	class                           uint8
	link                            int
	path                            string
}

func plainStyle() style { return style{link: -1} }

func (s style) run(text string) Run {
	return Run{
		Text: text, Role: s.role, Bold: s.bold, Italic: s.italic, Underline: s.underline,
		Strike: s.strike, Class: s.class, Link: s.link, Path: s.path,
	}
}

// spaceMode says how the spaces of a seg wrap.
type spaceMode uint8

const (
	spaceBreak spaceMode = iota // wrap points; runs of them collapse to one
	spaceKeep                   // wrap points that are never collapsed
	spaceFixed                  // plain cells that never wrap
)

// seg is a piece of inline text in one style, a line break or an image.
type seg struct {
	text string
	st   style
	sp   spaceMode
	brk  bool
	soft bool      // a break from a block tag, which collapses with its neighbours
	img  *imageRef // an image, which takes a line of its own
}

// imageRef is an image seen in inline content. src and host are empty when the
// source is not one the layout can name.
type imageRef struct{ alt, src, host string }

// inliner flattens one block's inline nodes into segs.
type inliner struct {
	l    *layouter
	segs []seg

	// Inline HTML opens and closes in separate nodes, so its state outlives one.
	bold, italic, kbd int
	block             bool // inside an HTML block: block tags break the line
	anchor            bool
	anchorLink        int
	anchorPath        string
}

// inlines flattens n's inline children.
func (l *layouter) inlines(n ast.Node) []seg {
	in := &inliner{l: l, anchorLink: -1}
	in.walk(n, plainStyle(), 0)

	return in.segs
}

// eff applies the inline HTML state to st.
func (in *inliner) eff(st style) style {
	if in.bold > 0 {
		st.bold = true
	}

	if in.italic > 0 {
		st.italic = true
	}

	if in.kbd > 0 && st.role == RoleText {
		st.role, st.bold = RoleKey, true
	}

	if in.anchor && st.link == -1 && st.path == "" {
		st.underline = true
		st.link, st.path = in.anchorLink, in.anchorPath

		if st.role == RoleText {
			st.role = RoleLink
		}
	}

	return st
}

func (in *inliner) walk(n ast.Node, st style, depth int) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if depth >= maxInlineDepth {
			in.flat(c, st)

			continue
		}

		switch c := c.(type) {
		case *ast.Text:
			in.text(c.Value.Value(in.l.d.src), st)

			switch {
			case c.HardLineBreak():
				in.segs = append(in.segs, seg{brk: true})
			case c.SoftLineBreak():
				in.segs = append(in.segs, seg{text: " ", st: in.eff(st)})
			}
		case *ast.CodeSpan:
			in.code(c.Value.Value(in.l.d.src), st)
		case *ast.Emphasis:
			st2 := st
			st2.italic = true
			in.walk(c, st2, depth+1)
		case *ast.Strong:
			st2 := st
			st2.bold = true
			in.walk(c, st2, depth+1)
		case *east.Strikethrough:
			st2 := st
			st2.strike = true
			in.walk(c, st2, depth+1)
		case *ast.Link:
			in.walk(c, in.linkStyle(st, c.Destination.Value(in.l.d.src)), depth+1)
		case *ast.AutoLink:
			in.text(c.Label.Value(in.l.d.src), in.linkStyle(st, c.Destination.Value(in.l.d.src)))
		case *ast.RawHTML:
			in.html(c.Value.Value(in.l.d.src), st)
		case *east.FootnoteReference:
			st2 := st
			st2.role = RoleFootnoteRef
			in.segs = append(in.segs, seg{text: "[" + strconv.Itoa(c.Index) + "]", st: in.eff(st2)})
		case *ast.Image:
			in.image(in.altText(c), c.Destination.Value(in.l.d.src), st)
		default:
			in.walk(c, st, depth+1)
		}
	}
}

// flat adds the text under n without styling or recursion, for content nested
// past maxInlineDepth.
func (in *inliner) flat(n ast.Node, st style) {
	stack := []ast.Node{n}

	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		switch c := c.(type) {
		case *ast.Text:
			in.text(c.Value.Value(in.l.d.src), st)
		case *ast.CodeSpan:
			in.code(c.Value.Value(in.l.d.src), st)
		case *ast.Image:
			continue
		}

		if t, ok := c.(*ast.Text); ok && (t.SoftLineBreak() || t.HardLineBreak()) {
			in.segs = append(in.segs, seg{text: " ", st: in.eff(st)})
		}

		var kids []ast.Node
		for k := c.FirstChild(); k != nil; k = k.NextSibling() {
			kids = append(kids, k)
		}

		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, kids[i])
		}
	}
}

// linkStyle is st for the text of a link to target, registering the link when
// the target is linkable.
func (in *inliner) linkStyle(st style, target string) style {
	st.role, st.underline = RoleLink, true
	st.link, st.path = in.l.addLink(in.l.d.resolve(target))

	return st
}

// text adds prose.
func (in *inliner) text(s string, st style) {
	if s = cleanProse(s); s != "" {
		in.segs = append(in.segs, seg{text: s, st: in.eff(st)})
	}
}

// code adds inline code, padded by a space on each side.
func (in *inliner) code(s string, st style) {
	st.role = RoleCode
	st = in.eff(st)
	pad := seg{text: " ", st: st, sp: spaceFixed}

	in.segs = append(in.segs, pad)
	in.segs = appendCode(in.segs, s, st)
	in.segs = append(in.segs, pad)
}

// html applies one inline HTML node: tags are dropped, a few change the style.
func (in *inliner) html(raw string, st style) {
	name, closing, ok := tagName(raw)
	if !ok {
		return
	}

	if in.block && breaksBlock(name) {
		in.segs = append(in.segs, seg{brk: true, soft: true})

		return
	}

	if name != "br" && name != "a" && name != "img" && strings.HasSuffix(raw, "/>") {
		return
	}

	delta := 1
	if closing {
		delta = -1
	}

	switch name {
	case "b", "strong":
		in.bold = max(in.bold+delta, 0)
	case "i", "em":
		in.italic = max(in.italic+delta, 0)
	case "kbd":
		in.kbd = max(in.kbd+delta, 0)
	case "br":
		if !closing {
			in.segs = append(in.segs, seg{brk: true})
		}
	case "img":
		if !closing {
			alt, _ := tagAttr(raw, "alt")
			src, _ := tagAttr(raw, "src")
			in.image(alt, src, st)
		}
	case "a":
		in.anchor = false

		if closing {
			return
		}

		if href, ok := tagAttr(raw, "href"); ok {
			in.anchor = true
			in.anchorLink, in.anchorPath = in.l.addLink(in.l.d.resolve(href))
		}
	}
}

// image adds an image seg; its source resolves by the link rules and only an
// http or https URL or a local path is kept.
func (in *inliner) image(alt, dest string, st style) {
	ref := &imageRef{alt: strings.Join(strings.Fields(cleanProse(alt)), " ")}
	res := in.l.d.resolve(dest)

	switch {
	case res.url != "":
		if u, err := url.Parse(res.url); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			ref.src, ref.host = res.url, u.Host
		}
	case res.path != "":
		ref.src, ref.host = res.path, filepath.Base(res.path)
	}

	in.segs = append(in.segs, seg{img: ref, st: in.eff(st)})
}

// altText is the plain text under an image node.
func (in *inliner) altText(n ast.Node) string {
	var b strings.Builder

	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		switch c := c.(type) {
		case *ast.Text:
			b.WriteString(c.Value.Value(in.l.d.src))

			if c.SoftLineBreak() || c.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.CodeSpan:
			b.WriteString(c.Value.Value(in.l.d.src))

			return ast.WalkSkipChildren, nil
		}

		return ast.WalkContinue, nil
	})

	return b.String()
}

// htmlSegs flattens the text of an HTML block or fragment by the inline-HTML
// rules: tags are dropped, a few style the text, block tags break the line and
// script and style content is dropped.
func (l *layouter) htmlSegs(raw string) []seg {
	in := &inliner{l: l, anchorLink: -1, block: true}
	st := plainStyle()

	for i := 0; i < len(raw); {
		j := strings.IndexByte(raw[i:], '<')
		if j < 0 {
			in.text(html.UnescapeString(raw[i:]), st)

			break
		}

		if j > 0 {
			in.text(html.UnescapeString(raw[i:i+j]), st)
		}

		i += j

		if strings.HasPrefix(raw[i:], "<!--") {
			end := strings.Index(raw[i+4:], "-->")
			if end < 0 {
				break
			}

			i += 4 + end + 3

			continue
		}

		name, closing, ok := tagName(raw[i:])
		if !ok {
			if i+1 < len(raw) && (raw[i+1] == '!' || raw[i+1] == '?') {
				i = tagEnd(raw, i)
			} else {
				in.text("<", st)

				i++
			}

			continue
		}

		end := tagEnd(raw, i)
		in.html(raw[i:end], st)
		i = end

		if !closing && (name == "script" || name == "style") {
			i = skipRaw(raw, i, name)
		}
	}

	return settleBreaks(in.segs)
}

// tagEnd returns the index after the '>' that closes the tag at raw[i], not
// counting a '>' inside a quoted attribute value; len(raw) when there is none.
func tagEnd(raw string, i int) int {
	var quote byte

	for j := i + 1; j < len(raw); j++ {
		switch c := raw[j]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return j + 1
		}
	}

	if gt := strings.IndexByte(raw[i:], '>'); gt >= 0 {
		return i + gt + 1
	}

	return len(raw)
}

// skipRaw returns the index after the closing tag of name, or len(raw).
func skipRaw(raw string, i int, name string) int {
	for i < len(raw) {
		j := strings.IndexByte(raw[i:], '<')
		if j < 0 {
			break
		}

		i += j
		if n, closing, ok := tagName(raw[i:]); ok && closing && n == name {
			return tagEnd(raw, i)
		}

		i++
	}

	return len(raw)
}

// settleBreaks drops block-tag breaks that follow another break or open the
// text, and those that close it.
func settleBreaks(segs []seg) []seg {
	out := segs[:0]
	atBreak := true

	for _, sg := range segs {
		switch {
		case sg.soft && atBreak:
			continue
		case sg.brk:
			atBreak = true
		case sg.img != nil || strings.TrimSpace(sg.text) != "":
			atBreak = false
		}

		out = append(out, sg)
	}

	for len(out) > 0 && out[len(out)-1].soft {
		out = out[:len(out)-1]
	}

	return out
}

// breaksBlock reports the tags that start a new line inside an HTML block.
func breaksBlock(name string) bool {
	switch name {
	case "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}

	return false
}

// addLink registers res and returns the Link index (-1 for none) and the path.
func (l *layouter) addLink(res resolved) (int, string) {
	if res.url == "" {
		return -1, res.path
	}

	l.out.Links = append(l.out.Links, Link{URL: res.url})

	return len(l.out.Links) - 1, res.path
}

// tagName reads the lower-case name of an HTML tag. Comments, declarations,
// processing instructions and anything that is not a tag report false.
func tagName(raw string) (name string, closing, ok bool) {
	if len(raw) < 3 || raw[0] != '<' {
		return "", false, false
	}

	s := raw[1:]
	if s[0] == '/' {
		closing, s = true, s[1:]
	}

	end := 0
	for end < len(s) && (s[end] >= 'a' && s[end] <= 'z' || s[end] >= 'A' && s[end] <= 'Z' || end > 0 && s[end] >= '0' && s[end] <= '9') {
		end++
	}

	if end == 0 {
		return "", false, false
	}

	return strings.ToLower(s[:end]), closing, true
}

// tagAttr returns the entity-decoded value of the attribute want of an
// opening tag.
func tagAttr(tag, want string) (string, bool) {
	s := tag
	if i := strings.IndexAny(s, " \t\r\n\f"); i >= 0 {
		s = s[i:]
	} else {
		return "", false
	}

	for {
		s = strings.TrimLeft(s, " \t\r\n\f/")
		if s == "" || s[0] == '>' {
			return "", false
		}

		n := strings.IndexAny(s, " \t\r\n\f/=>")
		if n < 0 {
			n = len(s)
		}

		name, value := s[:n], ""
		s = s[n:]

		rest := strings.TrimLeft(s, " \t\r\n\f")
		if strings.HasPrefix(rest, "=") {
			rest = strings.TrimLeft(rest[1:], " \t\r\n\f")

			switch {
			case rest != "" && (rest[0] == '"' || rest[0] == '\''):
				end := strings.IndexByte(rest[1:], rest[0])
				if end < 0 {
					value, rest = rest[1:], ""
				} else {
					value, rest = rest[1:end+1], rest[end+2:]
				}
			default:
				end := strings.IndexAny(rest, " \t\r\n\f>")
				if end < 0 {
					end = len(rest)
				}

				value, rest = rest[:end], rest[end:]
			}

			s = rest
		}

		if strings.EqualFold(name, want) {
			return html.UnescapeString(value), true
		}
	}
}

// cleanProse makes untrusted prose safe to draw: invalid UTF-8 becomes U+FFFD,
// invisible characters and controls are dropped, and a tab or a line break
// becomes a space.
func cleanProse(s string) string {
	s = invisible.Strip(strings.ToValidUTF8(s, "�"))

	if !strings.ContainsFunc(s, unicode.IsControl) {
		return s
	}

	var b strings.Builder

	for _, r := range s {
		switch {
		case r == '\t' || r == '\n':
			b.WriteByte(' ')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
}

// appendCode adds code text as segs in st: characters that draw nothing or
// control the terminal become a RoleHidden tag, so they stay visible. A tab
// becomes a space. Code blocks reuse it.
func appendCode(segs []seg, s string, st style) []seg {
	hidden := st
	hidden.role, hidden.bold = RoleHidden, true

	var plain strings.Builder

	flush := func() {
		if plain.Len() > 0 {
			segs = append(segs, seg{text: plain.String(), st: st, sp: spaceKeep})
			plain.Reset()
		}
	}

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size

		switch {
		case r == utf8.RuneError && size == 1:
			plain.WriteRune('�')
		case r == '\t':
			plain.WriteByte(' ')
		case unicode.IsControl(r) || invisible.Rune(r):
			flush()

			segs = append(segs, seg{text: hiddenTag(r), st: hidden})
		default:
			plain.WriteRune(r)
		}
	}

	flush()

	return segs
}

// hiddenTag spells r as ⟨U+XXXX⟩.
func hiddenTag(r rune) string {
	h := strings.ToUpper(strconv.FormatInt(int64(r), 16))
	if len(h) < 4 {
		h = strings.Repeat("0", 4-len(h)) + h
	}

	return "⟨U+" + h + "⟩"
}

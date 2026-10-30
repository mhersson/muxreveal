package md

import (
	"bytes"
	"sort"
	"strings"

	"github.com/yuin/goldmark/v2/ast"
	east "github.com/yuin/goldmark/v2/extension/ast"
)

// SourceRange is a stretch of source lines, 1-based and inclusive. The zero
// value is a layout line no source made, such as a blank between blocks.
type SourceRange struct{ First, Last int }

// unset marks a layout line whose range is not known yet; Layout never returns it.
var unset = SourceRange{First: -1, Last: -1}

// IsMarkdown reports whether name has a markdown extension, in any case.
func IsMarkdown(name string) bool {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return false
	}

	switch strings.ToLower(name[i:]) {
	case ".md", ".markdown":
		return true
	}

	return false
}

// FoldCount is the number of folds in the document; their IDs run from 0 to
// FoldCount()-1.
func (d *Doc) FoldCount() int { return d.folds }

// indexLines records where each source line starts.
func (d *Doc) indexLines() {
	d.starts = append(d.starts[:0], 0)

	for i, b := range d.src {
		if b == '\n' {
			d.starts = append(d.starts, i+1)
		}
	}
}

// lineAt is the 1-based line holding the byte offset off.
func (d *Doc) lineAt(off int) int {
	return sort.Search(len(d.starts), func(i int) bool { return d.starts[i] > off })
}

// blankLine reports a line with nothing but whitespace and quote markers.
func (d *Doc) blankLine(line int) bool {
	end := len(d.src)
	if line < len(d.starts) {
		end = d.starts[line]
	}

	return len(bytes.Trim(d.src[d.starts[line-1]:end], " \t\r\n>")) == 0
}

// trimBlank moves last up past blank lines, but not above first.
func (d *Doc) trimBlank(first, last int) int {
	for last > first && d.blankLine(last) {
		last--
	}

	return last
}

// setLine gives a node md made the source line it stands for.
func (d *Doc) setLine(n ast.Node, line int) {
	if line > 0 {
		d.at[n] = line
	}
}

// lineOf is the first source line of n, or 0 when it has none.
func (d *Doc) lineOf(n ast.Node) int {
	if l, ok := d.at[n]; ok {
		return l
	}

	if p := n.Pos(); p >= 0 {
		return d.lineAt(p)
	}

	return 0
}

// inlineLine is the source line of the first node under n, n included, that has
// a position, or 0.
func (d *Doc) inlineLine(n ast.Node) int {
	line := 0

	walkTree(n, func(c ast.Node) bool {
		if line == 0 && c.Pos() >= 0 {
			line = d.lineAt(c.Pos())
		}

		return line == 0
	})

	return line
}

// childLine is the source line of the first inline child of p that has one.
func (d *Doc) childLine(p ast.Node) int {
	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		if l := d.inlineLine(c); l > 0 {
			return l
		}
	}

	return 0
}

// holdsBlocks reports the nodes whose children are blocks with ranges of their own.
func holdsBlocks(n ast.Node) bool {
	switch n.(type) {
	case *ast.Document, *ast.Blockquote, *alertNode, *foldNode, *ast.List, *ast.ListItem, *east.FootnoteDefinition:
		return true
	}

	return false
}

// indexRanges computes every block's range once: from the line of its position
// to the line before its next sibling's (the parent's end for the last), with
// trailing blank lines dropped.
func (d *Doc) indexRanges() {
	d.ranges = map[ast.Node]SourceRange{}
	root := SourceRange{First: 1, Last: d.trimBlank(1, len(d.starts))}
	d.ranges[d.root] = root

	walkTree(d.root, func(n ast.Node) bool {
		if n == d.root {
			return true
		}

		par := d.ranges[n.Parent()]

		first := d.lineOf(n)
		if first == 0 {
			first = par.First

			if p, ok := d.ranges[n.PreviousSibling()]; ok {
				first = p.First
			}
		}

		first = min(max(first, par.First), par.Last)
		last := par.Last

		for s := n.NextSibling(); s != nil; s = s.NextSibling() {
			if l := d.lineOf(s); l > 0 {
				last = min(max(l-1, first), par.Last)

				break
			}
		}

		d.ranges[n] = SourceRange{First: first, Last: d.trimBlank(first, last)}

		return holdsBlocks(n)
	})
}

// stamp gives the lines from index from on that have no range yet the range r.
func (f *frag) stamp(from int, r SourceRange) {
	f.sync()

	for i := from; i < len(f.src); i++ {
		if f.src[i] == unset {
			f.src[i] = r
		}
	}
}

// restamp gives every line from index from on the range r.
func (f *frag) restamp(from int, r SourceRange) {
	f.sync()

	for i := from; i < len(f.src); i++ {
		f.src[i] = r
	}
}

// markNone gives line i, the last one appended, no source.
func (f *frag) markNone(i int) {
	f.sync()
	f.src[i] = SourceRange{}
}

// sync pads src with unset ranges to the number of lines.
func (f *frag) sync() {
	for len(f.src) < len(f.lines) {
		f.src = append(f.src, unset)
	}

	f.src = f.src[:len(f.lines)]
}

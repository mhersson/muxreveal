package md

import "github.com/yuin/goldmark/v2/ast"

// nests reports the nodes that hold blocks of their own, the ones whose layout
// recurses.
func nests(n ast.Node) bool {
	switch n.(type) {
	case *ast.Blockquote, *alertNode, *foldNode, *ast.List:
		return true
	}

	return false
}

type flatKind uint8

const (
	kindBlocks flatKind = iota // a quote, alert or fold body: blocks a blank line apart
	kindList                   // a list: its children are items
	kindItem                   // a list item: blocks with no blank line between them
)

// flatFrame is a container being laid out by flat.
type flatFrame struct {
	kind    flatKind
	next    ast.Node // the next child to lay out
	c       blockCtx // what the children inherit
	width   int      // the cells the children are laid out in
	start   int      // the first line of the container's content
	pending int      // the separator line before the child being laid out, or -1
	list    *flatList
	marker  Line // an item's marker
}

type flatList struct {
	n     *ast.List
	items []ast.Node
	numW  int
	depth int // lists entered before this one, which picks the bullet
	i     int // the next item
}

// flat lays out the container n and everything nested in it without recursing,
// for n at or past maxDepth. No container draws a bar or indentation there, so
// one width serves the whole walk, and an item only narrows its own content by
// its marker. An item's marker goes on its first line when that line has room
// for it, so a marker never pushes text out.
func (l *layouter) flat(out *frag, n ast.Node, width int, c blockCtx) {
	stack := l.openFlat(nil, out, n, width, c)

	for len(stack) > 0 {
		f := &stack[len(stack)-1]

		if f.kind == kindList {
			if f.list.i == len(f.list.items) {
				stack = stack[:len(stack)-1]

				continue
			}

			item := f.list.items[f.list.i]
			marker := l.marker(f.list.n, item, f.list.i, f.list.numW, f.list.depth)
			ic := f.c
			ic.done = isDone(item)

			f.list.i++

			stack = append(stack, flatFrame{
				kind: kindItem, next: item.FirstChild(), c: ic, pending: -1,
				width: max(width-lineWidth(marker), 1), start: len(out.lines), marker: marker,
			})

			continue
		}

		if f.pending >= 0 {
			dropBlank(out, f.pending)
			f.pending = -1
		}

		ch := f.next
		if ch == nil {
			if f.kind == kindItem {
				endItem(out, f, width)
			}

			stack = stack[:len(stack)-1]

			continue
		}

		f.next = ch.NextSibling()

		before := len(out.lines)
		sep := f.kind == kindBlocks && before > f.start

		if sep {
			out.lines = append(out.lines, nil)
		}

		if !nests(ch) {
			l.block(out, ch, f.width, f.c)

			if sep {
				dropBlank(out, before)
			}

			continue
		}

		cc, cw := f.c, f.width
		cc.done = false

		// A frame whose last child is a container has nothing left to do, so a
		// chain of single-child quotes or folds reuses one slot.
		if f.next == nil && f.kind == kindBlocks && !sep {
			stack = stack[:len(stack)-1]
		}

		top := len(stack)

		stack = l.openFlat(stack, out, ch, cw, cc)

		switch {
		case len(stack) == top && sep:
			dropBlank(out, before)
		case sep:
			stack[top-1].pending = before
		}
	}
}

// openFlat lays out the own lines of the container n, a title or summary, in w
// cells and pushes its frame when there is something inside to walk.
func (l *layouter) openFlat(stack []flatFrame, out *frag, n ast.Node, w int, c blockCtx) []flatFrame {
	c.depth++

	switch n := n.(type) {
	case *ast.Blockquote:
		c.text = RoleQuoteText
	case *alertNode:
		head, _ := l.alertTitle(n)
		out.lines = append(out.lines, fit(head, w))
		c.text = RoleText
	case *foldNode:
		if !l.foldHead(out, n, w) {
			return stack
		}
	case *ast.List:
		items, numW := listItems(n)
		c.listDepth++

		return append(stack, flatFrame{
			kind: kindList, c: c, pending: -1,
			list: &flatList{n: n, items: items, numW: numW, depth: c.listDepth - 1},
		})
	}

	if n.FirstChild() == nil {
		return stack
	}

	return append(stack, flatFrame{kind: kindBlocks, next: n.FirstChild(), c: c, width: w, start: len(out.lines), pending: -1})
}

// endItem puts the marker of the finished item f on its first line.
func endItem(out *frag, f *flatFrame, width int) {
	if len(out.lines) == f.start {
		out.lines = append(out.lines, nil)
	}

	first := out.lines[f.start]
	if len(first) == 0 || lineWidth(first)+lineWidth(f.marker) <= width {
		out.lines[f.start] = fit(concat(f.marker, first), width)
	}
}

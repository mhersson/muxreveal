package md

import "github.com/yuin/goldmark/v2/ast"

// walkTree calls visit on root and each of its descendants in document order;
// visit returns false to skip a node's children. It follows parent links
// instead of recursing, since a document can nest deeper than any stack allows.
func walkTree(root ast.Node, visit func(ast.Node) bool) {
	c := root

	for {
		if visit(c) && c.HasChildren() {
			c = c.FirstChild()

			continue
		}

		for c != root && c.NextSibling() == nil {
			c = c.Parent()
		}

		if c == root {
			return
		}

		c = c.NextSibling()
	}
}

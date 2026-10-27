package md

import (
	"strings"

	"github.com/yuin/goldmark/v2/ast"
)

// maxWidth is the widest line a layout produces, whatever width is asked for.
const maxWidth = 100

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
	out   Layout
}

// Layout wraps the document to o.Width cells (at most 100, at least 1).
func (d *Doc) Layout(o LayoutOptions) Layout {
	l := &layouter{d: d, width: min(max(o.Width, 1), maxWidth)}

	for c := d.root.FirstChild(); c != nil; c = c.NextSibling() {
		if p, ok := c.(*ast.Paragraph); ok {
			l.paragraph(p)
		}
	}

	return l.out
}

// paragraph appends p's lines, after a blank line when something precedes it.
func (l *layouter) paragraph(p *ast.Paragraph) {
	lines := wrap(l.inlines(p), l.width)
	if len(lines) == 0 {
		return
	}

	if len(l.out.Lines) > 0 {
		l.out.Lines = append(l.out.Lines, nil)
	}

	l.out.Lines = append(l.out.Lines, lines...)
}

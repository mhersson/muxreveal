// Ported from tuios internal/diffview/render.go at commit
// aaaa128cd35b38ba8aa08a5f69e688f01385ec3e (github.com/Gaurav-Gosain/tuios).
// Copyright (c) 2025 Gaurav Gosain. MIT License; see THIRD_PARTY.md.

package diffview

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// reset ends every piece the view draws, so nothing it set leaks into what
// the caller draws next.
const reset = "\x1b[m"

// Code draws a line's text in exactly width cells, from column xOff of the
// text on. spans colour it and changed marks the part that changed; both are
// in bytes of text. A line longer than the room ends in an ellipsis. That a
// line is scrolled sideways is Sign's to show, so no cell of code is given
// up for it.
//
// text has to be printable already: the view draws it as it is.
func (t *Theme) Code(text string, spans []Span, changed Range, kind Kind, cursor bool, xOff, width int) string {
	one := [1]Range{changed}
	return t.CodeRanges(text, spans, one[:], kind, cursor, xOff, width)
}

// CodeRanges is Code with more than one changed part, in order and apart:
// each is lit, and the text between them is not.
func (t *Theme) CodeRanges(text string, spans []Span, changed []Range, kind Kind, cursor bool, xOff, width int) string {
	if width <= 0 {
		return ""
	}
	c := b2i(cursor)
	codes := &t.code[kind][c]
	styled := styleText(codes, text, spans, changed)
	plain := codes[0][Plain]
	left := ansi.StringWidth(text) - xOff
	if xOff > 0 && left > 0 {
		styled = ansi.TruncateLeft(styled, xOff, "")
	}
	ell := Ellipsis()
	ellW := ansi.StringWidth(ell)
	switch {
	case left <= 0:
		styled = ""
	case left > width && width > ellW:
		styled = ansi.Truncate(styled, width-ellW, "") + plain + ell
	case left > width:
		styled = ansi.Truncate(styled, width, "")
	}
	if w := ansi.StringWidth(styled); w < width {
		styled += plain + strings.Repeat(" ", width-w)
	}
	return styled + reset
}

// styleText is text with each span's class and each changed range's word
// ground written into it, in bytes of text.
func styleText(codes *[2][NumClasses]string, text string, spans []Span, changed []Range) string {
	var b strings.Builder
	b.Grow(len(text) + 32*(len(spans)+2))
	si, ri := 0, 0
	for i := 0; i < len(text); {
		for si < len(spans) && spans[si].End <= i {
			si++
		}
		class, next := Plain, len(text)
		if si < len(spans) {
			if spans[si].Start <= i {
				class, next = spans[si].Class, spans[si].End
			} else {
				next = spans[si].Start
			}
		}
		word := false
		for ri < len(changed) && (changed[ri].End <= i || changed[ri].Empty()) {
			ri++
		}
		if ri < len(changed) {
			if i < changed[ri].Start {
				next = min(next, changed[ri].Start)
			} else {
				word = true
				next = min(next, changed[ri].End)
			}
		}
		next = max(min(next, len(text)), i+1)
		b.WriteString(codes[b2i(word)][class])
		b.WriteString(text[i:next])
		i = next
	}
	return b.String()
}

// CodePart draws the cells [from, to) of text as Code styles them, padded on
// the code ground to width cells and never ending in an ellipsis. A part
// wider than width is cut at the edge.
func (t *Theme) CodePart(text string, spans []Span, changed []Range, kind Kind, cursor bool, from, to, width int) string {
	if width <= 0 {
		return ""
	}
	codes := &t.code[kind][b2i(cursor)]
	to = min(to, from+width)
	styled := ansi.Cut(styleText(codes, text, spans, changed), from, to)
	if w := ansi.StringWidth(styled); w < width {
		styled += codes[0][Plain] + strings.Repeat(" ", width-w)
	}
	return styled + reset
}

// CodeBg is the ground of a line's code, without the word tint.
func (t *Theme) CodeBg(kind Kind, cursor bool) color.Color {
	return t.Bg(kind, cursor, false)
}

// Gutter draws a line number right-aligned in width cells, with a space on
// its right. n of zero or less leaves the cells blank, for the side of a
// line that has no number there.
func (t *Theme) Gutter(n, width int, kind Kind, cursor bool) string {
	if width <= 0 {
		return ""
	}
	s := ""
	if n > 0 {
		s = strconv.Itoa(n)
	}
	s += " "
	if len(s) > width {
		s = s[len(s)-width:]
	}
	return t.gutter[kind][b2i(cursor)] + strings.Repeat(" ", width-len(s)) + s + reset
}

// SignWidth is how many cells Sign draws.
const SignWidth = 2

// Sign draws the mark before a line's code: "+" for an added line, "-" for
// a removed one, blank otherwise, then an ellipsis when the code is scrolled
// sideways and there is code to its left that does not show.
func (t *Theme) Sign(kind Kind, cursor, scrolled bool) string {
	mark := " "
	switch kind {
	case Add:
		mark = "+"
	case Delete:
		mark = "-"
	}
	after := " "
	if scrolled {
		after = Ellipsis()
		if ansi.StringWidth(after) != 1 {
			after = "<"
		}
	}
	return t.sign[kind][b2i(cursor)] + mark + after + reset
}

// Blank draws width empty cells on a line's code ground.
func (t *Theme) Blank(kind Kind, cursor bool, width int) string {
	if width <= 0 {
		return ""
	}
	return t.code[kind][b2i(cursor)][0][Plain] + strings.Repeat(" ", width) + reset
}

// BlankGutter draws width empty cells on a line's number column ground.
func (t *Theme) BlankGutter(kind Kind, cursor bool, width int) string {
	if width <= 0 {
		return ""
	}
	return t.gutter[kind][b2i(cursor)] + strings.Repeat(" ", width) + reset
}

package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/git/invisible"
	"github.com/mhersson/muxreveal/internal/md"
	"github.com/mhersson/muxreveal/internal/ui/diffview"
)

// diffUnit is one thing a diff line draws: a grapheme of text, a tab, a
// hidden-character tag or the cut's ellipsis. raw is where it starts in the
// line.
type diffUnit struct {
	raw, cells int
	text       string
	whole      bool
}

// isHiddenRune reports whether cleanText draws r as a tag.
func isHiddenRune(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) || invisible.Rune(r)
}

// copyText is s as a copy takes it: a tab stays, a hidden character is its tag
// and an invalid byte is U+FFFD.
func copyText(s string) string {
	if plainText(s) {
		return s
	}

	var b strings.Builder

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size

		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
		case r == '\t':
			b.WriteByte('\t')
		case isHiddenRune(r):
			fmt.Fprintf(&b, "⟨U+%04X⟩", r)
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
}

// diffUnits splits s, a line cut to the budget, into what cleanText draws.
func diffUnits(s string) []diffUnit {
	var (
		units []diffUnit
		col   int // cleanText's count, which sets the tab stops
	)

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])

		switch {
		case r == utf8.RuneError && size == 1:
			units = append(units, diffUnit{raw: i, cells: ansi.StringWidth(string(utf8.RuneError)), text: string(utf8.RuneError)})
			col++
			i += size
		case r == '\t':
			n := tabStop - col%tabStop
			units = append(units, diffUnit{raw: i, cells: n, text: "\t", whole: true})
			col += n
			i += size
		case isHiddenRune(r):
			tag := fmt.Sprintf("⟨U+%04X⟩", r)
			units = append(units, diffUnit{raw: i, cells: ansi.StringWidth(tag), text: tag, whole: true})
			col += utf8.RuneCountInString(tag)
			i += size
		default:
			j := i
			for j < len(s) {
				r2, n := utf8.DecodeRuneInString(s[j:])
				if (r2 == utf8.RuneError && n == 1) || r2 == '\t' || isHiddenRune(r2) {
					break
				}

				j += n
			}

			for seg := s[i:j]; seg != ""; {
				cl, w, rest := cluster(seg)

				units = append(units, diffUnit{raw: j - len(seg), cells: w, text: cl})

				for _, c := range cl {
					col += runeWidth(c)
				}

				seg = rest
			}

			i = j
		}
	}

	return units
}

// diffPieces are the pieces of a diff line's code cells from cell 0, as
// lineShown and the drawing's cut make them. tagCR says the line shows its CR
// as an ending tag. A cut line's ellipsis cell holds the rest of the line, so
// a selection that reaches it copies the line whole.
func diffPieces(raw string, tagCR bool, width int) []md.Piece {
	if width <= 0 {
		return nil
	}

	line := strings.TrimSuffix(raw, "\r")
	units := lineUnits(raw, tagCR)

	total := 0
	for _, u := range units {
		total += u.cells
	}

	var ellipsis *md.Piece

	if total > width {
		limit := width
		if width > 1 {
			limit = width - 1
		}

		n, cum := 0, 0
		for n < len(units) && cum+units[n].cells <= limit {
			cum += units[n].cells
			n++
		}

		if width > 1 && n < len(units) {
			u := units[n]
			rest := copyText(line[min(u.raw, len(line)):])

			if tagCR {
				rest += endingTag
			}

			// The drawing cuts at width-1 cells, so a tab or tag that straddles the
			// cut shows its leading cells before the ellipsis; a character that
			// does not fit is dropped and the ellipsis follows the cells before it.
			end := cum
			if u.whole && limit > cum {
				end = limit
			}

			ellipsis = &md.Piece{Col: cum, Cells: end - cum + 1, Text: rest, Whole: true}
		}

		units = units[:n]
	}

	var out []md.Piece

	col := 0

	for _, u := range units {
		switch {
		case u.whole && u.text == "":
			// The budget's ellipsis, which holds the rest of the line.
			out = append(out, md.Piece{Col: col, Cells: u.cells, Text: copyText(line[u.raw:]), Whole: true})
		case u.whole:
			out = append(out, md.Piece{Col: col, Cells: u.cells, Text: u.text, Whole: true})
		case len(out) > 0 && !out[len(out)-1].Whole && out[len(out)-1].Col+out[len(out)-1].Cells == col:
			last := &out[len(out)-1]
			last.Text += u.text
			last.Cells += u.cells
		default:
			out = append(out, md.Piece{Col: col, Cells: u.cells, Text: u.text})
		}

		col += u.cells
	}

	if ellipsis != nil {
		out = append(out, *ellipsis)
	}

	return out
}

// shiftPieces moves pieces right by n cells.
func shiftPieces(ps []md.Piece, n int) []md.Piece {
	for i := range ps {
		ps[i].Col += n
	}

	return ps
}

// rowText is what row i of the diff column copies, in the column's cells: the
// code of a line, the path of a file header, the header and section of a hunk,
// and a rendered file's text. Info and fold rows copy nothing. A side by side
// pair gives the code of side, 0 left and 1 right, and nothing where that side
// is blank.
func (v *reviewView) rowText(lay drawerLayout, i, side int) md.LineText {
	skip := md.LineText{Skip: true}

	if i < 0 || i >= len(v.rows) {
		return skip
	}

	r := v.rows[i]
	f := v.res.Files[r.file]

	switch r.kind {
	case rowFile:
		d := &drawCtx{v: v, c: v.derived(), icons: v.mdIcons, lay: lay}
		text, shown := d.filePath(f, d.fileRight(r.file, r.read))

		return md.LineText{Pieces: []md.Piece{{Col: fileHeadW, Cells: ansi.StringWidth(shown), Text: text, Whole: true}}}
	case rowHunk:
		h := f.Hunks[r.hunk]
		text := hunkHeader(h)

		if section, _ := cleanText(h.Section); section != "" {
			text += " " + section
		}

		cells := min(ansi.StringWidth(text), max(lay.diffW-hunkHeadW, 0))

		return md.LineText{Pieces: []md.Piece{{Col: hunkHeadW, Cells: cells, Text: text, Whole: true}}}
	case rowLine:
		h := f.Hunks[r.hunk]
		l := h.Lines[r.line]
		p := partner(h.Lines, r.line)

		other := ""
		if p >= 0 {
			other = h.Lines[p].Text
		}

		shown, _ := shownLine(l.Text)
		tag := lineShown(l.Text, other, p >= 0) != shown

		return md.LineText{Pieces: shiftPieces(diffPieces(l.Text, tag, lay.unifiedCode(2)), markW+2*lay.gw+diffview.SignWidth)}
	case rowPair:
		left, right := pairSides(f.Hunks[r.hunk], r)

		idx, other := left, right
		if side != 0 {
			idx, other = right, left
		}

		if idx < 0 {
			return skip
		}

		ls := f.Hunks[r.hunk].Lines
		paired := left >= 0 && right >= 0 && left != right
		shown, _ := shownLine(ls[idx].Text)
		o := ""

		if paired {
			o = ls[other].Text
		}

		tag := lineShown(ls[idx].Text, o, paired) != shown
		col, width := lay.splitCodeAt(side)

		return md.LineText{Pieces: shiftPieces(diffPieces(ls[idx].Text, tag, width), col)}
	case rowFull, rowGhost:
		return md.LineText{Pieces: shiftPieces(diffPieces(r.text, false, lay.unifiedCode(1)), markW+lay.gw+diffview.SignWidth)}
	case rowMD:
		return v.mdRowText(r, f)
	}

	return skip
}

// mdRowText is what a rendered file's line copies. A picture's Once is made
// unique per file, as rows of two files can share one.
func (v *reviewView) mdRowText(r reviewRow, f git.FileDiff) md.LineText {
	mf := v.mdFiles[f.Path]
	if mf == nil {
		return md.LineText{Skip: true}
	}

	lay := mf.doc.layout(max(v.mdW, 1), v.mdIcons)
	if r.line >= len(lay.Lines) {
		return md.LineText{Skip: true}
	}

	lt := shiftText(lay.Text(r.line))

	for k := range lt.Pieces {
		if lt.Pieces[k].Once > 0 {
			lt.Pieces[k].Once += (r.file + 1) << 20
		}
	}

	return lt
}

// selGrid is the rows as a selection grid in the layout the selection was made in.
func (v *reviewView) selGrid() textGrid { return v.gridIn(v.selLay, v.selSide) }

// gridIn is the rows as a selection grid in lay, the side by side rows on side.
func (v *reviewView) gridIn(lay drawerLayout, side int) textGrid {
	return textGrid{
		lines: func(i int) md.LineText { return v.rowText(lay, i, side) },
		rows:  len(v.rows), cols: lay.diffW,
	}
}

// selectedText is the text of the selection, "" when none.
func (v *reviewView) selectedText() string {
	if v.sel == nil {
		return ""
	}

	g := v.selGrid()
	if g.rows == 0 {
		return ""
	}

	s := *v.sel
	s.anchor, s.head = clampPoint(g, s.anchor), clampPoint(g, s.head)

	return textCopy(g.lines, s, g.cols)
}

// unitText is the text of the unit lineAt takes at row i. A side by side row
// gives its left side, or its right when the left is blank.
func (v *reviewView) unitText(lay drawerLayout, i int) string {
	side := 0

	if i >= 0 && i < len(v.rows) && v.rows[i].kind == rowPair {
		r := v.rows[i]
		if left, _ := pairSides(v.res.Files[r.file].Hunks[r.hunk], r); left < 0 {
			side = 1
		}
	}

	g := v.gridIn(lay, side)
	if g.rows == 0 {
		return ""
	}

	return textCopy(g.lines, lineAt(g, point{i, 0}), g.cols)
}

// selSpans are the cells of row i the selection paints, within cells below end.
func (d *drawCtx) selSpans(i, end int) []spanPaint {
	s := d.v.sel
	if s == nil {
		return nil
	}

	from, to, ok := s.rowSpan(i, d.lay.diffW)
	if !ok {
		return nil
	}

	var spans []spanPaint

	for _, c := range textPaint(d.v.rowText(d.lay, i, d.v.selSide), from, to) {
		if c[0] < end {
			spans = append(spans, spanPaint{from: c[0], to: min(c[1], end-1), bg: d.ui.Selection})
		}
	}

	return spans
}

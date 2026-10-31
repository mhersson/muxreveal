package md

import (
	"sort"
	"strings"
)

// Join says how a line continues the line above it in a copy.
type Join uint8

const (
	JoinNone  Join = iota // a line of its own
	JoinSpace             // a wrap dropped spaces before it: one space
	JoinTight             // a wrap split a word before it, or a picture's later row: nothing
	JoinBreak             // a hard line break: a newline
)

// Piece is a stretch of a line's cells that a selection copies.
type Piece struct {
	Col, Cells int    // the cells it covers, from the line's cell 0
	Text       string // what it copies
	Whole      bool   // all of Text when any of its cells is picked
	Cell       int    // its table cell, from 0
	Join       Join   // on a table row's later line, how it continues its cell
	Once       int    // above 0: only the first picked piece with this value copies
}

// LineText is what one laid-out line gives a copy.
type LineText struct {
	Join    Join
	Skip    bool // drawn frames only: a copy leaves the line out
	Table   bool // a line of a table grid: pieces group by Cell
	Picture bool // a row of a picture: its cells are never painted
	Pieces  []Piece
}

// CellRange is the cells of a line a selection picks, inclusive; From > To picks none.
type CellRange struct{ From, To int }

// chromeRun is a run drawn but never copied: an indent or a pad.
func chromeRun(st style, text string) Run {
	r := st.run(text)
	r.chrome = true

	return r
}

// isChrome reports whether r is drawn but never copied.
func isChrome(r Run) bool {
	if r.chrome {
		return true
	}

	switch r.Role {
	case RoleCodeFrame, RoleCodeLang, RoleLineNumber, RoleTableFrame, RoleQuoteBar,
		RoleFoldHint, RoleSummaryMark, RoleRule, RoleFootnoteRule:
		return true
	}

	return false
}

// imageOn returns the index in l.Images of the image that line i belongs to
// (a picture's row or an image line), or -1.
func (l Layout) imageOn(i int) int {
	k := sort.Search(len(l.Images), func(k int) bool { return l.Images[k].Line > i }) - 1
	if k < 0 {
		return -1
	}

	if im := l.Images[k]; i < im.Line+max(im.Rows, 1) {
		return k
	}

	return -1
}

// Text says what line i copies. A line out of range copies nothing.
func (l Layout) Text(i int) LineText {
	var lt LineText

	if i < 0 || i >= len(l.Lines) {
		return lt
	}

	line := l.Lines[i]
	img := l.imageOn(i)

	var (
		src        string
		picRow     = -1
		imageLine  bool
		col, bars  int
		textSeen   bool
		skippable  = len(line) > 0
		frameFound bool
	)

	if img >= 0 {
		im := l.Images[img]
		src = "![" + im.Alt + "](" + im.Src + ")"

		if im.Rows > 0 {
			picRow = i - im.Line
		} else {
			imageLine = true
		}
	}

	for _, r := range line {
		w := textWidth(r.Text)

		switch r.Role {
		case RoleTableFrame:
			lt.Table = true
			bars++

			frameFound = true
		case RoleCodeFrame, RoleRule, RoleFootnoteRule:
			frameFound = true
		}

		// A row's side bars tell it from a rule: an empty code or table row is
		// still a line.
		if !isChrome(r) || r.Role == RoleLineNumber || strings.Contains(r.Text, "│") {
			skippable = false
		}

		if isChrome(r) {
			col += w

			continue
		}

		if !textSeen {
			textSeen, lt.Join = true, r.join
		}

		cell := max(bars-1, 0)

		switch {
		case r.Role == RolePicture && picRow >= 0:
			lt.Picture = true
			lt.Pieces = append(lt.Pieces, Piece{Col: col, Cells: w, Text: src, Whole: true, Cell: cell, Once: img + 1})
		case r.Role == RoleImage && imageLine:
			lt.Pieces = append(lt.Pieces, Piece{Col: col, Cells: lineWidth(line) - col, Text: src, Whole: true, Cell: cell, Once: img + 1})

			col = lineWidth(line)
		default:
			lt.Pieces = appendPieces(lt.Pieces, r, col, w, cell)
		}

		col += w

		if col >= lineWidth(line) && imageLine && r.Role == RoleImage {
			break
		}
	}

	lt.Skip = skippable && frameFound
	if lt.Skip {
		lt.Pieces = nil
	}

	if picRow > 0 {
		lt.Join = JoinTight
	}

	if lt.Table {
		for _, p := range lt.Pieces {
			if p.Join != JoinNone {
				lt.Join = JoinSpace

				break
			}
		}
	}

	return lt
}

// appendPieces adds the pieces of text run r, which starts at col and is w cells wide.
func appendPieces(out []Piece, r Run, col, w, cell int) []Piece {
	if r.Text == "" {
		return out
	}

	first := len(out)

	switch {
	case r.tab:
		out = append(out, Piece{Col: col, Cells: w, Text: "\t", Whole: true, Cell: cell})
	case r.Role == RoleHidden:
		for s := r.Text; s != ""; {
			tag := s
			if k := strings.Index(s, "⟩"); k >= 0 {
				tag = s[:k+len("⟩")]
			}

			tw := textWidth(tag)
			out = append(out, Piece{Col: col, Cells: tw, Text: tag, Whole: true, Cell: cell})
			col += tw
			s = s[len(tag):]
		}
	case r.Text == "…" && r.rest != "":
		out = append(out, Piece{Col: col, Cells: w, Text: r.rest, Whole: true, Cell: cell})
	default:
		out = append(out, Piece{Col: col, Cells: w, Text: r.Text, Cell: cell})
	}

	out[first].Join = r.join

	return out
}

// take returns what the cells in pick copy of p.
func (p Piece) take(pick CellRange) (string, bool) {
	end := p.Col + max(p.Cells, 1) - 1
	if pick.From > pick.To || pick.From > end || pick.To < p.Col {
		return "", false
	}

	if p.Whole {
		return p.Text, true
	}

	var b strings.Builder

	col := p.Col

	for s := p.Text; s != ""; {
		g, w := nextCluster(s)
		s = s[len(g):]

		if col <= pick.To && col+max(w, 1)-1 >= pick.From {
			b.WriteString(g)
		}

		col += w
	}

	return b.String(), true
}

// tableRow collects the cells of the table row being copied.
type tableRow struct {
	open  bool
	cells []rowCell
}

type rowCell struct {
	text  string
	taken bool
}

func (r *tableRow) add(cell int, join Join, text string) {
	for len(r.cells) <= cell {
		r.cells = append(r.cells, rowCell{})
	}

	c := &r.cells[cell]
	if c.taken && join != JoinTight && join != JoinNone {
		c.text += " "
	}

	c.text += text
	c.taken = true
}

func (r *tableRow) String() string {
	var parts []string

	for i := range r.cells {
		if r.cells[i].taken {
			parts = append(parts, strings.Trim(r.cells[i].text, " "))
		}
	}

	return strings.Join(parts, "\t")
}

// joinSep is what a line with join j puts between itself and the line above.
func joinSep(j Join) string {
	switch j {
	case JoinSpace:
		return " "
	case JoinBreak:
		return "\n"
	case JoinTight:
		return ""
	}

	return "\n"
}

// CopyLines assembles the text of the cells picks pick on lines, where picks[i]
// is for lines[i]. A line is in the copy when its pick is not empty. With block,
// each line is a line of its own and joins are ignored.
func CopyLines(lines []LineText, picks []CellRange, block bool) string {
	var (
		b       strings.Builder
		row     tableRow
		started bool
		prevTab bool
		seen    = map[int]bool{}
	)

	flush := func() {
		if row.open {
			b.WriteString(row.String())
			row = tableRow{}
		}
	}

	for i, lt := range lines {
		if i >= len(picks) || picks[i].From > picks[i].To || lt.Skip {
			continue
		}

		pick := picks[i]
		continuing := row.open && lt.Table && lt.Join != JoinNone && !block

		if !continuing {
			flush()

			if started {
				switch {
				case block, lt.Table, prevTab:
					b.WriteByte('\n')
				default:
					b.WriteString(joinSep(lt.Join))
				}
			}

			started = true
		}

		prevTab = lt.Table

		if lt.Table && !block {
			row.open = true
		}

		var cells tableRow

		for _, p := range lt.Pieces {
			text, ok := p.take(pick)
			if !ok {
				continue
			}

			if p.Once > 0 {
				if seen[p.Once] {
					text = ""
				}

				seen[p.Once] = true
			}

			switch {
			case lt.Table && !block:
				row.add(p.Cell, p.Join, text)
			case lt.Table:
				cells.add(p.Cell, JoinNone, text)
			default:
				b.WriteString(text)
			}
		}

		if lt.Table && block {
			b.WriteString(cells.String())
		}
	}

	flush()

	return b.String()
}

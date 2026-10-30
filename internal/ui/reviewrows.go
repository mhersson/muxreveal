package ui

import (
	"strconv"
	"strings"

	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/git/linediff"
	"github.com/mhersson/muxreveal/internal/ui/diffview"
)

// reviewRowKind is what a row of the diff column is.
type reviewRowKind uint8

// Row kinds.
const (
	rowFile  reviewRowKind = iota // a file's header
	rowInfo                       // a note under a header; text holds it
	rowHunk                       // a hunk header
	rowLine                       // a line of a hunk, unified
	rowPair                       // a line pair of a hunk, side by side
	rowFull                       // a new-side line of the full file view
	rowGhost                      // a removed line in the full file view
	rowFold                       // a folded block of ghost lines
	rowMD                         // a line of a rendered markdown file
)

// reviewRow is one row of the diff column. It holds indexes into the result,
// -1 where the kind has none, and text only for info rows.
//
//   - rowLine: hunk and line are the hunk's and the line's indexes.
//   - rowPair: line is the left (old) side's index, else the right side's;
//     other is the right side's index, -1 when it has no line.
//   - rowFull: line indexes the new text; other indexes the old text for a kept
//     line and is -1 for an added one. text is the line.
//   - rowGhost: line indexes the old text; block is the ghost block. text is
//     the line.
//   - rowFold: line is the block's first old line, other the number of lines.
//   - rowMD: line indexes the layout; edit and other are the first and last
//     source line that made it; with none, edit is the editor's line (the next
//     block's first, else the previous block's last, else 1) and other is -1;
//     text is its plain text; mark is its change mark.
//
// A ghost or fold row also holds edit, the line of the new file an editor
// opens at for it: the first new line after its block, or the last line when
// the block ends the file. The old numbers of removed lines mean nothing there.
type reviewRow struct {
	kind                    reviewRowKind
	file, hunk, line, other int
	text                    string
	block                   int
	edit                    int
	mark                    changeMark
}

// rowOpts selects how buildRows lays the result out.
type rowOpts struct {
	split bool
	// full holds the text of the files read whole, by path; whole says which of
	// them are shown so.
	full  map[string]fullText
	whole map[string]bool
	// md holds the rendered files, by path; a nil entry is a file still parsing.
	md map[string]*mdRows
	// folded says whether a ghost block is folded, by foldKey; a block without
	// an entry takes ghostsStartFolded.
	folded            map[string]bool
	ghostsStartFolded bool
}

// fullText is both sides of a file, or the error that kept them from loading.
type fullText struct {
	old, new []string
	err      error
}

// foldKey names ghost block n of the file at path.
func foldKey(path string, n int) string { return path + "\x00" + strconv.Itoa(n) }

// lcsCells bounds the table linediff.LCS builds for the middle of a file.
const lcsCells = 4 << 20

const (
	infoBinary    = "binary file, not shown"
	infoTruncated = "too large, press f for the full file"
	infoRendering = "rendering…"
)

func newRow(kind reviewRowKind, file int) reviewRow {
	return reviewRow{kind: kind, file: file, hunk: -1, line: -1, other: -1, block: -1, edit: -1}
}

func infoRow(file int, text string) reviewRow {
	r := newRow(rowInfo, file)
	r.text = text

	return r
}

// buildRows lays the files out as rows: a header per file, then its info,
// hunks or full text.
func buildRows(res git.Result, o rowOpts) []reviewRow {
	var rows []reviewRow

	for i, f := range res.Files {
		rows = append(rows, newRow(rowFile, i))

		ft, have := o.full[f.Path]
		whole := have && o.whole[f.Path]
		mr, rendered := o.md[f.Path]

		switch {
		case f.Binary:
			rows = append(rows, infoRow(i, infoBinary))
		case rendered && have && ft.err != nil:
			rows = append(rows, infoRow(i, ft.err.Error()))
		case rendered && mr == nil:
			rows = append(rows, infoRow(i, infoRendering))
		case rendered:
			rows = appendMD(rows, i, mr)
		case whole && ft.err != nil:
			rows = append(rows, infoRow(i, ft.err.Error()))
		case whole:
			rows = appendFull(rows, i, f.Path, ft, o)
		case f.Truncated:
			rows = append(rows, infoRow(i, infoTruncated))
		case len(f.Hunks) == 0 && f.Status == git.ModeChanged:
			rows = append(rows, infoRow(i, "mode "+f.OldMode+" → "+f.NewMode))
		default:
			rows = appendHunks(rows, i, f, o.split)
		}
	}

	return rows
}

// appendMD adds a rendered file's lines.
func appendMD(rows []reviewRow, file int, mr *mdRows) []reviewRow {
	plain := mr.lay.Plain()

	for i := range mr.lay.Lines {
		r := newRow(rowMD, file)
		r.line, r.text, r.edit = i, plain[i], blockEditLine(mr.lay.Source, i)

		if i < len(mr.lay.Source) && mr.lay.Source[i].First != 0 {
			r.other = mr.lay.Source[i].Last
		}

		if i < len(mr.marks) {
			r.mark = mr.marks[i]
		}

		rows = append(rows, r)
	}

	return rows
}

func appendHunks(rows []reviewRow, file int, f git.FileDiff, split bool) []reviewRow {
	for hi, h := range f.Hunks {
		hr := newRow(rowHunk, file)
		hr.hunk = hi
		rows = append(rows, hr)

		if !split {
			for li := range h.Lines {
				r := newRow(rowLine, file)
				r.hunk, r.line = hi, li
				rows = append(rows, r)
			}

			continue
		}

		kinds := make([]diffview.Kind, len(h.Lines))
		for li, l := range h.Lines {
			switch l.Kind {
			case git.Add:
				kinds[li] = diffview.Add
			case git.Del:
				kinds[li] = diffview.Delete
			}
		}

		for _, p := range diffview.Pairs(kinds) {
			r := newRow(rowPair, file)
			r.hunk, r.line, r.other = hi, p.Left, p.Right

			if p.Left < 0 {
				r.line, r.other = p.Right, -1
			}

			rows = append(rows, r)
		}
	}

	return rows
}

// lineOps diffs two texts line by line: the lines both start with, the lines
// both end with and the operations on what lies between.
func lineOps(old, nw []string) (pre, suf int, ops []linediff.Op) {
	for pre < len(old) && pre < len(nw) && old[pre] == nw[pre] {
		pre++
	}

	for suf < len(old)-pre && suf < len(nw)-pre && old[len(old)-1-suf] == nw[len(nw)-1-suf] {
		suf++
	}

	midOld, midNew := old[pre:len(old)-suf], nw[pre:len(nw)-suf]

	if len(midOld)*len(midNew) <= lcsCells {
		return pre, suf, linediff.LCS(midOld, midNew)
	}

	return pre, suf, linediff.Replace(midOld, midNew)
}

func appendFull(rows []reviewRow, file int, path string, ft fullText, o rowOpts) []reviewRow {
	old, nw := ft.old, ft.new
	pre, suf, ops := lineOps(old, nw)

	kept := func(oi, ni int) reviewRow {
		r := newRow(rowFull, file)
		r.line, r.other, r.text = ni, oi, nw[ni]

		return r
	}

	oi, ni, block := 0, 0, 0

	for ; oi < pre; oi, ni = oi+1, ni+1 {
		rows = append(rows, kept(oi, ni))
	}

	for i := 0; i < len(ops); {
		switch ops[i].Op {
		case ' ':
			rows = append(rows, kept(oi, ni))
			oi++
			ni++
			i++
		case '+':
			r := kept(-1, ni)
			rows = append(rows, r)
			ni++
			i++
		default:
			n := 0
			for i+n < len(ops) && ops[i+n].Op == '-' {
				n++
			}

			folded, ok := o.folded[foldKey(path, block)]
			if !ok {
				folded = o.ghostsStartFolded
			}

			// The block sits before new line ni; past the end it is the last line.
			edit := min(ni, len(nw)-1) + 1

			if folded {
				r := newRow(rowFold, file)
				r.line, r.other, r.block, r.edit = oi, n, block, edit
				rows = append(rows, r)
			} else {
				for k := range n {
					r := newRow(rowGhost, file)
					r.line, r.block, r.text, r.edit = oi+k, block, old[oi+k], edit
					rows = append(rows, r)
				}
			}

			oi += n
			i += n
			block++
		}
	}

	for range suf {
		rows = append(rows, kept(oi, ni))
		oi++
		ni++
	}

	return rows
}

// hunkHeader is git's range line without the section, such as "@@ -43,9 +43,9 @@".
func hunkHeader(h git.Hunk) string {
	return "@@ -" + hunkRange(h.OldStart, h.OldLines) + " +" + hunkRange(h.NewStart, h.NewLines) + " @@"
}

func hunkRange(start, n int) string {
	if n == 1 {
		return strconv.Itoa(start)
	}

	return strconv.Itoa(start) + "," + strconv.Itoa(n)
}

// rowAnchor remembers what a row showed so that it can be found again in a
// later result. index and row are the fallbacks for a result that no longer
// holds it.
type rowAnchor struct {
	path                       string
	hunkNewStart, hunkNewLines int
	kind                       git.LineKind
	text                       string
	row                        reviewRowKind
	index                      int
	block                      int // a fold row's ghost block
	first, last                int // a rendered row's source range
}

// anchorAt describes row i. Rows that hold no hunk keep only the file.
func anchorAt(res git.Result, rows []reviewRow, i int) rowAnchor {
	a := rowAnchor{index: i}
	if i < 0 || i >= len(rows) {
		return a
	}

	r := rows[i]
	a.row = r.kind
	a.path = res.Files[r.file].Path

	switch r.kind {
	case rowFold:
		a.block = r.block
	case rowMD:
		a.first, a.last, a.text = r.edit, r.other, r.text
	case rowFull:
		a.kind, a.text = git.Context, r.text
		if r.other < 0 {
			a.kind = git.Add
		}
	case rowGhost:
		a.kind, a.text = git.Del, r.text
	case rowHunk, rowLine, rowPair:
		h := res.Files[r.file].Hunks[r.hunk]
		a.hunkNewStart, a.hunkNewLines = h.NewStart, h.NewLines

		if r.kind != rowHunk {
			l := h.Lines[r.line]
			a.kind, a.text = l.Kind, l.Text
		}
	}

	return a
}

// restoreAnchor finds the row for a in rows, built from res. It keeps the
// line, else the hunk overlapping the old hunk's new-side range, else the
// file, else the nearest index. A line is kept in a hunk that overlaps the old
// range or has the old hunk's size, so a hunk that only moved still matches.
func restoreAnchor(res git.Result, rows []reviewRow, a rowAnchor) int {
	if len(rows) == 0 {
		return 0
	}

	header := -1
	hunkRow := -1
	line, lineDist := -1, 0
	whole := -1
	fold := -1
	md := mdAnchorMatch{exact: -1, holds: -1, next: -1, last: -1}

	for i, r := range rows {
		f := res.Files[r.file]
		if f.Path != a.path {
			continue
		}

		switch r.kind {
		case rowFile:
			header = i
		case rowHunk:
			if hunkRow < 0 && overlaps(f.Hunks[r.hunk], a) {
				hunkRow = i
			}
		case rowMD:
			if a.row == rowMD {
				md.consider(r, i, a)
			}
		case rowFold:
			if a.row == rowFold && r.block == a.block && (fold < 0 || abs(i-a.index) < abs(fold-a.index)) {
				fold = i
			}
		case rowFull, rowGhost:
			if r.kind == a.row && r.text == a.text && (r.kind == rowGhost || (r.other < 0) == (a.kind == git.Add)) &&
				(whole < 0 || abs(i-a.index) < abs(whole-a.index)) {
				whole = i
			}
		case rowLine, rowPair:
			h := f.Hunks[r.hunk]
			if a.row == rowFile || a.row == rowHunk || (!overlaps(h, a) && h.NewLines != a.hunkNewLines) {
				continue
			}

			if !sameLine(h, r, a) {
				continue
			}

			d := abs(h.NewStart - a.hunkNewStart)
			if overlaps(h, a) {
				d = 0
			}

			if line < 0 || d < lineDist || (d == lineDist && abs(i-a.index) < abs(line-a.index)) {
				line, lineDist = i, d
			}
		}
	}

	switch {
	case a.row == rowFile && header >= 0:
		return header
	case a.row == rowMD && md.pick() >= 0:
		return md.pick()
	case a.row == rowFold && fold >= 0:
		return fold
	case (a.row == rowFull || a.row == rowGhost) && whole >= 0:
		return whole
	case (a.row == rowLine || a.row == rowPair) && line >= 0:
		return line
	case (a.row == rowLine || a.row == rowPair || a.row == rowHunk) && hunkRow >= 0:
		return hunkRow
	case header >= 0:
		return header
	}

	return min(max(a.index, 0), len(rows)-1)
}

// mdAnchorMatch finds a rendered row again by its source range: the row with
// the same range nearest the old index, else the narrowest range holding the
// first source line, else the next block, else the file's last row.
type mdAnchorMatch struct {
	exact, holds, next, last int // row indexes, -1 for none
	holdsW, nextFirst        int
}

func (m *mdAnchorMatch) consider(r reviewRow, i int, a rowAnchor) {
	m.last = i

	switch {
	case r.edit == a.first && r.other == a.last:
		if m.exact < 0 || abs(i-a.index) < abs(m.exact-a.index) {
			m.exact = i
		}
	case r.other <= 0 || a.last <= 0:
	case r.edit <= a.first && a.first <= r.other:
		if w := r.other - r.edit; m.holds < 0 || w < m.holdsW {
			m.holds, m.holdsW = i, w
		}
	case r.edit > a.first && (m.next < 0 || r.edit < m.nextFirst):
		m.next, m.nextFirst = i, r.edit
	}
}

func (m *mdAnchorMatch) pick() int {
	for _, i := range [...]int{m.exact, m.holds, m.next, m.last} {
		if i >= 0 {
			return i
		}
	}

	return -1
}

func overlaps(h git.Hunk, a rowAnchor) bool {
	return h.NewStart < a.hunkNewStart+max(a.hunkNewLines, 1) && a.hunkNewStart < h.NewStart+max(h.NewLines, 1)
}

func sameLine(h git.Hunk, r reviewRow, a rowAnchor) bool {
	for _, li := range []int{r.line, r.other} {
		if li >= 0 && h.Lines[li].Kind == a.kind && h.Lines[li].Text == a.text {
			return true
		}
	}

	return false
}

// searchRows returns the first row after from whose text holds q, ignoring
// case, wrapping around and ending on from itself. A file row's text is its
// path, a hunk row's its header and section.
func searchRows(res git.Result, rows []reviewRow, from int, q string) (int, bool) {
	if q == "" || len(rows) == 0 {
		return 0, false
	}

	q = strings.ToLower(q)

	for k := 1; k <= len(rows); k++ {
		i := (from + k) % len(rows)
		if i < 0 {
			i += len(rows)
		}

		for _, s := range rowTexts(res, rows[i]) {
			if strings.Contains(strings.ToLower(s), q) {
				return i, true
			}
		}
	}

	return 0, false
}

func rowTexts(res git.Result, r reviewRow) []string {
	f := res.Files[r.file]

	switch r.kind {
	case rowFile:
		return []string{f.Path, f.OldPath}
	case rowInfo, rowFull, rowGhost, rowMD:
		return []string{r.text}
	case rowHunk:
		h := f.Hunks[r.hunk]

		return []string{hunkHeader(h) + " " + h.Section}
	case rowLine:
		return []string{f.Hunks[r.hunk].Lines[r.line].Text}
	case rowPair:
		ls := f.Hunks[r.hunk].Lines
		out := []string{ls[r.line].Text}

		if r.other >= 0 && r.other != r.line {
			out = append(out, ls[r.other].Text)
		}

		return out
	}

	return nil
}

// freshRows marks the rows of next whose line is not among the lines prev held
// for the same file, matching by kind and text. A pair is fresh when either of
// its lines is. A file prev lacks is all fresh.
func freshRows(prev, next git.Result, rows []reviewRow) map[int]bool {
	type key struct {
		kind git.LineKind
		text string
	}

	had := make(map[string]map[key]bool, len(prev.Files))

	for _, f := range prev.Files {
		set := map[key]bool{}

		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				set[key{l.Kind, l.Text}] = true
			}
		}

		had[f.Path] = set
	}

	out := map[int]bool{}

	for i, r := range rows {
		if r.kind != rowLine && r.kind != rowPair {
			continue
		}

		f := next.Files[r.file]
		ls := f.Hunks[r.hunk].Lines
		set := had[f.Path]

		for _, li := range []int{r.line, r.other} {
			if li >= 0 && !set[key{ls[li].Kind, ls[li].Text}] {
				out[i] = true
			}
		}
	}

	return out
}

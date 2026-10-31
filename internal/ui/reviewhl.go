package ui

import (
	"crypto/sha256"
	"encoding/binary"
	"io"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/git"
)

const (
	// hlChunk is how many lines of one side of a hunk, or of a whole file, are
	// tokenised as one text.
	hlChunk = 512
	// highlightCap bounds the lines tokenised in one pass.
	highlightCap = 2000
)

// Sides of a hunk that are tokenised on their own.
const (
	sideOld uint8 = iota
	sideNew
)

// spanKey names one chunk of tokenised lines: a side of a hunk, or with hunk
// -1 the new text of a whole file.
type spanKey struct {
	file, hunk int
	side       uint8
	chunk      int
}

// chunkSum names a chunk by its file's path and its text. Spans are a function
// of the two, so a chunk that reads the same in the next result keeps them.
type chunkSum [sha256.Size]byte

// sumOf is the chunkSum of a path and the lines of its chunk; each part is
// length-prefixed so no two chunks hash the same bytes.
func sumOf(path string, lines []string) chunkSum {
	h := sha256.New()

	var n [binary.MaxVarintLen64]byte

	put := func(s string) {
		_, _ = h.Write(n[:binary.PutUvarint(n[:], uint64(len(s)))])
		_, _ = io.WriteString(h, s)
	}

	put(path)

	for _, l := range lines {
		put(l)
	}

	var sum chunkSum

	h.Sum(sum[:0])

	return sum
}

// drawerPaintMsg asks for another pass of highlighting.
type drawerPaintMsg struct{}

func drawerPaint() tea.Msg { return drawerPaintMsg{} }

// drawerAfterUpdate settles the open drawer after an update: it lays the rows
// out for the width, then tokenises the rows near the view. It returns a
// command that sends drawerPaintMsg only while rows in range wait for the cap,
// so nothing runs while the drawer is idle.
func (a *App) drawerAfterUpdate() tea.Cmd {
	a.drawer.tokenised = 0

	st := a.store.State()

	box, ok := a.drawerFull(st)
	if !ok {
		return nil
	}

	v := a.drawer.views[st.Tab().ID]
	if v == nil {
		return nil
	}

	lay := a.drawerLayoutOf(box, v, drawerTree(st))

	// The rows under a page are laid out again when it goes.
	if p := v.page(); p != nil {
		p.resize(lay.diffW, lay.diffH, a.icons)

		return nil
	}

	// Rendered rows are laid out four cells narrower than the column.
	mdW := max(lay.diffW-4, 1)
	moved := v.mdW != mdW || v.mdIcons != a.icons
	v.mdW, v.mdIcons = mdW, a.icons

	stale := v.marksStale(st)
	if stale {
		v.syncMarks(st)
	}

	pics := v.picturesMoved()

	switch {
	case pics:
		v.rebuildPictures(lay.split)
	case v.rowsSplit != lay.split || (moved && len(v.mdFiles) > 0) || stale:
		v.rebuild(lay.split)
	}

	if a.highlightRows(v, lay) {
		return drawerPaint
	}

	return nil
}

// highlightRows tokenises the chunks the rows within one screen of the visible
// ones need and no cache holds, up to highlightCap lines. It reports whether
// chunks were left for a later pass.
func (a *App) highlightRows(v *reviewView, lay drawerLayout) (more bool) {
	if len(v.rows) == 0 || lay.diffH <= 0 {
		return false
	}

	c := v.derived()
	top := v.viewTop(lay.diffH)
	key := scanKey{rev: v.rowsRev, top: top, bodyH: lay.diffH}

	if c.scanned && c.scan == key {
		return false
	}

	a.drawer.scans++

	budget := highlightCap

	for i := max(top-lay.diffH, 0); i < min(top+2*lay.diffH, len(v.rows)); i++ {
		for _, ref := range v.rowSpans(i) {
			if _, done := c.spans[ref.key]; done {
				continue
			}

			path, lines := v.chunkLines(ref.key)
			sum := sumOf(path, lines)

			spans, known := v.carry[sum]
			if !known {
				spans, known = c.bySum[sum]
			}

			if !known {
				if len(lines) > budget {
					more = true

					continue
				}

				budget -= len(lines)
				a.drawer.tokenised += len(lines)
				spans = a.drawer.hl.Highlight(path, lines)
			}

			c.spans[ref.key], c.bySum[sum] = spans, spans
			c.spansRev++
		}
	}

	c.scan, c.scanned = key, !more

	return more
}

// scanKey is what the rows searched for chunks to tokenise depend on besides
// the result's generation: the rows and the part of them in view.
type scanKey struct{ rev, top, bodyH int }

// spanRef locates one line's spans: the chunk, and the line's place in it.
type spanRef struct {
	key spanKey
	at  int
}

// viewTop is the first visible row for a view h rows tall: the stored top,
// moved so the cursor's line shows and clamped to the rows. A line that fits
// shows whole; a taller one keeps at least a row in view, and starts at the top
// when it was out of view.
func (v *reviewView) viewTop(h int) int {
	if h <= 0 {
		return 0
	}

	top := min(max(v.top, 0), max(len(v.rows)-h, 0))
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return max(top, 0)
	}

	start, end := v.lineStart(v.cursor), v.lineEnd(v.cursor)

	switch {
	case end-start > h:
		if end <= top || start >= top+h {
			top = start
		}
	case start < top:
		top = start
	case end > top+h:
		top = end - h
	}

	return max(top, 0)
}

// lineRef is where line li of hunk hi lies among the tokenised lines: its side
// is the new one unless the line was removed. ok is false for a line whose
// number does not fit the hunk.
func lineRef(file, hi int, h git.Hunk, li int) (spanRef, bool) {
	l := h.Lines[li]
	side, idx := sideNew, l.New-h.NewStart

	if l.Kind == git.Del {
		side, idx = sideOld, l.Old-h.OldStart
	}

	if idx < 0 {
		return spanRef{}, false
	}

	return spanRef{spanKey{file, hi, side, idx / hlChunk}, idx % hlChunk}, true
}

// rowSpans lists the spans of the lines row i shows; a row without code has none.
func (v *reviewView) rowSpans(i int) []spanRef {
	r := v.rows[i]

	switch r.kind {
	case rowFull:
		return []spanRef{{spanKey{r.file, -1, sideNew, r.line / hlChunk}, r.line % hlChunk}}
	case rowLine, rowPair:
		h := v.res.Files[r.file].Hunks[r.hunk]

		var out []spanRef

		for _, li := range [...]int{r.line, r.other} {
			if li < 0 {
				continue
			}

			if ref, ok := lineRef(r.file, r.hunk, h, li); ok {
				out = append(out, ref)
			}
		}

		return out
	}

	return nil
}

// chunkLines is the path and the cleaned lines of one chunk, as they are drawn.
func (v *reviewView) chunkLines(k spanKey) (path string, lines []string) {
	f := v.res.Files[k.file]
	start, end := k.chunk*hlChunk, (k.chunk+1)*hlChunk

	if k.hunk < 0 {
		text := v.full[f.Path].new
		for _, s := range text[min(start, len(text)):min(end, len(text))] {
			c, _ := shownLine(s)
			lines = append(lines, c)
		}

		return f.Path, lines
	}

	n := 0

	for _, l := range f.Hunks[k.hunk].Lines {
		if (k.side == sideNew && l.Kind == git.Del) || (k.side == sideOld && l.Kind == git.Add) {
			continue
		}

		if n >= end {
			break
		}

		if n >= start {
			c, _ := shownLine(l.Text)
			lines = append(lines, c)
		}

		n++
	}

	return f.Path, lines
}

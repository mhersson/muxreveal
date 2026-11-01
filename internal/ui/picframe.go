package ui

import (
	"cmp"
	"image"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/kitty"
	"github.com/mhersson/aim/internal/ui/theme"
)

// picRow is one row of a picture, drawn at screen cell X, Y.
type picRow struct {
	X, Y      int
	ID        uint32
	Row, Cols int
	Ground    theme.Hex
}

// frameLayers records, in drawing order, the box and the picture rows of each
// layer View draws, so pictures can be placed last and hidden under later boxes.
type frameLayers struct {
	boxes []core.Rect
	pics  [][]picRow
}

// add records a layer. It is nil-safe so draws without a frame stay simple.
func (f *frameLayers) add(box core.Rect, pics []picRow) {
	if f == nil {
		return
	}

	f.boxes = append(f.boxes, box)
	f.pics = append(f.pics, pics)
}

// visible returns the rows of every picture no later layer's box overlaps. A
// picture is the rows of one id in one layer; its rect is their union.
func (f *frameLayers) visible() []picRow {
	if f == nil {
		return nil
	}

	var out []picRow

	for i, rows := range f.pics {
		for _, id := range picIDs(rows) {
			var group []picRow

			for _, r := range rows {
				if r.ID == id {
					group = append(group, r)
				}
			}

			if !f.covered(i, picRect(group)) {
				out = append(out, group...)
			}
		}
	}

	return out
}

// covered reports whether a box of a layer after i intersects r.
func (f *frameLayers) covered(i int, r image.Rectangle) bool {
	for _, b := range f.boxes[i+1:] {
		if r.Overlaps(image.Rect(b.X, b.Y, b.X+b.W, b.Y+b.H)) {
			return true
		}
	}

	return false
}

// picIDs lists the distinct picture ids of rows in first-seen order.
func picIDs(rows []picRow) []uint32 {
	var out []uint32

	for _, r := range rows {
		if !slices.Contains(out, r.ID) {
			out = append(out, r.ID)
		}
	}

	return out
}

// picRect is the union of the cells rows cover.
func picRect(rows []picRow) image.Rectangle {
	var u image.Rectangle

	for _, r := range rows {
		u = u.Union(image.Rect(r.X, r.Y, r.X+r.Cols, r.Y+1))
	}

	return u
}

// placePictures draws each row as placeholder cells over base, spliced into
// the rows they cover, or in one compositor pass when the splice cannot place
// them without changing another cell; spliced says which drew them.
func placePictures(base string, width, height int, rows []picRow) (out string, spliced bool) {
	if out, ok := splicePictures(base, width, height, rows); ok {
		return out, true
	}

	return composePictures(base, width, height, rows), false
}

// splicePictures writes each row's placeholder cells over the cells it covers
// and keeps the rest of the frame's bytes; a row off the screen is dropped. It
// reports false, drawing nothing, unless the cells it leaves are the
// compositor's: base must have height rows, each picture row must end inside
// the screen and its frame row, stay apart from the others on that row and
// start and end on cell edges (see cutPictures), and its frame row must start
// with nothing open and hold nothing uv carries across cells. Like spliceDrawer
// it has no counterpart to the compositor's bounds guard.
func splicePictures(base string, width, height int, rows []picRow) (string, bool) {
	on := make([]picRow, 0, len(rows))
	grow := len(base)

	for _, r := range rows {
		if r.Y < 0 || r.Y >= height || r.X < 0 || r.X >= width || r.Cols <= 0 {
			continue
		}

		if r.X+r.Cols > width {
			return "", false
		}

		on = append(on, r)
		grow += 128 + 12*r.Cols // a placeholder cell is at most 12 bytes
	}

	if len(on) == 0 {
		return base, true
	}

	if strings.Count(base, "\n") != height-1 {
		return "", false
	}

	slices.SortFunc(on, func(p, q picRow) int { return cmp.Or(cmp.Compare(p.Y, q.Y), cmp.Compare(p.X, q.X)) })

	var (
		b    strings.Builder
		cuts []picCut
	)

	b.Grow(grow)

	done, y, start := 0, 0, 0 // done is the bytes of base written; row y starts at start

	// The frame before the rows already passed ends clean and holds nothing
	// waiting for a cell, so each check reads only the bytes since the last.
	opened, carried := 0, 0

	for i := 0; i < len(on); {
		j := i + 1
		for ; j < len(on) && on[j].Y == on[i].Y; j++ {
			if on[j].X < on[j-1].X+on[j-1].Cols {
				return "", false
			}
		}

		for ; y < on[i].Y; y++ {
			start += strings.IndexByte(base[start:], '\n') + 1
		}

		end := len(base)
		if k := strings.IndexByte(base[start:], '\n'); k >= 0 {
			end = start + k
		}

		row := base[start:end]

		if carriesAcrossCells(row) || !endsClean(base[opened:start]) {
			return "", false
		}

		opened = start

		// A string sequence before the row rides on its first cell, which a
		// picture at the left edge would take.
		if on[i].X == 0 {
			if carriesAcrossCells(base[carried:start]) {
				return "", false
			}

			carried = start
		}

		var last, ok bool

		if cuts, last, ok = cutPictures(cuts[:0], row, on[i:j]); !ok || (last && foldsBack(base[end:])) {
			return "", false
		}

		b.WriteString(base[done : start+cuts[0].a])

		for k, c := range cuts {
			writePlaceholders(&b, on[i+k])

			if !c.pen.IsZero() {
				b.WriteString(c.pen.String())
			}

			if c.link != (uv.Link{}) {
				b.WriteString(ansi.SetHyperlink(c.link.URL, c.link.Params))
			}

			next := len(row)
			if k+1 < len(cuts) {
				next = cuts[k+1].a
			}

			b.WriteString(row[c.b:next])
		}

		done, i = end, j
	}

	b.WriteString(base[done:])

	return b.String(), true
}

// picCut is where a picture cuts its row: it covers the bytes from a to b, and
// the cells after it start with pen and link.
type picCut struct {
	a, b int
	pen  uv.Style
	link uv.Link
}

// cutPictures walks row once, as uv parses it, and appends to cuts where each
// of pics, in order and apart, cuts it. It reports false unless each picture
// starts and ends on a cell edge within the row and no zero-width mark follows
// one before the next cell: uv folds such a mark into the cell drawn before
// it. last reports that no cell follows the last picture in the row.
func cutPictures(cuts []picCut, row string, pics []picRow) (_ []picCut, last, ok bool) {
	p := ansi.GetParser()
	defer ansi.PutParser(p)

	var (
		state byte
		pen   uv.Style
		link  uv.Link
	)

	col, k := 0, 0
	after := false // a picture ended and no cell has followed it

	for i := 0; i < len(row); {
		seq, width, n, next := decodeCell(row[i:], state, p)

		switch {
		case width > 0:
			after = false

			if k < len(pics) {
				x, end := pics[k].X, pics[k].X+pics[k].Cols
				if (col < x && col+width > x) || (col < end && col+width > end) {
					return nil, false, false
				}

				if col == x {
					cuts = append(cuts, picCut{a: i})
				}

				if col+width == end {
					cuts[k].b, cuts[k].pen, cuts[k].link = i+n, pen, link
					k++
					after = true
				}
			}
		case seq[0] >= 0xc0:
			if after {
				return nil, false, false
			}
		case ansi.HasCsiPrefix(seq) && p.Command() == 'm':
			uv.ReadStyle(p.Params(), &pen)
		case ansi.HasOscPrefix(seq) && p.Command() == 8:
			uv.ReadLink(p.Data(), &link)
		}

		col += width
		i += n
		state = next
	}

	return cuts, after, k == len(pics)
}

// foldsBack reports whether s holds, before its first cell, what uv folds into
// the cell drawn before s: a zero-width mark, or a string sequence that may
// find no later cell to ride on.
func foldsBack(s string) bool {
	p := ansi.GetParser()
	defer ansi.PutParser(p)

	var state byte

	for i := 0; i < len(s); {
		seq, width, n, next := decodeCell(s[i:], state, p)

		switch {
		case width > 0:
			return false
		case seq[0] >= 0xc0 || carriesAcrossCells(seq):
			return true
		}

		i += n
		state = next
	}

	return false
}

// decodeCell decodes the next sequence or grapheme cluster of s as uv's parse
// does, which takes an ASCII character and the marks after it as one cluster.
func decodeCell(s string, state byte, p *ansi.Parser) (seq string, width, n int, next byte) {
	seq, width, n, next = ansi.DecodeSequence(s, state, p)
	if n == 1 && seq[0] > 0x1f && seq[0] < 0x7f && len(s) > 1 && s[1] >= 0xc0 {
		if cluster, cw := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth); len(cluster) > 1 {
			seq, width, n = cluster, cw, len(cluster)
		}
	}

	return seq, width, n, next
}

// writePlaceholders writes r's placeholder cells in the id's colour over its
// ground. It closes every style and link first and leaves no style open.
func writePlaceholders(b *strings.Builder, r picRow) {
	st := ansi.Style{}.ForegroundColor(lipgloss.Color(kitty.IDColor(r.ID)))
	if r.Ground != "" {
		st = st.BackgroundColor(lipgloss.Color(string(r.Ground)))
	}

	b.WriteString(drawerReset)
	b.WriteString(st.String())
	b.WriteString(kitty.Placeholders(r.Row, r.Cols))
	b.WriteString(ansi.ResetStyle)
}

// drawPictures places rows over the frame and counts the frames the compositor
// placed them in instead of the splice.
func (a *App) drawPictures(base string, rows []picRow) string {
	out, spliced := placePictures(base, a.width, a.height, rows)
	if !spliced {
		a.pictures.fallbacks++
	}

	return out
}

// composePictures draws each row as placeholder cells over base in one
// compositor pass. A row off the screen is dropped.
func composePictures(base string, width, height int, rows []picRow) string {
	layers := []*lipgloss.Layer{lipgloss.NewLayer(base)}

	for _, r := range rows {
		if r.Y < 0 || r.Y >= height || r.X < 0 || r.X >= width || r.Cols <= 0 {
			continue
		}

		st := lipgloss.NewStyle().Foreground(lipgloss.Color(kitty.IDColor(r.ID)))
		if r.Ground != "" {
			st = st.Background(lipgloss.Color(string(r.Ground)))
		}

		layers = append(layers, lipgloss.NewLayer(st.Render(kitty.Placeholders(r.Row, r.Cols))).X(r.X).Y(r.Y).Z(1))
	}

	if len(layers) == 1 {
		return base
	}

	comp := lipgloss.NewCompositor(layers...)
	if comp.Bounds() != image.Rect(0, 0, width, height) {
		return base
	}

	return comp.Render()
}

// recordLayer records the box lines fill at x, y, clipped to the screen, and
// the picture rows (relative to x, y) inside it.
func recordLayer(f *frameLayers, width, height, x, y int, lines []string, pics []picRow) {
	if f == nil {
		return
	}

	w := 0
	for _, l := range lines {
		w = max(w, ansi.StringWidth(l))
	}

	recordBox(f, width, height, x, y, w, len(lines), pics)
}

// recordBox is recordLayer for n lines whose widest is w cells, known without
// measuring them.
func recordBox(f *frameLayers, width, height, x, y, w, n int, pics []picRow) {
	if f == nil {
		return
	}

	h := min(n, height-y)
	box := core.Rect{X: x, Y: y, W: min(w, width-x), H: h}

	var kept []picRow

	for _, p := range pics {
		if p.Y < 0 || p.Y >= h {
			continue
		}

		p.X += x
		p.Y += y
		kept = append(kept, p)
	}

	f.add(box, kept)
}

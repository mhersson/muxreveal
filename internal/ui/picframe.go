package ui

import (
	"image"
	"slices"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/kitty"
	"github.com/mhersson/muxreveal/internal/ui/theme"
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

// placePictures draws each row as placeholder cells over base in one
// compositor pass. A row off the screen is dropped.
func placePictures(base string, width, height int, rows []picRow) string {
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

	h := min(len(lines), height-y)
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

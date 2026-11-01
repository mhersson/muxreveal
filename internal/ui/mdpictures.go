package ui

import (
	"slices"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/md"
)

// imageRows is how many layout lines an image takes: its picture's rows, else one.
func imageRows(im md.Image) int { return max(im.Rows, 1) }

// pictureAt reports the picture that has line i as one of its rows, and which row.
func pictureAt(lay md.Layout, i int) (im md.Image, row int, ok bool) {
	for _, im := range lay.Images {
		if im.Rows > 0 && i >= im.Line && i < im.Line+im.Rows {
			return im, i - im.Line, true
		}
	}

	return md.Image{}, 0, false
}

// pictureCol is the cells of l before its picture run: the container's prefix.
func pictureCol(l md.Line) int {
	col := 0

	for _, r := range l {
		if r.Role == md.RolePicture {
			return col
		}

		col += ansi.StringWidth(r.Text)
	}

	return col
}

// pictureShift is how far line i of old moves in cur, two layouts of one doc
// that differ only in their picture rows. A line inside an image's old rows maps
// into its new rows, at most its last.
func pictureShift(old, cur md.Layout, i int) int {
	if len(old.Images) != len(cur.Images) {
		return 0
	}

	shift := 0

	for k, o := range old.Images {
		c := cur.Images[k]

		switch {
		case i >= o.Line+imageRows(o):
			shift = c.Line + imageRows(c) - (o.Line + imageRows(o))
		case i >= o.Line:
			return c.Line + min(i-o.Line, imageRows(c)-1) - i
		default:
			return shift
		}
	}

	return shift
}

// placed shows rows' pictures to the registry, which renders any it has not sent,
// and keeps the rows whose image the terminal holds; the rest stay blank.
func (a *App) placed(rows []picRow) []picRow {
	if len(rows) == 0 {
		return nil
	}

	a.pictures.shown(picIDs(rows))

	return slices.DeleteFunc(rows, func(r picRow) bool { return !a.pictures.sent(r.ID) })
}

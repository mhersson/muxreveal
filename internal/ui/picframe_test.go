package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/kitty"
)

func picRows(id uint32, x, y, n, cols int) []picRow {
	rows := make([]picRow, n)
	for i := range rows {
		rows[i] = picRow{X: x, Y: y + i, ID: id, Row: i, Cols: cols}
	}

	return rows
}

func TestFrameVisible(t *testing.T) {
	t.Parallel()

	box := func(x, y, w, h int) core.Rect { return core.Rect{X: x, Y: y, W: w, H: h} }

	tests := []struct {
		name  string
		build func(f *frameLayers)
		want  int
	}{
		{"no later box", func(f *frameLayers) { f.add(box(0, 0, 10, 10), picRows(1, 2, 2, 3, 4)) }, 3},
		{"later box over one row hides all", func(f *frameLayers) {
			f.add(box(0, 0, 10, 10), picRows(1, 2, 2, 3, 4))
			f.add(box(3, 3, 1, 1), nil)
		}, 0},
		{"earlier box leaves it", func(f *frameLayers) {
			f.add(box(0, 0, 10, 10), nil)
			f.add(box(0, 0, 10, 10), picRows(1, 2, 2, 3, 4))
		}, 3},
		{"later box beside it", func(f *frameLayers) {
			f.add(box(0, 0, 10, 10), picRows(1, 2, 2, 3, 4))
			f.add(box(6, 2, 3, 3), nil)
		}, 3},
		{"only the overlapped picture hides", func(f *frameLayers) {
			f.add(box(0, 0, 20, 10), append(picRows(1, 1, 1, 2, 3), picRows(2, 10, 1, 2, 3)...))
			f.add(box(10, 1, 2, 1), nil)
		}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var f frameLayers

			tt.build(&f)

			if got := len(f.visible()); got != tt.want {
				t.Fatalf("visible rows = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFrameLayersNilSafe(t *testing.T) {
	t.Parallel()

	var f *frameLayers

	f.add(core.Rect{W: 1, H: 1}, nil)

	if len(f.visible()) != 0 {
		t.Fatal("nil frame has visible rows")
	}
}

func TestPlacePictures(t *testing.T) {
	t.Parallel()

	const w, h = 20, 5

	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", w)+"\n", h), "\n")
	out := placePictures(blank, w, h, []picRow{
		{X: 3, Y: 1, ID: 42, Row: 0, Cols: 4},
		{X: 3, Y: 5, ID: 42, Row: 1, Cols: 4},
	})

	rows := strings.Split(out, "\n")
	if len(rows) != h {
		t.Fatalf("rows = %d, want %d", len(rows), h)
	}

	for y, r := range rows {
		if got := ansi.StringWidth(r); got != w {
			t.Fatalf("row %d is %d cells, want %d", y, got, w)
		}
	}

	if !strings.Contains(rows[1], "38;2;0;0;42") {
		t.Fatalf("row 1 lacks the id foreground: %q", rows[1])
	}

	want := strings.Repeat(".", 3) + kitty.Placeholders(0, 4) + strings.Repeat(".", 13)
	if got := ansi.Strip(rows[1]); got != want {
		t.Fatalf("row 1 = %q, want %q", got, want)
	}

	for _, y := range []int{0, 2, 3, 4} {
		if got := ansi.Strip(rows[y]); got != strings.Repeat(".", w) {
			t.Fatalf("row %d changed: %q", y, got)
		}
	}
}

func TestOverlayAtRecords(t *testing.T) {
	t.Parallel()

	const w, h = 20, 5

	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", w)+"\n", h), "\n")
	line := strings.Repeat("x", 6)

	t.Run("box and rows", func(t *testing.T) {
		t.Parallel()

		var f frameLayers

		overlayAt(&f, blank, w, h, 2, 1, []string{line, line, line}, []picRow{{X: 1, Y: 1, ID: 7, Row: 0, Cols: 2}})

		if !reflect.DeepEqual(f.boxes, []core.Rect{{X: 2, Y: 1, W: 6, H: 3}}) {
			t.Fatalf("%s = %v, want %v", "f.boxes", f.boxes, []core.Rect{{X: 2, Y: 1, W: 6, H: 3}})
		}

		if !reflect.DeepEqual(f.pics, [][]picRow{{{X: 3, Y: 2, ID: 7, Row: 0, Cols: 2}}}) {
			t.Fatalf("%s = %v, want %v", "f.pics", f.pics, [][]picRow{{{X: 3, Y: 2, ID: 7, Row: 0, Cols: 2}}})
		}
	})

	t.Run("clipped by the bottom", func(t *testing.T) {
		t.Parallel()

		var f frameLayers

		overlayAt(&f, blank, w, h, 2, 3, []string{line, line, line}, []picRow{
			{X: 0, Y: 0, ID: 7, Row: 0, Cols: 2},
			{X: 0, Y: 2, ID: 7, Row: 2, Cols: 2},
		})

		if !reflect.DeepEqual(f.boxes, []core.Rect{{X: 2, Y: 3, W: 6, H: 2}}) {
			t.Fatalf("%s = %v, want %v", "f.boxes", f.boxes, []core.Rect{{X: 2, Y: 3, W: 6, H: 2}})
		}

		if len(f.pics[0]) != 1 || f.pics[0][0].Y != 3 {
			t.Fatalf("pics = %v", f.pics)
		}
	})
}

func TestDrawerRecordsPictures(t *testing.T) {
	t.Parallel()

	const w, h = 20, 5

	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", w)+"\n", h), "\n")
	r := core.Rect{X: 12, Y: 0, W: 8, H: h}
	lines := strings.Split(strings.TrimSuffix(strings.Repeat(strings.Repeat("y", 8)+"\n", h), "\n"), "\n")
	pics := []picRow{{X: 1, Y: 2, ID: 9, Row: 0, Cols: 3}}

	for _, fits := range []bool{true, false} {
		var f frameLayers

		_, spliced := overlayDrawer(&f, blank, w, h, r, lines, pics, fits)
		if spliced != fits {
			t.Fatalf("fits=%v spliced=%v", fits, spliced)
		}

		if !reflect.DeepEqual(f.boxes, []core.Rect{r}) || !reflect.DeepEqual(f.pics, [][]picRow{{{X: 13, Y: 2, ID: 9, Row: 0, Cols: 3}}}) {
			t.Fatalf("fits=%v recorded %v %v", fits, f.boxes, f.pics)
		}
	}
}

func TestOverlayLinesAndDrawerRecordPictures(t *testing.T) {
	t.Parallel()

	const w, h = 20, 6

	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", w)+"\n", h), "\n")
	pics := []picRow{{X: 0, Y: 0, ID: 3, Row: 0, Cols: 2}}

	var f frameLayers

	overlayLines(&f, blank, w, h, 2, []string{"aaaa", "bbbb"}, 4, pics)

	if !reflect.DeepEqual(f.boxes, []core.Rect{{X: 8, Y: 2, W: 4, H: 2}}) || f.pics[0][0].X != 8 || f.pics[0][0].Y != 2 {
		t.Fatalf("overlayLines recorded %v %v", f.boxes, f.pics)
	}

	a := &App{width: w, height: h}

	var g frameLayers

	a.drawDrawer(&g, blank, core.Rect{X: 16, Y: 0, W: 4, H: 2}, []string{"cccc", "dddd"}, pics, true)

	if len(g.pics) != 1 || g.pics[0][0].X != 16 {
		t.Fatalf("drawDrawer recorded %v", g.pics)
	}
}

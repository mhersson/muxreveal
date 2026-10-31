package md

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

type pictureCall struct {
	src     string
	maxCols int
}

func pictureLayout(src string, width int, size PictureSize) (Layout, []pictureCall) {
	var calls []pictureCall

	l := layoutWith(src, LayoutOptions{Width: width, Picture: func(s string, maxCols int) PictureSize {
		calls = append(calls, pictureCall{s, maxCols})

		return size
	}}, "")

	return l, calls
}

func pictureRows(l Layout) []int {
	var rows []int

	for i, line := range l.Lines {
		if slices.ContainsFunc(line, func(r Run) bool { return r.Role == RolePicture }) {
			rows = append(rows, i)
		}
	}

	return rows
}

func TestPicture(t *testing.T) {
	t.Parallel()

	const img = "![a](https://h/y.png)"

	t.Run("rows", func(t *testing.T) {
		t.Parallel()

		l, calls := pictureLayout("text\n\n"+img+"\n\ntext", 40, PictureSize{Cols: 10, Rows: 3, ID: 7})
		wantLines(t, l, "text", "", "", "", "", "", "text")

		if want := []int{2, 3, 4}; !slices.Equal(pictureRows(l), want) {
			t.Fatalf("picture rows = %v, want %v", pictureRows(l), want)
		}

		for i := 2; i <= 4; i++ {
			want := Line{{Text: strings.Repeat(" ", 10), Role: RolePicture, Link: -1}}
			if !slices.Equal(l.Lines[i], want) {
				t.Errorf("line %d = %+v, want %+v", i, l.Lines[i], want)
			}
		}

		if want := (Image{Line: 2, Rows: 3, Cols: 10, ID: 7, Alt: "a", Src: "https://h/y.png", Dest: "https://h/y.png", Link: -1}); len(l.Images) != 1 || l.Images[0] != want {
			t.Errorf("images = %+v, want [%+v]", l.Images, want)
		}

		if want := []pictureCall{{"https://h/y.png", 40}}; !slices.Equal(calls, want) {
			t.Errorf("calls = %v, want %v", calls, want)
		}
	})

	t.Run("cols capped", func(t *testing.T) {
		t.Parallel()

		l, _ := pictureLayout(img, 40, PictureSize{Cols: 200, Rows: 2, ID: 1})
		if len(l.Lines) != 2 || l.Lines[0][0].Text != strings.Repeat(" ", 40) || l.Images[0].Cols != 40 {
			t.Errorf("lines = %q, images = %+v", l.Plain(), l.Images)
		}
	})

	t.Run("no picture", func(t *testing.T) {
		t.Parallel()

		for _, size := range []PictureSize{{}, {Cols: 5}, {Rows: 5}} {
			l, _ := pictureLayout(img, 40, size)
			if len(pictureRows(l)) != 0 || len(l.Lines) != 1 || l.Images[0].Rows != 0 || l.Images[0].ID != 0 {
				t.Errorf("size %+v: lines = %q, images = %+v", size, l.Plain(), l.Images)
			}
		}
	})

	t.Run("empty source", func(t *testing.T) {
		t.Parallel()

		l, calls := pictureLayout("![a]()", 40, PictureSize{Cols: 10, Rows: 3, ID: 7})
		if len(calls) != 0 || len(pictureRows(l)) != 0 || len(l.Lines) != 1 {
			t.Errorf("calls = %v, lines = %q", calls, l.Plain())
		}
	})

	t.Run("loading", func(t *testing.T) {
		t.Parallel()

		l, _ := pictureLayout(img, 40, PictureSize{Loading: true})
		last := l.Lines[0][len(l.Lines[0])-1]

		if !strings.HasSuffix(l.Plain()[0], "  loading…") || last.Role != RoleImageHost {
			t.Errorf("line = %q, last run %+v", l.Plain()[0], last)
		}

		l, _ = pictureLayout("![a very long alternative text](https://h/y.png)", 20, PictureSize{Loading: true})
		if got := l.Plain()[0]; !strings.HasSuffix(got, "…") || ansi.StringWidth(got) > 20 {
			t.Errorf("line = %q, width %d", got, ansi.StringWidth(got))
		}
	})

	t.Run("quote", func(t *testing.T) {
		t.Parallel()

		l, calls := pictureLayout("> "+img, 40, PictureSize{Cols: 10, Rows: 2})
		if len(l.Lines) != 2 {
			t.Fatalf("lines = %q", l.Plain())
		}

		for i, line := range l.Lines {
			if line[0].Role != RoleQuoteBar || line[len(line)-1].Role != RolePicture {
				t.Errorf("line %d = %+v", i, line)
			}
		}

		if calls[0].maxCols >= 40 {
			t.Errorf("maxCols = %d, want under 40", calls[0].maxCols)
		}
	})

	t.Run("list", func(t *testing.T) {
		t.Parallel()

		_, calls := pictureLayout("- "+img, 40, PictureSize{Cols: 10, Rows: 2})

		if len(calls) != 1 || calls[0].maxCols != 36 {
			t.Errorf("calls = %v", calls)
		}
	})

	t.Run("link", func(t *testing.T) {
		t.Parallel()

		l, _ := pictureLayout("[!["+"a](https://h/y.png)](https://h/t)", 40, PictureSize{Cols: 10, Rows: 2})
		if len(l.Links) != 1 || l.Images[0].Link != 0 {
			t.Fatalf("links = %+v, images = %+v", l.Links, l.Images)
		}

		for i, line := range l.Lines {
			if line[0].Role != RolePicture || line[0].Link != 0 {
				t.Errorf("line %d = %+v", i, line)
			}
		}
	})

	t.Run("calls", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			src  string
			want int
		}{
			"table cell":  {"| a |\n|---|\n| " + img + " |\n", 0},
			"closed fold": {"<details>\n<summary>s</summary>\n\n" + img + "\n\n</details>\n", 0},
			"open fold":   {"<details open>\n<summary>s</summary>\n\n" + img + "\n\n</details>\n", 1},
		} {
			_, calls := pictureLayout(tc.src, 40, PictureSize{Cols: 10, Rows: 2})
			if len(calls) != tc.want {
				t.Errorf("%s: %d calls, want %d", name, len(calls), tc.want)
			}
		}
	})

	t.Run("source", func(t *testing.T) {
		t.Parallel()

		l, _ := pictureLayout("a\n\nb\n"+img+"\n\nc", 40, PictureSize{Cols: 10, Rows: 3})
		rows := pictureRows(l)

		if len(rows) != 3 {
			t.Fatalf("rows = %v in %q", rows, l.Plain())
		}

		para := l.Source[rows[0]]
		for _, i := range rows {
			if l.Source[i] != para || para.First != 3 || para.Last != 4 {
				t.Errorf("line %d source = %+v, want 3-4", i, l.Source[i])
			}
		}
	})

	t.Run("plain", func(t *testing.T) {
		t.Parallel()

		l, _ := pictureLayout("> "+img, 40, PictureSize{Cols: 10, Rows: 2})
		wantLines(t, l, "", "")
	})
}

package md

import (
	"slices"
	"strings"
	"testing"
)

// allCells picks every cell of a line, a blank line included.
var allCells = CellRange{From: 0, To: 1 << 20}

// noCells picks nothing.
var noCells = CellRange{From: 1, To: 0}

func picksAll(l Layout) []CellRange {
	picks := make([]CellRange, len(l.Lines))
	for i := range picks {
		picks[i] = allCells
	}

	return picks
}

func linesText(l Layout) []LineText {
	out := make([]LineText, len(l.Lines))
	for i := range out {
		out[i] = l.Text(i)
	}

	return out
}

func pieceTexts(lt LineText) []string {
	var out []string
	for _, p := range lt.Pieces {
		out = append(out, p.Text)
	}

	return out
}

func TestCopyLines(t *testing.T) {
	t.Parallel()

	const wrapSrc = "one two three four five six"

	tests := []struct {
		name  string
		src   string
		width int
		base  string
		picks func(l Layout) []CellRange // nil picks every cell of every line
		block bool
		want  string
	}{
		{name: "wrap", src: wrapSrc, width: 12, want: wrapSrc},
		{
			name: "partial wrap", src: wrapSrc, width: 12, want: "two three",
			picks: func(l Layout) []CellRange {
				p := make([]CellRange, len(l.Lines))
				for i := range p {
					p[i] = noCells
				}

				p[0], p[1] = CellRange{4, 6}, CellRange{0, 4}

				return p
			},
		},
		{name: "split word", src: "abcdefghijklmnop", width: 10, want: "abcdefghijklmnop"},
		{name: "hard break", src: "a  \nb", width: 20, want: "a\nb"},
		{name: "list", src: "- one two three four five", width: 14, want: "• one two three four five"},
		{name: "nested list", src: "- a\n  - b", width: 20, want: "• a\n◦ b"},
		{name: "quote", src: "> one two three four", width: 12, want: "one two three four"},
		{name: "code tab", src: "```go\n\tx := 1\n```", width: 30, want: "\tx := 1"},
		{name: "cut code", src: "```\nabcdefghijklmnopqrstuvwxyz\n```", width: 20, want: "abcdefghijklmnopqrstuvwxyz"},
		{
			name: "cut code start", src: "```\nabcdefghijklmnopqrstuvwxyz\n```", width: 20, want: "abc",
			picks: func(l Layout) []CellRange {
				p := make([]CellRange, len(l.Lines))
				for i := range p {
					p[i] = noCells
				}

				col := l.Text(1).Pieces[0].Col
				p[1] = CellRange{col, col + 2}

				return p
			},
		},
		{
			name: "table", src: "| h1 | h2 |\n| --- | --- |\n| one two three | x |", width: 20,
			want: "h1\th2\none two three\tx",
		},
		{
			name: "image line", src: "![alt](https://example.com/a.png)", width: 40,
			want: "![alt](https://example.com/a.png)",
		},
		{name: "fold", src: "<details>\n<summary>More</summary>\n\nhidden\n\n</details>", width: 30, want: "More"},
		{
			name: "block", src: wrapSrc, width: 12, block: true, want: "one\nthr",
			picks: func(l Layout) []CellRange {
				p := make([]CellRange, len(l.Lines))
				for i := range p {
					p[i] = noCells
				}

				p[0], p[1] = CellRange{0, 2}, CellRange{0, 2}

				return p
			},
		},
		{
			name: "wide", src: "日本語", width: 20, want: "日",
			picks: func(l Layout) []CellRange { return []CellRange{{1, 1}} },
		},
		{name: "hidden", src: "x `a\u200bb` y", width: 30, want: "x a⟨U+200B⟩b y"},
		{name: "two tabs", src: "```\n\t\tx\n```", width: 30, want: "\t\tx"},
		{
			name: "cut code with tab and hidden", src: "```\nabcdefghijklmnopq\trst\u200buvw\n```", width: 20,
			want: "abcdefghijklmnopq\trst\u27e8U+200B\u27e9uvw",
		},
		{name: "wrap before inline code", src: "aaa bbb `code` ccc", width: 10, want: "aaa bbb code ccc"},
		{name: "wrap before later inline code", src: "a b\n\n`xx` yy `zz` `ww`", width: 10, want: "a b\n\nxx yy zz ww"},
		{
			name: "wrapped table cell with inline code", src: "| h |\n| --- |\n| aaa bbb `cc` d |", width: 10,
			want: "h\naaa bbb cc d",
		},
		{
			name: "inside a hidden tag", src: "x `a\u200bb` y", width: 30, want: "\u27e8U+200B\u27e9",
			picks: func(l Layout) []CellRange {
				for _, p := range l.Text(0).Pieces {
					if p.Whole {
						return []CellRange{{p.Col + 2, p.Col + 2}}
					}
				}

				return nil
			},
		},
		{name: "break rule", src: "a\n\n---\n\nb", width: 20, want: "a\n\n\nb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := layoutOf(tt.src, tt.width, tt.base)

			picks := picksAll(l)
			if tt.picks != nil {
				picks = tt.picks(l)
			}

			if got := CopyLines(linesText(l), picks, tt.block); got != tt.want {
				t.Errorf("CopyLines = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCopyPicture(t *testing.T) {
	t.Parallel()

	opts := LayoutOptions{Width: 40, Picture: func(src string, maxCols int) PictureSize {
		return PictureSize{Cols: 4, Rows: 3, ID: 1}
	}}
	l := Parse([]byte("![alt](a.png)"), ParseOptions{Base: "/w"}).Layout(opts)

	if len(l.Lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(l.Lines))
	}

	const want = "![alt](a.png)"

	if got := CopyLines(linesText(l), picksAll(l), false); got != want {
		t.Errorf("all rows copy %q, want %q", got, want)
	}

	for i := 1; i <= 2; i++ {
		lt := l.Text(i)
		if lt.Join != JoinTight || !lt.Picture {
			t.Errorf("row %d: Join %v Picture %v, want JoinTight and Picture", i, lt.Join, lt.Picture)
		}
	}

	if !l.Text(0).Picture || l.Text(0).Join != JoinNone {
		t.Errorf("row 0: %+v", l.Text(0))
	}

	picks := []CellRange{noCells, noCells, allCells}
	if got := CopyLines(linesText(l), picks, false); got != want {
		t.Errorf("row 2 alone copies %q, want %q", got, want)
	}
}

func TestLineTextChrome(t *testing.T) {
	t.Parallel()

	layout := func(src string, icons bool) Layout {
		return Parse([]byte(src), ParseOptions{}).Layout(LayoutOptions{Width: 30, Icons: icons})
	}

	t.Run("code box", func(t *testing.T) {
		t.Parallel()

		l := layout("```go\nx\n```", false)
		if !l.Text(0).Skip || !l.Text(2).Skip {
			t.Errorf("code box top and bottom must be Skip: %+v %+v", l.Text(0), l.Text(2))
		}

		lt := l.Text(1)
		if lt.Skip || len(lt.Pieces) != 1 {
			t.Fatalf("code line: %+v", lt)
		}

		// "| " and the line number "1 " are chrome, the pad after "x" too.
		if p := lt.Pieces[0]; p.Col != 4 || p.Cells != 1 || p.Text != "x" {
			t.Errorf("piece = %+v, want x at cell 4", p)
		}
	})

	t.Run("empty code line", func(t *testing.T) {
		t.Parallel()

		l := layout("```\na\n\nb\n```", false)
		if lt := l.Text(2); lt.Skip || len(lt.Pieces) != 0 {
			t.Errorf("an empty code line is a line with nothing: %+v", lt)
		}

		if got := CopyLines(linesText(l), picksAll(l), false); got != "a\n\nb" {
			t.Errorf("copy = %q", got)
		}
	})

	t.Run("table", func(t *testing.T) {
		t.Parallel()

		l := layout("| h1 | h2 |\n| --- | --- |\n| a | |", false)
		for _, i := range []int{0, 2, 4} {
			if !l.Text(i).Skip {
				t.Errorf("table rule %d must be Skip", i)
			}
		}

		if got := pieceTexts(l.Text(1)); !slices.Equal(got, []string{"h1", "h2"}) {
			t.Errorf("header pieces = %q", got)
		}

		if lt := l.Text(3); lt.Skip || !lt.Table {
			t.Errorf("a row with an empty cell is a table line: %+v", lt)
		}

		if got := CopyLines(linesText(l), picksAll(l), false); got != "h1\th2\na" {
			t.Errorf("copy = %q", got)
		}
	})

	t.Run("quote bar", func(t *testing.T) {
		t.Parallel()

		l := layout("> a\n>\n> b", false)
		if len(l.Lines) != 3 {
			t.Fatalf("lines = %d: %q", len(l.Lines), l.Plain())
		}

		if p := l.Text(0).Pieces; len(p) != 1 || p[0].Col != 2 || p[0].Text != "a" {
			t.Errorf("quote line pieces = %+v", p)
		}

		if lt := l.Text(1); lt.Skip || len(lt.Pieces) != 0 {
			t.Errorf("bar-only line = %+v", lt)
		}

		if got := CopyLines(linesText(l), picksAll(l), false); got != "a\n\nb" {
			t.Errorf("copy = %q", got)
		}
	})

	t.Run("list indent and number pad", func(t *testing.T) {
		t.Parallel()

		l := layout("- a", false)
		if p := l.Text(0).Pieces; len(p) != 2 || p[0].Col != 2 || p[0].Text != "• " {
			t.Errorf("bullet pieces = %+v", p)
		}

		l = layout("9. a\n10. b", false)
		if p := l.Text(0).Pieces; len(p) != 2 || p[0].Col != 3 || p[0].Text != "9. " {
			t.Errorf("number pieces = %+v", p)
		}

		if got := CopyLines(linesText(l), picksAll(l), false); got != "9. a\n10. b" {
			t.Errorf("copy = %q", got)
		}
	})

	t.Run("fold hint", func(t *testing.T) {
		t.Parallel()

		l := layout("<details>\n<summary>S</summary>\n</details>", false)
		if got := pieceTexts(l.Text(0)); !slices.Equal(got, []string{"S"}) {
			t.Errorf("fold head pieces = %q", got)
		}
	})

	t.Run("alert icon", func(t *testing.T) {
		t.Parallel()

		l := layout("> [!NOTE]\n> text", true)
		if got := strings.Join(pieceTexts(l.Text(0)), ""); got != "Note" {
			t.Errorf("alert title copies %q, want Note", got)
		}

		if got := CopyLines(linesText(l), picksAll(l), false); got != "Note\ntext" {
			t.Errorf("copy = %q", got)
		}
	})
}

func TestLineTextTable(t *testing.T) {
	t.Parallel()

	l := layoutOf("| h1 | h2 |\n| --- | --- |\n| one two three | x |", 20, "")
	if len(l.Lines) != 6 {
		t.Fatalf("lines = %d: %q", len(l.Lines), l.Plain())
	}

	head := l.Text(1)
	if !head.Table || head.Join != JoinNone {
		t.Errorf("header = %+v", head)
	}

	first, later := l.Text(3), l.Text(4)
	if !first.Table || !later.Table {
		t.Fatalf("body lines are table lines: %+v %+v", first, later)
	}

	if len(first.Pieces) != 2 || first.Pieces[0].Cell != 0 || first.Pieces[1].Cell != 1 {
		t.Errorf("first body line pieces = %+v", first.Pieces)
	}

	if first.Join != JoinNone {
		t.Errorf("first body line Join = %v", first.Join)
	}

	if later.Join != JoinSpace || len(later.Pieces) != 1 || later.Pieces[0].Cell != 0 || later.Pieces[0].Join != JoinSpace {
		t.Errorf("wrapped cell's later line = %+v", later)
	}
}

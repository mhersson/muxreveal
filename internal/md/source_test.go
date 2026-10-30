package md

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// sourceRows is each layout line as "first-last text"; a line no source made
// reads "0-0".
func sourceRows(l Layout) []string {
	plain := l.Plain()
	rows := make([]string, len(plain))

	for i, p := range plain {
		rows[i] = fmt.Sprintf("%d-%d %s", l.Source[i].First, l.Source[i].Last, strings.TrimRight(p, " "))
	}

	return rows
}

func TestLayoutSource(t *testing.T) {
	t.Parallel()

	const rule = "────────────────────────────────────────"

	tests := []struct {
		name string
		src  string
		open map[int]bool
		want []string
	}{
		{
			name: "heading rule and wrapped paragraph",
			src:  "# Title\n\nthe first line of a paragraph that wraps\nsecond line\n",
			want: []string{
				"1-1 Title",
				"1-1 " + rule,
				"0-0 ",
				"3-4 the first line of a paragraph that wraps",
				"3-4 second line",
			},
		},
		{
			name: "compact list with a nested item and a blank-separated item",
			src:  "- one\n  - nested\n- two\n\n- three\n",
			want: []string{
				"1-1   • one",
				"2-2     ◦ nested",
				"3-3   • two",
				"5-5   • three",
			},
		},
		{
			name: "fenced code box runs fence to fence",
			src:  "intro\n\n```go\nx := 1\n\ny := 2\n```\n",
			want: []string{
				"1-1 intro",
				"0-0 ",
				"3-7 ╭─ go ─────────────────────────────────╮",
				"3-7 │ 1 x := 1                             │",
				"3-7 │ 2                                    │",
				"3-7 │ 3 y := 2                             │",
				"3-7 ╰──────────────────────────────────────╯",
			},
		},
		{
			name: "table",
			src:  "| a | b |\n|---|---|\n| 1 | 2 |\n",
			want: []string{
				"1-3 ╭───┬───╮",
				"1-3 │ a │ b │",
				"1-3 ├───┼───┤",
				"1-3 │ 1 │ 2 │",
				"1-3 ╰───┴───╯",
			},
		},
		{
			name: "quote and alerts",
			src:  "> quoted\n> more\n\n> [!NOTE]\n> body\n\n> [!TIP]\n> one\n>\n> two\n",
			want: []string{
				"1-2 ▎ quoted more",
				"0-0 ",
				"4-5 ▎ Note",
				"5-5 ▎ body",
				"0-0 ",
				"7-10 ▎ Tip",
				"8-8 ▎ one",
				"0-0 ▎",
				"10-10 ▎ two",
			},
		},
		{
			name: "folded fold",
			src:  "<details>\n<summary>Sum</summary>\n\nbody\n\n</details>\n\nafter\n",
			want: []string{
				"1-6 ▸ Sum   enter opens",
				"0-0 ",
				"8-8 after",
			},
		},
		{
			name: "open fold",
			src:  "<details>\n<summary>Sum</summary>\n\nbody\n\n</details>\n\nafter\n",
			open: map[int]bool{0: true},
			want: []string{
				"1-6 ▾ Sum",
				"4-6   body",
				"0-0 ",
				"8-8 after",
			},
		},
		{
			name: "trailing blank lines and CRLF",
			src:  "a\r\nb\r\n\r\nc\r\n\r\n\r\n",
			want: []string{
				"1-2 a b",
				"0-0 ",
				"4-4 c",
			},
		},
		{
			name: "footnotes",
			src:  "see[^1]\n\n[^1]: the note\n    more\n",
			want: []string{
				"1-1 see[1]",
				"0-0 ",
				"0-0 ────────────────",
				"3-4  1. the note more",
			},
		},
		{
			name: "paragraph split by an inline fold",
			src:  "before <details open>\nbody text\n</details>\n",
			open: map[int]bool{0: true},
			want: []string{
				"1-1 before",
				"0-0 ",
				"1-3 ▾ Details",
				"2-3   body text",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := Parse([]byte(tt.src), ParseOptions{}).Layout(LayoutOptions{Width: 40, Open: tt.open})

			if len(l.Source) != len(l.Lines) {
				t.Fatalf("%d source ranges for %d lines", len(l.Source), len(l.Lines))
			}

			if got := sourceRows(l); !slices.Equal(got, tt.want) {
				t.Errorf("source rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestFoldCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want int
	}{
		{"none", "plain text\n", 0},
		{"one", "<details>\n<summary>a</summary>\n\nx\n\n</details>\n", 1},
		{
			"nested",
			"<details open>\n\n<details open>\n\n<details>\n\nx\n\n</details>\n\n</details>\n\n</details>\n",
			3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := Parse([]byte(tt.src), ParseOptions{})

			if got := d.FoldCount(); got != tt.want {
				t.Errorf("FoldCount = %d, want %d", got, tt.want)
			}

			if got := len(layoutAllOpen(d, LayoutOptions{Width: 40}).Folds); got != tt.want {
				t.Errorf("folds laid out open = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIsMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want bool
	}{
		{"a.md", true},
		{"A.MD", true},
		{"x.markdown", true},
		{"docs/Readme.Markdown", true},
		{"x.mdx", false},
		{"md", false},
		{"x.md.txt", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := IsMarkdown(tt.name); got != tt.want {
				t.Errorf("IsMarkdown(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

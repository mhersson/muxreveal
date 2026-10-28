package md

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// widthSamples are clusters whose width differs from the sum of their runes.
var widthSamples = map[string]string{
	"flag":    "\U0001F1F3\U0001F1F4",
	"keycap":  "1\uFE0F\u20E3",
	"zwj":     "\U0001F469\u200D\U0001F4BB",
	"cjk":     "\u4E2D",
	"combine": "e\u0301",
}

// assertBordersAligned checks that the last │ of every framed row sits in one
// cell column, measured by grapheme cluster.
func assertBordersAligned(t *testing.T, l Layout, rows []string) {
	t.Helper()

	want := -1

	for i, p := range rows {
		j := strings.LastIndex(p, "│")
		if j < 0 {
			continue
		}

		col := ansi.StringWidth(p[:j])
		if want < 0 {
			want = col
		} else if col != want {
			t.Errorf("row %d: right border at cell %d, want %d: %q", i, col, want, p)
		}
	}
}

func TestCodeWidthByCluster(t *testing.T) {
	for name, sample := range widthSamples {
		t.Run(name, func(t *testing.T) {
			src := "```\nplain line\nab" + sample + "cd\n" + sample + sample + "\nplain\n```\n"

			for _, w := range []int{14, 20, 40} {
				l := codeLayout(src, w, nil)
				assertSafe(t, l, w)
				assertBordersAligned(t, l, l.Plain()[1:len(l.Lines)-1])
			}
		})
	}
}

func TestTableWidthByCluster(t *testing.T) {
	for name, sample := range widthSamples {
		t.Run(name, func(t *testing.T) {
			src := "| a | b |\n|---|---|\n| plain | x |\n| ab" + sample + "cd | " + sample + " |\n| zz | yyyy |\n"

			for _, w := range []int{14, 30, 60} {
				l := layoutOf(src, w, "")
				assertSafe(t, l, w)
				assertBordersAligned(t, l, l.Plain())

				for i, p := range l.Plain() {
					if got := ansi.StringWidth(p); got != ansi.StringWidth(l.Plain()[0]) {
						t.Errorf("width %d row %d is %d cells, frame is %d: %q", w, i, got, ansi.StringWidth(l.Plain()[0]), p)
					}
				}
			}
		})
	}
}

func TestTextWidthMatchesStringWidth(t *testing.T) {
	for name, s := range widthSamples {
		if got, want := textWidth("a"+s+"b"), ansi.StringWidth("a"+s+"b"); got != want {
			t.Errorf("%s: textWidth = %d, StringWidth = %d", name, got, want)
		}
	}
}

// splitSamples are clusters cut in two by a style change.
var splitSamples = map[string]string{
	"flag":   "**\U0001F1F3**\U0001F1F4",
	"keycap": "**1**\uFE0F\u20E3",
}

func TestTableSplitClusterAligned(t *testing.T) {
	for name, cell := range splitSamples {
		t.Run(name, func(t *testing.T) {
			src := "| a | b |\n|---|---|\n| plain | x |\n| ab " + cell + " cd | " + cell + " |\n| zz | yyyy |\n"

			for _, w := range []int{14, 30, 60} {
				l := layoutOf(src, w, "")
				assertSafe(t, l, w)
				assertBordersAligned(t, l, l.Plain())

				for i, p := range l.Plain() {
					if got, want := ansi.StringWidth(p), ansi.StringWidth(l.Plain()[0]); got != want {
						t.Errorf("width %d row %d is %d cells, frame is %d: %q", w, i, got, want, p)
					}
				}
			}
		})
	}
}

func TestProseSplitClusterFits(t *testing.T) {
	for name, cell := range splitSamples {
		t.Run(name, func(t *testing.T) {
			for _, w := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 20} {
				l := layoutOf("ab "+cell+" cd "+cell+" ef", w, "")
				assertSafe(t, l, w)
			}
		})
	}
}

func TestSplitClusterTakesFirstStyle(t *testing.T) {
	for name, cell := range splitSamples {
		t.Run(name, func(t *testing.T) {
			l := layoutOf("x "+cell+" y", 40, "")

			if len(l.Lines) != 1 {
				t.Fatalf("got %d lines: %q", len(l.Lines), l.Plain())
			}

			var cluster *Run

			for i, r := range l.Lines[0] {
				if r.Bold && r.Text != "" {
					cluster = &l.Lines[0][i]
				}
			}

			if cluster == nil {
				t.Fatalf("no bold run in %+v", l.Lines[0])
			}

			// Prose drops the variation selector, so the keycap is "1" plus U+20E3.
			want := map[string]string{"flag": widthSamples["flag"], "keycap": "1\u20E3"}[name]
			if cluster.Text != want {
				t.Errorf("bold run = %q, want the whole cluster %q", cluster.Text, want)
			}

			if got := ansi.StringWidth(l.Plain()[0]); got != lineWidth(l.Lines[0]) {
				t.Errorf("lineWidth = %d, text is %d cells", lineWidth(l.Lines[0]), got)
			}
		})
	}
}

func TestLongSplitClusterFits(t *testing.T) {
	src := "e" + strings.Repeat("**\u0301**~~\u0302~~", 500)

	for _, w := range []int{1, 2, 10, 20, 100} {
		assertSafe(t, layoutOf(src, w, ""), w)
	}
}

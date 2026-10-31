package diffview

import (
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func partTheme() *Theme {
	var syn [NumClasses]color.Color

	syn[Keyword] = color.RGBA{R: 0xff, G: 0x20, B: 0x80, A: 0xff}

	return NewTheme(Palette{
		Ground: color.RGBA{R: 0x1e, G: 0x1e, B: 0x2e, A: 0xff},
		Fg:     color.RGBA{R: 0xcd, G: 0xd6, B: 0xf4, A: 0xff},
		Accent: color.RGBA{R: 0x89, G: 0xb4, B: 0xfa, A: 0xff},
		Add:    color.RGBA{R: 0xa6, G: 0xe3, B: 0xa1, A: 0xff},
		Delete: color.RGBA{R: 0xf3, G: 0x8b, B: 0xa8, A: 0xff},
		Syntax: syn,
	})
}

func TestCodePart(t *testing.T) {
	t.Parallel()

	th := partTheme()
	text := "func abcdefghijklmnopqrs" // 24 cells
	text += "t"                        // 25
	spans := []Span{{Start: 5, End: 15, Class: Keyword}}
	kw := th.code[Add][0][0][Keyword]

	whole := func(changed []Range) string {
		return th.CodeRanges(text, spans, changed, Add, false, 0, 25)
	}

	tests := []struct {
		name          string
		changed       []Range
		from, to, w   int
		wantCells     string
		wantSame      bool // equals the same cells of the whole line
		wantKeyword   bool
		wantWordTints int
	}{
		{"first part", nil, 0, 10, 10, text[:10], true, true, 0},
		{"a span across the cut", nil, 10, 20, 10, text[10:20], true, true, 0},
		{"a changed range across the cut", []Range{{Start: 8, End: 14}}, 0, 10, 10, text[:10], true, true, 1},
		{"the second part of a changed range", []Range{{Start: 8, End: 14}}, 10, 20, 10, text[10:20], true, true, 1},
		{"padded on the code ground", nil, 20, 25, 10, text[20:] + strings.Repeat(" ", 5), false, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := th.CodePart(text, spans, tt.changed, Add, false, tt.from, tt.to, tt.w)

			if plain := ansi.Strip(got); plain != tt.wantCells {
				t.Fatalf("cells = %q, want %q", plain, tt.wantCells)
			}

			if strings.Contains(got, "…") {
				t.Errorf("an ellipsis in %q", got)
			}

			if tt.wantSame {
				want := ansi.Cut(whole(tt.changed), tt.from, tt.to)
				if got != want {
					t.Errorf("styling differs from the line's own cells:\n got %q\nwant %q", got, want)
				}
			}

			if tt.wantKeyword && !strings.Contains(got, kw) {
				t.Errorf("the keyword colour is missing from %q", got)
			}

			word := th.code[Add][0][1][Keyword]
			if has := strings.Contains(got, word); has != (tt.wantWordTints > 0) {
				t.Errorf("word tint present = %v, want %v", has, tt.wantWordTints > 0)
			}

			if tt.to-tt.from < tt.w {
				plain := th.code[Add][0][0][Plain]
				if !strings.HasSuffix(got, plain+strings.Repeat(" ", tt.w-(tt.to-tt.from))+reset) {
					t.Errorf("the padding is not on the code ground: %q", got)
				}
			}
		})
	}
}

func TestCodePartClipsToWidth(t *testing.T) {
	t.Parallel()

	th := partTheme()
	got := th.CodePart("abcdefgh", nil, nil, Context, false, 0, 8, 5)

	if w := ansi.StringWidth(got); w != 5 {
		t.Errorf("width = %d, want 5", w)
	}

	if plain := ansi.Strip(got); plain != "abcde" {
		t.Errorf("cells = %q, want abcde", plain)
	}
}

func TestCodeBg(t *testing.T) {
	t.Parallel()

	th := partTheme()
	if got, want := th.CodeBg(Add, true), th.Bg(Add, true, false); got != want {
		t.Errorf("CodeBg = %v, want %v", got, want)
	}
}

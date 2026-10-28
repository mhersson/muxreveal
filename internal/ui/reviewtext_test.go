package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mhersson/muxreveal/internal/git"
)

func TestCleanText(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   string
		hidden bool
	}{
		{"plain", "hello", "hello", false},
		{"empty", "", "", false},
		{"tab at column 0", "\tx", "    x", false},
		{"tab mid-stop", "ab\tx", "ab  x", false},
		{"tab on a stop", "abcd\tx", "abcd    x", false},
		{"tab after a tag counts the tag", "\x01\tx", "⟨U+0001⟩    x", true},
		{"escape", "a\x1bb", "a⟨U+001B⟩b", true},
		{"csi sequence", "\x1b[31mred", "⟨U+001B⟩[31mred", true},
		{"bidi override", "a\u202eb", "a⟨U+202E⟩b", true},
		{"zero width space", "a\u200bb", "a⟨U+200B⟩b", true},
		{"line separator", "a\u2028b", "a⟨U+2028⟩b", true},
		{"delete", "a\x7fb", "a⟨U+007F⟩b", true},
		{"crlf line", "text\r", "text⟨U+000D⟩", true},
		{"c1 next line", "a\u0085b", "a⟨U+0085⟩b", true},
		{"c1 csi as utf-8", "a\u009bb", "a⟨U+009B⟩b", true},
		{"astral format character", "a\U000e0041b", "a⟨U+E0041⟩b", true},
		{"lone 0x9b byte", "a\x9bb", "a�b", false},
		{"invalid utf-8 bytes", "a\xff\xfeb", "a��b", false},
		{"truncated sequence", "a\xe2\x82", "a��", false},
		{"wide characters are not touched", "日本\tx", "日本    x", false},
		{"emoji joiner is hidden", "a\u200db", "a⟨U+200D⟩b", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, hidden := cleanText(tt.in)
			if got != tt.want || hidden != tt.hidden {
				t.Errorf("cleanText(%q) = %q, %v; want %q, %v", tt.in, got, hidden, tt.want, tt.hidden)
			}

			if !utf8.ValidString(got) {
				t.Errorf("result %q is not valid UTF-8", got)
			}

			for _, r := range got {
				if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
					t.Errorf("control %U survived in %q", r, got)
				}
			}
		})
	}
}

func TestCleanTextLongLine(t *testing.T) {
	in := strings.Repeat("x", 1<<20)

	got, hidden := cleanText(in)
	if got != in || hidden {
		t.Errorf("a 1 MiB line changed: len %d, hidden %v", len(got), hidden)
	}

	in = strings.Repeat("a\x1b", 1<<19)

	got, hidden = cleanText(in)
	if !hidden || len(got) != (1<<19)*(1+len("⟨U+001B⟩")) {
		t.Errorf("a 1 MiB hostile line: len %d, hidden %v", len(got), hidden)
	}
}

func TestCleanLine(t *testing.T) {
	tests := []struct {
		name, in, want string
		hidden         bool
	}{
		{"lf line", "text", "text", false},
		{"crlf line", "text\r", "text", false},
		{"only the last cr", "text\r\r", "text⟨U+000D⟩", true},
		{"mid-line cr", "a\rb", "a⟨U+000D⟩b", true},
		{"cr then other control", "a\x1b\r", "a⟨U+001B⟩", true},
		{"lone cr line", "\r", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, hidden := cleanLine(tt.in)
			if got != tt.want || hidden != tt.hidden {
				t.Errorf("cleanLine(%q) = %q, %v; want %q, %v", tt.in, got, hidden, tt.want, tt.hidden)
			}
		})
	}
}

func TestShownLine(t *testing.T) {
	const cutMark = "\u2026"

	tests := []struct {
		name, in, want string
		cut            bool
	}{
		{"short line", "abc", "abc", false},
		{"empty", "", "", false},
		{"crlf", "abc\r", "abc", false},
		{"lone cr line", "\r", "", false},
		{"exactly the budget", xs(lineBudget), xs(lineBudget), false},
		{"one past the budget", xs(lineBudget + 1), xs(lineBudget) + cutMark, true},
		{"a crlf body at the budget", xs(lineBudget) + "\r", xs(lineBudget), false},
		{"a crlf body past the budget", xs(lineBudget+1) + "\r", xs(lineBudget) + cutMark, true},
		{"a tag is one rune of the budget", strings.Repeat("\x1b", lineBudget+1), strings.Repeat("\u27e8U+001B\u27e9", lineBudget) + cutMark, true},
		{"an invalid byte is one rune of the budget", strings.Repeat("\xff", lineBudget+1), strings.Repeat("\ufffd", lineBudget) + cutMark, true},
		{"a wide rune is not split", xs(lineBudget-1) + "\u65e5\u672c", xs(lineBudget-1) + "\u65e5" + cutMark, true},
		{"tabs expand from the start of the line", "\t" + xs(lineBudget), "    " + xs(lineBudget-1) + cutMark, true},
		{"marks past the budget are still a cut", "e" + marks(5000), "e" + marks(lineBudget-1) + cutMark, true},
		{"1 MiB plain", strings.Repeat("x", 1<<20), xs(lineBudget) + cutMark, true},
		{"1 MiB of tags", strings.Repeat("a\x1b", 1<<19), strings.Repeat("a\u27e8U+001B\u27e9", lineBudget/2) + cutMark, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cut := shownLine(tt.in)
			if got != tt.want || cut != tt.cut {
				t.Fatalf("shownLine(%.40q...) = %.40q... (%d bytes), %v; want %.40q... (%d bytes), %v", tt.in, got, len(got), cut, tt.want, len(tt.want), tt.cut)
			}

			if !utf8.ValidString(got) {
				t.Error("the result is not valid UTF-8")
			}

			// What is kept is what cleaning the whole line gives.
			if whole, _ := cleanLine(tt.in); !strings.HasPrefix(whole, strings.TrimSuffix(got, cutMark)) {
				t.Error("the result is not the start of the cleaned line")
			}
		})
	}
}

func TestLineShown(t *testing.T) {
	tests := []struct {
		name, raw, other string
		paired           bool
		want             string
	}{
		{"unpaired crlf", "x\r", "", false, "x"},
		{"both crlf", "x\r", "x\r", true, "x"},
		{"only this has the cr", "x\r", "x", true, "x⟨U+000D⟩"},
		{"only the other has the cr", "x", "x\r", true, "x"},
		{"content differs", "x\r", "y", true, "x"},
		{"crlf at the budget keeps the tag", xs(lineBudget) + "\r", xs(lineBudget), true, xs(lineBudget) + "\u27e8U+000D\u27e9"},
		{"crlf past the budget is cut, with no tag", xs(lineBudget+1) + "\r", xs(lineBudget + 1), true, xs(lineBudget) + "\u2026"},
		{"crlf past the budget is not a mid-line cr", xs(5000) + "\r", "", false, xs(lineBudget) + "\u2026"},
		{"a cr inside the budget of a long line is tagged", "a\rb" + xs(5000), "", false, "a\u27e8U+000D\u27e9b" + xs(lineBudget-3) + "\u2026"},
		{"a cr past the budget is dropped with the rest", xs(5000) + "\rtail", "", false, xs(lineBudget) + "\u2026"},
		{"a cr at the cut is dropped, not read as an ending", xs(lineBudget) + "\rtail", "", false, xs(lineBudget) + "\u2026"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lineShown(tt.raw, tt.other, tt.paired); got != tt.want {
				t.Errorf("lineShown(%q, %q, %v) = %q, want %q", tt.raw, tt.other, tt.paired, got, tt.want)
			}
		})
	}
}

func xs(n int) string { return strings.Repeat("x", n) }

// A CR past the cut is not drawn, but the file it is in is still flagged.
func TestFileHiddenSeesPastTheCut(t *testing.T) {
	for _, tt := range []struct {
		name string
		line string
		want bool
	}{
		{"a cr past the cut", "+" + xs(5000) + "\rtail", true},
		{"an escape past the cut", "+" + xs(5000) + "\x1b[2J", true},
		{"a crlf ending past the cut", "+" + xs(5000) + "\r", false},
		{"a plain long line", "+" + xs(5000), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := fileSpec("long.txt", git.Modified, hunkSpec(1, 1, "", tt.line))
			if got := fileHidden(f); got != tt.want {
				t.Errorf("fileHidden = %v, want %v", got, tt.want)
			}
		})
	}
}

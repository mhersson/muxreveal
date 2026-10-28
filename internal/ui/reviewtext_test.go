package ui

import (
	"strings"
	"testing"
	"unicode/utf8"
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

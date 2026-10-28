package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/git/invisible"
)

const tabStop = 4

// cleanText makes diff text safe to draw: tabs expand to the next multiple of
// 4 columns, and hidden characters and C0 or C1 controls become <U+XXXX> tags.
// Each invalid UTF-8 byte becomes U+FFFD, so no raw byte reaches the terminal.
// hidden reports whether a tag was written; U+FFFD does not count. The text is
// returned whole: cutting to the width is drawing's job.
func cleanText(s string) (text string, hidden bool) {
	if plainText(s) {
		return s, false
	}

	var (
		b   strings.Builder
		col int
	)

	b.Grow(len(s) + len(s)/8)

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size

		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)

			col++
		case r == '\t':
			n := tabStop - col%tabStop
			b.WriteString(strings.Repeat(" ", n))

			col += n
		case r < 0x20 || (r >= 0x7f && r <= 0x9f) || invisible.Rune(r):
			tag := fmt.Sprintf("⟨U+%04X⟩", r)
			b.WriteString(tag)

			col += utf8.RuneCountInString(tag)
			hidden = true
		default:
			b.WriteRune(r)

			col += runeWidth(r)
		}
	}

	return b.String(), hidden
}

// plainText reports whether s needs no cleaning: printable ASCII.
func plainText(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f {
			return false
		}
	}

	return true
}

func runeWidth(r rune) int {
	if r < 0x7f {
		return 1
	}

	return ansi.StringWidth(string(r))
}

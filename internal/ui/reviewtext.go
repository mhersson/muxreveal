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

// cleanLine is cleanText for a line of a file: one CR right before the line
// end is part of the line ending, so it is dropped and does not count as
// hidden. A CR anywhere else is tagged.
func cleanLine(s string) (text string, hidden bool) {
	return cleanText(strings.TrimSuffix(s, "\r"))
}

const endingTag = "⟨U+000D⟩"

// lineShown is the text drawn for a line. When the line it is paired with
// differs only in its ending and this one has the CR, the CR shows as a tag so
// the change is visible.
func lineShown(raw, other string, paired bool) string {
	text, _ := cleanLine(raw)

	if paired && strings.HasSuffix(raw, "\r") && other == strings.TrimSuffix(raw, "\r") {
		text += endingTag
	}

	return text
}

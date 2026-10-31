package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/git/invisible"
	"github.com/mhersson/muxreveal/internal/ui/diffview"
)

const tabStop = 4

// lineBudget is how many runes of one line are cleaned, drawn and tokenised.
// The rest is dropped before any per-rune work, so a frame costs the same for a
// 1 MiB line as for a short one. It is wider than any column the drawer has.
const lineBudget = 1024

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
// hidden. A CR anywhere else is tagged. It covers the whole line, so a file is
// flagged for what lies past the part drawn too.
func cleanLine(s string) (text string, hidden bool) {
	return cleanText(strings.TrimSuffix(s, "\r"))
}

// shownLine is the text drawn for a line of a file: the line without its
// ending, cut to lineBudget runes and then cleaned. A cut line ends in the
// ellipsis, so its row says it holds less than the line even when the part kept
// is narrower than the column. The ending is judged before the cut, so a CRLF
// ending never becomes a CR in the middle.
func shownLine(s string) (text string, cut bool) {
	s, cut = cutRunes(strings.TrimSuffix(s, "\r"), lineBudget)
	text, _ = cleanText(s)

	if cut {
		text += diffview.Ellipsis()
	}

	return text, cut
}

// cutRunes is the first n runes of s, an invalid byte counting as one as in
// cleanText, and whether any of s was left out. It looks at n runes at most.
func cutRunes(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}

	i := 0
	for ; n > 0 && i < len(s); n-- {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}

	return s[:i], i < len(s)
}

const endingTag = "⟨U+000D⟩"

// lineShown is the text drawn for a line. When the line it is paired with
// differs only in its ending and this one has the CR, the CR shows as a tag so
// the change is visible; a cut line shows none, as its ending lies past the cut.
func lineShown(raw, other string, paired bool) string {
	text, cut := shownLine(raw)

	if !cut && crTag(raw, other, paired) {
		text += endingTag
	}

	return text
}

// tagsCR reports whether lineShown ends the line with the CR tag.
func tagsCR(raw, other string, paired bool) bool {
	_, cut := cutRunes(strings.TrimSuffix(raw, "\r"), lineBudget)

	return !cut && crTag(raw, other, paired)
}

// crTag is the part of the CR tag rule that does not depend on the cut.
func crTag(raw, other string, paired bool) bool {
	return paired && strings.HasSuffix(raw, "\r") && other == strings.TrimSuffix(raw, "\r")
}

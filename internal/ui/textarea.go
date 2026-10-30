package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// textAreaPage is how many lines pgup and pgdown move.
const textAreaPage = 10

// textArea is a multi-line text field without soft wrap. Its cursor is a
// (row, col) in runes; top is the first row View draws. A max over zero caps
// the value's characters, newlines included.
type textArea struct {
	lines    [][]rune
	row, col int
	top, max int
}

// newTextArea returns a field holding text cut at max characters, with the
// cursor at its end.
func newTextArea(text string, limit int) textArea {
	t := textArea{lines: [][]rune{nil}, max: limit}
	t.Paste(text)

	return t
}

// Value is the text, lines joined by line feeds.
func (t *textArea) Value() string {
	parts := make([]string, len(t.lines))
	for i, l := range t.lines {
		parts[i] = string(l)
	}

	return strings.Join(parts, "\n")
}

func (t *textArea) size() int {
	n := len(t.lines) - 1
	for _, l := range t.lines {
		n += len(l)
	}

	return n
}

func (t *textArea) full() bool { return t.max > 0 && t.size() >= t.max }

func (t *textArea) insert(r rune) bool {
	if t.full() {
		return false
	}

	if r == '\n' {
		line := t.lines[t.row]
		rest := append([]rune(nil), line[t.col:]...)
		t.lines[t.row] = line[:t.col:t.col]
		t.lines = append(t.lines, nil)
		copy(t.lines[t.row+2:], t.lines[t.row+1:])
		t.lines[t.row+1] = rest
		t.row++
		t.col = 0

		return true
	}

	line := t.lines[t.row]
	line = append(line, 0)
	copy(line[t.col+1:], line[t.col:])
	line[t.col] = r
	t.lines[t.row] = line
	t.col++

	return true
}

// Paste inserts s at the cursor with CRLF and CR made LF, cut at the cap.
func (t *textArea) Paste(s string) {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)

	for _, r := range s {
		if !t.insert(r) {
			return
		}
	}
}

func (t *textArea) clamp() { t.col = min(t.col, len(t.lines[t.row])) }

// Key applies a key press and reports whether the text or the cursor changed.
func (t *textArea) Key(msg tea.KeyPressMsg) bool {
	row, col, n := t.row, t.col, t.size()
	changed := false

	switch msg.String() {
	case "enter":
		changed = t.insert('\n')
	case "backspace":
		changed = t.backspace()
	case "delete":
		changed = t.delete()
	case "left":
		if t.col > 0 {
			t.col--
		} else if t.row > 0 {
			t.row--
			t.col = len(t.lines[t.row])
		}
	case "right":
		if t.col < len(t.lines[t.row]) {
			t.col++
		} else if t.row < len(t.lines)-1 {
			t.row++
			t.col = 0
		}
	case "up":
		t.move(-1)
	case "down":
		t.move(1)
	case "pgup":
		t.move(-textAreaPage)
	case "pgdown":
		t.move(textAreaPage)
	case "home":
		t.col = 0
	case "end":
		t.col = len(t.lines[t.row])
	default:
		if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			before := t.size()
			t.Paste(msg.Text)

			return t.size() != before
		}
	}

	return changed || t.row != row || t.col != col || t.size() != n
}

func (t *textArea) move(d int) {
	t.row = max(0, min(t.row+d, len(t.lines)-1))
	t.clamp()
}

func (t *textArea) backspace() bool {
	switch {
	case t.col > 0:
		line := t.lines[t.row]
		t.lines[t.row] = append(line[:t.col-1], line[t.col:]...)
		t.col--
	case t.row > 0:
		prev := t.lines[t.row-1]
		t.col = len(prev)
		t.lines[t.row-1] = append(prev, t.lines[t.row]...)
		t.lines = append(t.lines[:t.row], t.lines[t.row+1:]...)
		t.row--
	default:
		return false
	}

	return true
}

func (t *textArea) delete() bool {
	line := t.lines[t.row]

	switch {
	case t.col < len(line):
		t.lines[t.row] = append(line[:t.col], line[t.col+1:]...)
	case t.row < len(t.lines)-1:
		t.lines[t.row] = append(line, t.lines[t.row+1]...)
		t.lines = append(t.lines[:t.row+1], t.lines[t.row+2:]...)
	default:
		return false
	}

	return true
}

// View draws h lines of at most w cells, scrolled to keep the cursor's row in
// view, and returns the cursor's row and column within them. The cursor's line
// scrolls sideways as fieldView does; the others are cut with an ellipsis. Text
// is drawn without control and invisible characters, and the column counts
// only the visible ones before the cursor.
func (t *textArea) View(w, h int, st lipgloss.Style) (lines []string, curRow, curCol int) {
	w, h = max(w, 0), max(h, 0)

	if t.row < t.top {
		t.top = t.row
	}

	if h > 0 && t.row >= t.top+h {
		t.top = t.row - h + 1
	}

	t.top = max(0, min(t.top, len(t.lines)-1))
	lines = make([]string, 0, h)

	for i := t.top; i < min(t.top+h, len(t.lines)); i++ {
		if i != t.row {
			lines = append(lines, st.Render(ansi.Truncate(printable(string(t.lines[i])), w, "…")))

			continue
		}

		runes := []rune(printable(string(t.lines[i])))
		pos := min(len([]rune(printable(string(t.lines[i][:t.col])))), len(runes))
		start := 0

		for start < pos && drawnWidth(string(runes[start:pos])) >= w {
			start++
		}

		curCol = drawnWidth(string(runes[start:pos]))
		lines = append(lines, st.Render(ansi.Truncate(string(runes[start:]), w, "…")))
	}

	return lines, t.row - t.top, curCol
}

package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The popup is the tallest overlay the leader opens; a row that pushes its
// bottom border off a 24-row terminal fails here.
func TestWhichKeyFitsTwentyFourRows(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	sized(t, a, 80, 24)
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})

	lines := strings.Split(ansi.Strip(a.View().Content), "\n")
	top, bottom := -1, -1

	for i, l := range lines {
		if strings.Contains(l, "╭─ "+iconLeader) {
			top = i
		}

		if strings.Contains(l, "┴") && strings.Contains(l, "╯") {
			bottom = i
		}
	}

	if top < 0 || bottom < 0 {
		t.Fatalf("popup top row %d or bottom border row %d missing:\n%s", top, bottom, strings.Join(lines, "\n"))
	}

	if want := len(lines) - 1; bottom != want {
		t.Errorf("bottom border on row %d, want the last row %d", bottom, want)
	}

	for _, want := range []string{"Push and pull requests", "Quit MuxReveal", "Jump to tab"} {
		if !strings.Contains(strings.Join(lines, "\n"), want) {
			t.Errorf("popup lacks %q", want)
		}
	}
}

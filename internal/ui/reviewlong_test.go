package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/git"
)

// marks is n combining acute accents: they take no cell, and cleanText leaves them alone.
func marks(n int) string { return strings.Repeat("\xcc\x81", n) }

// longLinesResult has lines past the budget whose kept part is narrower than
// any column, and one just inside the budget.
func longLinesResult() git.Result {
	f := fileSpec("long.txt", git.Modified, hunkSpec(1, 1, "",
		"+Z"+marks(5000),
		"+short",
		"-Q"+marks(5000),
		"+Q"+marks(5000)+"!",
		"+K"+marks(lineBudget-2),
	))

	return git.Result{Turn: 1, Files: []git.FileDiff{f}}
}

func longLinesFull() fullText {
	return fullText{
		old: []string{"head", "Q" + marks(5000), "tail"},
		new: []string{"head", "Z" + marks(5000), "K" + marks(lineBudget-2), "tail"},
	}
}

// diffEnd is a drawn row without the drawer's right border and the padding.
func diffEnd(row string) string {
	return strings.TrimRight(strings.TrimSuffix(strings.TrimRight(row, " "), "│"), " ")
}

// A row ends in the cut marker whenever any of its line was dropped, even when
// the part kept is narrower than the column, and only then.
func TestDrawnLongLinesEndInTheCutMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		split bool
		full  bool
		// the rows to find, the cut markers each must hold
		rows map[string]int
	}{
		{"unified", false, false, map[string]int{"Z\xcc\x81": 1, "Q\xcc\x81": 1, "K\xcc\x81": 0, "short": 0}},
		{"split", true, false, map[string]int{"Z\xcc\x81": 1, "Q\xcc\x81": 2, "K\xcc\x81": 0, "short": 0}},
		{"full file", false, true, map[string]int{"Z\xcc\x81": 1, "Q\xcc\x81": 1, "K\xcc\x81": 0, "tail": 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 240, h: 40, res: longLinesResult(), turn: 1, turns: 1, focused: true}
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.split = tt.split

				if tt.full {
					v.full = map[string]fullText{"long.txt": longLinesFull()}
					v.fullOn = allWhole(v.full)
					v.rebuild(false)
				}
			}
			a := s.build(t)

			st := a.store.State()
			r, _ := a.drawerRect(st)
			rows := a.drawerLines(st, r)

			v := a.reviewViewOf(st.Tab().ID)
			if v.rowsSplit != tt.split {
				t.Fatalf("rows are split = %v, want %v", v.rowsSplit, tt.split)
			}

			for start, want := range tt.rows {
				found := false

				for _, row := range rows {
					plain := ansi.Strip(row)
					if !strings.Contains(plain, start) {
						continue
					}

					found = true

					if got := strings.Count(diffEnd(plain), "…"); got != want {
						t.Errorf("the row of %q holds %d cut markers, want %d: %q", start, got, want, plain)
					}

					if want > 0 && !strings.HasSuffix(diffEnd(plain), "…") {
						t.Errorf("the row of %q does not end in the cut marker: %q", start, plain)
					}
				}

				if !found {
					t.Errorf("no row holds %q", start)
				}
			}
		})
	}
}

// What the highlighter is handed is bounded, whatever the length of the line.
func TestChunkLinesAreBounded(t *testing.T) {
	t.Parallel()

	f := fileSpec("long.go", git.Modified, hunkSpec(1, 1, "",
		"+"+longPlain(),
		"+"+longTags(),
		"+"+longPlain()+"\r",
		"-"+longTags(),
	))
	res := git.Result{Turn: 1, Files: []git.FileDiff{f}}

	for _, tt := range []struct {
		name string
		key  spanKey
		full fullText
	}{
		{"new side of a hunk", spanKey{file: 0, hunk: 0, side: sideNew}, fullText{}},
		{"old side of a hunk", spanKey{file: 0, hunk: 0, side: sideOld}, fullText{}},
		{"a whole file", spanKey{file: 0, hunk: -1, side: sideNew}, fullText{new: []string{longPlain(), longTags(), longPlain() + "\r"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := &reviewView{res: res, full: map[string]fullText{"long.go": tt.full}}

			path, lines := v.chunkLines(tt.key)
			if path != "long.go" || len(lines) == 0 {
				t.Fatalf("chunkLines = %q, %d lines", path, len(lines))
			}

			for i, l := range lines {
				// A rune of text is cleaned into at most ten.
				if n := utf8.RuneCountInString(l); n > 10*lineBudget+1 {
					t.Errorf("line %d has %d runes, want at most %d", i, n, 10*lineBudget+1)
				}
			}

			if tt.key.side == sideNew && utf8.RuneCountInString(lines[0]) > lineBudget+1 {
				t.Errorf("a plain line has %d runes, want at most %d", utf8.RuneCountInString(lines[0]), lineBudget+1)
			}
		})
	}
}

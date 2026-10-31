package ui

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/git"
)

func TestWrapLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		text  string
		units []diffUnit
		width int
		want  [][2]int
	}{
		{"exact fit", "abcdefghij", nil, 10, [][2]int{{0, 10}}},
		{"one over", "abcdefghijk", nil, 10, [][2]int{{0, 10}, {10, 11}}},
		{"space in reach", strings.Repeat("a", 21) + " " + strings.Repeat("b", 8), nil, 25, [][2]int{{0, 22}, {22, 30}}},
		{"space out of reach", strings.Repeat("a", 7) + " " + strings.Repeat("b", 22), nil, 25, [][2]int{{0, 25}, {25, 30}}},
		{"tab moves whole", "abcdefghi\tx", nil, 10, [][2]int{{0, 9}, {9, 13}}},
		{"tag moves whole", "abcdefgh\u200bxy", nil, 10, [][2]int{{0, 8}, {8, 18}}},
		{"tag wider than the column", "\u200b", nil, 5, [][2]int{{0, 8}}},
		{"empty", "", nil, 10, [][2]int{{0, 0}}},
		{"ending tag wraps alone", "", lineUnits("abcdefghij", true), 10, [][2]int{{0, 10}, {10, 18}}},
		{"ending tag skips the reach space", "", lineUnits("foo barxyz", true), 10, [][2]int{{0, 10}, {10, 18}}},
		{"budget ellipsis wraps alone", "", lineUnits(strings.Repeat("a", 1500), false), 1024, [][2]int{{0, 1024}, {1024, 1025}}},
		{"budget ellipsis skips the reach space", "", lineUnits(strings.Repeat("a", 1010)+" "+strings.Repeat("b", 100), false), 1024, [][2]int{{0, 1024}, {1024, 1025}}},
		{"no width", "abc def", nil, 0, [][2]int{{0, 7}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			units := tt.units
			if units == nil {
				units = diffUnits(tt.text)
			}

			parts := wrapLine(units, tt.width)

			var got [][2]int
			for _, p := range parts {
				got = append(got, [2]int{p.c0, p.c1})
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parts = %v, want %v", got, tt.want)
			}

			if tt.width <= 0 && (len(parts) != 1 || parts[0].u0 != 0 || parts[0].u1 != len(units)) {
				t.Fatalf("width %d: part %+v does not hold all %d units", tt.width, parts, len(units))
			}
		})
	}
}

func TestLineUnits(t *testing.T) {
	t.Parallel()

	long := lineUnits(strings.Repeat("x", 1500), false)
	if len(long) != lineBudget+1 {
		t.Fatalf("units = %d, want %d", len(long), lineBudget+1)
	}

	if last := long[len(long)-1]; !last.whole || last.cells != 1 {
		t.Fatalf("last unit = %+v, want a whole 1-cell ellipsis", last)
	}

	tagged := lineUnits("a", true)
	if len(tagged) != 2 || tagged[1].text != endingTag {
		t.Fatalf("tagged units = %+v, want the ending tag last", tagged)
	}

	if got := lineUnits("a\r", false); len(got) != 1 {
		t.Fatalf("a CR without the tag gave %d units, want 1", len(got))
	}
}

func TestLinePartsFastPath(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("ab c\t\u200b\r")

	for range 200 {
		var b strings.Builder
		for range rng.IntN(60) {
			b.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}

		raw := b.String()
		width := 1 + rng.IntN(40)

		for _, tagCR := range []bool{false, true} {
			got := lineParts(raw, tagCR, width)
			want := wrapLine(lineUnits(raw, tagCR), width)

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("lineParts(%q, %v, %d) = %v, want %v", raw, tagCR, width, got, want)
			}
		}
	}

	long := strings.Repeat("a", 1500)
	if got, want := lineParts(long, false, 2000), wrapLine(lineUnits(long, false), 2000); !reflect.DeepEqual(got, want) {
		t.Fatalf("long line: lineParts = %v, want %v", got, want)
	}
}

const wrapPath = "wrap.go"

// longLine is 70 characters with no space, so a 30-cell edge breaks it at
// cells 30 and 60.
var longLine = strings.Repeat("0123456789", 7)

// wrapResult is one file whose hunk holds a long line, a short one, a tab
// indented long one and a removed/added pair of different lengths.
func wrapResult() git.Result {
	f := fileSpec(wrapPath, git.Modified, hunkSpec(1, 1, "",
		" "+longLine,
		" short",
		" \t"+strings.Repeat("x", 70),
		"-"+strings.Repeat("abcdefghi.", 7),
		"+"+strings.Repeat("abcdefghi.", 16),
	))

	return git.Result{Turn: 1, Files: []git.FileDiff{f}}
}

// partsOf counts the rows of line li of hunk 0 and checks their parts run 0, 1, ...
func partsOf(t *testing.T, rows []reviewRow, li int) int {
	t.Helper()

	n := 0

	for _, r := range rows {
		if (r.kind != rowLine && r.kind != rowPair) || r.line != li {
			continue
		}

		if r.part != n {
			t.Fatalf("line %d: row %d has part %d, want %d", li, n, r.part, n)
		}

		n++
	}

	return n
}

func TestWrappedRows(t *testing.T) {
	t.Parallel()

	res := wrapResult()
	tests := []struct {
		name  string
		split bool
		wrap  codeWidths
		want  map[int]int // line index -> rows
	}{
		{"no wrap", false, codeWidths{}, map[int]int{0: 1, 1: 1, 2: 1, 3: 1, 4: 1}},
		{"unified 30", false, codeWidths{line: 30}, map[int]int{0: 3, 1: 1, 2: 3, 3: 3, 4: 6}},
		{"unified 70 fits", false, codeWidths{line: 70}, map[int]int{0: 1, 1: 1, 3: 1, 4: 3}},
		{"split takes the longer side", true, codeWidths{left: 30, right: 50}, map[int]int{0: 3, 1: 1, 3: 4}},
		{"split without a width", true, codeWidths{line: 30}, map[int]int{0: 1, 3: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := buildRows(res, rowOpts{split: tt.split, wrap: tt.wrap})

			for li, want := range tt.want {
				if got := partsOf(t, rows, li); got != want {
					t.Errorf("line %d: %d rows, want %d", li, got, want)
				}
			}

			for _, r := range rows {
				if r.kind == rowLine || r.kind == rowPair {
					if r.hunk != 0 || r.file != 0 {
						t.Errorf("row %+v is off its hunk", r)
					}
				} else if r.part != 0 {
					t.Errorf("row of kind %d has part %d", r.kind, r.part)
				}
			}
		})
	}
}

func TestWrappedFullRows(t *testing.T) {
	t.Parallel()

	res := wrapResult()
	ghost, added := strings.Repeat("g", 70), strings.Repeat("n", 70)
	ft := fullText{old: []string{"a", ghost, "b"}, new: []string{"a", added, "b"}}

	for _, folded := range []bool{false, true} {
		rows := buildRows(res, rowOpts{
			full: map[string]fullText{wrapPath: ft}, whole: map[string]bool{wrapPath: true},
			ghostsStartFolded: folded, wrap: codeWidths{full: 30, line: 7},
		})

		counts := map[reviewRowKind]int{}

		var fullParts []int

		for _, r := range rows {
			counts[r.kind]++

			if r.kind == rowFull {
				fullParts = append(fullParts, r.part)
			}
		}

		if want := []int{0, 0, 1, 2, 0}; !slices.Equal(fullParts, want) {
			t.Errorf("folded=%v: full row parts %v, want %v", folded, fullParts, want)
		}

		if folded && (counts[rowFold] != 1 || counts[rowGhost] != 0) {
			t.Errorf("folded: counts %v, want one fold row and no ghost", counts)
		}

		if !folded && counts[rowGhost] != 3 {
			t.Errorf("%d ghost rows, want 3", counts[rowGhost])
		}

		if counts[rowFile] != 1 {
			t.Errorf("%d file rows, want 1", counts[rowFile])
		}
	}
}

// wrapScene is a scene over res whose rows wrap at w.
func wrapScene(t *testing.T, res git.Result, w codeWidths, h int) (*App, *reviewView) {
	t.Helper()

	s := reviewScene{
		theme: "catppuccin-mocha", w: 100, h: h, res: res, turn: 1, turns: 1, focused: true,
		tune: func(_ *testing.T, _ *App, v *reviewView) {
			v.wrapW = w
			v.rebuild(false)
		},
	}
	a := s.build(t)

	return a, a.drawer.views[a.store.State().Tab().ID]
}

// padded is a file of n short context lines, then lines built from the rest.
func padded(n int, rest ...string) git.Result {
	specs := make([]string, 0, n+len(rest))
	for i := range n {
		specs = append(specs, fmt.Sprintf(" pad %d", i))
	}

	f := fileSpec(wrapPath, git.Modified, hunkSpec(1, 1, "", append(specs, rest...)...))

	return git.Result{Turn: 1, Files: []git.FileDiff{f}}
}

func TestWrappedCursorMoves(t *testing.T) {
	t.Parallel()

	a, v := wrapScene(t, padded(1, " "+longLine), codeWidths{line: 30}, 30)
	// Rows: file 0, hunk 1, pad 2, the long line 3-5.
	at := func(want int, what string) {
		t.Helper()

		if v.cursor != want {
			t.Fatalf("%s: cursor %d, want %d", what, v.cursor, want)
		}
	}

	press(a, keyRune('j'), keyRune('j'), keyRune('j'))
	at(3, "j onto the long line")

	press(a, keyRune('j'))
	at(3, "j from the last line stays")

	press(a, keyRune('k'))
	at(2, "k back")

	press(a, keyRune('j'), keyRune('k'))
	at(2, "k from the first row of the long line")

	press(a, keyRune('G'))
	at(3, "G lands on the last line's first row")

	press(a, keyRune('g'))
	at(0, "g")

	// A half page that lands on a continuation row puts the cursor on its first row.
	half := max(a.reviewBodyH(v)/2, 1)
	a, v = wrapScene(t, padded(half-1, " "+longLine), codeWidths{line: 30}, 30)
	press(a, keyRune('j'), keyRune('j'))
	press(a, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

	if v.cursor != half+1 || v.rows[v.cursor].part != 0 {
		// Rows: file, hunk, half-1 pads, so the long line starts at half+1.
		t.Errorf("ctrl+d: cursor %d part %d, want row %d part 0", v.cursor, v.rows[v.cursor].part, half+1)
	}
}

func TestWrappedClickLandsOnFirstRow(t *testing.T) {
	t.Parallel()

	a, v := wrapScene(t, padded(1, " "+longLine), codeWidths{line: 30}, 30)

	h, ok := a.rowsHost()
	if !ok {
		t.Fatal("no rows host")
	}

	clickRun(a, h.rect.Min.X+15, h.rect.Min.Y+5-h.top)

	if v.cursor != 3 {
		t.Errorf("a click on part 2: cursor %d, want 3", v.cursor)
	}
}

func TestWrappedTallLine(t *testing.T) {
	t.Parallel()

	tall := strings.Repeat("t", 1024)
	res := padded(0, append(append(manyPads(3), " "+tall), manyShort(25)...)...)

	var (
		a *App
		v *reviewView
	)

	for sh := 20; sh <= 40; sh++ {
		a, v = wrapScene(t, res, codeWidths{line: 40}, sh)
		if a.reviewBodyH(v) == 20 {
			break
		}
	}

	h := a.reviewBodyH(v)
	if h != 20 {
		t.Fatalf("diffH = %d, want 20", h)
	}

	// Rows: file 0, hunk 1, pads 2-4, the tall line 5-30 (26 rows), short lines 31-.
	const first, end = 5, 31

	if v.lineStart(first+4) != first || v.lineEnd(first+4) != end {
		t.Fatalf("the tall line spans %d-%d, want %d-%d", v.lineStart(first+4), v.lineEnd(first+4), first, end)
	}

	shows := func() bool { return v.lineEnd(v.cursor) > v.top && v.lineStart(v.cursor) < v.top+h }

	for range 5 {
		press(a, keyRune('j'))
	}

	if v.cursor != first || v.top != first {
		t.Fatalf("j onto the tall line: cursor %d top %d, want both %d", v.cursor, v.top, first)
	}

	x, y := wheelCell(t, a)

	for n := 0; ; n++ {
		if n > 30 {
			t.Fatal("the wheel never moved the cursor off the tall line")
		}

		wheelAt(a, x, y, tea.MouseWheelDown)

		if v.cursor == first {
			if v.top >= end {
				t.Fatalf("notch %d: cursor on the tall line with top %d past it", n, v.top)
			}

			continue
		}

		if v.cursor != v.top || v.rows[v.cursor].part != 0 {
			t.Fatalf("notch %d: cursor %d, want the first line starting in the view (top %d)", n, v.cursor, v.top)
		}

		break
	}

	// j and k step one line at a time and keep the cursor's line in view.
	press(a, keyRune('g'))

	for v.cursor != len(v.rows)-1 {
		prev := v.cursor

		press(a, keyRune('j'))

		if v.cursor <= prev || !shows() {
			t.Fatalf("j from %d: cursor %d, top %d", prev, v.cursor, v.top)
		}
	}

	for v.cursor != 0 {
		prev := v.cursor

		press(a, keyRune('k'))

		if v.cursor >= prev || !shows() {
			t.Fatalf("k from %d: cursor %d, top %d", prev, v.cursor, v.top)
		}
	}

	// A half page moves on too.
	press(a, keyRune('j'), keyRune('j'), keyRune('j'), keyRune('j'), keyRune('j'))

	for prev := v.cursor; v.cursor < len(v.rows)-1; prev = v.cursor {
		press(a, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

		if v.cursor <= prev || !shows() {
			t.Fatalf("ctrl+d from %d: cursor %d", prev, v.cursor)
		}
	}
}

func manyPads(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(" pad %d", i)
	}

	return out
}

func manyShort(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(" short %d", i)
	}

	return out
}

// wheelCell is a cell of the diff rows to scroll over.
func wheelCell(t *testing.T, a *App) (x, y int) {
	t.Helper()

	h, ok := a.rowsHost()
	if !ok {
		t.Fatal("no rows host")
	}

	return h.rect.Min.X + 5, h.rect.Min.Y + 3
}

func TestWrappedSearchOncePerLine(t *testing.T) {
	t.Parallel()

	long := "needle" + strings.Repeat("-", 64)
	a, v := wrapScene(t, padded(0, " "+long, " mid", " a needle here"), codeWidths{line: 30}, 30)

	typed := func(s string) {
		press(a, keyRune('/'))

		for _, r := range s {
			press(a, keyRune(r))
		}

		press(a, tea.KeyPressMsg{Code: tea.KeyEnter})
	}

	typed("needle")

	if r := v.rows[v.cursor]; r.line != 0 || r.part != 0 {
		t.Fatalf("first match: row %+v, want line 0 part 0", r)
	}

	typed("")

	if r := v.rows[v.cursor]; r.line != 2 || r.part != 0 {
		t.Fatalf("second match: row %+v, want line 2 part 0", r)
	}

	typed("")

	if r := v.rows[v.cursor]; r.line != 0 || r.part != 0 {
		t.Fatalf("wrapped match: row %+v, want line 0 part 0", r)
	}
}

func TestWrappedFreshRows(t *testing.T) {
	t.Parallel()

	prev := padded(0, " "+longLine, " mid")
	next := padded(0, " "+longLine, " mid", "+"+strings.Repeat("z", 70))
	rows := buildRows(next, rowOpts{wrap: codeWidths{line: 30}})
	fresh := freshRows(prev, next, rows)

	for i, r := range rows {
		want := r.kind == rowLine && r.line == 2

		if fresh[i] != want {
			t.Errorf("row %d (line %d part %d): fresh = %v, want %v", i, r.line, r.part, fresh[i], want)
		}
	}

	if len(fresh) != 3 {
		t.Errorf("%d fresh rows, want the 3 of the added line", len(fresh))
	}
}

func TestWrappedAnchorPart(t *testing.T) {
	t.Parallel()

	res := wrapResult()
	rows := buildRows(res, rowOpts{wrap: codeWidths{line: 30}})
	third := slices.IndexFunc(rows, func(r reviewRow) bool { return r.kind == rowLine && r.line == 0 && r.part == 2 })

	if third < 0 {
		t.Fatal("no third part")
	}

	anchor := anchorAt(res, rows, third)
	if anchor.part != 2 {
		t.Fatalf("anchor part = %d, want 2", anchor.part)
	}

	// A reload that keeps the line, with a line added before it, and a view
	// whose width wraps the line in two rows only.
	later := wrapResult()
	later.Files[0].Hunks[0].Lines = slices.Insert(later.Files[0].Hunks[0].Lines, 0, git.Line{Kind: git.Context, Old: 1, New: 1, Text: "first"})

	tests := []struct {
		name string
		wrap codeWidths
		part int
	}{
		{"same width", codeWidths{line: 30}, 2},
		{"narrower rows", codeWidths{line: 40}, 1},
		{"no wrap", codeWidths{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows2 := buildRows(later, rowOpts{wrap: tt.wrap})
			r := rows2[restoreAnchor(later, rows2, anchor)]

			if r.kind != rowLine || r.line != 1 || r.part != tt.part {
				t.Errorf("restored row %+v, want line 1 part %d", r, tt.part)
			}
		})
	}

	plain := anchorAt(res, rows, third)
	plain.part = 0

	if got := restoreAnchor(res, rows, plain); rows[got].part != 0 {
		t.Errorf("a part-0 anchor restored to part %d", rows[got].part)
	}
}

func TestWrappedViewKeepsSelectionPart(t *testing.T) {
	t.Parallel()

	_, v := wrapScene(t, wrapResult(), codeWidths{line: 30}, 30)
	third := slices.IndexFunc(v.rows, func(r reviewRow) bool { return r.kind == rowLine && r.line == 0 && r.part == 2 })

	v.cursor = third
	v.sel = &selection{anchor: point{row: third - 2}, head: point{row: third}}
	v.rebuild(false)

	if v.rows[v.cursor].part != 0 || v.cursor != third-2 {
		t.Errorf("cursor row %d part %d, want row %d part 0", v.cursor, v.rows[v.cursor].part, third-2)
	}

	if v.sel.head.row != third || v.sel.anchor.row != third-2 {
		t.Errorf("selection ends %d and %d, want %d and %d", v.sel.anchor.row, v.sel.head.row, third-2, third)
	}
}

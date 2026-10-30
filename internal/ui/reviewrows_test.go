package ui

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/mhersson/muxreveal/internal/git"
)

func ln(k git.LineKind, old, nw int, text string) git.Line {
	return git.Line{Kind: k, Old: old, New: nw, Text: text}
}

func hunkOf(oldStart, newStart int, lines ...git.Line) git.Hunk {
	h := git.Hunk{OldStart: oldStart, NewStart: newStart, Lines: lines}
	for _, l := range lines {
		if l.Kind != git.Add {
			h.OldLines++
		}

		if l.Kind != git.Del {
			h.NewLines++
		}
	}

	return h
}

func rowKinds(rows []reviewRow) []reviewRowKind {
	out := make([]reviewRowKind, len(rows))
	for i, r := range rows {
		out[i] = r.kind
	}

	return out
}

func TestBuildRowsUnified(t *testing.T) {
	t.Parallel()

	res := git.Result{Files: []git.FileDiff{
		{Path: "mod.go", Status: git.Modified, Hunks: []git.Hunk{
			hunkOf(1, 1, ln(git.Context, 1, 1, "a"), ln(git.Del, 2, 0, "b"), ln(git.Add, 0, 2, "c")),
			hunkOf(9, 9, ln(git.Add, 0, 9, "z")),
		}},
		{Path: "new.go", Status: git.Added, Hunks: []git.Hunk{hunkOf(0, 1, ln(git.Add, 0, 1, "x"))}},
		{Path: "gone.go", Status: git.Deleted, Hunks: []git.Hunk{hunkOf(1, 0, ln(git.Del, 1, 0, "y"))}},
		{Path: "to.go", OldPath: "from.go", Status: git.Renamed},
		{Path: "img.png", Status: git.Modified, Binary: true},
		{Path: "huge.go", Status: git.Modified, Truncated: true},
		{Path: "run.sh", Status: git.ModeChanged, OldMode: "100644", NewMode: "100755"},
	}}

	rows := buildRows(res, rowOpts{})

	type want struct {
		kind reviewRowKind
		file int
		text string
	}

	wants := []want{
		{rowFile, 0, ""},
		{rowHunk, 0, ""},
		{rowLine, 0, ""},
		{rowLine, 0, ""},
		{rowLine, 0, ""},
		{rowHunk, 0, ""},
		{rowLine, 0, ""},
		{rowFile, 1, ""},
		{rowHunk, 1, ""},
		{rowLine, 1, ""},
		{rowFile, 2, ""},
		{rowHunk, 2, ""},
		{rowLine, 2, ""},
		{rowFile, 3, ""},
		{rowFile, 4, ""},
		{rowInfo, 4, "binary file, not shown"},
		{rowFile, 5, ""},
		{rowInfo, 5, "too large, press f for the full file"},
		{rowFile, 6, ""},
		{rowInfo, 6, "mode 100644 → 100755"},
	}

	if len(rows) != len(wants) {
		t.Fatalf("got %d rows %v, want %d", len(rows), rowKinds(rows), len(wants))
	}

	for i, w := range wants {
		r := rows[i]
		if r.kind != w.kind || r.file != w.file || r.text != w.text {
			t.Errorf("row %d = {kind %d file %d %q}, want {kind %d file %d %q}", i, r.kind, r.file, r.text, w.kind, w.file, w.text)
		}
	}

	if r := rows[3]; r.hunk != 0 || r.line != 1 {
		t.Errorf("row 3 hunk %d line %d, want 0 and 1", r.hunk, r.line)
	}

	if r := rows[6]; r.hunk != 1 || r.line != 0 {
		t.Errorf("row 6 hunk %d line %d, want 1 and 0", r.hunk, r.line)
	}
}

func TestBuildRowsSplit(t *testing.T) {
	t.Parallel()

	res := git.Result{Files: []git.FileDiff{{Path: "a.go", Status: git.Modified, Hunks: []git.Hunk{
		hunkOf(1, 1,
			ln(git.Context, 1, 1, "a"),
			ln(git.Del, 2, 0, "b"),
			ln(git.Del, 3, 0, "c"),
			ln(git.Add, 0, 2, "B"),
			ln(git.Add, 0, 3, "d"),
			ln(git.Add, 0, 4, "e"),
		),
	}}}}

	rows := buildRows(res, rowOpts{split: true})
	// Left index first, falling back to the right index; other is the right.
	wantPairs := [][2]int{{0, 0}, {1, 3}, {2, 4}, {5, -1}}

	if len(rows) != 2+len(wantPairs) {
		t.Fatalf("got %d rows %v", len(rows), rowKinds(rows))
	}

	for i, w := range wantPairs {
		r := rows[2+i]
		if r.kind != rowPair || r.line != w[0] || r.other != w[1] {
			t.Errorf("pair %d = {kind %d line %d other %d}, want {line %d other %d}", i, r.kind, r.line, r.other, w[0], w[1])
		}
	}
}

func TestBuildRowsSplitAddOnly(t *testing.T) {
	t.Parallel()

	res := git.Result{Files: []git.FileDiff{{Path: "a.go", Status: git.Added, Hunks: []git.Hunk{
		hunkOf(0, 1, ln(git.Add, 0, 1, "x")),
	}}}}

	rows := buildRows(res, rowOpts{split: true})
	if r := rows[2]; r.kind != rowPair || r.line != 0 || r.other != -1 {
		t.Errorf("row = %+v, want an add-only pair on line 0 with no other", r)
	}
}

func TestBuildRowsFullFileGhosts(t *testing.T) {
	t.Parallel()

	res := git.Result{Files: []git.FileDiff{
		{Path: "a.go", Status: git.Modified, Hunks: []git.Hunk{hunkOf(1, 1, ln(git.Del, 2, 0, "b"))}},
		{Path: "img.png", Status: git.Modified, Binary: true},
	}}
	full := map[string]fullText{
		"a.go": {
			old: []string{"a", "b", "c", "d", "e"},
			new: []string{"a", "c", "X", "e", "f"},
		},
	}
	// old b removed (ghost), c kept, d removed + X added (ghost then added), e kept, f added.
	type want struct {
		kind        reviewRowKind
		line, other int
	}

	shown := []want{
		{rowFull, 0, 0},
		{rowGhost, 1, -1},
		{rowFull, 1, 2},
		{rowGhost, 3, -1},
		{rowFull, 2, -1},
		{rowFull, 3, 4},
		{rowFull, 4, -1},
	}

	check := func(t *testing.T, rows []reviewRow, wants []want) {
		t.Helper()

		if len(rows) != len(wants) {
			t.Fatalf("got %d rows %v, want %d", len(rows), rowKinds(rows), len(wants))
		}

		for i, w := range wants {
			r := rows[i]
			if r.kind != w.kind || r.line != w.line || r.other != w.other {
				t.Errorf("row %d = {kind %d line %d other %d}, want {kind %d line %d other %d}", i, r.kind, r.line, r.other, w.kind, w.line, w.other)
			}
		}
	}

	rows := buildRows(res, rowOpts{full: full, whole: allWhole(full)})
	if rows[0].kind != rowFile {
		t.Fatalf("row 0 = %d, want a file header", rows[0].kind)
	}

	check(t, rows[1:8], shown)

	if rows[2].block != 0 || rows[4].block != 1 {
		t.Errorf("ghost blocks = %d and %d, want 0 and 1", rows[2].block, rows[4].block)
	}

	if last := rows[len(rows)-2]; last.kind != rowFile || last.file != 1 {
		t.Errorf("the binary file's header is missing: %+v", last)
	}

	t.Run("a folded block is one row", func(t *testing.T) {
		rows := buildRows(res, rowOpts{full: full, whole: allWhole(full), folded: map[string]bool{foldKey("a.go", 1): true}})
		check(t, rows[1:7], []want{
			{rowFull, 0, 0},
			{rowGhost, 1, -1},
			{rowFull, 1, 2},
			{rowFold, 3, 1},
			{rowFull, 2, -1},
			{rowFull, 3, 4},
		})

		if rows[4].block != 1 {
			t.Errorf("fold block = %d, want 1", rows[4].block)
		}
	})

	t.Run("ghosts start folded", func(t *testing.T) {
		rows := buildRows(res, rowOpts{
			full:              full,
			whole:             allWhole(full),
			ghostsStartFolded: true,
			folded:            map[string]bool{foldKey("a.go", 0): false},
		})
		check(t, rows[1:7], []want{
			{rowFull, 0, 0},
			{rowGhost, 1, -1},
			{rowFull, 1, 2},
			{rowFold, 3, 1},
			{rowFull, 2, -1},
			{rowFull, 3, 4},
		})
	})

	t.Run("a failed read is an info row", func(t *testing.T) {
		rows := buildRows(res, rowOpts{full: map[string]fullText{"a.go": {err: fmt.Errorf("boom")}}, whole: map[string]bool{"a.go": true}})
		if rows[1].kind != rowInfo || rows[1].text != "boom" {
			t.Errorf("row 1 = %+v, want an info row holding the error", rows[1])
		}
	})

	t.Run("a binary file keeps its info row", func(t *testing.T) {
		rows := buildRows(res, rowOpts{full: map[string]fullText{"img.png": {}}, whole: map[string]bool{"img.png": true}})
		if got := rows[len(rows)-1]; got.kind != rowInfo || got.text != "binary file, not shown" {
			t.Errorf("last row = %+v", got)
		}
	})
}

func TestBuildRowsFullFileLongRewrite(t *testing.T) {
	t.Parallel()

	old := make([]string, 3000)
	nw := make([]string, 3000)

	for i := range old {
		old[i] = fmt.Sprintf("old %d", i)
		nw[i] = fmt.Sprintf("new %d", i)
	}

	res := git.Result{Files: []git.FileDiff{{Path: "a", Status: git.Modified}}}
	rows := buildRows(res, rowOpts{full: map[string]fullText{"a": {old: old, new: nw}}, whole: map[string]bool{"a": true}})

	if got, want := len(rows), 1+3000+3000; got != want {
		t.Fatalf("got %d rows, want %d", got, want)
	}
}

func fixtureRes() git.Result {
	return git.Result{Files: []git.FileDiff{
		{Path: "a.go", Status: git.Modified, Hunks: []git.Hunk{
			hunkOf(1, 1, ln(git.Context, 1, 1, "keep"), ln(git.Del, 2, 0, "gone"), ln(git.Add, 0, 2, "fresh")),
			hunkOf(20, 20, ln(git.Add, 0, 20, "tail")),
		}},
		{Path: "b.go", Status: git.Modified, Hunks: []git.Hunk{
			hunkOf(5, 5, ln(git.Add, 0, 5, "other")),
		}},
	}}
}

func TestRestoreAnchor(t *testing.T) {
	t.Parallel()

	res := fixtureRes()
	rows := buildRows(res, rowOpts{})
	// 0 file a, 1 hunk, 2 keep, 3 gone, 4 fresh, 5 hunk, 6 tail, 7 file b, 8 hunk, 9 other

	tests := []struct {
		name string
		at   int
		next func() git.Result
		want int
	}{
		{"same line kept", 4, fixtureRes, 4},
		{"hunk row kept", 5, fixtureRes, 5},
		{"file header kept", 7, fixtureRes, 7},
		{"a line inserted above moves the index but keeps the row", 6, func() git.Result {
			r := fixtureRes()
			r.Files[0].Hunks[0].Lines = append([]git.Line{ln(git.Add, 0, 1, "inserted")}, r.Files[0].Hunks[0].Lines...)
			r.Files[0].Hunks[0].NewLines++
			r.Files[0].Hunks[1].NewStart++

			return r
		}, 7},
		{"the line gone falls back to the overlapping hunk", 4, func() git.Result {
			r := fixtureRes()
			r.Files[0].Hunks[0].Lines[2].Text = "different"

			return r
		}, 1},
		{"the hunk gone falls back to the file header", 6, func() git.Result {
			r := fixtureRes()
			r.Files[0].Hunks = r.Files[0].Hunks[:1]

			return r
		}, 0},
		{"the file gone gives the nearest index", 9, func() git.Result {
			r := fixtureRes()
			r.Files = r.Files[:1]

			return r
		}, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := anchorAt(res, rows, tt.at)
			next := tt.next()
			nrows := buildRows(next, rowOpts{})

			if got := restoreAnchor(next, nrows, a); got != tt.want {
				t.Errorf("restoreAnchor = %d, want %d (rows %v)", got, tt.want, rowKinds(nrows))
			}
		})
	}
}

func TestAnchorAt(t *testing.T) {
	t.Parallel()

	res := fixtureRes()
	rows := buildRows(res, rowOpts{})

	a := anchorAt(res, rows, 4)
	want := rowAnchor{path: "a.go", hunkNewStart: 1, hunkNewLines: 2, kind: git.Add, text: "fresh", row: rowLine, index: 4}

	if a != want {
		t.Errorf("anchorAt = %+v, want %+v", a, want)
	}

	if a := anchorAt(res, rows, 0); a.path != "a.go" || a.hunkNewLines != 0 || a.text != "" {
		t.Errorf("file anchor = %+v", a)
	}
}

func TestSearchRowsWraps(t *testing.T) {
	t.Parallel()

	res := fixtureRes()
	res.Files[1].Hunks[0].Section = "func Needle()"
	rows := buildRows(res, rowOpts{})
	// 0 file a, 1 hunk, 2 keep, 3 gone, 4 fresh, 5 hunk, 6 tail, 7 file b, 8 hunk, 9 other

	tests := []struct {
		name string
		from int
		q    string
		want int
		ok   bool
	}{
		{"next row after the cursor", 0, "KEEP", 2, true},
		{"skips the cursor row", 2, "keep", 2, true},
		{"wraps past the end", 8, "fresh", 4, true},
		{"matches a path on its header", 3, "b.go", 7, true},
		{"matches a hunk section", 0, "needle", 8, true},
		{"a miss", 0, "nothing here", 0, false},
		{"an empty query", 0, "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := searchRows(res, rows, tt.from, tt.q)
			if ok != tt.ok || (ok && got != tt.want) {
				t.Errorf("searchRows = %d, %v; want %d, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSearchRowsInfoAndPairs(t *testing.T) {
	t.Parallel()

	res := git.Result{Files: []git.FileDiff{
		{Path: "x.bin", Binary: true},
		{Path: "p.go", Hunks: []git.Hunk{hunkOf(1, 1, ln(git.Del, 1, 0, "left words"), ln(git.Add, 0, 1, "right words"))}},
	}}
	rows := buildRows(res, rowOpts{split: true})

	if got, ok := searchRows(res, rows, 0, "not shown"); !ok || rows[got].kind != rowInfo {
		t.Errorf("info search = %d, %v", got, ok)
	}

	if got, ok := searchRows(res, rows, 0, "right"); !ok || rows[got].kind != rowPair {
		t.Errorf("pair search = %d, %v", got, ok)
	}
}

func TestFreshRows(t *testing.T) {
	t.Parallel()

	prev := fixtureRes()
	next := fixtureRes()
	next.Files[0].Hunks[0].Lines = append(next.Files[0].Hunks[0].Lines, ln(git.Add, 0, 3, "brand new"))
	next.Files[1].Hunks[0].Lines[0].Text = "changed"
	next.Files = append(next.Files, git.FileDiff{Path: "c.go", Hunks: []git.Hunk{hunkOf(1, 1, ln(git.Add, 0, 1, "whole file"))}})

	rows := buildRows(next, rowOpts{})
	got := freshRows(prev, next, rows)

	var texts []string

	for i := range rows {
		if got[i] {
			if rows[i].kind != rowLine {
				t.Errorf("row %d is fresh but is kind %d", i, rows[i].kind)
			}

			texts = append(texts, next.Files[rows[i].file].Hunks[rows[i].hunk].Lines[rows[i].line].Text)
		}
	}

	want := []string{"brand new", "changed", "whole file"}
	if !slices.Equal(texts, want) {
		t.Errorf("fresh lines = %q, want %q", texts, want)
	}

	if f := freshRows(next, next, rows); len(f) != 0 {
		t.Errorf("an unchanged result has fresh rows: %v", f)
	}

	t.Run("a pair is fresh when either side is", func(t *testing.T) {
		prows := buildRows(next, rowOpts{split: true})
		fresh := freshRows(prev, next, prows)

		var n int

		for i, r := range prows {
			if fresh[i] && r.kind != rowPair {
				t.Errorf("row %d fresh but kind %d", i, r.kind)
			}

			if fresh[i] {
				n++
			}
		}

		if n != 3 {
			t.Errorf("fresh pairs = %d, want 3", n)
		}
	})
}

func fullFixture() (git.Result, map[string]fullText) {
	res := git.Result{Files: []git.FileDiff{{Path: "a.go", Status: git.Modified}}}

	return res, map[string]fullText{"a.go": {
		old: []string{"a", "b", "c"},
		new: []string{"a", "c", "Added"},
	}}
}

func TestRestoreAnchorFullFile(t *testing.T) {
	t.Parallel()

	res, full := fullFixture()
	rows := buildRows(res, rowOpts{full: full, whole: allWhole(full)})
	// 0 file, 1 a, 2 ghost b, 3 c, 4 Added

	tests := []struct {
		name string
		at   int
		next func(map[string]fullText) map[string]fullText
		want int
	}{
		{"a line inserted above keeps the row", 3, func(f map[string]fullText) map[string]fullText {
			f["a.go"] = fullText{old: f["a.go"].old, new: append([]string{"top"}, f["a.go"].new...)}

			return f
		}, 4},
		{"a ghost line keeps its row", 2, func(f map[string]fullText) map[string]fullText { return f }, 2},
		{"an added line keeps its row", 4, func(f map[string]fullText) map[string]fullText { return f }, 4},
		{"the line gone falls back to the file header", 4, func(f map[string]fullText) map[string]fullText {
			f["a.go"] = fullText{old: f["a.go"].old, new: []string{"a", "c"}}

			return f
		}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := anchorAt(res, rows, tt.at)
			nrows := buildRows(res, rowOpts{full: tt.next(maps.Clone(full)), whole: map[string]bool{"a.go": true}})

			if got := restoreAnchor(res, nrows, a); got != tt.want {
				t.Errorf("restoreAnchor = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSearchRowsFullFile(t *testing.T) {
	t.Parallel()

	res, full := fullFixture()
	rows := buildRows(res, rowOpts{full: full, whole: allWhole(full)})

	if got, ok := searchRows(res, rows, 0, "ADDED"); !ok || got != 4 {
		t.Errorf("full row search = %d, %v; want 4", got, ok)
	}

	if got, ok := searchRows(res, rows, 3, "b"); !ok || got != 2 {
		t.Errorf("ghost row search = %d, %v; want 2", got, ok)
	}
}

func TestRestoreAnchorRepeatedLine(t *testing.T) {
	t.Parallel()

	mk := func() git.Result {
		return git.Result{Files: []git.FileDiff{{Path: "a.go", Status: git.Modified, Hunks: []git.Hunk{
			hunkOf(1, 1, ln(git.Add, 0, 1, "}"), ln(git.Add, 0, 2, "x"), ln(git.Add, 0, 3, "}"), ln(git.Add, 0, 4, "}")),
		}}}}
	}
	res := mk()
	rows := buildRows(res, rowOpts{})
	// 0 file, 1 hunk, 2 "}", 3 x, 4 "}", 5 "}"
	for _, at := range []int{2, 4, 5} {
		a := anchorAt(res, rows, at)
		if got := restoreAnchor(res, rows, a); got != at {
			t.Errorf("cursor on row %d restored to %d", at, got)
		}
	}
}

func TestRestoreAnchorFold(t *testing.T) {
	t.Parallel()

	res := git.Result{Files: []git.FileDiff{{Path: "a.go", Status: git.Modified}}}
	full := map[string]fullText{"a.go": {
		old: []string{"a", "b", "c", "d", "e"},
		new: []string{"a", "c", "e"},
	}}
	folded := map[string]bool{foldKey("a.go", 0): true, foldKey("a.go", 1): true}
	rows := buildRows(res, rowOpts{full: full, whole: allWhole(full), folded: folded})
	// 0 file, 1 a, 2 fold(b), 3 c, 4 fold(d), 5 e
	a := anchorAt(res, rows, 4)
	if a.row != rowFold {
		t.Fatalf("anchor row = %d", a.row)
	}

	moved := map[string]fullText{"a.go": {old: full["a.go"].old, new: []string{"top", "a", "c", "e"}}}
	nrows := buildRows(res, rowOpts{full: moved, whole: allWhole(moved), folded: folded})

	if got := restoreAnchor(res, nrows, a); got != 5 {
		t.Errorf("restoreAnchor = %d, want 5", got)
	}
}

func TestHunkHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		h    git.Hunk
		want string
	}{
		{"both ranges", git.Hunk{OldStart: 43, OldLines: 9, NewStart: 43, NewLines: 9}, "@@ -43,9 +43,9 @@"},
		{"single lines omit the count", git.Hunk{OldStart: 1, OldLines: 1, NewStart: 7, NewLines: 1}, "@@ -1 +7 @@"},
		{"empty ranges", git.Hunk{OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 3}, "@@ -0,0 +1,3 @@"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hunkHeader(tt.h); got != tt.want {
				t.Errorf("hunkHeader = %q, want %q", got, tt.want)
			}
		})
	}
}

// allWhole says every file of full is shown whole.
func allWhole(full map[string]fullText) map[string]bool {
	out := make(map[string]bool, len(full))
	for path := range full {
		out[path] = true
	}

	return out
}

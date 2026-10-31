//go:build bench

package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/git"
)

// dragRows is how many rows each drag grows its selection to. Every motion and
// edge-scroll tick reads the whole selection's text again, so the last ones
// cost the most.
const dragRows = 3000

// A drag's motions and ticks are drawer frames, under the paging gates.
const (
	dragGateP99 = scrollGateP99
	dragGateMax = scrollGateMax
)

// dragJitter is how many edge-scroll ticks pass between two motions of the
// pointer held past the bottom: it moves a column, as a held hand drifts.
const dragJitter = 4

// dragMDPath is the rendered case's markdown file.
const dragMDPath = "docs/big.md"

// dragCase is a diff the drawer shows and how it is turned into the view the
// drag selects in.
type dragCase struct {
	name  string
	res   git.Result
	setup func(t *testing.T, app *App, v *reviewView)
}

// dragMarkdown is the source of a long markdown file: sections of a heading, a
// paragraph that wraps, a list and a code block.
func dragMarkdown(sections int) []string {
	var lines []string

	for s := range sections {
		lines = append(lines,
			fmt.Sprintf("## Step %d", s),
			"",
			fmt.Sprintf("Step %d reads the request's id, loads the item from the store, checks that the caller may see it and "+
				"writes it back as JSON. Each stage logs the request's id, so a slow request can be followed through "+
				"the handler, the store and the encoder.", s),
			"",
			fmt.Sprintf("- the id comes from the route, `mux.Vars(r)[\"id\"]` (step %d)", s),
			"- the store call takes the request's **context**, so a closed connection ends it",
			"- a missing item is a `404`, any other error a `500`",
			"",
			"```go",
			fmt.Sprintf("func (s *Server) step%d(ctx context.Context, id string) (Item, error) {", s),
			"\titem, err := s.store.Get(ctx, id)",
			"\tif err != nil {",
			"\t\treturn Item{}, fmt.Errorf(\"get %s: %w\", id, err)",
			"\t}",
			"",
			"\treturn item, nil",
			"}",
			"```",
			"",
		)
	}

	return lines
}

// dragMDResult is one added markdown file of src's lines.
func dragMDResult(src []string) git.Result {
	specs := make([]string, len(src))
	for i, l := range src {
		specs[i] = "+" + l
	}

	f := fileSpec(dragMDPath, git.Added, hunkSpec(0, 1, "", specs...))
	f.NewOID = "6b1f0ad3f2c5f1d1b2a4c0e9d8f7a6b5c4d3e2f1"

	return git.Result{Old: "base", New: "tree", Live: true, Files: []git.FileDiff{f}}
}

// renderDragMD turns the markdown file into rendered rows: m asks for them, and
// the parse's answer, which the app's engine would read and parse off the
// update loop, is delivered here.
func renderDragMD(src []string) func(*testing.T, *App, *reviewView) {
	return func(t *testing.T, app *App, v *reviewView) {
		t.Helper()

		app.Update(keyRune('m'))

		if !v.mdOn[dragMDPath] {
			t.Fatal("m did not turn the file to rendered")
		}

		f := v.res.Files[0]
		ids := blobIDs{f.OldOID, f.NewOID}
		text := []byte(strings.Join(src, "\n") + "\n")
		doc := parseMarkdown(text, filepath.Join(v.root, filepath.Dir(dragMDPath)), app.drawer.hl.HighlightLang).withFoldKey(app.foldKey())

		app.Update(reviewMDMsg{
			tab: app.store.State().Tab().ID, gen: v.epoch, path: dragMDPath, ids: ids,
			file: &mdFile{doc: doc, text: fullText{new: src}, ids: ids},
		})

		if v.mdFiles[dragMDPath] == nil || len(v.rows) < 2 || v.rows[1].kind != rowMD {
			t.Fatal("the file is not shown rendered")
		}
	}
}

// splitDrag shows the diff side by side.
func splitDrag(t *testing.T, app *App, v *reviewView) {
	t.Helper()

	app.Update(keyRune('u'))

	lay, ok := app.rowsLayout(app.store.State().Tab().ID, v)
	if !v.rowsSplit || !ok || !lay.split {
		t.Fatal("the diff is not side by side")
	}
}

// TestBenchDrawerDrag times a left-button drag in the drawer that grows its
// selection to dragRows rows: motions down the view, then a pointer held past
// the bottom, whose edge-scroll ticks scroll a row each while the pointer moves
// a column every dragJitter ticks. Each motion and tick is timed through
// update, view and present, for a unified diff, the same side by side and a
// rendered markdown file. Run it with:
//
//	CGO_ENABLED=1 go test -tags bench ./internal/ui -run TestBenchDrawerDrag -v
func TestBenchDrawerDrag(t *testing.T) {
	src := dragMarkdown(200)

	for _, c := range []dragCase{
		{name: "unified", res: benchScrollResult()},
		{name: "side by side", res: benchScrollResult(), setup: splitDrag},
		{name: "rendered markdown", res: dragMDResult(src), setup: renderDragMD(src)},
	} {
		t.Run(c.name, func(t *testing.T) { benchDrag(t, c) })
	}
}

// selectedRows is how many rows the drawer's selection spans, 0 for none.
func selectedRows(v *reviewView) int {
	if v.sel == nil {
		return 0
	}

	return max(v.sel.head.row-v.sel.anchor.row, v.sel.anchor.row-v.sel.head.row) + 1
}

func benchDrag(t *testing.T, c dragCase) {
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}

	app, _, err := newBenchApp(benchConfig{Panes: 1, Width: benchWidth, Height: benchHeight, Review: true}, clk)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = app.Close(context.Background()) }()

	// The long diff is a new source: it replaces the first result, at the top.
	app.Update(benchReviewMsg(app, c.res, false))

	v := app.drawer.views[app.store.State().Tab().ID]
	if c.setup != nil {
		c.setup(t, app, v)
	}

	h, ok := app.rowsHost()
	if !ok {
		t.Fatal("the diff rows are not a text host")
	}

	if len(v.rows) < dragRows+h.rect.Dy() {
		t.Fatalf("the view holds %d rows; want at least %d, so the drag can grow to %d", len(v.rows), dragRows+h.rect.Dy(), dragRows)
	}

	hl := newHLRunner(app)
	hl.settle()

	pr := newPresenter(benchWidth, benchHeight)

	// The press lands on the second text row, clear of a pinned header, and a
	// quarter in from the right: on the new side when side by side.
	x, y, outY := h.rect.Max.X-h.rect.Dx()/4, h.rect.Min.Y+1, h.rect.Max.Y

	app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	hl.start()

	if err := pr.present(app.View().Content); err != nil {
		t.Fatalf("present: %v", err)
	}

	runtime.GC()

	var motions, ticks, update, draw frameTimes

	step := func(into *frameTimes, msg tea.Msg) tea.Cmd {
		start := time.Now()

		hl.deliver()

		_, cmd := app.Update(msg)

		hl.start()

		updateD := time.Since(start)

		err := pr.present(app.View().Content)
		frameD := time.Since(start)

		into.add(frameD)
		update.add(updateD)
		draw.add(frameD - updateD)

		if err != nil {
			t.Fatalf("present: %v", err)
		}

		return cmd
	}
	motion := func(px, py int) tea.Cmd {
		clk.Advance(time.Second / 60)

		return step(&motions, tea.MouseMotionMsg{X: px, Y: py, Button: tea.MouseLeft})
	}

	for row := y + 1; row < outY; row++ {
		motion(x, row)
	}

	if motion(x, outY) == nil || app.mouse.text == nil {
		t.Fatal("a drag held past the text started no edge scroll")
	}

	seq := app.mouse.text.seq

	for i := 1; selectedRows(v) < dragRows; i++ {
		clk.Advance(dragScrollEvery)

		if step(&ticks, dragScrollMsg{seq: seq}) == nil {
			t.Fatalf("the edge scroll stopped with %d rows selected", selectedRows(v))
		}

		if i%dragJitter == 0 {
			motion(x-(i/dragJitter)%2, outY)
		}
	}

	if app.text == nil || app.text.rows != v || app.text.shown == "" {
		t.Fatal("the drag left no selection of the diff rows")
	}

	if h.lay.split && app.text.side != 1 {
		t.Fatalf("the selection is on side %d; want the new side, 1", app.text.side)
	}

	hl.settle()

	rows, size := selectedRows(v), len(app.text.shown)

	start := time.Now()

	app.Update(tea.MouseReleaseMsg{X: x, Y: outY, Button: tea.MouseLeft})

	releaseD := time.Since(start)
	m, k, u, d := motions.summary(), ticks.summary(), update.summary(), draw.summary()

	t.Logf("drag to %d rows (%d bytes selected), each motion or tick timed through update, view and present", rows, size)
	t.Logf("motions (%d): p50 %s, p99 %s (gate %s), max %s (gate %s)",
		m.Count, fmtMS(m.P50), fmtMS(m.P99), fmtMS(dragGateP99), fmtMS(m.Max), fmtMS(dragGateMax))
	t.Logf("ticks (%d): p50 %s, p99 %s (gate %s), max %s (gate %s)",
		k.Count, fmtMS(k.P50), fmtMS(k.P99), fmtMS(dragGateP99), fmtMS(k.Max), fmtMS(dragGateMax))
	t.Logf("per-component p50/p99 over both (they do not sum to the frame's): update %s/%s, view+present %s/%s; release update %s",
		fmtMS(u.P50), fmtMS(u.P99), fmtMS(d.P50), fmtMS(d.P99), fmtMS(releaseD))

	if app.drawer.fallbacks != 0 {
		t.Errorf("the compositor drew the drawer in %d frames, so the run did not measure the splice", app.drawer.fallbacks)
	}

	for _, g := range []struct {
		name string
		s    summary
	}{{"motion", m}, {"tick", k}} {
		if g.s.P99 > dragGateP99 {
			t.Errorf("%s p99 = %v, gate is %v", g.name, g.s.P99, dragGateP99)
		}

		if g.s.Max > dragGateMax {
			t.Errorf("%s max = %v, gate is %v", g.name, g.s.Max, dragGateMax)
		}
	}
}

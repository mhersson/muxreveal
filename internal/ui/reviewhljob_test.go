package ui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
)

// applyLive hands the view res as a live result.
func applyLive(a *App, v *reviewView, res git.Result) {
	a.reviewApply(v, reviewLoadedMsg{tab: a.store.State().Tab().ID, res: res, live: true, info: reviewInfo{project: "muxreveal", branch: "feat/review-pane", base: "origin/main", turns: 4, hidden: hiddenFlags(res.Files)}})
}

// Tokenising happens in a command: the drawer draws plain until its answer.
// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestHighlightRunsOffTheLoop(t *testing.T) {
	a := cacheScene().build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)
	v.gen++
	v.rebuild(v.split)

	r := drawerRectOf(t, a, 0)
	lines := func() string { return strings.Join(a.drawerLines(a.store.State(), r), "\n") }
	plain := lines()

	cmd := a.drawerAfterUpdate()
	if cmd == nil || a.drawer.hlJob == nil {
		t.Fatalf("no batch was started: cmd %v, job %v", cmd != nil, a.drawer.hlJob != nil)
	}

	if lines() != plain {
		t.Error("the drawer changed before the batch answered")
	}

	scans := a.drawer.scans

	if again := a.drawerAfterUpdate(); again != nil || a.drawer.scans != scans {
		t.Errorf("a second settle with a batch running returned a command (%v) or scanned %d times", again != nil, a.drawer.scans-scans)
	}

	run(a, cmd)

	if a.drawer.hlJob != nil {
		t.Error("the batch is still running after its answer")
	}

	coloured := lines()
	if coloured == plain {
		t.Error("the answer did not colour the drawer")
	}

	v.cache, v.carry = reviewCache{}, nil

	settleDrawer(t, a)

	if lines() != coloured {
		t.Error("the answer coloured the drawer differently from a cold settle")
	}
}

func TestHighlightAnswerFollowsItsView(t *testing.T) {
	t.Parallel()

	noLexer := func() reviewScene {
		s := cacheScene()
		lines := []string{" \tone", "-\ttwo", "+\tthree", " \tfour"}
		s.res = git.Result{Old: "base", New: "tree", Live: true, Files: []git.FileDiff{fileSpec("notes.unknownext", git.Modified, hunkSpec(1, 1, "", lines...))}}
		s.turn, s.turns = 1, 1

		return s
	}

	tests := []struct {
		name   string
		scene  func() reviewScene
		change func(t *testing.T, a *App, v *reviewView)
		check  func(t *testing.T, a *App, v *reviewView)
	}{
		{"same text", cacheScene, func(t *testing.T, a *App, v *reviewView) {
			applyLive(a, v, turn4Result())
		}, func(t *testing.T, a *App, v *reviewView) {
			if got := settleCounting(t, a); got != 0 {
				t.Errorf("the same text was tokenised again: %d lines", got)
			}

			if len(spanTexts(v)) == 0 {
				t.Error("no chunk in view holds spans")
			}
		}},
		{"changed text", cacheScene, func(t *testing.T, a *App, v *reviewView) {
			applyLive(a, v, edited())
		}, func(t *testing.T, a *App, v *reviewView) {
			settleDrawer(t, a)

			spans := v.derived().spans
			if len(spans) == 0 {
				t.Fatal("no chunk holds spans")
			}

			for k, got := range spans {
				path, lines := v.chunkLines(k)
				if want := a.drawer.hl.Highlight(path, lines); !reflect.DeepEqual(got, want) {
					t.Errorf("chunk %v holds spans of other text", k)
				}
			}
		}},
		{"drawer closed", cacheScene, func(t *testing.T, a *App, v *reviewView) {
			openIn(t, a, core.CloseReview{})
			a.Update(struct{}{})
		}, func(t *testing.T, a *App, v *reviewView) {}},
		{"no lexer", noLexer, func(t *testing.T, a *App, v *reviewView) {}, func(t *testing.T, a *App, v *reviewView) {
			var k spanKey

			for i := range v.rows {
				if refs := v.rowSpans(i); len(refs) > 0 {
					k = refs[0].key

					break
				}
			}

			got, ok := v.derived().spans[k]
			if !ok {
				t.Fatal("the chunk holds no entry, so it would be asked again")
			}

			path, lines := v.chunkLines(k)
			if want := a.drawer.hl.Highlight(path, lines); !reflect.DeepEqual(got, want) {
				t.Errorf("spans %v, want %v", got, want)
			}

			if cmd := a.drawerAfterUpdate(); cmd != nil || a.drawer.hlJob != nil || a.drawer.tokenised != 0 {
				t.Errorf("the first settle after the answer started work: cmd %v, job %v, tokenised %d", cmd != nil, a.drawer.hlJob != nil, a.drawer.tokenised)
			}

			scans := a.drawer.scans

			if cmd := a.drawerAfterUpdate(); cmd != nil || a.drawer.hlJob != nil {
				t.Error("a second settle started work")
			}

			if a.drawer.scans != scans {
				t.Errorf("a settled drawer scanned %d more times", a.drawer.scans-scans)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := tt.scene().build(t)
			v := a.reviewViewOf(a.store.State().Tab().ID)
			v.gen++
			v.rebuild(v.split)

			if a.drawerAfterUpdate() == nil || a.drawer.hlJob == nil {
				t.Fatal("no batch was started")
			}

			j := a.drawer.hlJob
			tt.change(t, a, v)

			msg, ok := j.run(context.Background(), &a.drawer.hl).(drawerSpansMsg)
			if !ok {
				t.Fatal("the batch gave no answer")
			}

			a.onDrawerSpans(msg)

			if a.drawer.hlJob != nil {
				t.Error("the answer left a batch running")
			}

			tt.check(t, a, v)
		})
	}
}

func TestHighlightStopsWithTheApp(t *testing.T) {
	t.Parallel()

	a := cacheScene().build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)
	v.gen++
	v.rebuild(v.split)
	a.drawerAfterUpdate()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if msg := a.drawer.hlJob.run(ctx, &a.drawer.hl); msg != nil {
		t.Errorf("a cancelled batch answered %T", msg)
	}
}

// An answer that arrives while the view changes under it must leave every
// visible chunk with spans of its own text.
func TestHighlightJobRace(t *testing.T) {
	t.Parallel()

	a := cacheScene().build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)
	v.gen++
	v.rebuild(v.split)

	if a.drawerAfterUpdate() == nil {
		t.Fatal("no batch was started")
	}

	j, hl := a.drawer.hlJob, &a.drawer.hl
	done := make(chan tea.Msg, 1)

	go func() { done <- j.run(context.Background(), hl) }()

	applyLive(a, v, edited())
	v.cursor += 3
	v.rebuild(!v.split)
	a.drawerLines(a.store.State(), drawerRectOf(t, a, 0))

	msg, ok := (<-done).(drawerSpansMsg)
	if !ok {
		t.Fatal("the batch gave no answer")
	}

	a.onDrawerSpans(msg)
	settleDrawer(t, a)

	spans := v.derived().spans
	for i := range v.rows {
		for _, ref := range v.rowSpans(i) {
			if _, ok := spans[ref.key]; !ok && i < 10 {
				t.Errorf("row %d: chunk %v has no spans", i, ref.key)
			}
		}
	}
}

func TestHighlightWindowOrder(t *testing.T) {
	t.Parallel()

	span := func(from, to int) []int {
		var out []int

		step := 1
		if to < from {
			step = -1
		}

		for i := from; i != to+step; i += step {
			out = append(out, i)
		}

		return out
	}

	tests := []struct {
		name         string
		top, h, n    int
		up           bool
		visible, rst []int
	}{
		{"down", 100, 10, 1000, false, span(100, 109), append(span(110, 129), span(99, 90)...)},
		{"up", 100, 10, 1000, true, span(100, 109), append(span(99, 80), span(110, 119)...)},
		{"at the top", 0, 10, 25, false, span(0, 9), span(10, 24)},
		{"at the end", 20, 10, 25, false, span(20, 24), span(19, 10)},
		{"short rows", 0, 50, 20, false, span(0, 19), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			visible, rest := hlWindow(tt.top, tt.h, tt.n, tt.up)
			if !reflect.DeepEqual(visible, tt.visible) || !reflect.DeepEqual(rest, tt.rst) {
				t.Errorf("hlWindow(%d, %d, %d, %v) = %v, %v; want %v, %v", tt.top, tt.h, tt.n, tt.up, visible, rest, tt.visible, tt.rst)
			}
		})
	}
}

func TestHighlightVisibleFirst(t *testing.T) {
	t.Parallel()

	res, _ := bigResult(5000)
	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: res, turn: 1, turns: 1, focused: true}
	a := s.build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)
	h := a.reviewBodyH(v)

	keysOf := func(from, to int) map[spanKey]bool {
		out := map[spanKey]bool{}

		for i := max(from, 0); i < min(to, len(v.rows)); i++ {
			for _, r := range v.rowSpans(i) {
				out[r.key] = true
			}
		}

		return out
	}
	inJob := func(j *hlJob) map[spanKey]bool {
		out := map[spanKey]bool{}

		for _, tx := range j.texts {
			for _, k := range tx.keys {
				out[k] = true
			}
		}

		return out
	}

	// Two screens ahead of the view reach into the next chunk of the file.
	at := func(row int) {
		v.top, v.cursor = row, row
		v.cache, v.carry = reviewCache{}, nil
	}

	at(5*hlChunk - 2*h)

	top := v.viewTop(h)

	a.drawerAfterUpdate()

	first := a.drawer.hlJob
	if first == nil {
		t.Fatal("no batch was started")
	}

	vis := keysOf(top, top+h)
	firstKeys := inJob(first)

	for k := range firstKeys {
		if !vis[k] {
			t.Errorf("the first batch holds %v, which no visible row reads", k)
		}
	}

	runHighlight(t, a)
	a.drawerAfterUpdate()

	next := a.drawer.hlJob
	if next == nil {
		t.Fatal("no second batch was started")
	}

	// Chunks of the screens ahead that the first batch lacked come before others.
	ahead := keysOf(top+h, top+3*h)
	seenOther := false

	for _, tx := range next.texts {
		for _, k := range tx.keys {
			if firstKeys[k] || vis[k] && !ahead[k] {
				continue
			}

			if ahead[k] && seenOther {
				t.Errorf("chunk %v of the screens ahead comes after another outside them", k)
			}

			if !ahead[k] {
				seenOther = true
			}
		}
	}

	settleDrawer(t, a)

	inside := keysOf(top-h, top+3*h)
	for k := range inside {
		if _, ok := v.derived().spans[k]; !ok {
			t.Errorf("chunk %v inside the window holds no spans", k)
		}
	}

	for k := range v.derived().spans {
		if !inside[k] {
			t.Errorf("chunk %v outside the window holds spans", k)
		}
	}

	// Moving up four screens makes up the direction: the screens above come first.
	// The screens above reach into the chunk before the one in view.
	at(4*hlChunk + 3*h/2)

	newTop := v.viewTop(h)

	a.drawerAfterUpdate()
	runHighlight(t, a)
	a.drawerAfterUpdate()

	up := a.drawer.hlJob
	if up == nil {
		t.Fatal("no batch after moving up")
	}

	want := keysOf(newTop-2*h, newTop)
	seenOther = false

	for _, tx := range up.texts {
		for _, k := range tx.keys {
			if want[k] {
				if seenOther {
					t.Errorf("chunk %v above the view comes after another outside it", k)
				}
			} else {
				seenOther = true
			}
		}
	}

	if len(up.texts) == 0 || !want[up.texts[0].keys[0]] {
		t.Errorf("the batch after moving up does not start with the screens above")
	}
}

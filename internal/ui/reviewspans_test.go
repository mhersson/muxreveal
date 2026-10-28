package ui

import (
	"strings"
	"testing"

	"github.com/mhersson/muxreveal/internal/git"
)

// spanTexts is the path and text of every chunk the view holds spans for.
func spanTexts(v *reviewView) map[string]int {
	out := map[string]int{}

	for k := range v.derived().spans {
		path, lines := v.chunkLines(k)
		out[path+"\x00"+strings.Join(lines, "\n")] = len(lines)
	}

	return out
}

// settleCounting runs the drawer's update work until it is done and returns the
// lines it tokenised.
func settleCounting(t *testing.T, a *App) int {
	t.Helper()

	total := 0

	for range 200 {
		cmd := a.drawerAfterUpdate()
		total += a.drawer.tokenised

		if cmd == nil {
			return total
		}
	}

	t.Fatal("the drawer never settled")

	return total
}

// A live result tokenises only the chunks whose text changed; the rest keep the
// spans they had, and what is drawn is what a cold start draws.
func TestLiveResultKeepsSpansOfUnchangedChunks(t *testing.T) {
	a := cacheScene().build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)

	had := spanTexts(v)
	if len(had) < 4 {
		t.Fatalf("the scene holds spans for %d chunks, want several", len(had))
	}

	res := edited()
	a.reviewApply(v, reviewLoadedMsg{tab: a.store.State().Tab().ID, res: res, live: true, info: reviewInfo{project: "muxreveal", branch: "feat/review-pane", base: "origin/main", turns: 4, hidden: hiddenFlags(res.Files)}})

	got := settleCounting(t, a)

	want, held := 0, 0

	for text, n := range spanTexts(v) {
		held += n

		if _, same := had[text]; !same {
			want += n
		}
	}

	if want == 0 || want >= held {
		t.Fatalf("the result changes %d of %d lines in view, want some but not all", want, held)
	}

	if got != want {
		t.Errorf("the live result tokenised %d lines, want the %d of the chunks that changed", got, want)
	}

	r := drawerRectOf(t, a, 0)
	carried := strings.Join(a.drawerLines(a.store.State(), r), "\n")

	v.cache, v.carry = reviewCache{}, nil

	settleDrawer(t, a)

	if cold := strings.Join(a.drawerLines(a.store.State(), r), "\n"); carried != cold {
		t.Error("the spans a live result kept draw differently from spans tokenised afresh")
	}
}

// A new source starts over: nothing is carried to it.
func TestNewSourceTokenisesAfresh(t *testing.T) {
	a := cacheScene().build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)

	held := 0
	for _, n := range spanTexts(v) {
		held += n
	}

	res := turn4Result()
	a.reviewApply(v, reviewLoadedMsg{tab: a.store.State().Tab().ID, res: res, src: git.BranchSource{Worktree: "/w/p1"}, info: reviewInfo{project: "muxreveal", branch: "feat/review-pane", base: "origin/main", hidden: hiddenFlags(res.Files)}})

	if got := settleCounting(t, a); got != held {
		t.Errorf("a new source tokenised %d lines, want all %d in view", got, held)
	}
}

// Paging through a long diff tokenises each chunk once, paging back tokenises
// nothing, and a step that brings no new chunk into range tokenises nothing.
func TestScrollTokenisesEachChunkOnce(t *testing.T) {
	res, _ := bigResult(5000)
	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: res, turn: 1, turns: 1, focused: true}
	a := s.build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)

	total := 0

	for i := 0; i < len(v.rows); i += 20 {
		v.cursor = i
		total += settleCounting(t, a)
	}

	held := 0
	for _, n := range spanTexts(v) {
		held += n
	}

	if total > held {
		t.Errorf("paging down tokenised %d lines for %d lines of chunks: some were tokenised twice", total, held)
	}

	for i := len(v.rows) - 1; i >= 0; i -= 20 {
		v.cursor = i
		if got := settleCounting(t, a); got != 0 {
			t.Fatalf("paging back to row %d tokenised %d lines, want 0", i, got)
		}
	}

	scans := a.drawer.scans
	v.cursor++

	if got := settleCounting(t, a); got != 0 || a.drawer.scans != scans {
		t.Errorf("a step inside the view tokenised %d lines and searched %d times", got, a.drawer.scans-scans)
	}
}

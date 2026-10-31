package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/github"
)

// convFor is the conversation fixture as pull request number n's, its times
// moved to the app clock's.
func convFor(n int) github.Conversation {
	c := convFixture()
	c.PR.Number = n

	d := t0.Sub(convNow)
	c.Created = c.Created.Add(d)

	for i := range c.Items {
		c.Items[i].At = c.Items[i].At.Add(d)
	}

	return c
}

// convApp is the PR drawer on #7 with reads that answer convFor(7).
func convApp(t *testing.T, icons bool, w, h int) (*App, *fakeReads) {
	t.Helper()

	s := reviewScene{
		theme: "catppuccin-mocha", icons: icons, w: w, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: prSceneWith(nil, 0),
	}

	a := s.build(t)
	reads := &fakeReads{fail: map[string]error{}, convs: []github.Conversation{convFor(7)}}
	a.reads = reads
	a.openLink = (&fakeOpen{}).open

	return a, reads
}

// convOpen presses i and delivers the answer.
func convOpen(t *testing.T, a *App) {
	t.Helper()

	ciDeliver(a, press(a, keyRune('i')))

	if a.conv == nil || a.conv.loading || a.conv.doc == nil {
		t.Fatalf("i opened no loaded conversation; notice %q", a.notice)
	}
}

func TestConversationFromTheList(t *testing.T) {
	t.Parallel()

	mine := map[string]github.MyPRs{"github.com": {Requested: []github.SearchPR{
		searched("github.com/acme/web", 311, "Fix the login form", "kari-dev", 2*time.Hour),
	}}}
	a, reads, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web")
	reads.convs = []github.Conversation{convFor(311)}

	openList(a)
	typedList(a, "2")
	ciDeliver(a, press(a, keyRune('i')))

	want := []convCall{{repo: github.Repo{Host: "github.com", Owner: "acme", Name: "web"}, number: 311}}
	if got := reads.convReads(); !slices.Equal(got, want) {
		t.Fatalf("reads = %v, want %v", got, want)
	}

	if a.conv == nil || a.conv.doc == nil {
		t.Fatalf("no conversation shown; notice %q", a.notice)
	}

	text := shownText(a)
	for _, s := range []string{"#311 conversation", "Fixes the drawer."} {
		if !strings.Contains(text, s) {
			t.Errorf("the overlay lacks %q:\n%s", s, text)
		}
	}

	press(a, keyEsc)

	if a.conv != nil || a.prList == nil {
		t.Fatalf("esc left conv = %v, list = %v; want the list alone", a.conv != nil, a.prList != nil)
	}

	if text := shownText(a); !strings.Contains(text, "Pull requests") || strings.Contains(text, "#311 conversation") {
		t.Errorf("the list does not show again:\n%s", text)
	}
}

func TestConversationFromTheDrawer(t *testing.T) {
	t.Parallel()

	t.Run("in PR mode with the diff loaded", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		reads := &fakeReads{fail: map[string]error{}, convs: []github.Conversation{convFor(7)}}
		r.reads = reads

		r.poll()
		r.opened(t)
		r.typed("p")
		r.until(t, "PR mode", r.inPRMode(7))
		r.typed("i")

		if r.conv == nil || r.conv.number != 7 {
			t.Fatalf("i opened %+v; notice %q", r.conv, r.notice)
		}

		r.until(t, "the conversation", func() bool { return r.conv != nil && r.conv.doc != nil })

		if got := reads.convReads(); len(got) != 1 || got[0].repo != prRepo || got[0].number != 7 {
			t.Errorf("reads = %v, want one read of o/r#7", got)
		}
	})

	t.Run("outside PR mode it shows the pull request first", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		reads := &fakeReads{fail: map[string]error{}, convs: []github.Conversation{convFor(7)}}
		r.reads = reads

		r.poll()
		r.opened(t)
		r.typed("i")

		if r.conv != nil {
			t.Fatal("i opened the overlay before the pull request loaded")
		}

		r.until(t, "the conversation", func() bool { return r.conv != nil && r.conv.doc != nil })

		if rv := r.review(); rv == nil || rv.PR == nil || rv.PR.Number != 7 {
			t.Errorf("review = %+v, want the drawer in PR mode on #7", rv)
		}
	})

	t.Run("a modal opened meanwhile gets the notice", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.reads = &fakeReads{fail: map[string]error{}, convs: []github.Conversation{convFor(7)}}

		r.poll()
		r.opened(t)
		r.typed("i")
		r.keys(keyEsc)

		r.until(t, "the PR to load", r.inPRMode(7))
		r.settle()

		if r.conv != nil {
			t.Error("the late answer opened the conversation")
		}

		if want := "#7 loaded; press i for the conversation"; r.notice != want {
			t.Errorf("notice = %q, want %q", r.notice, want)
		}
	})

	t.Run("PR mode's hints hold it before checks", func(t *testing.T) {
		t.Parallel()

		a, _ := convApp(t, true, 200, 48)
		text := shownText(a)

		i, c := strings.Index(text, "i conversation"), strings.Index(text, "c checks")
		if i < 0 || c < 0 || i > c {
			t.Errorf("hints hold i conversation at %d and c checks at %d:\n%s", i, c, text)
		}
	})
}

func TestConversationKeys(t *testing.T) {
	t.Parallel()

	keys := []struct {
		name string
		key  tea.KeyPressMsg
		want func(v *mdView, start, h int) (cursor, top int)
		from int // the line it starts on
	}{
		{"j", keyRune('j'), func(_ *mdView, s, _ int) (int, int) { return s + 1, 0 }, 0},
		{"down", tea.KeyPressMsg{Code: tea.KeyDown}, func(_ *mdView, s, _ int) (int, int) { return s + 1, 0 }, 0},
		{"k", keyRune('k'), func(_ *mdView, s, _ int) (int, int) { return s - 1, 0 }, 5},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, func(_ *mdView, s, _ int) (int, int) { return s - 1, 0 }, 5},
		{"ctrl+d", tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, func(_ *mdView, s, h int) (int, int) { return s + h/2, 0 }, 0},
		{"ctrl+u", tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, func(_ *mdView, s, h int) (int, int) { return s - h/2, 0 }, 10},
		{"ctrl+f", tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}, func(_ *mdView, s, h int) (int, int) { return s + h - 1, h - 1 }, 0},
		{"pgdown", tea.KeyPressMsg{Code: tea.KeyPgDown}, func(_ *mdView, s, h int) (int, int) { return s + h - 1, h - 1 }, 0},
		{"g", keyRune('g'), func(*mdView, int, int) (int, int) { return 0, 0 }, 12},
		{"G", keyRune('G'), func(v *mdView, _, h int) (int, int) {
			n := len(v.layout().Lines)

			return n - 1, n - h
		}, 0},
	}

	for _, tc := range keys {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, _ := convApp(t, true, 160, 30)
			convOpen(t, a)

			v := &a.conv.view
			if v.cursor != 0 {
				t.Fatalf("it opened on line %d, want the description's top line", v.cursor)
			}

			v.cursor, v.top = tc.from, 0
			v.clamp()

			press(a, tc.key)

			wc, _ := tc.want(v, tc.from, v.h)
			if v.cursor != wc {
				t.Errorf("%s: cursor = %d, want %d", tc.name, v.cursor, wc)
			}
		})
	}

	t.Run("pgup and ctrl+b page back", func(t *testing.T) {
		t.Parallel()

		for _, k := range []tea.KeyPressMsg{{Code: tea.KeyPgUp}, {Code: 'b', Mod: tea.ModCtrl}} {
			a, _ := convApp(t, true, 160, 20)
			convOpen(t, a)

			v := &a.conv.view
			press(a, keyRune('G'))

			top, cursor := v.top, v.cursor

			press(a, k)

			if v.top != top-(v.h-1) || v.cursor != cursor-(v.h-1) {
				t.Errorf("%v: top %d cursor %d, want %d and %d", k, v.top, v.cursor, top-(v.h-1), cursor-(v.h-1))
			}
		}
	})

	t.Run("] and [ visit the items", func(t *testing.T) {
		t.Parallel()

		a, _ := convApp(t, true, 160, 48)
		convOpen(t, a)

		d, v := a.conv.doc, &a.conv.view

		next, ok := d.itemLine(0, 1)
		if !ok {
			t.Fatal("the conversation has no second item")
		}

		press(a, keyRune(']'))

		if v.cursor != next {
			t.Fatalf("] went to line %d, want the next item's first line %d", v.cursor, next)
		}

		press(a, keyRune('j'))
		press(a, keyRune('['))

		if v.cursor != next {
			t.Errorf("[ from inside the item went to %d, want its top %d", v.cursor, next)
		}

		press(a, keyRune('['))

		if v.cursor != 0 {
			t.Errorf("[ from the item's top went to %d, want the previous item's top 0", v.cursor)
		}
	})

	t.Run("enter opens the bot row", func(t *testing.T) {
		t.Parallel()

		a, _ := convApp(t, true, 160, 48)
		convOpen(t, a)

		if strings.Contains(shownText(a), "Coverage is 80%.") {
			t.Fatal("the bot comment shows before it was opened")
		}

		lay := a.conv.view.layout()
		if len(lay.Folds) == 0 {
			t.Fatal("the conversation has no fold")
		}

		v := &a.conv.view
		v.cursor = lay.Folds[0].Line
		v.clamp()

		press(a, keyEnter)

		if !strings.Contains(shownText(a), "Coverage is 80%.") {
			t.Errorf("enter did not open the bot row:\n%s", shownText(a))
		}
	})

	t.Run("every other key and the leader are dropped", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		convOpen(t, a)

		v := &a.conv.view
		v.cursor, v.top = 3, 0
		before, calls := *v, len(reads.convReads())

		for _, k := range []tea.KeyPressMsg{keyRune('x'), keyRune('a'), keyRune('c'), keyRune('y'), keyLeader, keyTab} {
			ciDeliver(a, press(a, k))
		}

		a.Update(tea.PasteMsg{Content: "pasted"})

		if v.cursor != before.cursor || v.top != before.top {
			t.Errorf("a dropped key moved the view to %d/%d", v.cursor, v.top)
		}

		if a.pending || a.popup || a.conv == nil || a.checks != nil || a.merge != nil || a.confirm != nil {
			t.Errorf("a dropped key reached beyond the overlay: pending %v, popup %v", a.pending, a.popup)
		}

		if got := len(reads.convReads()); got != calls {
			t.Errorf("a dropped key read again: %d reads, want %d", got, calls)
		}
	})
}

// convGrown is convFor(7) with a comment added above the review, so the review
// moves down by that comment's lines.
func convGrown() github.Conversation {
	c := convFor(7)
	extra := github.TimelineItem{Kind: github.ItemComment, ID: "IC_0", Author: "pat", At: t0.Add(-150 * time.Minute), Body: convText("One\n\ntwo\n\nthree.")}
	c.Items = append([]github.TimelineItem{extra}, c.Items...)

	return c
}

func TestConversationRereads(t *testing.T) {
	t.Parallel()

	t.Run("r reads again", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		convOpen(t, a)

		ciDeliver(a, press(a, keyRune('r')))

		if got := len(reads.convReads()); got != 2 {
			t.Errorf("reads = %d, want 2", got)
		}
	})

	t.Run("the poll's update time", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		reads := &fakeReads{fail: map[string]error{}, convs: []github.Conversation{convFor(7)}}
		r.reads = reads

		at := t0.Add(-time.Hour)
		snap := func(at time.Time) {
			pr := prSeven()
			pr.UpdatedAt = at
			flatten(r.step(snapshotOf(pr)))
		}

		snap(at)
		r.opened(t)
		r.typed("p")
		r.until(t, "PR mode", r.inPRMode(7))
		r.typed("i")
		r.until(t, "the conversation", func() bool { return r.conv != nil && r.conv.doc != nil })

		snap(at)

		if got := len(reads.convReads()); got != 1 {
			t.Fatalf("a snapshot of the same update time read again: %d reads", got)
		}

		snap(at.Add(time.Minute))

		if got := len(reads.convReads()); got != 2 {
			t.Fatalf("a newer update time made %d reads, want 2", got)
		}

		snap(at.Add(time.Minute))

		if got := len(reads.convReads()); got != 2 {
			t.Errorf("the same newer time read again: %d reads", got)
		}
	})

	t.Run("an item that grew above keeps the place", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 30)
		reads.convs = []github.Conversation{convFor(7), convGrown()}

		convOpen(t, a)

		v := &a.conv.view
		review, _ := a.conv.doc.itemLine(0, 1)

		for range 3 {
			next, ok := a.conv.doc.itemLine(review, 1)
			if !ok {
				t.Fatal("too few items")
			}

			review = next
		}

		v.cursor, v.top = review+1, review-2
		v.clamp()

		id, off := a.conv.doc.itemAt(v.cursor)
		above := v.cursor - v.top

		ciDeliver(a, press(a, keyRune('r')))

		gotID, gotOff := a.conv.doc.itemAt(v.cursor)
		if gotID != id || gotOff != off {
			t.Errorf("the cursor is on %q+%d, want %q+%d", gotID, gotOff, id, off)
		}

		if v.cursor-v.top != above {
			t.Errorf("the top keeps %d rows above the cursor, want %d", v.cursor-v.top, above)
		}
	})
}

// convReadsOf runs cmd and returns the conversation answers it raised.
func convReadsOf(cmd tea.Cmd) []convReadMsg {
	var out []convReadMsg

	for _, m := range flatten(cmd) {
		if c, ok := m.(convReadMsg); ok {
			out = append(out, c)
		}
	}

	return out
}

func TestConversationStale(t *testing.T) {
	t.Parallel()

	t.Run("after esc", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		cmd := press(a, keyRune('i'))
		press(a, keyEsc)
		ciDeliver(a, cmd)

		if a.conv != nil {
			t.Error("an answer after esc reopened the overlay")
		}

		if got := len(reads.convReads()); got != 1 {
			t.Errorf("reads = %d, want the one", got)
		}
	})

	t.Run("after a newer read", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		reads.convs = []github.Conversation{convFor(7), convGrown()}

		first := convReadsOf(press(a, keyRune('i')))
		second := convReadsOf(press(a, keyRune('r')))

		if len(first) != 1 || len(second) != 1 {
			t.Fatalf("answers = %d and %d, want one each", len(first), len(second))
		}

		a.Update(first[0])

		if a.conv.doc != nil || !a.conv.loading {
			t.Fatal("the older answer was kept while a newer read is on its way")
		}

		a.Update(second[0])

		doc := a.conv.doc
		if doc == nil || a.conv.loading {
			t.Fatal("the newer answer was dropped")
		}

		a.Update(first[0])

		if a.conv.doc != doc {
			t.Error("the older answer replaced the newer one")
		}
	})
}

func TestConversationWatch(t *testing.T) {
	t.Parallel()

	mine := map[string]github.MyPRs{"github.com": {Requested: []github.SearchPR{
		searched("github.com/acme/web", 311, "Fix the login form", "kari-dev", 2*time.Hour),
	}}}
	a, reads, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web")
	reads.convs = []github.Conversation{convFor(311)}

	web := github.Repo{Host: "github.com", Owner: "acme", Name: "web"}
	visible := func() []int {
		for _, w := range a.prWatches(a.store.State()) {
			if w.Repo == web {
				return w.Visible
			}
		}

		return nil
	}

	openList(a)
	typedList(a, "2")

	if got := visible(); slices.Contains(got, 311) {
		t.Fatalf("#311 is watched before the overlay opened: %v", got)
	}

	ciDeliver(a, press(a, keyRune('i')))

	if got := visible(); !slices.Contains(got, 311) {
		t.Errorf("watched = %v, want #311 visible while it shows", got)
	}

	press(a, keyEsc)

	if got := visible(); slices.Contains(got, 311) {
		t.Errorf("watched = %v after esc, want #311 gone", got)
	}
}

func TestConversationResize(t *testing.T) {
	t.Parallel()

	a, _ := convApp(t, true, 160, 48)
	convOpen(t, a)

	for range 3 {
		press(a, keyRune(']'))
	}

	press(a, keyRune('j'))

	id, off := a.conv.doc.itemAt(a.conv.view.cursor)
	before := a.conv.view.textWidth()

	a.Update(tea.WindowSizeMsg{Width: 90, Height: 48})

	if a.conv.view.textWidth() >= before {
		t.Fatalf("the text did not narrow: %d then %d", before, a.conv.view.textWidth())
	}

	gotID, gotOff := a.conv.doc.itemAt(a.conv.view.cursor)
	if gotID != id || gotOff != off {
		t.Errorf("the cursor is on %q+%d, want %q+%d", gotID, gotOff, id, off)
	}
}

func TestConversationOff(t *testing.T) {
	t.Parallel()

	a, _ := convApp(t, true, 160, 48)
	a.reads = nil

	ciDeliver(a, press(a, keyRune('i')))

	if a.conv != nil {
		t.Error("i opened the overlay without reads")
	}

	if a.notice != noticePRsOff {
		t.Errorf("notice = %q, want %q", a.notice, noticePRsOff)
	}
}

func TestConversationTooSmall(t *testing.T) {
	t.Parallel()

	a, _ := convApp(t, true, 160, 48)
	convOpen(t, a)

	a.Update(tea.WindowSizeMsg{Width: 20, Height: 8})

	if text := shownText(a); strings.Contains(text, "conversation") {
		t.Errorf("20x8 drew the box:\n%s", text)
	}

	press(a, keyRune('j'))

	if a.conv == nil {
		t.Fatal("a key other than esc closed the box")
	}

	press(a, keyEsc)

	if a.conv != nil {
		t.Error("esc did not close a box too small to draw")
	}
}

func TestConvRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		h                  int
		rows               int
		blanks, hint, head bool
	}{
		{44, 35, true, true, true},
		{12, 3, true, true, true},
		{11, 5, false, true, true},
		{9, 3, false, true, true},
		{8, 3, false, false, true},
		{7, 5, false, false, false},
		{3, 1, false, false, false},
	}

	for _, tc := range tests {
		rows, blanks, hint, head := convRows(tc.h)
		if rows != tc.rows || blanks != tc.blanks || hint != tc.hint || head != tc.head {
			t.Errorf("convRows(%d) = %d %v %v %v, want %d %v %v %v", tc.h, rows, blanks, hint, head, tc.rows, tc.blanks, tc.hint, tc.head)
		}
	}
}

// convScene opens the conversation at the given size. mode says what the read
// answers: "ok", "loading" (nothing delivered) or "error".
func convScene(t *testing.T, icons bool, w, h int, mode string) *App {
	t.Helper()

	a, reads := convApp(t, icons, w, h)
	if mode == "error" {
		reads.convErr = errors.New("HTTP 502 Bad Gateway")
	}

	cmd := press(a, keyRune('i'))
	if mode != "loading" {
		ciDeliver(a, cmd)
	}

	return a
}

type convSceneCase struct {
	name string
	w, h int
	mode string
}

func convScenes() []convSceneCase {
	return []convSceneCase{
		{"160x48", 160, 48, "ok"},
		{"120x40", 120, 40, "ok"},
		{"loading", 120, 30, "loading"},
		{"read-failed", 120, 30, "error"},
		{"80x24", 80, 24, "ok"},
		{"40x12", 40, 12, "ok"},
	}
}

// TestConversationGolden draws the overlay once in colour; it compares
// highlighted colours, so it runs serially.
func TestConversationGolden(t *testing.T) {
	for _, c := range convScenes() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(convScene(t, true, c.w, c.h, c.mode).View().Content))
		})
	}
}

// TestConversationText is the same screens without styling, so the layout reads.
func TestConversationText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, c := range convScenes() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, c.name), func(t *testing.T) {
				t.Parallel()
				golden.RequireEqual(t, []byte(ansi.Strip(convScene(t, icons, c.w, c.h, c.mode).View().Content)))
			})
		}
	}
}

// TestConversationSmall: at 80x24 and 40x12 the blank rows, then the hints,
// then the header go before a row of the view is cut.
func TestConversationSmall(t *testing.T) {
	t.Parallel()

	a := convScene(t, true, 80, 24, "ok")
	text := shownText(a)

	for _, want := range []string{"#7 Show the pull request over its diff", "j/k line", "esc close"} {
		if !strings.Contains(text, want) {
			t.Errorf("80x24 lacks %q:\n%s", want, text)
		}
	}

	a = convScene(t, true, 40, 12, "ok")
	text = shownText(a)

	if !strings.Contains(text, "#7 Show the pull") || strings.Contains(text, "esc close") {
		t.Errorf("40x12 keeps the header and drops the hints:\n%s", text)
	}

	if rows, _, _, _ := convRows(8); rows < convMinRows {
		t.Errorf("a box of 8 rows keeps %d view rows", rows)
	}
}

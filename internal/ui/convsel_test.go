package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

// convSelRig is the conversation overlay at 120x40 on the fixture, with a fake
// clipboard.
type convSelRig struct {
	a    *App
	clip *fakeRun
}

func newConvSelRig(t *testing.T) *convSelRig {
	t.Helper()

	a, _ := convApp(t, true, 120, 40)
	f := fakeClip(a)
	convOpen(t, a)

	return &convSelRig{a: a, clip: f}
}

// word is the first and last cell of the first view row holding row, where word
// starts; the search stays inside the view's cells.
func (r *convSelRig) word(t *testing.T, row, word string) (x0, x1, y int) {
	t.Helper()

	rect, ok := convDocRect(r.a.width, r.a.height)
	if !ok {
		t.Fatal("no doc rect")
	}

	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		line := ansi.Strip(strings.Split(r.a.View().Content, "\n")[y])
		if !strings.Contains(line, row) {
			continue
		}

		i := strings.Index(line, row) + strings.Index(row, word)
		x0 = ansi.StringWidth(line[:i])

		return x0, x0 + ansi.StringWidth(word) - 1, y
	}

	t.Fatalf("no view row holds %q", row)

	return 0, 0, 0
}

func (r *convSelRig) key(k tea.KeyPressMsg) { run(r.a, press(r.a, k)) }

func (r *convSelRig) selected() bool { return r.a.text != nil }

func (r *convSelRig) sel() *selection { return r.a.conv.view.sel }

const noFrames = "│╭╰─"

func TestConversationDocRect(t *testing.T) {
	t.Parallel()

	r := newConvSelRig(t)

	rect, ok := convDocRect(r.a.width, r.a.height)
	if !ok {
		t.Fatal("no rect")
	}

	// The first view row is the first item's top border, in the cells the rect
	// names.
	lines := strings.Split(ansi.Strip(r.a.View().Content), "\n")
	row := lines[rect.Min.Y]

	if got := ansi.Cut(row, rect.Min.X, rect.Max.X); !strings.Contains(got, "╭─ @mhersson opened #7") {
		t.Errorf("first rect row = %q", got)
	}

	if _, ok := convDocRect(20, 40); ok {
		t.Error("a box too narrow to draw has a rect")
	}
}

func TestConversationDragCopies(t *testing.T) {
	t.Parallel()

	t.Run("body into the next header", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		x0, _, y0 := r.word(t, "Looks fine.", "Looks")
		_, x1, y1 := r.word(t, "@sam reviewed · 20m", "20m")

		dragRun(r.a, x0, y0, x1, y1, 0)

		got := primaryWrites(r.clip)
		if len(got) != 1 || got[0] != "Looks fine.\n@sam reviewed · 20m" {
			t.Errorf("primary writes %q", got)
		}
	})

	t.Run("hunk", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		x0, _, y0 := r.word(t, "if k == keyTab", "if")
		_, _, y1 := r.word(t, "212 +", "212")

		dragRun(r.a, x0, y0, 1000, y1, 0)

		got := primaryWrites(r.clip)
		if len(got) != 1 {
			t.Fatalf("primary writes %q", got)
		}

		if strings.ContainsAny(got[0], noFrames+"+") || strings.Contains(got[0], "210") || strings.Contains(got[0], "212") {
			t.Errorf("copied %q, want no frames, numbers or signs", got[0])
		}

		if lines := strings.Split(got[0], "\n"); len(lines) != 3 {
			t.Errorf("copied %d lines, want 3: %q", len(lines), got[0])
		}
	})

	t.Run("review top line", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		x0, _, y := r.word(t, "@kari-dev requested changes", "@kari-dev")
		_, x1, _ := r.word(t, "@kari-dev requested changes · 1h", "1h")

		dragRun(r.a, x0, y, x1, y, 0)

		if got := primaryWrites(r.clip); !slices.Equal(got, []string{"@kari-dev requested changes · 1h"}) {
			t.Errorf("primary writes %q", got)
		}
	})
}

const convSelPara = "Alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega " +
	"and then alpha again with beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau end."

// newConvParaRig is the rig with the description a paragraph that wraps.
func newConvParaRig(t *testing.T) *convSelRig {
	t.Helper()

	a, reads := convApp(t, true, 120, 40)
	f := fakeClip(a)

	c := convFor(7)
	c.Body = github.Text{Markdown: convSelPara}
	reads.convs = []github.Conversation{c}

	convOpen(t, a)

	return &convSelRig{a: a, clip: f}
}

func TestConversationMultiClick(t *testing.T) {
	t.Parallel()

	r := newConvParaRig(t)

	g0, _, gy := r.word(t, "Alpha beta gamma", "gamma")
	clickRun(r.a, g0, gy)
	clickRun(r.a, g0, gy)

	if got := primaryWrites(r.clip); !slices.Equal(got, []string{"gamma"}) {
		t.Errorf("double click wrote %q, want [gamma]", got)
	}

	// The paragraph's second row starts at "and then" when the first one fills.
	tx, _, ty := r.word(t, "tau end.", "end.")
	_, _, y0 := r.word(t, "Alpha", "Alpha")

	if ty == y0 {
		t.Fatalf("the paragraph is one row (%d), want it wrapped", ty)
	}

	clickRun(r.a, tx, ty)
	clickRun(r.a, tx, ty)
	clickRun(r.a, tx, ty)

	got := primaryWrites(r.clip)
	if n := len(got); n != 3 || got[2] != convSelPara {
		t.Errorf("triple click wrote %q, want the paragraph as one line", got)
	}
}

func TestConversationClickClears(t *testing.T) {
	t.Parallel()

	r := newConvSelRig(t)
	x0, _, y0 := r.word(t, "Looks fine.", "Looks")
	_, x1, _ := r.word(t, "Looks fine.", "fine.")
	dragRun(r.a, x0, y0, x1, y0, 0)

	if !r.selected() {
		t.Fatal("no selection after the drag")
	}

	cursor := r.a.conv.view.cursor
	cx, _, cy := r.word(t, "Old.", "Old.")
	clickRun(r.a, cx, cy)

	if r.selected() || r.sel() != nil {
		t.Error("selection kept after a click")
	}

	if r.a.conv.view.cursor != cursor {
		t.Errorf("cursor moved to %d, want %d", r.a.conv.view.cursor, cursor)
	}

	if got := primaryWrites(r.clip); len(got) != 1 {
		t.Errorf("primary writes %q, want only the drag's", got)
	}
}

func TestConversationYKey(t *testing.T) {
	t.Parallel()

	t.Run("selection", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		x0, _, y0 := r.word(t, "Looks fine.", "Looks")
		_, x1, _ := r.word(t, "Looks fine.", "fine.")
		dragRun(r.a, x0, y0, x1, y0, 0)
		r.key(keyRune('y'))

		if got := clipboardWrites(r.clip); !slices.Equal(got, []string{"Looks fine."}) {
			t.Errorf("clipboard writes %q", got)
		}

		if !r.selected() {
			t.Error("selection cleared by y")
		}
	})

	t.Run("cursor unit", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		convOnLine(t, r.a, "Looks fine.")

		r.key(keyRune('y'))

		if got, want := clipboardWrites(r.clip), []string{"Looks fine."}; !slices.Equal(got, want) {
			t.Errorf("clipboard writes %q, want %q", got, want)
		}
	})
}

func TestConversationKeysClear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys []tea.KeyPressMsg
		want bool // the selection is kept
	}{
		{"move", []tea.KeyPressMsg{keyRune('j')}, false},
		{"item", []tea.KeyPressMsg{keyRune(']')}, false},
		{"search then esc", []tea.KeyPressMsg{keyRune('/'), keyEsc}, false},
		{"refresh", []tea.KeyPressMsg{keyRune('r')}, false},
		{"dropped key", []tea.KeyPressMsg{keyRune('x')}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newConvSelRig(t)
			convDragLooks(t, r)

			for _, k := range tt.keys {
				r.key(k)
			}

			if got := r.selected(); got != tt.want {
				t.Errorf("selected = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("search without a hint row", func(t *testing.T) {
		t.Parallel()

		a, _ := convApp(t, true, 120, 11)
		r := &convSelRig{a: a, clip: fakeClip(a)}
		convOpen(t, a)

		x0, _, y0 := r.word(t, "@mhersson opened", "@mhersson")
		_, x1, _ := r.word(t, "@mhersson opened", "opened")
		dragRun(a, x0, y0, x1, y0, 0)

		if !r.selected() {
			t.Fatal("no selection after the drag")
		}

		r.key(keyRune('/'))

		if !r.selected() || a.conv.searching {
			t.Errorf("selected = %v, searching = %v; want it kept and no prompt", r.selected(), a.conv.searching)
		}
	})

	t.Run("wheel", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		convDragLooks(t, r)
		r.a.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelDown})

		if !r.selected() {
			t.Error("the wheel cleared the selection")
		}
	})
}

// convDragLooks selects "Looks fine." by a drag.
func convDragLooks(t *testing.T, r *convSelRig) {
	t.Helper()

	x0, _, y0 := r.word(t, "Looks fine.", "Looks")
	_, x1, _ := r.word(t, "Looks fine.", "fine.")
	dragRun(r.a, x0, y0, x1, y0, 0)

	if !r.selected() {
		t.Fatal("no selection after the drag")
	}
}

func TestConversationRereadSelection(t *testing.T) {
	t.Parallel()

	same := convFor(7)

	changed := convFor(7)
	for i := range changed.Items {
		if changed.Items[i].ID == "PRR_4" {
			changed.Items[i].Body = convText("Looks fine!")
		}
	}

	tests := []struct {
		name string
		next github.Conversation
		keep bool
	}{
		{"same text", same, true},
		{"changed text", changed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, reads := convApp(t, true, 120, 40)
			reads.convs = []github.Conversation{convFor(7), tt.next}
			r := &convSelRig{a: a, clip: fakeClip(a)}
			convOpen(t, a)
			convDragLooks(t, r)

			old := a.conv.doc
			ciDeliver(a, a.readConversation(a.conv))

			if a.conv.doc == old {
				t.Fatal("the re-read kept the old doc")
			}

			if got := r.selected(); got != tt.keep {
				t.Errorf("selected = %v, want %v", got, tt.keep)
			}
		})
	}
}

func TestConversationCloseClears(t *testing.T) {
	t.Parallel()

	r := newConvSelRig(t)
	convDragLooks(t, r)
	r.key(keyEsc)

	if r.a.conv != nil {
		t.Fatal("the overlay is still open")
	}

	if r.selected() {
		t.Error("selection kept after the overlay closed")
	}

	run(r.a, press(r.a, keyLeader, keyRune('y')))

	if got := clipboardWrites(r.clip); len(got) != 0 {
		t.Errorf("leader y wrote %q after close", got)
	}
}

func TestConversationDragEndsAtEdge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		x, y int
	}{
		{"box border", 6, 12},
		{"outside the box", 0, 20},
		{"last screen row", 50, 39},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newConvSelRig(t)

			procs := map[core.PaneID]*fakeProc{}

			for id, p := range r.a.panes {
				fp, ok := p.proc.(*fakeProc)
				if !ok {
					t.Fatalf("pane %s has a %T", id, p.proc)
				}

				feedAndWait(t, p, fp, "\x1b[?1000h\x1b[?1006h")

				procs[id] = fp
			}

			x0, _, y0 := r.word(t, "Looks fine.", "Looks")
			dragRun(r.a, x0, y0, tt.x, tt.y, 0)

			for id, fp := range procs {
				if got := fp.sent(); got != "" {
					t.Errorf("pane %s received %q", id, got)
				}
			}

			rect, _ := convDocRect(r.a.width, r.a.height)
			sel := r.sel()

			if sel == nil {
				t.Fatal("no selection")
			}

			lines := len(r.a.conv.view.layout().Lines)
			for _, p := range []point{sel.anchor, sel.head} {
				if p.row < 0 || p.row >= lines || p.col < 0 || p.col >= rect.Dx() {
					t.Errorf("selection end %v lies outside the view", p)
				}
			}

			if got := primaryWrites(r.clip); len(got) != 1 || got[0] == "" {
				t.Errorf("primary writes %q, want one", got)
			}
		})
	}
}

// TestConversationSelectionsAreTheirOverlays: a selection held by the plan
// overlay's view survives the conversation closing, and the reverse.
func TestConversationSelectionsAreTheirOverlays(t *testing.T) {
	t.Parallel()

	t.Run("plan selection survives closeConv", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		pv := &planView{}
		r.a.plan = pv
		r.a.text = &textSelection{view: &pv.view}

		r.a.closeConv()

		if r.a.text == nil {
			t.Error("closing the conversation cleared the plan's selection")
		}
	})

	t.Run("conversation selection survives closePlan", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		r.a.plan = &planView{}
		r.a.text = &textSelection{view: &r.a.conv.view}

		r.a.closePlan()

		if r.a.text == nil {
			t.Error("closing the plan cleared the conversation's selection")
		}
	})
}

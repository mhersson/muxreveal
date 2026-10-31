package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
)

// selPage is a page rig with docs/a.md shown, a fake clipboard and the drawer
// holding the keyboard.
type selPage struct {
	*pageRig
	p    *mdPage
	clip *fakeRun
}

func newSelPage(t *testing.T, opts ...func(*Options)) *selPage {
	t.Helper()

	m := newPageRig(t, nil, opts...)
	f := fakeClip(m.App)
	p := m.openPage(t, "docs/a.md", false)

	m.drawerTake()
	m.drawn(t)

	return &selPage{pageRig: m, p: p, clip: f}
}

// word is the screen cells of the first drawer row holding row: the cells of
// word on it, first and last, and the row.
func (m *selPage) word(t *testing.T, row, word string) (x0, x1, y int) {
	t.Helper()

	rect := mustRect(t, m.drawerRig)

	for y, line := range strings.Split(m.drawn(t), "\n") {
		if !strings.Contains(line, row) {
			continue
		}

		i := strings.Index(line, word)
		x0 = rect.X + utf8.RuneCountInString(line[:i])

		return x0, x0 + utf8.RuneCountInString(word) - 1, rect.Y + y
	}

	t.Fatalf("no drawer row holds %q", row)

	return 0, 0, 0
}

// drawerHasSel reports whether the active tab's drawer draws a selection.
func (m *selPage) drawerHasSel() (selection, bool) {
	v := m.view()
	if v == nil || v.page() == nil || v.page().view.sel == nil {
		return selection{}, false
	}

	return *v.page().view.sel, true
}

func (m *selPage) selected() bool { return m.text != nil && m.p.view.sel != nil }

// dragIntro selects "intro text".
func (m *selPage) dragIntro(t *testing.T) {
	t.Helper()

	x0, x1, y := m.word(t, "intro text", "intro text")
	dragRun(m.App, x0, y, x1, y, 0)
}

func (m *selPage) key(k tea.KeyPressMsg) { run(m.App, press(m.App, k)) }

func TestPageDragCopies(t *testing.T) {
	t.Parallel()

	m := newSelPage(t)
	m.dragIntro(t)

	if got := primaryWrites(m.clip); !slices.Equal(got, []string{"intro text"}) {
		t.Errorf("primary writes %q, want [intro text]", got)
	}

	if !m.selected() || !m.drawer.focused {
		t.Errorf("selected = %v, drawer has the keyboard = %v, want both", m.selected(), m.drawer.focused)
	}
}

func TestPageClickMovesCursorOnRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dy          int // rows the pointer moves down before the release
		wantPress   bool
		wantRelease bool
	}{
		{"click", 0, false, true},
		{"drag two rows", 2, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newSelPage(t)
			m.p.view.cursor = 0
			m.p.view.clamp()

			want := pageLine(t, m.p, "item two")
			x, _, y := m.word(t, "item two", "item two")

			mdown(m.App, x, y, tea.MouseLeft, 0)

			if got := m.p.view.cursor == want; got != tt.wantPress {
				t.Errorf("cursor on the press = %d, at the row = %v, want %v", m.p.view.cursor, got, tt.wantPress)
			}

			if tt.dy > 0 {
				mmove(m.App, x, y+tt.dy, tea.MouseLeft)
			}

			release(m.App, x, y+tt.dy)

			if got := m.p.view.cursor == want; got != tt.wantRelease {
				t.Errorf("cursor after the release = %d, at the row = %v, want %v", m.p.view.cursor, got, tt.wantRelease)
			}
		})
	}
}

func TestPageClickOffTheText(t *testing.T) {
	t.Parallel()

	m := newSelPage(t)
	m.p.view.cursor = 2
	rect := mustRect(t, m.drawerRig)

	// Below the last line the press takes the keyboard and moves nothing.
	m.drawerRelease()

	y := rect.Y + 1 + len(m.p.view.layout().Lines) - m.p.view.top
	if y >= rect.Y+1+m.layout(t).diffH {
		t.Skip("the page fills the drawer")
	}

	clickRun(m.App, rect.X+6, y)

	if m.p.view.cursor != 2 || !m.drawer.focused {
		t.Errorf("cursor = %d, drawer has the keyboard = %v, want 2 and true", m.p.view.cursor, m.drawer.focused)
	}
}

func TestReviewCopy(t *testing.T) {
	t.Parallel()

	t.Run("selection", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)
		m.key(keyRune('y'))

		if got := clipboardWrites(m.clip); !slices.Equal(got, []string{"intro text"}) {
			t.Errorf("clipboard writes %q", got)
		}

		if !m.selected() {
			t.Error("y cleared the selection")
		}
	})

	t.Run("cursor item", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.p.view.cursor = pageLine(t, m.p, "item one")
		m.key(keyRune('y'))

		if got := clipboardWrites(m.clip); !slices.Equal(got, []string{"• item one"}) {
			t.Errorf("clipboard writes %q, want [• item one]", got)
		}
	})

	t.Run("palette hint", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)

		if got := m.paletteHint("review.copy"); got != "drawer: y" {
			t.Errorf("hint = %q, want drawer: y", got)
		}
	})

	t.Run("remapped", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t, func(o *Options) { o.Keys = drawerKeys("Y", "review.copy", "y", "none") })
		m.p.view.cursor = pageLine(t, m.p, "item one")

		m.key(keyRune('y'))

		if got := clipboardWrites(m.clip); len(got) != 0 {
			t.Errorf("y wrote %q, want nothing", got)
		}

		m.key(keyRune('Y'))

		if got := clipboardWrites(m.clip); !slices.Equal(got, []string{"• item one"}) {
			t.Errorf("Y wrote %q, want [• item one]", got)
		}
	})

	t.Run("tree focused", func(t *testing.T) {
		t.Parallel()

		m := newPageRig(t, nil)
		f := fakeClip(m.App)

		m.keys(keyTab)

		if !m.drawer.tree {
			t.Fatal("tab did not give the tree the keyboard")
		}

		m.keys(keyRune('y'))

		if got := clipboardWrites(f); len(got) != 0 {
			t.Errorf("y with the tree focused wrote %q, want nothing", got)
		}
	})
}

func TestDrawerKeysClear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys []tea.KeyPressMsg
		want bool // the selection is kept
	}{
		{"move", []tea.KeyPressMsg{keyRune('j')}, false},
		{"search then a letter", []tea.KeyPressMsg{keyRune('/'), keyRune('z')}, false},
		{"back", []tea.KeyPressMsg{keyEsc}, false},
		{"copy", []tea.KeyPressMsg{keyRune('y')}, true},
		{"full file, which a page drops", []tea.KeyPressMsg{keyRune('f')}, true},
		{"split, which a page drops", []tea.KeyPressMsg{keyRune('u')}, true},
		{"file next, which a page drops", []tea.KeyPressMsg{keyRune('}')}, true},
		{"dropped key", []tea.KeyPressMsg{keyRune('x')}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newSelPage(t)
			m.dragIntro(t)

			for _, k := range tt.keys {
				m.key(k)
			}

			if got := m.text != nil; got != tt.want {
				t.Errorf("selection kept = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("wheel", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)

		rect := mustRect(t, m.drawerRig)
		m.Update(tea.MouseWheelMsg{X: rect.X + 6, Y: rect.Y + 5, Button: tea.MouseWheelDown})

		if m.text == nil {
			t.Error("the wheel cleared the selection")
		}
	})
}

func TestOneSelectionAcrossHosts(t *testing.T) {
	t.Parallel()

	t.Run("pane then page", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.sel = &paneSelection{pane: m.focus()}

		m.dragIntro(t)

		if m.sel != nil || m.text == nil {
			t.Errorf("pane selection = %v, text selection = %v, want only the page's", m.sel, m.text)
		}
	})

	t.Run("tab switch keeps it", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)

		first := m.tabID()
		chord(m.App, 'c')

		if m.tabID() == first {
			t.Fatal("no second tab")
		}

		if m.text == nil || m.p.view.sel == nil {
			t.Error("a tab switch cleared the drawer's selection")
		}

		m.run(m.openReview(m.focus(), true))
		m.until(t, "the other tab's drawer to open", func() bool { return m.store.State().Tab().Review != nil })

		other := m.drawer.views[m.tabID()]
		if other == nil {
			t.Fatal("the other tab has no drawer view")
		}

		if other.page() != nil && other.page().view.sel != nil {
			t.Error("the other tab's drawer holds the selection")
		}

		if _, ok := m.drawerHasSel(); ok {
			t.Error("the other tab's drawer draws a selection")
		}

		openIn(t, m.App, core.SelectTab{Index: 0})

		if m.text == nil || m.p.view.sel == nil || m.view() != m.drawer.views[first] {
			t.Error("the selection is gone after switching back")
		}

		if !strings.Contains(m.drawn(t), "intro text") {
			t.Error("the page is not drawn")
		}
	})

	t.Run("rail click keeps it", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		chord(m.App, 'a')

		rail := m.railRect()
		if rail.W == 0 {
			t.Fatal("no rail")
		}

		m.dragIntro(t)

		clickRun(m.App, rail.X+1, rail.Y+1)

		if m.text == nil {
			t.Error("a rail click cleared the selection")
		}
	})

	t.Run("hover keeps it", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)

		m.focusFollows = true
		hover(m.App, 70, 10)

		if m.text == nil {
			t.Error("hovering a pane cleared the selection")
		}
	})

	t.Run("overlay", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)

		path := filepath.Join(m.dir, "plan.md")
		write(t, path, selPlan)

		id := m.agent(t, m.dir, "")
		openIn(t, m.App, core.NotePlan{ID: id, Plan: core.Plan{Path: path, Root: m.dir, Kind: core.PlanFile, At: t0}})

		msgs := planMsgs(m.openPlan(id))
		if len(msgs) != 1 {
			t.Fatalf("the plan ask raised %d plan reads, want 1", len(msgs))
		}

		m.Update(msgs[0])

		rect, ok := planDocRect(m.width, m.height)
		if !ok {
			t.Fatal("no plan doc")
		}

		dragRun(m.App, rect.Min.X, rect.Min.Y+2, rect.Min.X+8, rect.Min.Y+2, 0)

		if m.p.view.sel != nil {
			t.Error("the page kept its selection under an overlay drag")
		}

		m.keys(keyEsc)
		m.keys(keyLeader, keyRune('y'))

		if got := clipboardWrites(m.clip); len(got) != 0 {
			t.Errorf("leader y wrote %q, want nothing", got)
		}
	})
}

func TestPageSelectionCleared(t *testing.T) {
	t.Parallel()

	t.Run("closing the drawer", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)
		m.closeDrawer(false)

		if m.text != nil {
			t.Error("closing the drawer kept the selection")
		}
	})

	t.Run("a key", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)
		m.key(keyRune('b'))

		if m.text != nil {
			t.Error("a command key kept the selection")
		}
	})

	reload := func(t *testing.T, from, to string) bool {
		t.Helper()

		m := newSelPage(t)
		m.dragIntro(t)

		write(t, filepath.Join(m.dir, "docs/a.md"), strings.Replace(pageDoc, from, to, 1))

		for _, msg := range flatten(m.batch(m.root, git.Batch{Paths: []string{"docs/a.md"}})) {
			if pm, ok := msg.(mdPageMsg); ok && pm.reload {
				m.flow(m.step(msg), nil)
			}
		}

		return m.text != nil
	}

	t.Run("the selected line changed", func(t *testing.T) {
		t.Parallel()

		if reload(t, "intro text", "intro words") {
			t.Error("a re-read with the selected line changed kept the selection")
		}
	})

	t.Run("another line changed", func(t *testing.T) {
		t.Parallel()

		if !reload(t, "last words", "final words") {
			t.Error("a re-read with only another line changed cleared the selection")
		}
	})
}

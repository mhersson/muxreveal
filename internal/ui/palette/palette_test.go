package palette

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/ui/theme"
)

func mustTheme(t *testing.T, name string) theme.Theme {
	t.Helper()
	th, err := theme.ByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func testItems() []Item {
	return []Item{
		{ID: "a", Title: "Theme: Catppuccin Latte", Hint: "ctrl+t"},
		{ID: "b", Title: "Theme: Catppuccin Mocha"},
		{ID: "c", Title: "Quit MuxReveal", Hint: "ctrl+q"},
		{ID: "d", Title: "Pick theme"},
	}
}

func newModel(t *testing.T) Model {
	t.Helper()
	return New("Commands", testItems(), mustTheme(t, "catppuccin-mocha"))
}

// collect runs cmd and returns every message it yields, unwrapping batches.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, collect(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func press(m Model, k tea.KeyPressMsg) (Model, []tea.Msg) {
	m, cmd := m.Update(k)
	return m, collect(cmd)
}

func typeText(m Model, s string) (Model, []tea.Msg) {
	var all []tea.Msg
	for _, r := range s {
		var msgs []tea.Msg
		m, msgs = press(m, tea.KeyPressMsg{Code: r, Text: string(r)})
		all = append(all, msgs...)
	}
	return m, all
}

var (
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyUp    = tea.KeyPressMsg{Code: tea.KeyUp}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyBksp  = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

func highlights(msgs []tea.Msg) []string {
	var ids []string
	for _, m := range msgs {
		if h, ok := m.(HighlightMsg); ok {
			ids = append(ids, h.ID)
		}
	}
	return ids
}

func selectedID(t *testing.T, m Model) string {
	t.Helper()
	it, ok := m.Selected()
	if !ok {
		return ""
	}
	return it.ID
}

func TestFilter(t *testing.T) {
	tests := []struct {
		query string
		want  []string
	}{
		{"", []string{"a", "b", "c", "d"}},
		{"latte", []string{"a"}},
		{"quit", []string{"c"}},
		{"zzz", nil},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			m, _ := typeText(newModel(t), tt.query)
			if got := m.visibleIDs(); !slices.Equal(got, tt.want) {
				t.Fatalf("visible = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNavigation(t *testing.T) {
	m := newModel(t)
	var msgs []tea.Msg
	m, msgs = press(m, keyDown)
	if got := highlights(msgs); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("first down highlights = %v, want [b]", got)
	}
	m, msgs = press(m, keyDown)
	if got := highlights(msgs); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("second down highlights = %v, want [c]", got)
	}
	if got := selectedID(t, m); got != "c" {
		t.Fatalf("selected = %q, want c", got)
	}

	m = newModel(t)
	m, msgs = press(m, keyUp)
	if got := highlights(msgs); !slices.Equal(got, []string{"d"}) {
		t.Fatalf("up from first highlights = %v, want [d]", got)
	}
	if got := selectedID(t, m); got != "d" {
		t.Fatalf("selected after wrap = %q, want d", got)
	}
}

func TestNavigationKeys(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
		want string
	}{
		{"down", tea.KeyPressMsg{Code: tea.KeyDown}, "b"},
		{"ctrl+n", tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}, "b"},
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}, "b"},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, "d"},
		{"ctrl+p", tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}, "d"},
		{"shift+tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, msgs := press(newModel(t), tt.key)
			if got := selectedID(t, m); got != tt.want {
				t.Fatalf("selected = %q, want %q", got, tt.want)
			}
			if got := highlights(msgs); !slices.Equal(got, []string{tt.want}) {
				t.Fatalf("highlights = %v, want [%s]", got, tt.want)
			}
		})
	}
}

func TestEnterAndEsc(t *testing.T) {
	m := newModel(t)
	_, msgs := press(m, keyEnter)
	if len(msgs) != 1 || msgs[0] != (ChosenMsg{ID: "a"}) {
		t.Fatalf("enter msgs = %v, want [ChosenMsg{a}]", msgs)
	}

	_, msgs = press(m, keyEsc)
	if len(msgs) != 1 || msgs[0] != (ClosedMsg{}) {
		t.Fatalf("esc msgs = %v, want [ClosedMsg{}]", msgs)
	}
}

func TestNoMatches(t *testing.T) {
	m, msgs := typeText(newModel(t), "zzz")
	if got := highlights(msgs); len(got) != 0 {
		t.Fatalf("typing to zero matches emitted highlights %v", got)
	}
	if _, ok := m.Selected(); ok {
		t.Fatal("something is selected with zero matches")
	}

	m2, cmd := m.Update(keyEnter)
	if cmd != nil {
		t.Fatalf("enter with no matches returned a cmd: %v", collect(cmd))
	}
	_, msgs = press(m2, keyDown)
	if len(msgs) != 0 {
		t.Fatalf("down with no matches emitted %v", msgs)
	}

	if view := ansi.Strip(m.View(40)); !strings.Contains(view, "No matches") {
		t.Fatalf("view lacks %q:\n%s", "No matches", view)
	}

	// Backspacing to an empty query restores the list with the first item
	// selected and highlighted.
	for range 3 {
		m, msgs = press(m, keyBksp)
	}
	if got := m.visibleIDs(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("visible after clear = %v", got)
	}
	if got := selectedID(t, m); got != "a" {
		t.Fatalf("selected after clear = %q, want a", got)
	}
	if got := highlights(msgs); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("highlights on clear = %v, want [a]", got)
	}
}

func TestNoHighlightWhenUnchanged(t *testing.T) {
	m, _ := typeText(newModel(t), "q")
	if got := selectedID(t, m); got != "c" {
		t.Fatalf("selected after q = %q, want c", got)
	}
	// Narrowing the query keeps c first; the selection does not change.
	m, msgs := typeText(m, "ui")
	if got := highlights(msgs); len(got) != 0 {
		t.Fatalf("highlights = %v, want none", got)
	}
	if got := selectedID(t, m); got != "c" {
		t.Fatalf("selected = %q, want c", got)
	}
}

func TestQueryResetsSelection(t *testing.T) {
	m, _ := press(newModel(t), keyDown)
	if got := selectedID(t, m); got != "b" {
		t.Fatalf("selected after down = %q, want b", got)
	}
	m, msgs := typeText(m, "l")
	if first := m.visibleIDs()[0]; selectedID(t, m) != first {
		t.Fatalf("selected = %q, want first match %q", selectedID(t, m), first)
	}
	if got := highlights(msgs); len(got) != 1 || got[0] != m.visibleIDs()[0] {
		t.Fatalf("highlights = %v, want first match", got)
	}

	m, _ = press(m, keyBksp)
	if got := m.visibleIDs(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("visible after backspace = %v", got)
	}
	if got := selectedID(t, m); got != "a" {
		t.Fatalf("selected after backspace = %q, want a", got)
	}
}

func TestSelect(t *testing.T) {
	m := newModel(t)
	m.Select("b")
	if got := selectedID(t, m); got != "b" {
		t.Fatalf("selected = %q, want b", got)
	}
	m.Select("zz")
	if got := selectedID(t, m); got != "b" {
		t.Fatalf("selected after unknown id = %q, want b", got)
	}
}

func TestScroll(t *testing.T) {
	items := make([]Item, 15)
	for i := range items {
		items[i] = Item{ID: fmt.Sprintf("id%02d", i), Title: fmt.Sprintf("Item %02d", i)}
	}
	m := New("Many", items, mustTheme(t, "catppuccin-mocha"))
	for range 12 {
		m, _ = press(m, keyDown)
	}
	view := ansi.Strip(m.View(60))
	if !strings.Contains(view, "Item 12") {
		t.Fatalf("selected item not visible:\n%s", view)
	}
	if strings.Contains(view, "Item 00") {
		t.Fatalf("view did not scroll:\n%s", view)
	}
	if rows := strings.Count(view, "Item "); rows != MaxRows {
		t.Fatalf("visible item rows = %d, want %d", rows, MaxRows)
	}
}

func TestViewWidth(t *testing.T) {
	queries := []string{"", "th", "zzz"}
	for _, w := range []int{60, 30, 12, 4, 3, 2, 1, 0} {
		for _, q := range queries {
			t.Run(fmt.Sprintf("w%d/%q", w, q), func(t *testing.T) {
				m, _ := typeText(newModel(t), q)
				view := m.View(w)
				for i, line := range strings.Split(view, "\n") {
					if got := lipgloss.Width(line); got > w {
						t.Fatalf("line %d width %d > %d: %q", i, got, w, line)
					}
				}
				if w >= 2 {
					for i, line := range strings.Split(view, "\n") {
						if got := lipgloss.Width(line); got != w {
							t.Fatalf("line %d width %d, want exactly %d", i, got, w)
						}
					}
				}
			})
		}
	}
}

func TestSetTheme(t *testing.T) {
	m := newModel(t)
	before := m.View(40)
	m.SetTheme(mustTheme(t, "catppuccin-latte"))
	if after := m.View(40); after == before {
		t.Fatal("view did not change with the theme")
	}
}

func TestPaletteGolden(t *testing.T) {
	m, _ := typeText(newModel(t), "th")
	golden.RequireEqual(t, []byte(m.View(60)))
}

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

func TestPaletteIconsGolden(t *testing.T) {
	m := New("\uF4B5 Commands", iconItems(), mustTheme(t, "catppuccin-mocha"))
	m, _ = press(m, keyDown)
	golden.RequireEqual(t, []byte(m.View(48)))
}

func iconItems() []Item {
	return []Item{
		{ID: "a", Title: "Zoom pane", Hint: "ctrl+space z", Icon: "\U000F0293"},
		{ID: "b", Title: "Close pane", Hint: "ctrl+space x", Icon: "\U000F0156"},
		{ID: "c", Title: "Quit", Icon: "\U000F0206"},
	}
}

func TestViewFrame(t *testing.T) {
	m := New("\U000F03D8 Theme", iconItems(), mustTheme(t, "catppuccin-mocha"))
	m, _ = press(m, keyDown)

	lines := strings.Split(ansi.Strip(m.View(40)), "\n")
	if len(lines) != 3+3+3 {
		t.Fatalf("view has %d lines, want 9:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	wantTop := "\u256d\u2500 \U000F03D8 Theme "
	if !strings.HasPrefix(lines[0], wantTop) || !strings.HasSuffix(lines[0], "\u256e") {
		t.Errorf("top = %q, want prefix %q and a rounded corner", lines[0], wantTop)
	}

	if !strings.HasPrefix(lines[1], "\u2502 \u276f ") {
		t.Errorf("input row = %q, want the \u276f prompt", lines[1])
	}

	if lines[2] != "\u251c"+strings.Repeat("\u2500", 38)+"\u2524" {
		t.Errorf("divider = %q", lines[2])
	}

	if !strings.Contains(lines[3], " \U000F0293 Zoom pane") || !strings.HasSuffix(lines[3], "ctrl+space z \u2502") {
		t.Errorf("row = %q, want icon, title and a right-aligned hint", lines[3])
	}

	if lines[6] != "\u251c"+strings.Repeat("\u2500", 38)+"\u2524" {
		t.Errorf("footer divider = %q", lines[6])
	}

	if want := "\u2502 up/down move   enter run   esc close"; !strings.HasPrefix(lines[7], want) || !strings.HasSuffix(lines[7], "\u2502") {
		t.Errorf("hint row = %q, want prefix %q", lines[7], want)
	}

	if want := " 2 of 3 \u2500\u256f"; !strings.HasSuffix(lines[8], want) || !strings.HasPrefix(lines[8], "\u2570") {
		t.Errorf("bottom = %q, want it to end with %q", lines[8], want)
	}
}

func TestFooterColours(t *testing.T) {
	th := mustTheme(t, "catppuccin-mocha")
	view := New("Commands", iconItems(), th).View(40)

	panel := lipgloss.NewStyle().Background(th.UI.Panel).Foreground(th.UI.Fg)
	for name, want := range map[string]string{
		"key":   panel.Foreground(th.UI.Key).Bold(true).Render("enter"),
		"label": panel.Foreground(th.UI.Muted).Render(" run"),
		"gap":   panel.Render("   "),
	} {
		if !strings.Contains(view, want) {
			t.Errorf("footer %s is not drawn as %q:\n%q", name, want, view)
		}
	}
}

func TestFooterWithNoMatches(t *testing.T) {
	m, _ := typeText(newModel(t), "zzz")

	lines := strings.Split(ansi.Strip(m.View(40)), "\n")
	if len(lines) != 3+1+3 || !strings.Contains(lines[5], "esc close") {
		t.Errorf("no-match view has %d lines, hint row %q", len(lines), lines[len(lines)-2])
	}
}

func TestRowColours(t *testing.T) {
	th := mustTheme(t, "catppuccin-mocha")
	m := New("Commands", iconItems(), th)
	m, _ = typeText(m, "zp")
	view := m.View(40)
	ui := th.UI

	sel := lipgloss.NewStyle().Background(ui.Selection)
	for name, want := range map[string]string{
		"selected icon":  sel.Foreground(ui.Accent).Render("\U000F0293"),
		"matched rune":   sel.Foreground(ui.Match).Bold(true).Render("Z"),
		"selected title": sel.Foreground(ui.Fg).Bold(true).Render("oom "),
		"hint":           sel.Foreground(ui.Key).Bold(true).Render("ctrl+space z"),
	} {
		if !strings.Contains(view, want) {
			t.Errorf("%s is not drawn as %q:\n%q", name, want, view)
		}
	}

	m = New("Commands", iconItems(), th)
	m, _ = press(m, keyDown)

	panel := lipgloss.NewStyle().Background(ui.Panel)
	if want := panel.Foreground(ui.Muted).Render("\U000F0293"); !strings.Contains(m.View(40), want) {
		t.Errorf("unselected icon is not Muted")
	}
}

func TestCountTracksFilter(t *testing.T) {
	m := New("Commands", iconItems(), mustTheme(t, "catppuccin-mocha"))
	if v := ansi.Strip(m.View(40)); !strings.Contains(v, " 1 of 3 ") {
		t.Errorf("unfiltered count missing:\n%s", v)
	}

	m, _ = typeText(m, "quit")
	if v := ansi.Strip(m.View(40)); !strings.Contains(v, " 1 of 1 ") {
		t.Errorf("filtered count missing:\n%s", v)
	}

	m, _ = typeText(m, "zzz")
	if v := ansi.Strip(m.View(40)); !strings.Contains(v, " 0 of 0 ") {
		t.Errorf("empty count missing:\n%s", v)
	}
}

func TestNoIconsNoGap(t *testing.T) {
	m := New("Commands", testItems(), mustTheme(t, "catppuccin-mocha"))

	lines := strings.Split(ansi.Strip(m.View(40)), "\n")
	if !strings.HasPrefix(lines[3], "\u2502 Theme: Catppuccin Latte") {
		t.Errorf("row without an icon = %q, want the title right after one margin cell", lines[3])
	}
}

func TestMultibyteTitle(t *testing.T) {
	th := mustTheme(t, "catppuccin-mocha")

	m := New("Commands", []Item{
		{ID: "a", Title: "Theme: Catppuccin Latte"},
		{ID: "b", Title: "Theme: Catppuccin Frappé"},
	}, th)
	for _, r := range "frap" {
		m, _ = press(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	const width = 40

	view := m.View(width)
	if !strings.Contains(ansi.Strip(view), "Frappé") {
		t.Errorf("stripped view lacks Frappé:\n%s", ansi.Strip(view))
	}

	for i, l := range strings.Split(view, "\n") {
		if got := lipgloss.Width(l); got > width {
			t.Errorf("line %d width = %d, want at most %d", i, got, width)
		}
	}

	hit := lipgloss.NewStyle().Background(th.UI.Selection).Foreground(th.UI.Match).Bold(true)
	if want := hit.Render("Frap"); !strings.Contains(view, want) {
		t.Errorf("view lacks the highlighted run %q:\n%q", want, view)
	}
}

func TestFooterEnterLabel(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    Model
		want string
	}{
		{"default", newModel(t), "enter run"},
		{"labelled", newModel(t).WithEnterLabel("apply"), "enter apply"},
	} {
		view := ansi.Strip(tc.m.View(60))
		if !strings.Contains(view, tc.want) {
			t.Errorf("%s: footer lacks %q:\n%s", tc.name, tc.want, view)
		}
	}
}

func TestBox(t *testing.T) {
	th := mustTheme(t, "catppuccin-mocha")
	strip := ansi.Strip

	t.Run("every piece is as wide as the box", func(t *testing.T) {
		for _, w := range []int{2, 3, 4, 5, 9, 10, 40} {
			box := NewBox(th, w)

			pieces := map[string]string{
				"row":            box.Row("some content that is longer than a small box", box.On(th.UI.Fg)),
				"short row":      box.Row("", box.On(th.UI.Fg)),
				"top":            box.Top("A long title for a narrow box"),
				"divider":        box.Divider(),
				"bottom":         box.Bottom(" 3 of 9 "),
				"bottom no tail": box.Bottom(""),
			}
			for name, p := range pieces {
				if got := lipgloss.Width(p); got != w {
					t.Errorf("width %d: %s is %d cells wide: %q", w, name, got, strip(p))
				}
			}
		}
	})

	box := NewBox(th, 30)

	dashes := func(n int) string { return strings.Repeat("\u2500", n) }

	for _, tt := range []struct {
		name, got, want string
	}{
		{"row", strip(box.Row("hi", box.On(th.UI.Fg))), "\u2502hi" + strings.Repeat(" ", 26) + "\u2502"},
		{"top", strip(box.Top("Title")), "\u256d\u2500 Title " + dashes(20) + "\u256e"},
		{"divider", strip(box.Divider()), "\u251c" + dashes(28) + "\u2524"},
		{"bottom with count", strip(box.Bottom(" 3 of 9 ")), "\u2570" + dashes(19) + " 3 of 9 \u2500\u256f"},
		{"bottom without", strip(box.Bottom("")), "\u2570" + dashes(28) + "\u256f"},
		{"hints", strip(box.Hints([][2]string{{"up/down", "move"}, {"esc", "close"}})), " up/down move   esc close"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}

	// A count that does not fit is left out, never cut.
	if got := strip(NewBox(th, 8).Bottom(" 3 of 9 ")); got != "\u2570"+dashes(6)+"\u256f" {
		t.Errorf("bottom in a narrow box = %q, want no count", got)
	}

	// The keys are in Key bold and the labels in Muted, on the panel colour.
	rgb := func(h theme.Hex) string {
		r, g, b, _ := h.RGBA()

		return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
	}

	hints := box.Hints([][2]string{{"esc", "close"}})
	for _, want := range []string{"1;" + rgb(th.UI.Key) + ";48;2;", rgb(th.UI.Muted) + ";48;2;"} {
		if !strings.Contains(hints, want) {
			t.Errorf("hints = %q, want %q in it", hints, want)
		}
	}
}

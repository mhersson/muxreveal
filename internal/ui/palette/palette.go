// Package palette is a fuzzy-filtered list in a framed box, used for the
// command palette and the theme picker.
package palette

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"

	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// MaxRows is the most item rows shown at once; the list scrolls past it.
const MaxRows = 10

// Item is one selectable row. Hint is shown right-aligned, e.g. a key binding;
// Icon is a single-cell glyph before the title, or empty.
type Item struct{ ID, Title, Hint, Icon string }

// ChosenMsg is sent when the user confirms the selected item.
type ChosenMsg struct{ ID string }

// ClosedMsg is sent when the user dismisses the palette.
type ClosedMsg struct{}

// HighlightMsg is sent when the selected item changes.
type HighlightMsg struct{ ID string }

// match is a visible item and the byte offsets of its title the query matched.
type match struct {
	item    int
	matched map[int]bool
}

// Model is the palette state. It is a value type like other Bubble Tea models.
type Model struct {
	title   string
	items   []Item
	input   textinput.Model
	matches []match
	sel     int // index into matches, -1 when nothing matches
	top     int // first visible row of matches
	theme   theme.Theme
	icons   bool   // some item has an icon, so every row reserves its column
	enter   string // label of the enter hint in the footer
}

type titles []Item

func (t titles) String(i int) string { return t[i].Title }
func (t titles) Len() int            { return len(t) }

// New returns a palette listing items, with the first one selected. The title
// is set into the top border.
func New(title string, items []Item, t theme.Theme) Model {
	in := textinput.New()
	in.Prompt = "❯ "
	// A static cursor keeps View independent of blink timing.
	in.SetStyles(inputStyles(t))
	in.Focus()
	m := Model{title: title, items: items, input: in, theme: t, enter: "run"}
	for _, it := range items {
		m.icons = m.icons || it.Icon != ""
	}
	m.refilter()
	return m
}

// WithEnterLabel returns m with the footer's enter hint labelled label.
func (m Model) WithEnterLabel(label string) Model {
	m.enter = label
	return m
}

func inputStyles(t theme.Theme) textinput.Styles {
	ui := t.UI
	onPanel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	state := textinput.StyleState{
		Text:        onPanel,
		Prompt:      onPanel.Foreground(ui.Accent),
		Placeholder: onPanel.Foreground(ui.Subtle),
		Suggestion:  onPanel.Foreground(ui.Subtle),
	}
	return textinput.Styles{
		Focused: state,
		Blurred: state,
		Cursor:  textinput.CursorStyle{Color: ui.Fg, Shape: tea.CursorBlock, Blink: false},
	}
}

// Init has no startup work; the parent model drives the palette.
func (m Model) Init() tea.Cmd { return nil }

// SetTheme recolours the palette.
func (m *Model) SetTheme(t theme.Theme) {
	m.theme = t
	m.input.SetStyles(inputStyles(t))
}

// Selected returns the highlighted item, if any item matches.
func (m Model) Selected() (Item, bool) {
	if m.sel < 0 || m.sel >= len(m.matches) {
		return Item{}, false
	}
	return m.items[m.matches[m.sel].item], true
}

// Select highlights the item with id; it is a no-op if that item is not visible.
func (m *Model) Select(id string) {
	for i, mt := range m.matches {
		if m.items[mt.item].ID == id {
			m.sel = i
			m.scrollToSel()
			return
		}
	}
}

func (m Model) visibleIDs() []string {
	var ids []string
	for _, mt := range m.matches {
		ids = append(ids, m.items[mt.item].ID)
	}
	return ids
}

func (m Model) selectedID() string {
	if it, ok := m.Selected(); ok {
		return it.ID
	}
	return ""
}

// refilter rebuilds matches from the query and selects the first one.
func (m *Model) refilter() {
	m.matches = m.matches[:0:0]
	if q := m.input.Value(); q == "" {
		for i := range m.items {
			m.matches = append(m.matches, match{item: i})
		}
	} else {
		for _, r := range fuzzy.FindFrom(q, titles(m.items)) {
			set := make(map[int]bool, len(r.MatchedIndexes))
			for _, ix := range r.MatchedIndexes {
				set[ix] = true
			}
			m.matches = append(m.matches, match{item: r.Index, matched: set})
		}
	}
	m.sel, m.top = -1, 0
	if len(m.matches) > 0 {
		m.sel = 0
	}
}

func (m *Model) scrollToSel() {
	if m.sel < 0 {
		return
	}
	if m.sel < m.top {
		m.top = m.sel
	}
	if m.sel >= m.top+MaxRows {
		m.top = m.sel - MaxRows + 1
	}
}

func (m *Model) move(delta int) {
	n := len(m.matches)
	if n == 0 {
		return
	}
	m.sel = ((m.sel+delta)%n + n) % n
	m.scrollToSel()
}

func highlight(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	return func() tea.Msg { return HighlightMsg{ID: id} }
}

// Update handles keys and returns a cmd carrying ChosenMsg, ClosedMsg or
// HighlightMsg, batched with the input's own commands.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	prev := m.selectedID()
	key, isKey := msg.(tea.KeyPressMsg)
	if isKey {
		switch key.String() {
		case "down", "ctrl+n", "tab":
			m.move(1)
			return m, m.changed(prev)
		case "up", "ctrl+p", "shift+tab":
			m.move(-1)
			return m, m.changed(prev)
		case "enter":
			if id := m.selectedID(); id != "" {
				return m, func() tea.Msg { return ChosenMsg{ID: id} }
			}
			return m, nil
		case "esc":
			return m, func() tea.Msg { return ClosedMsg{} }
		}
	}
	oldQuery := m.input.Value()
	var inCmd tea.Cmd
	m.input, inCmd = m.input.Update(msg)
	if m.input.Value() == oldQuery {
		return m, inCmd
	}
	m.refilter()
	return m, tea.Batch(inCmd, m.changed(prev))
}

// changed returns a HighlightMsg cmd if the selected item differs from prev.
func (m Model) changed(prev string) tea.Cmd {
	if id := m.selectedID(); id != prev {
		return highlight(id)
	}
	return nil
}

// View draws the box exactly width cells wide, clipping content instead of
// wrapping. It returns "" when width < 2.
func (m Model) View(width int) string {
	if width < 2 {
		return ""
	}
	ui := m.theme.UI
	inner := width - 2
	panel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	border := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.BorderFocus)

	// fit clips content to the inner width and pads it with fill's background.
	fit := func(content string, fill lipgloss.Style) string {
		content = ansi.Truncate(content, inner, "")
		pad := fill.Render(strings.Repeat(" ", max(0, inner-lipgloss.Width(content))))
		return border.Render("│") + content + pad + border.Render("│")
	}

	// The title starts two cells from the corner: the corner, one dash, then the title.
	dash := min(1, inner)
	title := ansi.Truncate(" "+m.title+" ", inner-dash, "")
	top := border.Render("╭"+strings.Repeat("─", dash)) +
		panel.Foreground(ui.Accent).Bold(true).Render(title) +
		border.Render(strings.Repeat("─", inner-dash-lipgloss.Width(title))+"╮")
	lines := []string{top}

	in := m.input
	in.SetWidth(max(1, inner-4))
	lines = append(lines, fit(panel.Render(" ")+in.View(), panel))
	lines = append(lines, border.Render("├"+strings.Repeat("─", inner)+"┤"))

	if len(m.matches) == 0 {
		lines = append(lines, fit(panel.Render(" ")+panel.Foreground(ui.Subtle).Render("No matches"), panel))
	}
	end := min(len(m.matches), m.top+MaxRows)
	for i := m.top; i < end; i++ {
		lines = append(lines, m.row(i, inner))
	}

	lines = append(lines, border.Render("├"+strings.Repeat("─", inner)+"┤"), fit(m.footer(panel), panel))

	// The count ends one dash before the corner; it is left out when it does not fit.
	count := " " + strconv.Itoa(max(m.sel+1, 0)) + " of " + strconv.Itoa(len(m.matches)) + " "
	cw := lipgloss.Width(count)
	if inner >= cw+1 {
		lines = append(lines, border.Render("╰"+strings.Repeat("─", inner-cw-1))+
			panel.Foreground(ui.Muted).Render(count)+border.Render("─╯"))
	} else {
		lines = append(lines, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
	}
	return strings.Join(lines, "\n")
}

// footer is the key hint row: each key in Key bold, its label in Muted, the
// pairs three cells apart, after one cell of margin.
func (m Model) footer(panel lipgloss.Style) string {
	ui := m.theme.UI
	var sb strings.Builder
	sb.WriteString(panel.Render(" "))
	for i, h := range [][2]string{{"up/down", "move"}, {"enter", m.enter}, {"esc", "close"}} {
		if i > 0 {
			sb.WriteString(panel.Render("   "))
		}
		sb.WriteString(panel.Foreground(ui.Key).Bold(true).Render(h[0]))
		sb.WriteString(panel.Foreground(ui.Muted).Render(" " + h[1]))
	}
	return sb.String()
}

// row draws match i: icon, title with matched runes emphasised, and the hint
// right-aligned.
func (m Model) row(i, inner int) string {
	ui := m.theme.UI
	base := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	selected := i == m.sel
	icon := base.Foreground(ui.Muted)
	title := base
	if selected {
		base = base.Background(ui.Selection)
		icon = base.Foreground(ui.Accent)
		title = base.Bold(true)
	}
	hit := base.Foreground(ui.Match).Bold(true)
	border := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.BorderFocus)

	mt := m.matches[i]
	it := m.items[mt.item]
	avail := max(0, inner-2) // one cell of margin on each side

	lead := ""
	if m.icons {
		lead = icon.Render(" ")
		if it.Icon != "" {
			lead = icon.Render(it.Icon)
		}
		lead += base.Render(" ")
		avail = max(0, avail-2)
	}

	hint := it.Hint
	if hint != "" && lipgloss.Width(it.Title)+1+lipgloss.Width(hint) > avail {
		hint = ""
	}
	titleAvail := avail
	if hint != "" {
		titleAvail = avail - lipgloss.Width(hint) - 1
	}

	var sb, run strings.Builder
	used, runHit := 0, false
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if runHit {
			sb.WriteString(hit.Render(run.String()))
		} else {
			sb.WriteString(title.Render(run.String()))
		}
		run.Reset()
	}
	for bi, r := range it.Title {
		w := lipgloss.Width(string(r))
		if used+w > titleAvail {
			break
		}
		used += w
		if mt.matched[bi] != runHit {
			flush()
			runHit = mt.matched[bi]
		}
		run.WriteRune(r)
	}
	flush()
	content := base.Render(" ") + lead + sb.String()
	if hint != "" {
		content += base.Render(strings.Repeat(" ", avail-used-lipgloss.Width(hint))) +
			base.Foreground(ui.Key).Bold(true).Render(hint)
	}
	content = ansi.Truncate(content, inner, "")
	pad := base.Render(strings.Repeat(" ", max(0, inner-lipgloss.Width(content))))
	return border.Render("│") + content + pad + border.Render("│")
}

package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const welcomeSample = `package main

// Agents run in panes; the review sits beside them.
func review(turn int) string {
	return fmt.Sprintf("turn %d ready", turn)
}
`

func renderBody(width, height int, st styles, sample []string, hints []hint) string {
	card := welcomeCard(st, sample, hints)
	if lipgloss.Width(card) > width || lipgloss.Height(card) > height {
		card = ""
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, card,
		lipgloss.WithWhitespaceStyle(st.body))
}

func welcomeCard(st styles, sample []string, hints []hint) string {
	lines := []string{
		st.cardTitle.Render("MuxReveal"),
		st.cardMuted.Render("No panes yet."),
		"",
	}
	keyW := 0
	for _, h := range hints {
		keyW = max(keyW, lipgloss.Width(h.key))
	}
	for _, h := range hints {
		lines = append(lines, st.cardKey.Render(h.key)+
			st.cardFiller.Render(strings.Repeat(" ", keyW-lipgloss.Width(h.key)+2)+h.title))
	}
	if len(hints) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, sample...)
	inner := 0
	for _, l := range lines {
		inner = max(inner, lipgloss.Width(l))
	}
	const padX = 1
	for i, l := range lines {
		pad := inner - lipgloss.Width(l)
		lines[i] = st.cardFiller.Render(strings.Repeat(" ", padX)) + l +
			st.cardFiller.Render(strings.Repeat(" ", pad+padX))
	}
	return st.card.Render(strings.Join(lines, "\n"))
}

// legendItem is one status bar legend pair: the command whose key it shows
// and its short label.
type legendItem struct{ command, label string }

func legendItems() []legendItem {
	return []legendItem{
		{"palette.open", "palette"},
		{"tab.new", "new tab"},
		{"tab.rename", "rename"},
		{"pane.zoom", "zoom"},
		{"app.quit", "quit"},
	}
}

// Status bar spacing: the legend keeps this many cells before the right side
// and reserves legendRoom cells when the right side is sized.
const (
	legendGap  = 3
	legendRoom = 40
	rightGap   = 2
	// minRight is the least text worth showing: the icon, a space, one
	// character and the ellipsis.
	minRight = 4
)

type legendPair struct{ key, label string }

// prefixLegend lists the keys that finish a sequence started by prefix; ""
// is the bare leader.
func prefixLegend(prefix string) []legendPair {
	switch prefix {
	case ">", ".":
		return []legendPair{{"1-9", "tab"}, {"esc", "cancel"}}
	case ";":
		return []legendPair{{"h j k l", "side"}, {"esc", "clear"}}
	}

	return []legendPair{{"esc", "cancel"}}
}

// statusMode is the status bar's leftmost segment.
type statusMode struct {
	bg         theme.Hex
	icon, text string
}

// statusModeOf picks the mode segment for the current input state.
func (a *App) statusModeOf(ui theme.UI) statusMode {
	switch {
	case a.prompt != nil && a.prompt.kind == promptSearch:
		return statusMode{bg: ui.Info, icon: iconSearch, text: "Search"}
	case a.prompt != nil:
		return statusMode{bg: ui.Accent, icon: iconRename, text: "Rename tab"}
	case a.copy != nil:
		return statusMode{bg: ui.Info, icon: iconCopy, text: "COPY"}
	case a.pending && a.prefix != "":
		return statusMode{bg: ui.Warning, icon: iconLeader, text: a.keymap.leader + " " + a.prefix}
	case a.pending:
		return statusMode{bg: ui.Warning, icon: iconLeader, text: a.keymap.leader}
	}

	return statusMode{bg: ui.Accent, icon: iconApp, text: "MuxReveal"}
}

// modeLabel is the mode segment's text and the cells it takes including the
// edge and the blank after it.
func (a *App) modeLabel(m statusMode) (label string, cells int) {
	label = " "
	if a.icons {
		label += m.icon + " "
	}

	label += m.text + " "
	cells = ansi.StringWidth(label) + 1

	if a.icons {
		cells++
	}

	return label, cells
}

// searchCursorX is the column of the search input's cursor in the status bar;
// false when the text leaves no room for it.
func (a *App) searchCursorX() (int, bool) {
	_, cells := a.modeLabel(a.statusModeOf(a.current().theme.UI))
	in := a.prompt.input
	x := cells + ansi.StringWidth(printable(string([]rune(in.Value())[:in.Position()])))

	return x, x < a.width
}

// statusBar draws the last row: the mode segment, the key legend and, right
// aligned, the notice or the focused pane's cwd. It is exactly width cells.
func (a *App) statusBar(width int, st core.State) string {
	if width <= 0 {
		return ""
	}

	ui := a.current().theme.UI
	ground := lipgloss.NewStyle().Background(ui.Bar)
	blanks := func(n int) string {
		if n <= 0 {
			return ""
		}

		return ground.Render(strings.Repeat(" ", n))
	}

	m := a.statusModeOf(ui)
	label, modeW := a.modeLabel(m)
	mode := paint(ui.OnAccent, m.bg, true).Render(label)

	if a.icons {
		mode += paint(m.bg, ui.Bar, false).Render(edgeSolid)
	}

	mode += blanks(1)

	if width <= modeW {
		return ansi.Truncate(mode, width, "")
	}

	lead := a.keymap.leader + " then"
	leadW := 0

	if !a.pending && a.prompt == nil && a.copy == nil {
		leadW = ansi.StringWidth(lead) + 2
	}

	// A notice takes the room it needs before the legend; the cwd leaves the
	// lead-in and legendRoom cells.
	room := width - modeW - leadW - legendRoom - 1
	if printable(a.notice) != "" {
		room = width - modeW - 1
	}

	right, rightW := a.statusRight(st, ui, room)
	legendEnd := width

	if rightW > 0 {
		legendEnd = width - rightW - rightGap
	}

	var sb strings.Builder

	sb.WriteString(mode)

	x := modeW
	// put appends s after sep blank cells when it still ends within legendEnd.
	put := func(sep int, s string, w int) bool {
		if x+sep+w > legendEnd {
			return false
		}

		sb.WriteString(blanks(sep))
		sb.WriteString(s)

		x += sep + w

		return true
	}

	pair := func(key, action string) (string, int) {
		return paint(ui.Key, ui.Bar, true).Render(key) + blanks(1) + paint(ui.Muted, ui.Bar, false).Render(action),
			ansi.StringWidth(key) + 1 + ansi.StringWidth(action)
	}

	var pairs []legendPair

	sep := 0

	switch {
	case a.prompt != nil && a.prompt.kind == promptSearch:
		text := ansi.Truncate(printable(a.prompt.input.Value()), max(legendEnd-x, 0), "")
		put(0, paint(ui.Fg, ui.Bar, false).Render(text), ansi.StringWidth(text))
	case a.prompt != nil:
		pairs = []legendPair{{"enter", "save"}, {"esc", "cancel"}, {"empty", "automatic name"}}
	case a.copy != nil:
		pairs = []legendPair{{"v", "select"}, {"y", "copy"}, {"/", "search"}, {"n", "next"}, {"q", "exit"}}
	case a.pending:
		pairs = prefixLegend(a.prefix)
	case put(0, paint(ui.Subtle, ui.Bar, false).Render(lead), ansi.StringWidth(lead)):
		sep = 2

		for _, it := range legendItems() {
			if key, ok := a.keymap.keyOf(it.command); ok {
				pairs = append(pairs, legendPair{key, it.label})
			}
		}
	}

	for _, p := range pairs {
		s, w := pair(p.key, p.label)
		if !put(sep, s, w) {
			break
		}

		sep = legendGap
	}

	sb.WriteString(blanks(width - x - rightW))
	sb.WriteString(right)

	return sb.String()
}

// tildeHome replaces a leading home directory in path with ~. A path that only
// shares home as a string prefix, and an empty home, are left alone.
func tildeHome(path, home string) string {
	home = strings.TrimRight(home, "/")
	switch {
	case home == "":
		return path
	case path == home:
		return "~"
	case strings.HasPrefix(path, home+"/"):
		return "~" + path[len(home):]
	}
	return path
}

// statusRight is the notice, else the focused pane's cwd, cut to room cells
// and followed by one blank cell at the edge. It is empty when nothing fits.
func (a *App) statusRight(st core.State, ui theme.UI, room int) (string, int) {
	text, fg, icon := printable(a.notice), ui.Warning, iconAlert

	if text == "" {
		text, fg, icon = tildeHome(printable(st.Panes[st.Tab().Focus].Cwd), a.getenv("HOME")), ui.Muted, iconFolder
	}

	if text == "" || room < minRight {
		return "", 0
	}

	if a.icons {
		text = icon + " " + text
	}

	text = ansi.Truncate(text, room, "…")
	ground := lipgloss.NewStyle().Background(ui.Bar)

	return paint(fg, ui.Bar, false).Render(text) + ground.Render(" "), ansi.StringWidth(text) + 1
}

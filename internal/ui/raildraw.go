package ui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const iconSearch = "\U000F0349" // nf-md-magnify

// railRun is a piece of rail text in one colour on the panes' ground.
type railRun struct {
	text string
	fg   theme.Hex
	bold bool
}

func runsWidth(runs []railRun) int {
	w := 0
	for _, r := range runs {
		w += ansi.StringWidth(r.text)
	}

	return w
}

// cutRuns cuts runs to w cells by display width, ending in … when anything is
// left out. A wide rune is never split.
func cutRuns(runs []railRun, w int) []railRun {
	if w <= 0 {
		return nil
	}

	if runsWidth(runs) <= w {
		return runs
	}

	var out []railRun

	budget := w - 1

	for _, r := range runs {
		rw := ansi.StringWidth(r.text)
		if rw <= budget {
			out = append(out, r)
			budget -= rw

			continue
		}

		cut := ansi.Truncate(r.text, budget, "")
		if cut != "" {
			out = append(out, railRun{text: cut, fg: r.fg, bold: r.bold})
		}

		out = append(out, railRun{text: "…", fg: r.fg, bold: r.bold})

		return out
	}

	return out
}

// hiRuns splits text into runs, the matched rune ranges in Match bold.
func hiRuns(text string, hits []railHit, fg, match theme.Hex, bold bool) []railRun {
	if len(hits) == 0 {
		return []railRun{{text: text, fg: fg, bold: bold}}
	}

	r := []rune(text)
	mark := make([]bool, len(r))

	for _, h := range hits {
		for i := max(h.start, 0); i < min(h.end, len(r)); i++ {
			mark[i] = true
		}
	}

	var out []railRun

	for i := 0; i < len(r); {
		j := i
		for j < len(r) && mark[j] == mark[i] {
			j++
		}

		if mark[i] {
			out = append(out, railRun{text: string(r[i:j]), fg: match, bold: true})
		} else {
			out = append(out, railRun{text: string(r[i:j]), fg: fg, bold: bold})
		}

		i = j
	}

	return out
}

func hitsOf(hits []railHit, f railField) []railHit {
	var out []railHit

	for _, h := range hits {
		if h.field == f {
			out = append(out, h)
		}
	}

	return out
}

// railPainter renders runs on the panes' ground.
type railPainter struct {
	bg theme.Hex
}

// line renders runs cut and padded to exactly w cells.
func (p railPainter) line(runs []railRun, w int) string {
	runs = cutRuns(runs, w)

	var sb strings.Builder

	for _, r := range runs {
		sb.WriteString(paint(r.fg, p.bg, r.bold).Render(r.text))
	}

	if pad := w - runsWidth(runs); pad > 0 {
		sb.WriteString(lipgloss.NewStyle().Background(p.bg).Render(strings.Repeat(" ", pad)))
	}

	return sb.String()
}

// railLines draws the rail box: exactly its height in lines of exactly its width.
func (a *App) railLines(st core.State, lk look, v railView) []string {
	ui := lk.theme.UI
	box := v.geom.box
	p := railPainter{bg: ui.Bg}

	edgeFg := ui.Border
	if a.rail.focused {
		edgeFg = ui.Accent
	}

	cw := max(box.W-4, 0) // content columns; the edge columns lie either side
	lines := make([]string, 0, box.H)

	lines = append(lines, a.railTop(st, ui, box.W, edgeFg))

	side := paint(edgeFg, ui.Bg, false).Render("│")
	inner := make([]string, max(box.H-2, 0))
	blank := p.line(nil, box.W-2)

	for i := range inner {
		inner[i] = blank
	}

	set := func(y int, s string) {
		if i := y - (box.Y + 1); i >= 0 && i < len(inner) {
			inner[i] = s
		}
	}

	if y := v.geom.searchY; y >= 0 {
		set(y, a.railSearchRow(ui, p, box.W-2, cw))
	}

	a.railListRows(st, ui, p, v, cw, set)

	if y := v.geom.hintY; y >= 0 {
		set(y, p.line(append([]railRun{{text: " "}}, railHints(ui, cw)...), box.W-2))
	}

	for _, s := range inner {
		lines = append(lines, side+s+side)
	}

	if box.H >= 2 {
		above, below := railHidden(v.rows, v.geom.list.H, v.top)
		lines = append(lines, railBottom(ui, box.W, edgeFg, above, below))
	}

	return lines[:min(len(lines), box.H)]
}

// railTop is the top border with the title and the per-state counts.
func (a *App) railTop(st core.State, ui theme.UI, w int, edgeFg theme.Hex) string {
	border := paint(edgeFg, ui.Bg, false)
	if w < 2 {
		return border.Render(strings.Repeat("─", max(w, 0)))
	}

	word := ui.Fg
	if a.rail.focused {
		word = ui.Accent
	}

	ids := st.AgentPanes()
	title := []railRun{{text: " "}}

	if a.icons {
		title = append(title, railRun{text: iconAgent, fg: ui.Agent, bold: true}, railRun{text: " "})
	}

	title = append(title,
		railRun{text: "Agents", fg: word, bold: true},
		railRun{text: " ", fg: ui.Fg},
		railRun{text: strconv.Itoa(len(ids)), fg: ui.Muted},
		railRun{text: " ", fg: ui.Fg},
	)

	var counts [3]int

	for _, id := range ids {
		switch st.Panes[id].State {
		case core.AgentBlocked:
			counts[0]++
		case core.AgentWorking:
			counts[1]++
		case core.AgentIdle:
			counts[2]++
		}
	}

	spin := spinFrame(a.spin)
	glyphs := [3]string{iconBlocked, spin, iconIdle}
	words := [3]string{"blocked ", spin, "idle "}
	cols := [3]theme.Hex{ui.Blocked, ui.Working, ui.Idle}

	var tally []railRun

	for i, n := range counts {
		if n == 0 {
			continue
		}

		g := glyphs[i]
		if !a.icons {
			g = words[i]
		}

		tally = append(tally, railRun{text: " ", fg: ui.Fg}, railRun{text: g + strconv.Itoa(n), fg: cols[i], bold: true})
	}

	if len(tally) > 0 {
		tally = append(tally, railRun{text: " ", fg: ui.Fg})
	}

	// The corner and a dash take two cells on each side.
	avail := w - 4
	p := railPainter{bg: ui.Bg}

	if avail < titleRoomMin {
		return border.Render("╭" + strings.Repeat("─", w-2) + "╮")
	}

	title = cutRuns(title, avail)
	tw := runsWidth(title)

	var sb strings.Builder

	sb.WriteString(border.Render("╭─"))
	sb.WriteString(p.line(title, tw))

	if cw := runsWidth(tally); cw > 0 && tw+1+cw <= avail {
		sb.WriteString(border.Render(strings.Repeat("─", avail-tw-cw)))
		sb.WriteString(p.line(tally, cw))
	} else {
		sb.WriteString(border.Render(strings.Repeat("─", avail-tw)))
	}

	sb.WriteString(border.Render("─╮"))

	return sb.String()
}

// railSearchRow is the search row's inner content: the icon and the text, one
// cell in from the edge column.
func (a *App) railSearchRow(ui theme.UI, p railPainter, inner, cw int) string {
	mark, fg := "/", ui.Muted
	if a.icons {
		mark = iconSearch
	}

	if a.rail.searching {
		fg = ui.Accent
	}

	text := ansi.Truncate(printable(a.rail.query), max(cw-3, 0), "")
	runs := []railRun{{text: " "}, {text: mark, fg: fg, bold: true}, {text: " ", fg: ui.Fg}, {text: text, fg: ui.Fg}}

	return p.line(runs, inner)
}

// railHints are the key hints that fit cw cells; pairs that do not are dropped from the end.
func railHints(ui theme.UI, cw int) []railRun {
	pairs := [...][2]string{{"j/k", "move"}, {"enter", "jump"}, {"/", "search"}, {"esc", "back"}}

	var out []railRun

	used := 0

	for i, kv := range pairs {
		need := ansi.StringWidth(kv[0]) + 1 + ansi.StringWidth(kv[1])
		if i > 0 {
			need += 2
		}

		if used+need > cw {
			break
		}

		if i > 0 {
			out = append(out, railRun{text: "  ", fg: ui.Muted})
		}

		out = append(out, railRun{text: kv[0], fg: ui.Key, bold: true}, railRun{text: " " + kv[1], fg: ui.Muted})
		used += need
	}

	return out
}

// railListRows draws the visible rows of the list, and the empty-state text.
func (a *App) railListRows(st core.State, ui theme.UI, p railPainter, v railView, cw int, set func(y int, s string)) {
	l := v.geom.list
	inner := v.geom.box.W - 2
	accent := func(s string) []railRun { return []railRun{{text: s, fg: ui.Accent}} }

	// edge builds an inner row from its edge cells and content.
	edge := func(left, right []railRun, content []railRun) string {
		return p.line(left, 1) + p.line(content, cw) + p.line(right, inner-1-cw)
	}

	if len(v.shown) == 0 {
		text := "no agent matches"
		if len(st.AgentPanes()) == 0 {
			text = "no running agents"
		}

		set(l.Y, p.line([]railRun{{text: " "}, {text: text, fg: ui.Subtle}}, inner))

		return
	}

	// The matcher runs once per card, not once per line.
	cached := make([][]railHit, len(v.shown))
	done := make([]bool, len(v.shown))
	hitsFor := func(i int) []railHit {
		if !done[i] {
			cached[i], _ = v.shown[i].matches(a.rail.query)
			done[i] = true
		}

		return cached[i]
	}

	for i := range l.H {
		idx := v.top + i
		if idx >= len(v.rows) {
			break
		}

		row := v.rows[idx]
		y := l.Y + i

		switch row.kind {
		case railRowHeader:
			set(y, edge(nil, nil, a.railGroupHeader(ui, v.groups[row.group], cw)))
		case railRowBlank:
			selAbove := idx+1 < len(v.rows) && v.rows[idx+1].kind == railRowCard && v.rows[idx+1].card == v.sel
			selBelow := idx > 0 && v.rows[idx-1].kind == railRowCard && v.rows[idx-1].card == v.sel

			switch {
			case selAbove:
				set(y, p.line(accent("╭"+strings.Repeat("─", max(inner-2, 0))+"╮"), inner))
			case selBelow:
				set(y, p.line(accent("╰"+strings.Repeat("─", max(inner-2, 0))+"╯"), inner))
			}
		case railRowCard:
			c := v.shown[row.card]
			left, right := []railRun(nil), []railRun(nil)

			switch {
			case row.card == v.sel:
				left, right = accent("│"), accent("│")
			case c.state == core.AgentBlocked && row.line == 0:
				bar := ui.Blocked
				if a.rail.pulseDim {
					bar = ui.Border
				}

				left = []railRun{{text: "▎", fg: bar}}
			}

			set(y, edge(left, right, a.railCardLine(ui, c, hitsFor(row.card), row.line, cw)))
		}
	}
}

// railGroupHeader is a group's label, a rule and its count.
func (a *App) railGroupHeader(ui theme.UI, g railGroup, cw int) []railRun {
	label := map[core.AgentState]string{
		core.AgentBlocked: "Blocked", core.AgentWorking: "Working", core.AgentIdle: "Idle", core.AgentUnknown: "No hooks",
	}[g.state]
	count := strconv.Itoa(len(g.cards))
	rule := max(cw-ansi.StringWidth(label)-2-len(count), 0)

	return []railRun{
		{text: label, fg: stateColour(g.state, ui, ui.Muted), bold: true},
		{text: " ", fg: ui.Fg},
		{text: strings.Repeat("─", rule), fg: ui.Border},
		{text: " ", fg: ui.Fg},
		{text: count, fg: ui.Muted},
	}
}

// railCardLine is line n (0 to 3) of a card, as runs; the drawing cuts it to cw.
func (a *App) railCardLine(ui theme.UI, c railCard, hits []railHit, n, cw int) []railRun {
	indent := railRun{text: "  ", fg: ui.Fg}

	switch n {
	case 0:
		return a.railTitleLine(ui, c, hits, cw)
	case 1:
		if c.branch == "" {
			return nil
		}

		runs := []railRun{indent}
		if a.icons {
			runs = append(runs, railRun{text: iconGit, fg: ui.Git}, railRun{text: " ", fg: ui.Fg})
		}

		return append(runs, hiRuns(c.branch, hitsOf(hits, railFieldBranch), ui.Dim, ui.Match, false)...)
	case 2:
		fg, bold := ui.Muted, false
		if c.state == core.AgentBlocked {
			fg, bold = ui.Blocked, true
		}

		return append([]railRun{indent}, hiRuns(c.action, hitsOf(hits, railFieldAction), fg, ui.Match, bold)...)
	}

	if !c.statsKnown {
		return nil
	}

	files := strconv.Itoa(c.stats.Files) + " files"
	if c.stats.Files == 1 {
		files = "1 file"
	}

	return []railRun{
		indent,
		{text: "+" + strconv.Itoa(c.stats.Added), fg: ui.Success},
		{text: " ", fg: ui.Fg},
		{text: "-" + strconv.Itoa(c.stats.Removed), fg: ui.Error},
		{text: "  ", fg: ui.Fg},
		{text: files, fg: ui.Subtle},
	}
}

// railTitleLine is a card's first line: state glyph, project, age and the unseen dot.
func (a *App) railTitleLine(ui theme.UI, c railCard, hits []railHit, cw int) []railRun {
	glyph := railRun{text: " ", fg: ui.Fg}
	spin := spinFrame(a.spin)

	switch c.state {
	case core.AgentWorking:
		glyph = railRun{text: spin, fg: ui.Working, bold: true}
	case core.AgentBlocked:
		if a.icons {
			glyph = railRun{text: iconBlocked, fg: ui.Blocked, bold: true}
		}
	case core.AgentIdle:
		if a.icons {
			glyph = railRun{text: iconIdle, fg: ui.Idle, bold: true}
		}
	default:
		if a.icons {
			glyph = railRun{text: iconAgent, fg: ui.Muted}
		}
	}

	var right []railRun

	if age := railAge(c.active, a.clock.Now()); age != "" {
		right = append(right, railRun{text: age, fg: ui.Subtle})
	}

	if c.unseen {
		dot := "●"
		if len(right) > 0 {
			dot = " " + dot
		}

		right = append(right, railRun{text: dot, fg: ui.Accent})
	}

	room := cw
	if rw := runsWidth(right); rw > 0 {
		room = cw - rw - 1
	}

	left := cutRuns(append([]railRun{glyph, {text: " ", fg: ui.Fg}}, hiRuns(c.project, hitsOf(hits, railFieldProject), ui.Fg, ui.Match, true)...), room)
	pad := cw - runsWidth(left) - runsWidth(right)

	if len(right) == 0 || pad < 1 {
		return left
	}

	return append(append(left, railRun{text: strings.Repeat(" ", pad), fg: ui.Fg}), right...)
}

// railCursor is the terminal cursor in the search row while the rail has the
// keyboard and the search is open.
func (a *App) railCursor(st core.State) *tea.Cursor {
	if !a.rail.focused || !a.rail.searching || !a.railShows(st.Rail) {
		return nil
	}

	box := bodyRect(a.width, a.height)
	box.W = railWidthFor(box)
	g := railGeometry(box, true)

	cw := max(box.W-4, 0)
	text := ansi.Truncate(printable(a.rail.query), max(cw-3, 0), "")

	return tea.NewCursor(box.X+2+2+ansi.StringWidth(text), g.searchY)
}

// railBottom is the bottom border with the counts of cards hidden above (left)
// and below (right); a count that does not fit between the corners is left out.
func railBottom(ui theme.UI, w int, edgeFg theme.Hex, above, below int) string {
	border := paint(edgeFg, ui.Bg, false)
	count := paint(ui.Accent, ui.Bg, true)
	room := max(w-2, 0)

	var left, right string

	if above > 0 {
		left = " \u2191 " + strconv.Itoa(above) + " "
	}

	if below > 0 {
		right = " " + strconv.Itoa(below) + " \u2193 "
	}

	// Each count has a dash on its outer side.
	if left != "" && 1+ansi.StringWidth(left) > room {
		left = ""
	}

	if right != "" && (1+ansi.StringWidth(right))+boolInt(left != "")*(1+ansi.StringWidth(left)) > room {
		right = ""
	}

	lw, rw := 0, 0
	if left != "" {
		lw = 1 + ansi.StringWidth(left)
	}

	if right != "" {
		rw = 1 + ansi.StringWidth(right)
	}

	dashes := strings.Repeat("─", room-lw-rw)

	if left == "" && right == "" {
		return border.Render("╰" + dashes + "╯")
	}

	var sb strings.Builder

	if left != "" {
		sb.WriteString(border.Render("╰─") + count.Render(left))
		sb.WriteString(border.Render(dashes))
	} else {
		sb.WriteString(border.Render("╰" + dashes))
	}

	if right != "" {
		sb.WriteString(count.Render(right) + border.Render("─╯"))
	} else {
		sb.WriteString(border.Render("╯"))
	}

	return sb.String()
}

func boolInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

// railKey is the rail picture's inputs that are plain fields. The core state
// is covered by its dispatch counter instead of by value, and the agents by
// railAgentKey: a frame is keyed without cloning the state or listing cards.
type railKey struct {
	storeVer  uint64
	w, h      int
	theme     string
	icons     bool
	focused   bool
	searching bool
	query     string
	sel       core.PaneID
	top       int
	spin      int
	pulseDim  bool
	renaming  bool
	rename    string
}

// railAgentKey is what an agent's card takes from outside the core state: the
// unseen mark, the probe results and the age as displayed (its unit and count,
// so an age that renders the same does not rebuild).
type railAgentKey struct {
	unseen  bool
	probe   railProbe
	ageN    int
	ageUnit byte
}

// railFitKey is the inputs of railFit. Without a search the cards' text does
// not matter to it, only which agents exist.
type railFitKey struct {
	storeVer  uint64
	w, h      int
	searching bool
	filtered  bool // the query is not empty
	sel       core.PaneID
	selIdx    int
	top       int
}

// railCache holds the last rail picture with the key it was drawn for, so a
// frame in which nothing in the rail changed reuses its lines.
type railCache struct {
	valid  bool
	key    railKey
	agents []railAgentKey
	spare  []railAgentKey
	ids    []core.PaneID // the agents of the build, valid while key.storeVer holds
	lines  []string
	width  int

	fit      railFitKey
	fitValid bool

	builds int // rebuilds of the lines, for tests
	fits   int // runs of railFit, for tests
}

func (a *App) railKeyOf(lk look) railKey {
	k := railKey{
		storeVer:  a.storeVer,
		w:         a.width,
		h:         a.height,
		theme:     lk.theme.Name,
		icons:     a.icons,
		focused:   a.rail.focused,
		searching: a.rail.searching,
		query:     a.rail.query,
		sel:       a.rail.sel,
		top:       a.rail.top,
		spin:      a.spin,
		pulseDim:  a.rail.pulseDim,
	}

	if a.prompt != nil && a.prompt.kind == promptRename {
		k.renaming, k.rename = true, a.prompt.input.Value()
	}

	return k
}

// railAgentKeys appends the keys of the agents ids, reading the memory the
// cards are built from.
func (a *App) railAgentKeys(dst []railAgentKey, ids []core.PaneID, now time.Time) []railAgentKey {
	for _, id := range ids {
		var k railAgentKey

		var active time.Time

		if rp := a.rail.panes[id]; rp != nil {
			k.unseen, k.probe, active = rp.unseen, rp.probe, rp.hookAt
		}

		if p := a.panes[id]; p != nil && p.lastOut.After(active) {
			active = p.lastOut
		}

		k.ageN, k.ageUnit = railAgeOf(active, now)
		dst = append(dst, k)
	}

	return dst
}

// railFrame is the rail's lines and width for this frame; (nil, 0) while the
// rail is hidden. The lines are rebuilt only when the key or an agent's key
// differs from the last build's; the cache is a memo of a pure function.
func (a *App) railFrame(st core.State, lk look) ([]string, int) {
	c := &a.rail.cache

	if !a.railShows(st.Rail) {
		*c = railCache{builds: c.builds, fits: c.fits, fit: c.fit, fitValid: c.fitValid}

		return nil, 0
	}

	now := a.clock.Now()
	key := a.railKeyOf(lk)

	if c.valid && key == c.key {
		c.spare = a.railAgentKeys(c.spare[:0], c.ids, now)
		if slices.Equal(c.spare, c.agents) {
			return c.lines, c.width
		}
	}

	v, ok := a.railViewOf(st)
	if !ok {
		return nil, 0
	}

	c.builds++
	c.valid, c.key, c.ids = true, key, st.AgentPanes()
	c.agents = a.railAgentKeys(c.agents[:0], c.ids, now)
	c.lines, c.width = a.railLines(st, lk, v), v.geom.box.W

	return c.lines, c.width
}

package ui

import (
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/ui/diffview"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const (
	// Tints mixed into a ground.
	tintHunk    = 0.10
	tintGhost   = 0.06
	tintFlash   = 0.35
	tintCursor  = 0.24
	tintGhostNr = 0.05

	markGlyph    = "▎" // ▎ at the first cell of the row under the cursor
	tagDigitsMin = 4   // the least hex digits of a hidden-character tag
	minNumW      = 3
)

// drawerLayout is how the drawer's inner box divides up.
type drawerLayout struct {
	box            core.Rect
	innerW, innerH int
	treeW, diffW   int  // treeW 0 hides the tree
	bodyH          int  // rows of the tree and diff columns
	hints          bool // the last inner row holds the key hints
	split          bool // side by side fits the diff column
	gw             int  // width of one line number column
}

// drawerLayoutOf divides the box for the view. Side by side is used only when
// the view wants it and 24 code columns fit on each side.
func (a *App) drawerLayoutOf(box core.Rect, v *reviewView) drawerLayout {
	c := v.derived()
	l := drawerLayout{box: box, innerW: max(box.W-2, 0), innerH: max(box.H-2, 0)}
	l.hints = l.innerH >= hintsMinInner
	l.bodyH = l.innerH

	if l.hints {
		l.bodyH--
	}

	l.diffW = l.innerW

	if l.innerW >= treeMinInner || v.treeOpen || a.drawer.tree {
		tw := treeWidth(c.tree, v.res.Files, c.hidden, l.innerW)
		// The mode row is a row of the tree too, up to the same share of the width.
		tw = max(tw, min(modeWidth(v, a.icons), l.innerW*treeMaxShare/100))

		if l.innerW-tw-1 >= 1 {
			l.treeW, l.diffW = tw, l.innerW-tw-1
		}
	}

	digits := c.digits
	for _, ft := range v.full {
		digits = max(digits, numDigits(max(len(ft.old), len(ft.new))))
	}

	l.gw = max(digits, minNumW) + 1
	l.split = v.split && l.splitCode() >= splitMinCode

	return l
}

// markW is the cell at the start of a row that holds the cursor mark.
const markW = 1

// unifiedCode is the code columns of a unified row, with n number columns.
func (l drawerLayout) unifiedCode(n int) int {
	return l.diffW - markW - n*l.gw - diffview.SignWidth
}

// splitCode is the code columns of the left side of a side by side row, the
// narrower one.
func (l drawerLayout) splitCode() int {
	return (l.diffW - markW - 1 - 2*(l.gw+diffview.SignWidth)) / 2
}

// drawCtx is what drawing one frame of the drawer needs.
type drawCtx struct {
	ui    theme.UI
	icons bool
	v     *reviewView
	c     *reviewCache
	lay   drawerLayout
	dv    *diffview.Theme
	flash *diffview.Theme
	keys  bool // the drawer has the keyboard
	tree  bool // and within it the tree has
	now   time.Time
	query string
	top   int
}

// diffKeys reports whether the diff, not the tree, has the keyboard.
func (d *drawCtx) diffKeys() bool { return d.keys && !d.tree }

func (d *drawCtx) glyph(icon string) string {
	if !d.icons {
		return ""
	}

	return icon
}

// diffThemes returns the diff view's theme and its flash twin for the look,
// building them once per theme.
func (a *App) diffThemes(lk look) (*diffview.Theme, *diffview.Theme) {
	if a.drawer.dv == nil || a.drawer.dvName != lk.theme.Name {
		pal := reviewPalette(lk.theme)
		a.drawer.dv = diffview.NewTheme(pal)
		pal.Ground = diffview.MixColors(pal.Ground, lk.theme.UI.Match, tintFlash)
		a.drawer.dvFlash = diffview.NewTheme(pal)
		a.drawer.dvName = lk.theme.Name
	}

	return a.drawer.dv, a.drawer.dvFlash
}

// drawerLines draws the drawer for r, the rect drawerRect returned: r.H lines
// of r.W cells. While it slides, r is narrower than the drawer and shows its
// left part.
func (a *App) drawerLines(st core.State, r core.Rect) []string {
	box, ok := a.drawerFull(st)
	if !ok || r.W <= 0 || r.H <= 0 {
		return nil
	}

	lk := a.current()
	v := a.drawer.views[st.Tab().ID]

	if v == nil {
		v = &reviewView{}
	}

	d := &drawCtx{ui: lk.theme.UI, icons: a.icons, v: v, c: v.derived(), now: a.clock.Now(), keys: a.drawer.focused, tree: a.drawer.focused && a.drawer.tree}
	d.lay = a.drawerLayoutOf(box, v)
	d.dv, d.flash = a.diffThemes(lk)
	d.top = v.viewTop(d.lay.bodyH)

	switch {
	case v.search != nil:
		d.query = *v.search
	default:
		d.query = v.last
	}

	lines := d.render()
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, r.W, "")
	}

	return lines[:min(len(lines), r.H)]
}

func colorHex(c color.Color) theme.Hex {
	r, g, b, _ := c.RGBA()

	return theme.Hex(fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8))
}

func mixHex(a, b theme.Hex, t float64) theme.Hex { return colorHex(diffview.MixColors(a, b, t)) }

func (d *drawCtx) edge() theme.Hex {
	if d.keys {
		return d.ui.Review
	}

	return d.ui.Border
}

// render draws the whole box: exactly box.H lines of box.W cells.
func (d *drawCtx) render() []string {
	box, lay := d.lay.box, d.lay
	blank := railPainter{bg: d.ui.Bg}.line(nil, box.W)

	if box.W < 2 || box.H < 2 {
		return slices.Repeat([]string{blank}, max(box.H, 0))
	}

	lines := make([]string, 0, box.H)
	lines = append(lines, d.topBorder())

	side := paint(d.edge(), d.ui.Bg, false).Render("│")
	rule := paint(d.ui.Border, d.ui.Bg, false).Render("│")

	for y := range lay.bodyH {
		row := side
		if lay.treeW > 0 {
			row += d.treeRow(y) + rule
		}

		lines = append(lines, row+d.diffRow(y)+side)
	}

	if lay.hints {
		hints := append([]railRun{{text: " "}}, d.hintRuns(lay.innerW-1)...)
		lines = append(lines, side+railPainter{bg: d.ui.Bg}.line(hints, lay.innerW)+side)
	}

	return append(lines, d.bottomBorder())
}

// borderLine is a border row: the corner and a dash, the left runs, dashes,
// the right runs, a dash and the other corner. What does not fit is cut.
func (d *drawCtx) borderLine(left, right string, lruns, rruns []railRun) string {
	w := d.lay.box.W
	fg := d.edge()
	edge := func(s string) railRun { return railRun{text: s, fg: fg} }

	runs := append([]railRun{edge(left + "─")}, lruns...)
	fill := w - runsWidth(runs) - runsWidth(rruns) - 2

	if fill < 0 {
		rruns, fill = nil, w-runsWidth(runs)-2
	}

	runs = append(runs, edge(strings.Repeat("─", max(fill, 0))))
	runs = append(runs, rruns...)
	runs = append(runs, edge("─"+right))

	return railPainter{bg: d.ui.Bg}.line(runs, w)
}

// topBorder holds the title on the left and the tags on the right.
func (d *drawCtx) topBorder() string {
	ui, v := d.ui, d.v
	fg, bold := ui.Muted, false

	if d.keys {
		fg, bold = ui.Review, true
	}

	// Tags sit on the right, the mode tag last; the split tag drops first.
	var tags []railRun

	tag := func(text string) railRun { return railRun{text: " " + text + " ", fg: ui.Accent, bold: true} }
	dash := railRun{text: "─", fg: d.edge()}

	if d.lay.split && v.rowsSplit {
		tags = append(tags, tag(d.withIcon(iconSplit, "split")), dash)
	}

	tags = append(tags, tag(d.modeTag()))

	// The corner and dash take two cells on each side, and a dash parts the title from the tags.
	room := d.lay.box.W - 4 - runsWidth(tags) - 1
	if room < titleRoomMin+2 {
		tags = tags[max(len(tags)-1, 0):]
		room = d.lay.box.W - 4 - runsWidth(tags) - 1
	}

	var title []railRun

	if text := d.titleText(); text != "" && room >= titleRoomMin+2 {
		text = ansi.Truncate(text, room-2-ansi.StringWidth(d.glyph(iconReview+" ")), "…")

		title = append(title, railRun{text: " "})

		if d.icons {
			title = append(title, railRun{text: iconReview + " ", fg: ui.Review, bold: true})
		}

		title = append(title, railRun{text: text, fg: fg, bold: bold}, railRun{text: " "})
	}

	return d.borderLine("╭", "╮", title, tags)
}

func (d *drawCtx) withIcon(icon, text string) string {
	if !d.icons {
		return text
	}

	return icon + " " + text
}

// titleText is "project · branch", or just "Review" before the view loaded.
func (d *drawCtx) titleText() string {
	v := d.v
	text, _ := cleanText(v.project)

	switch {
	case text == "":
		return "Review"
	case v.branch != "":
		b, _ := cleanText(v.branch)

		return text + " · " + b
	}

	return text
}

// shownTurn is the turn the drawer shows.
func (v *reviewView) shownTurn() int {
	if v.res.Turn > 0 {
		return v.res.Turn
	}

	if ts, ok := v.src.(git.TurnSource); ok && ts.Turn > 0 {
		return ts.Turn
	}

	return v.turns
}

// modeTag is the text of the mode tag.
func (d *drawCtx) modeTag() string {
	if _, ok := d.v.src.(git.TurnSource); ok {
		return d.withIcon(iconTurn, "turn "+strconv.Itoa(d.v.shownTurn())+"/"+strconv.Itoa(d.v.turns))
	}

	return d.withIcon(iconBranchMode, "branch")
}

// bottomBorder holds the search prompt while it is open.
func (d *drawCtx) bottomBorder() string {
	if d.v.search == nil {
		return d.borderLine("╰", "╯", nil, nil)
	}

	// Corner, dash, dash and corner take four cells; the prompt's blank, mark,
	// caret and blank take four more.
	room := d.lay.box.W - 8
	if room < 0 {
		return d.borderLine("╰", "╯", nil, nil)
	}

	q, _ := cleanText(*d.v.search)
	if w := ansi.StringWidth(q); w > room {
		q = ansi.TruncateLeft(q, w-room, "")
	}

	runs := []railRun{
		{text: " "},
		{text: "/", fg: d.ui.Info, bold: true},
		{text: q, fg: d.ui.Fg},
		{text: "▏", fg: d.ui.Fg},
		{text: " "},
	}

	return d.borderLine("╰", "╯", runs, nil)
}

// hintRuns are the key hints that fit cw cells; pairs that do not are dropped
// from the end.
func (d *drawCtx) hintRuns(cw int) []railRun {
	pairs := [][2]string{
		{"j/k", "line"},
		{"]/[", "hunk"},
		{"}/{", "file"},
		{"tab", "tree"},
		{"f", "full"},
		{"u", "split"},
		{"b/t", "mode"},
		{",/.", "turn"},
		{"e", "edit"},
		{"/", "search"},
	}

	if d.tree {
		pairs = [][2]string{{"j/k", "file"}, {"enter", "open"}, {"tab", "diff"}}
	}

	var out []railRun

	used := 0

	for i, kv := range pairs {
		need := ansi.StringWidth(kv[0]) + 1 + ansi.StringWidth(kv[1])
		if i > 0 {
			need += 3
		}

		if used+need > cw {
			break
		}

		if i > 0 {
			out = append(out, railRun{text: "   ", fg: d.ui.Muted})
		}

		out = append(out, railRun{text: kv[0], fg: d.ui.Key, bold: true}, railRun{text: " " + kv[1], fg: d.ui.Muted})
		used += need
	}

	return out
}

// ---- file tree ----

// statusMark is a file's status letter and its colour.
func (d *drawCtx) statusMark(f git.FileDiff) (string, theme.Hex) {
	switch {
	case f.Untracked || f.Status == git.Added:
		return "A", d.ui.Success
	case f.Status == git.Deleted:
		return "D", d.ui.Error
	case f.Status == git.Renamed:
		return "R", d.ui.Warning
	case f.Status == git.Copied:
		return "C", d.ui.Warning
	}

	return "M", d.ui.Info
}

func (d *drawCtx) hiddenFlag() string {
	if !d.icons {
		return "!"
	}

	return iconBlocked
}

// treeFile is the file the tree marks: the selected one while the tree has the
// keyboard, else the one under the diff's cursor; -1 for none.
func (d *drawCtx) treeFile() int {
	if d.tree {
		return d.v.treeSel
	}

	if d.v.cursor >= 0 && d.v.cursor < len(d.v.rows) {
		return d.v.rows[d.v.cursor].file
	}

	return -1
}

// treeTop is the first tree item shown, so the marked file shows, centred when
// the tree scrolls.
func (d *drawCtx) treeTop(room int) int {
	items := d.c.tree
	if len(items) <= room || room <= 0 {
		return 0
	}

	at := slices.IndexFunc(items, func(it treeItem) bool { return !it.dir && it.file == d.treeFile() })
	if at < 0 {
		return 0
	}

	return min(max(at-room/2, 0), len(items)-room)
}

// treeRow draws row y of the tree column: the mode row, the totals row, a
// blank row, then the items.
func (d *drawCtx) treeRow(y int) string {
	w, ui := d.lay.treeW, d.ui
	p := railPainter{bg: ui.Bg}

	switch y {
	case 0:
		return p.line(d.modeRuns(), w)
	case 1:
		return p.line(d.totalRuns(), w)
	case 2:
		return p.line(nil, w)
	}

	room := d.lay.bodyH - 3
	i := d.treeTop(room) + y - 3

	if i < 0 || i >= len(d.c.tree) {
		return p.line(nil, w)
	}

	it := d.c.tree[i]
	name, _ := cleanText(it.name)
	indent := strings.Repeat(" ", it.depth*2)

	if it.dir {
		runs := []railRun{{text: " " + indent}}
		if d.icons {
			runs = append(runs, railRun{text: iconFolder + " ", fg: ui.Muted})
		}

		return p.line(append(runs, railRun{text: name + "/", fg: ui.Muted}), w)
	}

	f := d.v.res.Files[it.file]
	bg, mark, picked := ui.Bg, " ", it.file == d.treeFile()

	switch {
	case picked && d.tree:
		bg, mark = ui.Selection, markGlyph
	case picked && d.diffKeys():
		bg = ui.Surface
	}

	letter, letterFg := d.statusMark(f)

	var right []railRun

	if it.file < len(d.c.hidden) && d.c.hidden[it.file] {
		right = append(right, railRun{text: d.hiddenFlag() + " ", fg: ui.Warning, bold: true})
	}

	if f.Binary {
		right = append(right, railRun{text: "bin", fg: ui.Subtle})
	} else {
		right = append(right, railRun{text: "+" + strconv.Itoa(f.Added), fg: ui.Success}, railRun{text: " "}, railRun{text: "-" + strconv.Itoa(f.Removed), fg: ui.Error})
	}

	lead := 1 + it.depth*2 + 2
	room2 := w - lead - 2 - runsWidth(right) - 1

	name = ansi.Truncate(name, max(room2, 0), "…")
	gap := max(w-lead-ansi.StringWidth(name)-runsWidth(right)-1, 1)

	runs := []railRun{
		{text: mark, fg: ui.Accent},
		{text: indent},
		{text: letter, fg: letterFg, bold: true},
		{text: " "},
		{text: name, fg: ui.Fg, bold: picked},
		{text: strings.Repeat(" ", gap)},
	}

	return railPainter{bg: bg}.line(append(runs, right...), w)
}

// modeParts are the pieces of the mode row: the icon (empty with icons off),
// the bold word and the rest. All are empty before the view has a source.
func modeParts(v *reviewView, icons bool) (icon, word, rest string) {
	switch src := v.src.(type) {
	case git.BranchSource:
		base := v.base
		if base == "" {
			base = src.Base
		}

		if base == "" {
			base = "the default branch"
		}

		base, _ = cleanText(base)
		icon, word, rest = iconBranchMode, "branch", " vs "+base
	case git.TurnSource:
		n, m := v.shownTurn(), v.turns

		icon, word, rest = iconTurn, "turn "+strconv.Itoa(n), " of "+strconv.Itoa(m)

		switch {
		case v.noSnapshot:
			rest += ", no snapshot"
		case v.res.Through > v.res.Turn && v.res.Turn > 0:
			word = "turns " + strconv.Itoa(v.res.Turn) + "-" + strconv.Itoa(v.res.Through)
		case n == m:
			rest += ", live"
		}
	default:
		return "", "", ""
	}

	if !icons {
		icon = ""
	}

	return icon, word, rest
}

// modeWidth is the width of the mode row with its margins.
func modeWidth(v *reviewView, icons bool) int {
	icon, word, rest := modeParts(v, icons)
	w := treePad + len(word) + ansi.StringWidth(rest)

	if icon != "" {
		w += 2
	}

	return w
}

// modeRuns is the mode row of the tree column.
func (d *drawCtx) modeRuns() []railRun {
	icon, word, rest := modeParts(d.v, d.icons)
	if word == "" {
		return nil
	}

	runs := []railRun{{text: " "}}
	if icon != "" {
		runs = []railRun{{text: " " + icon + " ", fg: d.ui.Accent}}
	}

	return append(runs, railRun{text: word, fg: d.ui.Fg, bold: true}, railRun{text: rest, fg: d.ui.Muted})
}

// totalRuns is the totals row: the files and the lines added and removed.
func (d *drawCtx) totalRuns() []railRun {
	files := d.v.res.Files
	added, removed := 0, 0

	for _, f := range files {
		added += f.Added
		removed += f.Removed
	}

	noun := " files"
	if len(files) == 1 {
		noun = " file"
	}

	return []railRun{
		{text: " " + strconv.Itoa(len(files)) + noun + "  ", fg: d.ui.Muted},
		{text: "+" + strconv.Itoa(added), fg: d.ui.Success},
		{text: " -" + strconv.Itoa(removed), fg: d.ui.Error},
	}
}

// ---- diff column ----

// stickyHeader is the row of the header to draw on the first row instead of
// the top one: the top row's file header, when it has scrolled off.
func (d *drawCtx) stickyHeader() (int, bool) {
	rows, top := d.v.rows, d.top
	if top <= 0 || top >= len(rows) || rows[top].kind == rowFile || top == d.v.cursor {
		return 0, false
	}

	if top+1 < len(rows) && rows[top+1].kind == rowFile {
		return 0, false
	}

	for i := top - 1; i >= 0; i-- {
		if rows[i].kind == rowFile && rows[i].file == rows[top].file {
			return i, true
		}
	}

	return 0, false
}

// diffRow draws row y of the diff column.
func (d *drawCtx) diffRow(y int) string {
	w := d.lay.diffW
	p := railPainter{bg: d.ui.Bg}

	if len(d.v.rows) == 0 {
		return p.line(d.emptyRuns(y), w)
	}

	i := d.top + y

	if y == 0 {
		if h, ok := d.stickyHeader(); ok {
			i = h
		}
	}

	if i >= len(d.v.rows) {
		return p.line(nil, w)
	}

	cursor := d.diffKeys() && i == d.v.cursor

	return d.exact(d.rowCells(i, cursor), w)
}

// exact makes s w cells: cut, or padded on the ground.
func (d *drawCtx) exact(s string, w int) string {
	got := ansi.StringWidth(s)

	switch {
	case got > w:
		return ansi.Truncate(s, w, "")
	case got < w:
		return s + railPainter{bg: d.ui.Bg}.line(nil, w-got)
	}

	return s
}

// emptyRuns are the info rows of a result with no files, at row y.
func (d *drawCtx) emptyRuns(y int) []railRun {
	v := d.v

	var lines []string

	switch src := v.src.(type) {
	case git.BranchSource:
		base := v.base
		if base == "" {
			base = src.Base
		}

		if base == "" {
			base = "the default branch"
		}

		lines = []string{"no changes against " + base}
	case git.TurnSource:
		n := strconv.Itoa(v.shownTurn())
		if v.noSnapshot {
			lines = []string{"turn " + n + " has no snapshot", ", and . step through the turns; b shows the branch"}
		} else {
			lines = []string{"turn " + n + " changed nothing"}
		}
	}

	if v.err != nil {
		lines = []string{v.err.Error()}
	}

	if y >= len(lines) {
		return nil
	}

	text, _ := cleanText(lines[y])

	return []railRun{{text: "    "}, {text: text, fg: d.ui.Subtle}}
}

// markRun is the first cell of a row: the cursor mark or a blank.
func (d *drawCtx) markRun(cursor bool) railRun {
	if cursor {
		return railRun{text: markGlyph, fg: d.ui.Accent}
	}

	return railRun{text: " "}
}

// rowCells draws row i, a row of the diff, as cells of any width; the caller
// fits them.
func (d *drawCtx) rowCells(i int, cursor bool) string {
	r, v, w := d.v.rows[i], d.v, d.lay.diffW
	ui := d.ui
	f := v.res.Files[r.file]
	fresh := v.fresh[i] && d.now.Before(v.flashUntil)

	switch r.kind {
	case rowFile:
		return d.fileRow(r.file, cursor)
	case rowInfo:
		bg := d.cursorGround(ui.Bg, cursor)
		text, _ := cleanText(r.text)

		return railPainter{bg: bg}.line([]railRun{d.markRun(cursor), {text: "   "}, {text: text, fg: ui.Subtle}}, w)
	case rowHunk:
		h := f.Hunks[r.hunk]
		bg := d.cursorGround(mixHex(ui.Bg, ui.Info, tintHunk), cursor)
		section, _ := cleanText(h.Section)

		runs := []railRun{d.markRun(cursor), {text: " " + hunkHeader(h), fg: ui.Info}}
		if section != "" {
			runs = append(runs, railRun{text: " " + section, fg: ui.Muted})
		}

		return railPainter{bg: bg}.line(runs, w)
	case rowLine:
		return d.unifiedRow(r, f, cursor, fresh)
	case rowPair:
		return d.pairRow(r, f, cursor, fresh)
	case rowFull:
		return d.fullRow(r, cursor, fresh)
	case rowGhost:
		return d.ghostRow(r, cursor, fresh)
	case rowFold:
		return d.foldRow(r, cursor, fresh)
	}

	return ""
}

// cursorGround is bg, with the accent mixed in under the cursor.
func (d *drawCtx) cursorGround(bg theme.Hex, cursor bool) theme.Hex {
	if cursor {
		return mixHex(bg, d.ui.Accent, tintCursor)
	}

	return bg
}

// fileRow is a file's header: status, path, flags and counts.
func (d *drawCtx) fileRow(fi int, cursor bool) string {
	f := d.v.res.Files[fi]
	ui, w := d.ui, d.lay.diffW
	bg := d.cursorGround(ui.Surface, cursor)
	letter, letterFg := d.statusMark(f)

	var right []railRun

	if fi < len(d.c.hidden) && d.c.hidden[fi] {
		text := d.hiddenFlag()
		if w >= 90 {
			text += " hidden characters"
		}

		right = append(right, railRun{text: text, fg: ui.Warning, bold: true}, railRun{text: "  "})
	}

	if _, whole := d.v.full[f.Path]; whole {
		right = append(right, railRun{text: d.withIcon(iconFull, "full"), fg: ui.Accent, bold: true}, railRun{text: "  "})
	}

	if f.Binary {
		right = append(right, railRun{text: "bin", fg: ui.Subtle})
	} else {
		right = append(right, railRun{text: "+" + strconv.Itoa(f.Added), fg: ui.Success}, railRun{text: " -" + strconv.Itoa(f.Removed), fg: ui.Error})
	}

	right = append(right, railRun{text: " "})

	path, _ := cleanText(f.Path)
	if f.OldPath != "" {
		old, _ := cleanText(f.OldPath)
		path = old + " → " + path
	}

	// The mark, a blank, the letter and a blank come first; two cells part the path from the flags.
	room := max(w-4-runsWidth(right)-2, 0)

	if pw := ansi.StringWidth(path); pw > room {
		path = ansi.TruncateLeft(path, pw-room+1, "…")
	}

	runs := []railRun{d.markRun(cursor), {text: " "}, {text: letter, fg: letterFg, bold: true}, {text: " "}}
	runs = append(runs, d.matchRuns(path, ui.Fg, true)...)
	runs = append(runs, railRun{text: strings.Repeat(" ", max(w-4-ansi.StringWidth(path)-runsWidth(right), 1))})

	return railPainter{bg: bg}.line(append(runs, right...), w)
}

// matchRuns splits text into runs, the search matches in Match and bold.
func (d *drawCtx) matchRuns(text string, fg theme.Hex, bold bool) []railRun {
	var runs []railRun

	pos := 0

	for _, m := range matchRanges(text, d.query) {
		if m.Start > pos {
			runs = append(runs, railRun{text: text[pos:m.Start], fg: fg, bold: bold})
		}

		runs = append(runs, railRun{text: text[m.Start:m.End], fg: d.ui.Match, bold: true})
		pos = m.End
	}

	return append(runs, railRun{text: text[pos:], fg: fg, bold: bold})
}

// matchRanges are the places q occurs in text, ignoring case. It finds none
// when lower-casing changes the length of text, which a byte range cannot follow.
func matchRanges(text, q string) []diffview.Range {
	if q == "" {
		return nil
	}

	lt, lq := strings.ToLower(text), strings.ToLower(q)
	if len(lt) != len(text) || lq == "" {
		return nil
	}

	var out []diffview.Range

	for at := 0; ; {
		i := strings.Index(lt[at:], lq)
		if i < 0 {
			return out
		}

		out = append(out, diffview.Range{Start: at + i, End: at + i + len(lq)})
		at += i + len(lq)
	}
}

// thFor is the diff theme for a row: the flash twin while the row flashes.
func (d *drawCtx) thFor(fresh bool) *diffview.Theme {
	if fresh {
		return d.flash
	}

	return d.dv
}

func diffKind(k git.LineKind) diffview.Kind {
	switch k {
	case git.Add:
		return diffview.Add
	case git.Del:
		return diffview.Delete
	}

	return diffview.Context
}

// spansAt is the tokenised spans of one line, or nil while untokenised.
func (d *drawCtx) spansAt(ref spanRef, ok bool) []diffview.Span {
	if !ok {
		return nil
	}

	ch := d.c.spans[ref.key]
	if ref.at < 0 || ref.at >= len(ch) {
		return nil
	}

	return ch[ref.at]
}

// mark is the first cell of a code row, on the gutter's ground.
func (d *drawCtx) mark(th *diffview.Theme, kind diffview.Kind, cursor bool) string {
	if !cursor {
		return th.BlankGutter(kind, false, markW)
	}

	return lipgloss.NewStyle().Foreground(d.ui.Accent).Background(th.GutterBg(kind, true)).Render(markGlyph)
}

// partner is the index of the line li is paired with, as side by side pairs a
// run of removed lines with the added ones after it; -1 for none.
func partner(ls []git.Line, li int) int {
	switch ls[li].Kind {
	case git.Del:
		d0 := li
		for d0 > 0 && ls[d0-1].Kind == git.Del {
			d0--
		}

		a0 := li
		for a0 < len(ls) && ls[a0].Kind == git.Del {
			a0++
		}

		a1 := a0
		for a1 < len(ls) && ls[a1].Kind == git.Add {
			a1++
		}

		if p := a0 + li - d0; p < a1 {
			return p
		}
	case git.Add:
		a0 := li
		for a0 > 0 && ls[a0-1].Kind == git.Add {
			a0--
		}

		d0 := a0
		for d0 > 0 && ls[d0-1].Kind == git.Del {
			d0--
		}

		if p := d0 + li - a0; p < a0 {
			return p
		}
	}

	return -1
}

// changedIn is the range of line li's cleaned text that differs from its
// partner's.
func changedIn(ls []git.Line, li int, text string) diffview.Range {
	p := partner(ls, li)
	if p < 0 {
		return diffview.Range{}
	}

	other, _ := cleanText(ls[p].Text)
	if ls[li].Kind == git.Del {
		r, _ := diffview.Changed(text, other)

		return r
	}

	_, r := diffview.Changed(other, text)

	return r
}

// unifiedRow is a line of a hunk: two number columns, the sign, the code.
func (d *drawCtx) unifiedRow(r reviewRow, f git.FileDiff, cursor, fresh bool) string {
	h := f.Hunks[r.hunk]
	l := h.Lines[r.line]
	text, _ := cleanText(l.Text)
	ref, ok := lineRef(r.file, r.hunk, h, r.line)

	return d.lineCells(d.thFor(fresh), diffKind(l.Kind), cursor, []int{l.Old, l.New}, text, d.spansAt(ref, ok), changedIn(h.Lines, r.line, text), d.lay.unifiedCode(2))
}

// lineCells is a line's mark, number columns, sign and code.
func (d *drawCtx) lineCells(th *diffview.Theme, kind diffview.Kind, cursor bool, nums []int, text string, spans []diffview.Span, changed diffview.Range, width int) string {
	var b strings.Builder

	b.WriteString(d.mark(th, kind, cursor))

	for _, n := range nums {
		b.WriteString(th.Gutter(n, d.lay.gw, kind, cursor))
	}

	b.WriteString(th.Sign(kind, cursor, false))
	b.WriteString(d.code(th, text, spans, changed, kind, cursor, width))

	return b.String()
}

// pairRow is a row of two lines side by side.
func (d *drawCtx) pairRow(r reviewRow, f git.FileDiff, cursor, fresh bool) string {
	th := d.thFor(fresh)
	h := f.Hunks[r.hunk]
	lay := d.lay

	var left, right int

	switch h.Lines[r.line].Kind {
	case git.Context:
		left, right = r.line, r.line
	case git.Del:
		left, right = r.line, r.other
	default:
		left, right = -1, r.line
	}

	var lt, rt string

	if left >= 0 {
		lt, _ = cleanText(h.Lines[left].Text)
	}

	if right >= 0 {
		rt, _ = cleanText(h.Lines[right].Text)
	}

	var lr, rr diffview.Range
	if left >= 0 && right >= 0 && left != right {
		lr, rr = diffview.Changed(lt, rt)
	}

	avail := lay.diffW - markW - 1 - 2*(lay.gw+diffview.SignWidth)
	lw := avail / 2

	var b strings.Builder

	side := func(first bool, idx int, text string, ch diffview.Range, num func(git.Line) int, width int) {
		if idx < 0 {
			if first {
				b.WriteString(d.mark(th, diffview.Missing, cursor))
			}

			b.WriteString(th.BlankGutter(diffview.Missing, cursor, lay.gw))
			b.WriteString(th.Blank(diffview.Missing, cursor, diffview.SignWidth+width))

			return
		}

		l := h.Lines[idx]
		kind := diffKind(l.Kind)
		ref, ok := lineRef(r.file, r.hunk, h, idx)

		if first {
			b.WriteString(d.mark(th, kind, cursor))
		}

		b.WriteString(th.Gutter(num(l), lay.gw, kind, cursor))
		b.WriteString(th.Sign(kind, cursor, false))
		b.WriteString(d.code(th, text, d.spansAt(ref, ok), ch, kind, cursor, width))
	}

	side(true, left, lt, lr, func(l git.Line) int { return l.Old }, lw)
	b.WriteString(paint(d.ui.Border, d.ui.Bg, false).Render("│"))
	side(false, right, rt, rr, func(l git.Line) int { return l.New }, avail-lw)

	return b.String()
}

// fullRow is a line of the file shown whole, with one number column.
func (d *drawCtx) fullRow(r reviewRow, cursor, fresh bool) string {
	kind := diffview.Context
	if r.other < 0 {
		kind = diffview.Add
	}

	text, _ := cleanText(r.text)
	ref := spanRef{spanKey{r.file, -1, sideNew, r.line / hlChunk}, r.line % hlChunk}

	return d.lineCells(d.thFor(fresh), kind, cursor, []int{r.line + 1}, text, d.spansAt(ref, true), diffview.Range{}, d.lay.unifiedCode(1))
}

// ghostGround is the ground of a removed line in the full view.
func (d *drawCtx) ghostGround(cursor, fresh bool) theme.Hex {
	g := mixHex(d.ui.Bg, d.ui.Error, tintGhost)
	if fresh {
		g = mixHex(g, d.ui.Match, tintFlash)
	}

	return d.cursorGround(g, cursor)
}

// ghostRow is a removed line in the full view: muted text on a faint Error
// ground, with no syntax colours.
func (d *drawCtx) ghostRow(r reviewRow, cursor, fresh bool) string {
	ui, lay := d.ui, d.lay
	g := d.ghostGround(cursor, fresh)
	gutter := mixHex(ui.Bg, ui.Fg, tintGhostNr)

	if fresh {
		gutter = mixHex(gutter, ui.Match, tintFlash)
	}

	gutter = d.cursorGround(gutter, cursor)
	text, _ := cleanText(r.text)

	num := strconv.Itoa(r.line + 1)
	num = strings.Repeat(" ", max(lay.gw-1-len(num), 0)) + num + " "

	var b strings.Builder

	b.WriteString(railPainter{bg: gutter}.line([]railRun{d.markRun(cursor)}, markW))
	b.WriteString(railPainter{bg: gutter}.line([]railRun{{text: num, fg: ui.Muted}}, lay.gw))
	b.WriteString(railPainter{bg: g}.line([]railRun{{text: "- ", fg: ui.Error, bold: true}}, diffview.SignWidth))
	b.WriteString(railPainter{bg: g}.line(d.matchRuns(text, ui.Muted, false), lay.unifiedCode(1)))

	return b.String()
}

// foldRow is a folded block of removed lines.
func (d *drawCtx) foldRow(r reviewRow, cursor, fresh bool) string {
	ui, lay := d.ui, d.lay
	g := d.ghostGround(cursor, fresh)

	noun := " removed lines"
	if r.other == 1 {
		noun = " removed line"
	}

	lead := markW + lay.gw + diffview.SignWidth

	runs := []railRun{d.markRun(cursor), {text: strings.Repeat(" ", lead-markW)}}
	if d.icons {
		runs = append(runs, railRun{text: iconGhost + " ", fg: ui.Muted})
	}

	runs = append(runs, railRun{text: strconv.Itoa(r.other) + noun, fg: ui.Muted}, railRun{text: "   enter shows them", fg: ui.Subtle})

	return railPainter{bg: g}.line(runs, lay.diffW)
}

// paintRange is a stretch of a line's text drawn over its code.
type paintRange struct {
	diffview.Range
	fg theme.Hex
}

// tagRanges are the hidden-character tags cleanText wrote into text.
func tagRanges(text string) []diffview.Range {
	var out []diffview.Range

	const open, closeTag = "⟨U+", "⟩"

	for at := 0; at < len(text); {
		i := strings.Index(text[at:], open)
		if i < 0 {
			break
		}

		start := at + i
		body := start + len(open)

		j := strings.Index(text[body:], closeTag)
		if j < 0 {
			break
		}

		hex := text[body : body+j]
		if _, err := strconv.ParseUint(hex, 16, 32); err != nil || len(hex) < tagDigitsMin || len(hex) > 6 {
			at = start + len(open)

			continue
		}

		end := body + j + len(closeTag)
		out = append(out, diffview.Range{Start: start, End: end})
		at = end
	}

	return out
}

// code draws a line's text in width cells with the diff theme, then draws the
// hidden-character tags in Error and the search matches in Match over it.
func (d *drawCtx) code(th *diffview.Theme, text string, spans []diffview.Span, changed diffview.Range, kind diffview.Kind, cursor bool, width int) string {
	styled := th.Code(text, spans, changed, kind, cursor, 0, width)

	tags := tagRanges(text)

	var over []paintRange

	for _, t := range tags {
		over = append(over, paintRange{t, d.ui.Error})
	}

	for _, m := range matchRanges(text, d.query) {
		if !slices.ContainsFunc(tags, func(t diffview.Range) bool { return m.Start < t.End && t.Start < m.End }) {
			over = append(over, paintRange{m, d.ui.Match})
		}
	}

	if len(over) == 0 || width <= 0 {
		return styled
	}

	slices.SortFunc(over, func(p, q paintRange) int { return p.Start - q.Start })

	// A cut line ends in an ellipsis that stays.
	limit := width
	if ansi.StringWidth(text) > width {
		limit--
	}

	var b strings.Builder

	pos := 0

	for _, o := range over {
		c0 := ansi.StringWidth(text[:o.Start])
		if c0 >= limit {
			break
		}

		c1 := min(c0+ansi.StringWidth(text[o.Start:o.End]), limit)
		word := changed.Start <= o.Start && o.Start < changed.End
		seg := ansi.Truncate(text[o.Start:o.End], c1-c0, "")

		b.WriteString(ansi.Cut(styled, pos, c0))
		b.WriteString(lipgloss.NewStyle().Foreground(o.fg).Background(th.Bg(kind, cursor, word)).Bold(true).Render(seg))

		pos = c1
	}

	b.WriteString(ansi.Cut(styled, pos, width))

	return b.String()
}

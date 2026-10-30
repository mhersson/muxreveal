package ui

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

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
	headH          int  // rows at the top of the diff column that hold the pull request header
	diffH          int  // rows of the diff column under the header
	modeShort      bool // the mode row drops " in this worktree" for lack of room
	hints          bool // the last inner row holds the key hints
	split          bool // side by side fits the diff column
	gw             int  // width of one line number column
}

// layoutKey is what the layout depends on besides the result's generation.
type layoutKey struct {
	box        core.Rect
	tree       bool // the tree is open
	split      bool
	modeW      int // the mode row's width
	modeShortW int // and its width without " in this worktree"
	headH      int // rows of the pull request header the diff column starts with
	fullDigits int // digits of the longest file shown whole
}

// fullDigits is the digits of the highest line number among the files shown whole.
func fullDigits(v *reviewView) int {
	n := 0

	for _, ft := range v.full {
		n = max(n, numDigits(max(len(ft.old), len(ft.new))))
	}

	return n
}

// drawerLayoutOf divides the box for the view; tree says the drawer's tree is
// open. Side by side is used only when the view wants it and 24 code columns
// fit on each side. The layout is kept until one of its inputs changes, as it
// is asked for on every update.
func (a *App) drawerLayoutOf(box core.Rect, v *reviewView, tree bool) drawerLayout {
	c := v.derived()
	full, short := modeWidth(v, a.icons, false), modeWidth(v, a.icons, true)
	headH := a.prHeaderH(v, max(box.H-2, 0))
	key := layoutKey{box: box, tree: tree, split: v.split, modeW: full, modeShortW: short, headH: headH, fullDigits: fullDigits(v)}

	if c.layOK && c.layKey == key {
		return c.lay
	}

	a.drawer.layouts++

	l := drawerLayout{box: box, innerW: max(box.W-2, 0), innerH: max(box.H-2, 0)}
	l.hints = l.innerH >= hintsMinInner
	l.bodyH = l.innerH

	if l.hints {
		l.bodyH--
	}

	l.headH, l.diffH = headH, l.bodyH-headH
	l.diffW = l.innerW

	if tree {
		tw := c.treeWidthAt(v.res.Files, l.innerW)
		limit := l.innerW * treeMaxShare / 100

		// The mode row is a row of the tree too, up to the same share of the
		// width. It drops its words about the worktree before it is cut.
		mw := full

		if full > max(tw, limit) {
			l.modeShort, mw = true, short
		}

		tw = max(tw, min(mw, limit))

		if l.innerW-tw-1 >= 1 {
			l.treeW, l.diffW = tw, l.innerW-tw-1
		}
	}

	l.gw = max(c.digits, key.fullDigits, minNumW) + 1
	l.split = v.split && l.splitCode() >= splitMinCode

	c.lay, c.layKey, c.layOK = l, key, true

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

	pr     core.PRInfo // the pull request the header shows; zero when headH is 0
	pinned bool        // the drawer is held on its root
	home   string      // shortens the paths it names
	list   *wtList     // the open worktree list, drawn over the drawer
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

// drawKey is every input the drawing reads, so drawerLines can keep its lines
// while the key stays the same. An input the drawing gains needs a field here.
type drawKey struct {
	w, h              int // the cells shown, which a slide narrows
	lay               drawerLayout
	theme             string
	ui                theme.UI
	icons, keys, tree bool

	rowsRev, spansRev    int
	rowsSplit            bool
	top, cursor, treeSel int
	query                string
	searching            bool
	fresh                string // the rows in view that flash, while a flash is on
	src                  git.Source
	project, branch      string
	base                 string
	turns                int
	resTurn              int
	noSnapshot           bool
	pinned               bool
	list                 wtKey
	err                  string
	hasErr               bool

	pr       core.PRInfo
	unpushed int
	age      string
	head     string // the loaded diff's head, which the header compares with the poll's
}

// key is the drawKey of d drawn into r, for the theme called name.
func (d *drawCtx) key(r core.Rect, name string) drawKey {
	v := d.v
	k := drawKey{
		w: r.W, h: r.H, lay: d.lay, theme: name, ui: d.ui,
		icons: d.icons, keys: d.keys, tree: d.tree,
		rowsRev: v.rowsRev, spansRev: d.c.spansRev, rowsSplit: v.rowsSplit,
		top: d.top, cursor: v.cursor, treeSel: v.treeSel,
		query: d.query, searching: v.search != nil,
		src: v.src, project: v.project, branch: v.branch, base: v.base,
		turns: v.turns, resTurn: v.res.Turn, noSnapshot: v.noSnapshot, pinned: d.pinned,
		list: d.listKey(),
		pr:   d.pr, unpushed: v.unpushed, head: v.res.Head,
	}

	if d.lay.headH > 0 {
		k.age = railAge(d.pr.UpdatedAt, d.now)
	}

	if v.err != nil {
		k.err, k.hasErr = v.err.Error(), true
	}

	if len(v.fresh) > 0 && d.now.Before(v.flashUntil) {
		k.fresh = d.freshInView()
	}

	return k
}

// freshInView names the rows in view that flash; a string keeps the key comparable.
func (d *drawCtx) freshInView() string {
	var rows []byte

	for i := d.top; i < min(d.top+d.lay.diffH, len(d.v.rows)); i++ {
		if d.v.fresh[i] {
			rows = binary.AppendVarint(rows, int64(i))
		}
	}

	return string(rows)
}

// drawerLines draws the drawer for r, the rect drawerRect returned: r.H lines
// of r.W cells. While it slides, r is narrower than the drawer and shows its
// left part. The lines are drawn again only when a drawKey field changed; they
// are shared with the cache, so callers must not change them. View uses
// drawerDrawn; this is the seam the drawing tests call.
func (a *App) drawerLines(st core.State, r core.Rect) []string {
	lines, _ := a.drawerDrawn(st, r)

	return lines
}

// drawerDrawn is drawerLines, and whether every line is exactly r.W cells, ends
// with no style or link open and carries nothing across cells, as splicing the
// lines into a frame needs.
func (a *App) drawerDrawn(st core.State, r core.Rect) (lines []string, fits bool) {
	d, name, ok := a.drawCtxFor(st)
	if !ok || r.W <= 0 || r.H <= 0 {
		return nil, false
	}

	key := d.key(r, name)
	if d.c.drawnOK && d.c.drawnKey == key {
		return d.c.drawn, d.c.drawnFit
	}

	a.drawer.renders++

	lines = d.lines(r)
	fits = !slices.ContainsFunc(lines, func(l string) bool { return ansi.StringWidth(l) != r.W || !endsClean(l) || carriesAcrossCells(l) })
	d.c.drawn, d.c.drawnFit, d.c.drawnKey, d.c.drawnOK = lines, fits, key, true

	return lines, fits
}

// drawCtxFor is what drawing the active tab's drawer needs, and the name of the
// theme it draws in; ok is false with no drawer to draw.
func (a *App) drawCtxFor(st core.State) (d *drawCtx, name string, ok bool) {
	box, ok := a.drawerFull(st)
	if !ok {
		return nil, "", false
	}

	lk := a.current()
	v := a.drawer.views[st.Tab().ID]

	if v == nil {
		v = &reviewView{}
	}

	tab := st.Tab()

	d = &drawCtx{
		ui: lk.theme.UI, icons: a.icons, v: v, c: v.derived(), now: a.clock.Now(), keys: a.drawer.focused, tree: a.drawer.focused && a.drawer.tree,
		pinned: tab.Review != nil && pinnedTo(tab, tab.Review.Pane), home: a.getenv("HOME"), list: a.listFor(tab),
	}
	d.lay = a.drawerLayoutOf(box, v, drawerTree(st))

	if d.lay.headH > 0 {
		d.pr, _ = a.prHeaderInfo(v)
	}

	d.dv, d.flash = a.diffThemes(lk)
	d.top = v.viewTop(d.lay.diffH)

	switch {
	case v.search != nil:
		d.query = *v.search
	default:
		d.query = v.last
	}

	return d, lk.theme.Name, true
}

// lines draws the box into r, r.H lines of r.W cells.
func (d *drawCtx) lines(r core.Rect) []string {
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

		lines = append(lines, row+d.diffColumn(y)+side)
	}

	if lay.hints {
		hints := append([]railRun{{text: " "}}, d.hintRuns(lay.innerW-1)...)
		lines = append(lines, side+railPainter{bg: d.ui.Bg}.line(hints, lay.innerW)+side)
	}

	lines = append(lines, d.bottomBorder())
	d.overlayList(lines)

	return lines
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

	// Tags sit on the right, the mode tag last. When the title would be left no
	// room, the split tag drops first, then the pinned tag.
	split, pin := d.lay.split && v.rowsSplit, d.pinned
	tags := d.tagRuns(split, pin)

	// The corner and dash take two cells on each side, and a dash parts the title from the tags.
	room := func() int { return d.lay.box.W - 4 - runsWidth(tags) - 1 }

	if room() < titleRoomMin+2 && split {
		tags = d.tagRuns(false, pin)
	}

	if room() < titleRoomMin+2 && pin {
		tags = d.tagRuns(false, false)
	}

	var title []railRun

	if text := d.titleText(); text != "" && room() >= titleRoomMin+2 {
		text = ansi.Truncate(text, room()-2-ansi.StringWidth(d.glyph(iconReview+" ")), "…")

		title = append(title, railRun{text: " "})

		if d.icons {
			title = append(title, railRun{text: iconReview + " ", fg: ui.Review, bold: true})
		}

		title = append(title, railRun{text: text, fg: fg, bold: bold}, railRun{text: " "})
	}

	return d.borderLine("╭", "╮", title, tags)
}

// tagRuns are the tags on the right of the top border, left to right: split,
// pinned, then the mode tag, each followed by a dash but the last.
func (d *drawCtx) tagRuns(split, pinned bool) []railRun {
	ui := d.ui
	tag := func(text string) railRun { return railRun{text: " " + text + " ", fg: ui.Accent, bold: true} }
	dash := railRun{text: "─", fg: d.edge()}

	var tags []railRun

	if split {
		tags = append(tags, tag(d.withIcon(iconSplit, "split")), dash)
	}

	if pinned {
		tags = append(tags, tag(d.withIcon(iconPin, "pinned")), dash)
	}

	return append(tags, tag(d.modeTag()))
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

// showsTurn reports whether the drawer shows a turn. A turn-mode drawer with no
// session or no turns yet shows the branch while core keeps turn mode.
func (v *reviewView) showsTurn() bool {
	_, ok := v.src.(git.TurnSource)

	return ok
}

// modeTag is the text of the mode tag.
func (d *drawCtx) modeTag() string {
	if src, ok := d.v.src.(git.PRSource); ok {
		return d.withIcon(iconPR, prNumberText(src.Number))
	}

	if d.v.showsTurn() {
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
		{"w", "worktree"},
		{"f", "full"},
		{"u", "split"},
		{"b/t", "mode"},
	}

	if d.v.showsTurn() {
		pairs = append(pairs, [2]string{",/.", "turn"})
	}

	pairs = append(pairs, [2]string{"e", "edit"}, [2]string{"/", "search"})

	if _, ok := d.v.src.(git.PRSource); ok {
		pairs = [][2]string{{"j/k", "line"}, {"]/[", "hunk"}, {"b/t", "mode"}}
	}

	if d.tree {
		pairs = [][2]string{{"j/k", "file"}, {"enter", "jump"}, {"tab", "hide"}, {"esc", "back"}}
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
	case git.PRSource:
		lines = []string{"no changes against " + prBaseText(v)}
	case git.TurnSource:
		n := strconv.Itoa(v.shownTurn())
		if v.noSnapshot {
			lines = []string{"turn " + n + " has no snapshot", ", and . step through the turns; b shows the branch"}
		} else {
			lines = []string{"turn " + n + " changed nothing"}
		}
	}

	fg := d.ui.Subtle

	if v.err != nil {
		lines = []string{v.err.Error()}

		var gone errWorktreeRemoved
		if errors.As(v.err, &gone) {
			lines, fg = []string{noticeRemoved + shortPath(gone.root, "", d.home)}, d.ui.Muted
		}
	}

	if y >= len(lines) {
		return nil
	}

	text, _ := cleanText(lines[y])

	return []railRun{{text: "    "}, {text: text, fg: fg}}
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

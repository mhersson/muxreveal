package ui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/core/tiling"
	"github.com/mhersson/muxreveal/internal/git"
)

const (
	// slideFrame is the pause between two frames of the drawer's slide.
	slideFrame = 16 * time.Millisecond
	// wheelRows is how many rows one wheel notch scrolls the drawer.
	wheelRows = 3
)

// The drawer's key notices; the others are in reviewops.go.
const (
	noticeFileDeleted = "the file is deleted"
	noticeNarrow      = "side by side needs a wider drawer"
	noticeNotFound    = "not found: "
)

// reviewKey binds a key, while the drawer has the keyboard, to a command.
type reviewKey struct{ key, command string }

// reviewKeys is the drawer's own keymap. The leader works as everywhere; a key
// that is not here is dropped.
var reviewKeys = []reviewKey{
	{"j", "review.line.next"},
	{"down", "review.line.next"},
	{"k", "review.line.prev"},
	{"up", "review.line.prev"},
	{"]", "review.hunk.next"},
	{"[", "review.hunk.prev"},
	{"}", "review.file.next"},
	{"{", "review.file.prev"},
	{"ctrl+d", "review.page.down"},
	{"ctrl+u", "review.page.up"},
	{"g", "review.top"},
	{"G", "review.bottom"},
	{"tab", "review.tree"},
	{"f", "review.full"},
	{"u", "review.split"},
	{"enter", "review.fold"},
	{"b", "review.mode.branch"},
	{"t", "review.mode.turn"},
	{",", "review.turn.prev"},
	{".", "review.turn.next"},
	{"/", "review.search"},
	{"e", "review.edit"},
	{"esc", "review.back"},
}

// treeCommand reports whether a drawer command works while the tree has the keyboard.
func treeCommand(id string) bool {
	switch id {
	case "review.line.next", "review.line.prev", "review.fold", "review.tree", "review.back":
		return true
	}

	return false
}

// drawerSlideMsg is one frame of the drawer's slide.
type drawerSlideMsg struct{}

// slideOpenMsg is the answer to a drawer opened by leader d, which slides in.
type slideOpenMsg struct{ reviewOpenMsg }

// reviewCommands are the drawer's commands, each bound in reviewKeys or the leader keymap.
func reviewCommands() []command {
	cmd := func(id, title string, run func(*App) tea.Cmd) command {
		return command{id: id, icon: iconReview, title: title, run: run}
	}
	line := func(d int) func(*App) tea.Cmd { return func(a *App) tea.Cmd { return a.reviewLine(d) } }
	hunk := func(d int) func(*App) tea.Cmd {
		return func(a *App) tea.Cmd { return a.reviewJump((*reviewView).blockStarts, d) }
	}
	file := func(d int) func(*App) tea.Cmd {
		return func(a *App) tea.Cmd { return a.reviewJump((*reviewView).fileStarts, d) }
	}
	page := func(d int) func(*App) tea.Cmd { return func(a *App) tea.Cmd { return a.reviewPage(d) } }
	mode := func(m core.ReviewMode) func(*App) tea.Cmd { return func(a *App) tea.Cmd { return a.setReviewMode(m) } }
	turn := func(d int) func(*App) tea.Cmd { return func(a *App) tea.Cmd { return a.stepReviewTurn(d) } }

	return []command{
		cmd("review.toggle", "Review drawer", (*App).reviewToggle),
		cmd("review.line.next", "Review: next line", line(1)),
		cmd("review.line.prev", "Review: previous line", line(-1)),
		cmd("review.hunk.next", "Review: next hunk", hunk(1)),
		cmd("review.hunk.prev", "Review: previous hunk", hunk(-1)),
		cmd("review.file.next", "Review: next file", file(1)),
		cmd("review.file.prev", "Review: previous file", file(-1)),
		cmd("review.page.down", "Review: half a page down", page(1)),
		cmd("review.page.up", "Review: half a page up", page(-1)),
		cmd("review.top", "Review: top", func(a *App) tea.Cmd { return a.reviewEnd(false) }),
		cmd("review.bottom", "Review: bottom", func(a *App) tea.Cmd { return a.reviewEnd(true) }),
		cmd("review.tree", "Review: file tree", (*App).reviewTree),
		cmd("review.full", "Review: full file", (*App).toggleReviewFull),
		cmd("review.split", "Review: unified or side by side", (*App).reviewSplit),
		cmd("review.fold", "Review: fold or show removed lines", (*App).reviewFold),
		cmd("review.mode.branch", "Review: branch mode", mode(core.ReviewBranch)),
		cmd("review.mode.turn", "Review: turn mode", mode(core.ReviewTurn)),
		cmd("review.turn.prev", "Review: previous turn", turn(-1)),
		cmd("review.turn.next", "Review: next turn", turn(1)),
		cmd("review.search", "Review: search", (*App).reviewSearch),
		cmd("review.edit", "Review: open in the editor", (*App).reviewEdit),
		cmd("review.back", "Review: back to the panes", (*App).reviewBack),
	}
}

// ---- keyboard ----

// drawerHasKeys reports whether the drawer is open and has the keyboard,
// whether or not it has slid into view.
func (a *App) drawerHasKeys(st core.State) bool {
	_, ok := a.drawerFull(st)

	return ok && a.drawer.focused
}

// drawerTake gives the drawer the keyboard, with the diff holding it; the rail
// gives it up.
func (a *App) drawerTake() {
	a.railRelease()
	a.drawer.focused, a.drawer.tree = true, false
}

// drawerRelease takes the keyboard from the drawer and closes its search prompt.
func (a *App) drawerRelease() {
	a.drawer.focused, a.drawer.tree = false, false

	for _, v := range a.drawer.views {
		v.search = nil
	}
}

// handleDrawerKey runs a key while the drawer has the keyboard.
func (a *App) handleDrawerKey(msg tea.KeyPressMsg) tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if v.search != nil {
		a.searchKey(v, msg)

		return nil
	}

	key := bindingKey(msg)

	i := slices.IndexFunc(reviewKeys, func(k reviewKey) bool { return k.key == key })
	if i < 0 || (a.drawer.tree && !treeCommand(reviewKeys[i].command)) {
		return nil
	}

	c, ok := a.reg.get(reviewKeys[i].command)
	if !ok {
		a.log.Error("drawer key names a missing command", "key", key, "command", reviewKeys[i].command)

		return nil
	}

	return c.run(a)
}

// searchKey types into the open search prompt.
func (a *App) searchKey(v *reviewView, msg tea.KeyPressMsg) {
	switch key := msg.String(); {
	case key == "enter":
		a.reviewFind(v)
	case key == "esc":
		v.search = nil
	case key == "backspace":
		if r := []rune(*v.search); len(r) > 0 {
			*v.search = string(r[:len(r)-1])
		}
	case msg.Text != "":
		*v.search += printable(msg.Text)
	}
}

// drawerPaste appends pasted text to the search prompt while it is open;
// otherwise it is dropped.
func (a *App) drawerPaste(text string) {
	if v := a.drawer.views[a.store.State().Tab().ID]; v != nil && v.search != nil {
		*v.search += printable(text)
	}
}

// reviewFind jumps to the next row holding the typed query and closes the
// prompt; an empty query repeats the last one.
func (a *App) reviewFind(v *reviewView) {
	q := *v.search
	v.search = nil

	if q == "" {
		q = v.last
	}

	if q == "" {
		return
	}

	v.last = q

	i, ok := searchRows(v.res, v.rows, v.cursor, q)
	if !ok {
		a.notice = noticeNotFound + q

		return
	}

	a.reviewGo(v, i)
}

// reviewSearch opens the search prompt and gives the drawer the keyboard to type in it.
func (a *App) reviewSearch() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	a.drawerTake()

	q := ""
	v.search = &q

	return nil
}

// reviewBack closes the search prompt, leaves the tree for the diff, or else
// returns the keyboard to the panes.
func (a *App) reviewBack() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	switch {
	case v.search != nil:
		v.search = nil
	case a.drawer.tree:
		a.reviewLeaveTree(v)
	default:
		return a.drawerLeave()
	}

	return nil
}

// ---- moving ----

// reviewBodyH is the rows of the drawer's diff column.
func (a *App) reviewBodyH(v *reviewView) int {
	box, ok := a.drawerFull(a.store.State())
	if !ok {
		return 0
	}

	return a.drawerLayoutOf(box, v).bodyH
}

// reviewGo puts the cursor on a row and scrolls only as far as it needs.
func (a *App) reviewGo(v *reviewView, row int) {
	if len(v.rows) == 0 {
		return
	}

	v.cursor = min(max(row, 0), len(v.rows)-1)
	v.top = v.viewTop(a.reviewBodyH(v))
}

// reviewLine moves the cursor a row; in the tree it moves the selected file.
func (a *App) reviewLine(delta int) tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if !a.drawer.tree {
		a.reviewGo(v, v.cursor+delta)

		return nil
	}

	files := v.treeFiles()
	if len(files) == 0 {
		return nil
	}

	v.treeSel = files[min(max(slices.Index(files, v.treeSel)+delta, 0), len(files)-1)]

	return nil
}

func (a *App) reviewPage(dir int) tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	a.reviewGo(v, v.cursor+dir*max(a.reviewBodyH(v)/2, 1))

	return nil
}

func (a *App) reviewEnd(bottom bool) tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if bottom {
		a.reviewGo(v, len(v.rows)-1)
	} else {
		a.reviewGo(v, 0)
	}

	return nil
}

// reviewJump moves the cursor to the next (dir 1) or previous (-1) row among
// the starts the view lists; at the end it stays.
func (a *App) reviewJump(starts func(*reviewView) []int, dir int) tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	rows := starts(v)
	if dir < 0 {
		slices.Reverse(rows)
	}

	if i := slices.IndexFunc(rows, func(r int) bool { return (r-v.cursor)*dir > 0 }); i >= 0 {
		a.reviewGo(v, rows[i])
	}

	return nil
}

// blockStarts are the rows where a hunk begins or, in the full-file view, a
// block of changes does.
func (v *reviewView) blockStarts() []int {
	var out []int

	inBlock := false

	for i, r := range v.rows {
		change := (r.kind == rowFull && r.other < 0) || r.kind == rowGhost || r.kind == rowFold
		if r.kind == rowHunk || (change && !inBlock) {
			out = append(out, i)
		}

		inBlock = change
	}

	return out
}

// fileStarts are the rows of the files' headers.
func (v *reviewView) fileStarts() []int {
	var out []int

	for i, r := range v.rows {
		if r.kind == rowFile {
			out = append(out, i)
		}
	}

	return out
}

// reviewFold folds the ghost block under the cursor into one row, or shows it
// again from that row; in the tree it jumps to the selected file.
func (a *App) reviewFold() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if a.drawer.tree {
		a.reviewLeaveTree(v)

		return nil
	}

	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}

	r := v.rows[v.cursor]
	if r.kind != rowGhost && r.kind != rowFold {
		return nil
	}

	fold := r.kind == rowGhost

	if v.folded == nil {
		v.folded = map[string]bool{}
	}

	v.folded[foldKey(v.res.Files[r.file].Path, r.block)] = fold
	v.rebuild(v.rowsSplit)

	// The block's row is another one now: the fold row, or the first ghost line.
	if i := slices.IndexFunc(v.rows, func(n reviewRow) bool {
		return n.file == r.file && n.block == r.block && (n.kind == rowFold || (!fold && n.kind == rowGhost))
	}); i >= 0 {
		a.reviewGo(v, i)
	}

	return nil
}

// reviewSplit switches between unified and side by side; a drawer too narrow
// for two sides keeps showing unified and says so.
func (a *App) reviewSplit() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	v.split = !v.split

	if box, shown := a.drawerFull(a.store.State()); v.split && shown && !a.drawerLayoutOf(box, v).split {
		a.notice = noticeNarrow
	}

	return nil
}

// ---- tree ----

// treeFiles are the files' indexes in the order the tree lists them.
func (v *reviewView) treeFiles() []int {
	var out []int

	for _, it := range v.derived().tree {
		if !it.dir {
			out = append(out, it.file)
		}
	}

	return out
}

// reviewTree gives the tree the keyboard, or from the tree jumps to the
// selected file and gives the diff the keyboard back.
func (a *App) reviewTree() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if a.drawer.tree {
		a.reviewLeaveTree(v)

		return nil
	}

	a.drawerTake()
	a.drawer.tree, v.treeSel = true, 0

	if v.cursor >= 0 && v.cursor < len(v.rows) {
		v.treeSel = v.rows[v.cursor].file
	}

	return nil
}

func (a *App) reviewLeaveTree(v *reviewView) {
	a.reviewJumpFile(v, v.treeSel)
	a.drawer.tree = false
}

// reviewJumpFile puts the cursor on a file's header and scrolls it to the top.
func (a *App) reviewJumpFile(v *reviewView, file int) {
	i := slices.IndexFunc(v.rows, func(r reviewRow) bool { return r.kind == rowFile && r.file == file })
	if i < 0 {
		return
	}

	v.cursor, v.top = i, i
	v.top = v.viewTop(a.reviewBodyH(v))
}

// ---- editor ----

// reviewEditor is the editor the e key opens: $VISUAL, else $EDITOR, else vi.
func reviewEditor(getenv func(string) string) string {
	for _, k := range [...]string{"VISUAL", "EDITOR"} {
		if e := getenv(k); e != "" {
			return e
		}
	}

	return "vi"
}

// editLine is the line of f the row stands for: the cursor line's new number,
// its old number on a removed line, the hunk's new start on a hunk header and 1
// on a file header.
func editLine(f git.FileDiff, r reviewRow) int {
	line := 1

	switch r.kind {
	case rowHunk:
		line = f.Hunks[r.hunk].NewStart
	case rowLine, rowPair:
		h := f.Hunks[r.hunk]

		li := r.other
		if li < 0 {
			li = r.line
		}

		if l := h.Lines[li]; l.Kind == git.Del {
			line = l.Old
		} else {
			line = l.New
		}
	case rowFull, rowGhost, rowFold:
		line = r.line + 1
	}

	return max(line, 1)
}

// reviewEdit opens the file at the cursor line in the editor, in a new shell
// pane beside the drawer's pane, and closes the drawer so the pane shows.
func (a *App) reviewEdit() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok || v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}

	r := v.rows[v.cursor]
	f := v.res.Files[r.file]

	if f.Status == git.Deleted {
		a.notice = noticeFileDeleted

		return nil
	}

	// A path that starts with a dash would be read as an option.
	path := f.Path
	if strings.HasPrefix(path, "-") {
		path = "./" + path
	}

	run := []string{reviewEditor(a.getenv), "+" + strconv.Itoa(editLine(f, r)), path}

	cmd, opened := a.openPaneAt(core.KindShell, core.SplitVertical, tab.Review.Pane, v.root, run)
	if opened {
		a.closeDrawer(false)
	}

	return cmd
}

// ---- toggling, focus and the slide ----

// reviewToggle is leader d: it opens the drawer on the focused pane, or gives
// an open drawer the keyboard (after retargeting it to the focused pane), or
// closes a drawer that has it.
func (a *App) reviewToggle() tea.Cmd {
	a.endSlide()

	st := a.store.State()
	tab := st.Tab()

	switch {
	case tab.Review == nil:
		return a.openSliding(tab.Focus)
	case a.drawerHasKeys(st):
		return a.closeDrawer(true)
	}

	var cmd tea.Cmd
	if tab.Focus != "" && tab.Focus != tab.Review.Pane {
		cmd = a.openReview(tab.Focus, true)
	}

	a.drawerTake()

	return cmd
}

// openSliding opens the drawer on a pane the way openReview does and makes the
// answer slide it in.
func (a *App) openSliding(pane core.PaneID) tea.Cmd {
	cmd := a.openReview(pane, true)
	if cmd == nil {
		return nil
	}

	return func() tea.Msg {
		m := cmd()
		if o, ok := m.(reviewOpenMsg); ok {
			return slideOpenMsg{o}
		}

		return m
	}
}

// onSlideOpen opens the drawer and, if that opened one, starts its slide in.
func (a *App) onSlideOpen(msg slideOpenMsg) tea.Cmd {
	was := a.store.State().Tab().Review != nil
	cmd := a.onReviewOpen(msg.reviewOpenMsg)

	if _, shown := a.drawerFull(a.store.State()); was || !shown {
		return cmd
	}

	return tea.Batch(cmd, a.startSlide(false))
}

// closeDrawer closes the active tab's drawer and returns the keyboard to the
// panes, sliding out when slide is set.
func (a *App) closeDrawer(slide bool) tea.Cmd {
	a.drawerRelease()

	if _, shown := a.drawerFull(a.store.State()); slide && shown {
		return a.startSlide(true)
	}

	// dispatch logs its own failures.
	_ = a.dispatch(core.CloseReview{})

	return nil
}

// startSlide begins a slide in, or out when closing, and asks for its frames.
func (a *App) startSlide(closing bool) tea.Cmd {
	a.drawer.slideFrom, a.drawer.closing = a.clock.Now(), closing

	return a.slideFrameCmd()
}

// slideFrameCmd asks for the next frame unless one is already on its way; the
// one outstanding tick is the slide's only owner.
func (a *App) slideFrameCmd() tea.Cmd {
	if a.slideTicking {
		return nil
	}

	a.slideTicking = true

	return a.tick(slideFrame, func(time.Time) tea.Msg { return drawerSlideMsg{} })
}

// onDrawerSlide draws a frame and asks for the next while the slide runs.
func (a *App) onDrawerSlide() tea.Cmd {
	a.slideTicking = false

	if a.drawer.slideFrom.IsZero() {
		return nil
	}

	if a.clock.Now().Sub(a.drawer.slideFrom) >= slideFor {
		a.endSlide()

		return nil
	}

	return a.slideFrameCmd()
}

// endSlide ends a running slide at once. A drawer that was sliding out is
// closed; the active tab is the one that closes, since any action that could
// change it ends the slide first.
func (a *App) endSlide() {
	closing := a.drawer.closing
	a.drawer.slideFrom, a.drawer.closing = time.Time{}, false

	if closing && a.store.State().Tab().Review != nil {
		// dispatch logs its own failures.
		_ = a.dispatch(core.CloseReview{})
	}
}

// covered reports whether pane rect r lies wholly inside the drawer's box.
func covered(r, box core.Rect) bool {
	return r.X >= box.X && r.Y >= box.Y && r.X+r.W <= box.X+box.W && r.Y+r.H <= box.Y+box.H
}

// focusDir is leader h, j, k and l: a focus move that skips the panes the
// drawer covers. With the drawer showing, leader l with no visible pane to the
// right gives the drawer the keyboard, and leader h from the drawer leaves it.
func (a *App) focusDir(d core.Dir) tea.Cmd {
	st := a.store.State()

	box, shown := a.drawerFull(st)
	if !shown || a.drawer.closing {
		return a.act(core.FocusDir{Dir: d, Area: a.bodyArea()})
	}

	if a.drawer.focused && d == core.Left {
		return a.drawerLeave()
	}

	if id, ok := a.visibleNeighbour(st, d, box); ok {
		return a.act(core.FocusPane{ID: id})
	}

	if d == core.Right {
		a.drawerTake()
	}

	return nil
}

// visibleNeighbour is the pane a focus move reaches among those the drawer does
// not cover. Scrolling layouts step by column, which core decides, so there it
// repeats the step until it lands on a pane that shows.
func (a *App) visibleNeighbour(st core.State, d core.Dir, box core.Rect) (core.PaneID, bool) {
	tab, area := st.Tab(), a.bodyArea()

	if tab.Mode == core.ModeScrolling {
		sim := core.NewStore(st)

		for range core.MaxPanes {
			before := sim.State().Tab().Focus
			if err := sim.Dispatch(core.FocusDir{Dir: d, Area: area}); err != nil || sim.State().Tab().Focus == before {
				return "", false
			}

			now := sim.State()
			if r, ok := core.Arrange(now.Tab(), area, now.Layout).Rects[now.Tab().Focus]; !ok || !covered(r, box) {
				return now.Tab().Focus, true
			}
		}

		return "", false
	}

	side, ok := sideOf(d)
	rects := core.Arrange(tab, area, st.Layout).Rects
	cur, drawn := rects[tab.Focus]

	if !ok || !drawn {
		return "", false
	}

	var (
		ids   []core.PaneID
		cands []tiling.Rect
	)

	for _, id := range tab.Panes {
		if r, ok := rects[id]; ok && id != tab.Focus && !covered(r, box) {
			ids = append(ids, id)
			cands = append(cands, tiling.Rect(r))
		}
	}

	if i := tiling.Neighbour(tiling.Rect(cur), cands, side, false); i >= 0 {
		return ids[i], true
	}

	return "", false
}

func sideOf(d core.Dir) (tiling.Side, bool) {
	switch d {
	case core.Left:
		return tiling.SideLeft, true
	case core.Down:
		return tiling.SideDown, true
	case core.Up:
		return tiling.SideUp, true
	case core.Right:
		return tiling.SideRight, true
	}

	return 0, false
}

// drawerLeave gives the keyboard to the shown pane nearest the drawer's left
// edge; with no pane showing it stays with the drawer.
func (a *App) drawerLeave() tea.Cmd {
	st := a.store.State()

	box, shown := a.drawerFull(st)
	if !shown {
		return nil
	}

	rects := a.geometry().rects

	var (
		ids   []core.PaneID
		cands []tiling.Rect
	)

	for _, id := range st.Tab().Panes {
		if r, ok := rects[id]; ok && !covered(r, box) {
			ids = append(ids, id)
			cands = append(cands, tiling.Rect(r))
		}
	}

	// Neighbour takes a pane for one on the drawer's left only when its right edge
	// is left of the drawer's, which a pane reaching the body's edge is not: the
	// drawer is stretched to the right so that those count too.
	from := tiling.Rect{X: box.X, Y: box.Y, W: box.W + a.width, H: box.H}

	i := tiling.Neighbour(from, cands, tiling.SideLeft, false)
	if i < 0 {
		return nil
	}

	a.drawerRelease()

	return a.focusPane(ids[i])
}

// uncoverFocus closes the active tab's drawer, with no slide, when the focused
// pane lies under it: the pane an action just focused must show.
func (a *App) uncoverFocus() {
	st := a.store.State()

	box, shown := a.drawerFull(st)
	if !shown || a.drawer.closing {
		return
	}

	if r, ok := a.geometry().rects[st.Tab().Focus]; ok && covered(r, box) {
		a.closeDrawer(false)
	}
}

// settleKey is what settleDrawer's outcome depends on.
type settleKey struct {
	ok              bool
	ver             uint64
	w, h            int
	drawer, closing bool
	rail            bool
}

// settleDrawer keeps the keyboard with something that shows after an update. A
// drawer that cannot show holds nothing. When the keyboard is with the panes and
// the focused pane is under the drawer, either an action of the operator's just
// focused that pane, and the drawer closes, or something else did, and the
// drawer takes the keyboard. prev is the state before the update, and acted says
// the update was the operator's and changed the state. Updates that change none
// of what the outcome depends on cost one comparison.
func (a *App) settleDrawer(prev core.State, acted bool) {
	key := settleKey{
		ok: true, ver: a.storeVer, w: a.width, h: a.height,
		drawer: a.drawer.focused, closing: a.drawer.closing, rail: a.rail.focused,
	}
	if a.settled == key {
		return
	}

	if !a.settleDrawerNow(prev, acted) {
		a.settled = key
	}
}

// settleDrawerNow does settleDrawer's work and reports whether it changed anything.
func (a *App) settleDrawerNow(prev core.State, acted bool) bool {
	st := a.store.State()

	box, shown := a.drawerFull(st)
	if !shown {
		if a.drawer.focused {
			a.drawerRelease()

			return true
		}

		return false
	}

	if a.drawer.closing || a.drawer.focused || (a.rail.focused && a.railVisible()) {
		return false
	}

	tab := st.Tab()
	if r, ok := a.geometry().rects[tab.Focus]; !ok || !covered(r, box) {
		return false
	}

	if was, found := tabByID(prev, tab.ID); acted && found && was.Focus != tab.Focus {
		a.closeDrawer(false)
	} else {
		a.drawerTake()
	}

	return true
}

// ---- mouse ----

// inDrawer reports whether the cell lies on the part of the drawer on screen.
func (a *App) inDrawer(x, y int) bool {
	r, ok := a.drawerRect(a.store.State())

	return ok && x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// drawerClick gives the drawer the keyboard; on a diff row it moves the cursor
// there and on a tree file it jumps to it.
func (a *App) drawerClick(x, y int) {
	st := a.store.State()
	treeWas := a.drawer.focused && a.drawer.tree

	a.drawerTake()

	r, shown := a.drawerRect(st)
	box, _ := a.drawerFull(st)
	v := a.drawer.views[st.Tab().ID]

	if !shown || v == nil {
		return
	}

	lay := a.drawerLayoutOf(box, v)
	ix, iy := x-r.X-1, y-r.Y-1

	if iy < 0 || iy >= lay.bodyH || ix < 0 || ix >= lay.innerW {
		return
	}

	top := v.viewTop(lay.bodyH)
	d := &drawCtx{v: v, c: v.derived(), lay: lay, top: top, tree: treeWas}

	switch {
	case lay.treeW > 0 && ix < lay.treeW:
		// The tree's rows start after the mode row, the totals row and a blank one.
		if i := d.treeTop(lay.bodyH-3) + iy - 3; iy >= 3 && i >= 0 && i < len(d.c.tree) && !d.c.tree[i].dir {
			a.reviewJumpFile(v, d.c.tree[i].file)
		}
	case lay.treeW == 0 || ix > lay.treeW:
		i := top + iy

		if sticky, ok := d.stickyHeader(); iy == 0 && ok {
			i = sticky
		}

		if i < len(v.rows) {
			a.reviewGo(v, i)
		}
	}
}

// drawerWheel scrolls the drawer's diff 3 rows per notch (dir 1 is down); the
// cursor follows when it would leave the view.
func (a *App) drawerWheel(dir int) {
	v := a.drawer.views[a.store.State().Tab().ID]
	if v == nil || len(v.rows) == 0 {
		return
	}

	h := a.reviewBodyH(v)

	v.top = min(max(v.viewTop(h)+dir*wheelRows, 0), max(len(v.rows)-h, 0))
	v.cursor = min(max(v.cursor, v.top), max(min(v.top+h-1, len(v.rows)-1), 0))
}

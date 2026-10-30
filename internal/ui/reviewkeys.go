package ui

import (
	"path/filepath"
	"slices"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
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
	{"p", "review.mode.pr"},
	{",", "review.turn.prev"},
	{".", "review.turn.next"},
	{"/", "review.search"},
	{"w", "review.worktrees"},
	{"e", "review.edit"},
	{"r", "review.refresh"},
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
		cmd("review.mode.pr", "Review: pull request", (*App).reviewPR),
		cmd("review.turn.prev", "Review: previous turn", turn(-1)),
		cmd("review.turn.next", "Review: next turn", turn(1)),
		cmd("review.search", "Review: search", (*App).reviewSearch),
		cmd("review.worktrees", "Review: worktrees", (*App).openWorktrees),
		cmd("review.edit", "Review: open in the editor", (*App).reviewEdit),
		cmd("review.refresh", "Review: refresh", (*App).reviewReload),
		cmd("review.back", "Review: back to the panes", (*App).reviewBack),
	}
}

// ---- keyboard ----

// handleDrawerKey runs a key while the drawer has the keyboard.
func (a *App) handleDrawerKey(msg tea.KeyPressMsg) tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if a.listFor(tab) != nil {
		return a.handleWorktreesKey(msg)
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
	a.closeWorktrees()

	q := ""
	v.search = &q

	return nil
}

// reviewBack closes the search prompt, gives the diff the keyboard back from the
// tree, or else returns the keyboard to the panes.
func (a *App) reviewBack() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	switch {
	case v.search != nil:
		v.search = nil
	case a.drawer.tree:
		a.reviewLeaveTree(v, false)
	default:
		return a.drawerLeave()
	}

	return nil
}

// ---- moving ----

// reviewBodyH is the rows of the drawer's diff column.
func (a *App) reviewBodyH(v *reviewView) int {
	st := a.store.State()

	box, ok := a.drawerFull(st)
	if !ok {
		return 0
	}

	return a.drawerLayoutOf(box, v, drawerTree(st)).bodyH
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

// blockStarts are the rows where a hunk's first changed line is or, in the
// full-file view, a block of changes begins.
func (v *reviewView) blockStarts() []int {
	var out []int

	inBlock := false

	for i, r := range v.rows {
		change := (r.kind == rowFull && r.other < 0) || r.kind == rowGhost || r.kind == rowFold

		switch {
		case r.kind == rowHunk:
			out = append(out, v.firstChange(i))
		case change && !inBlock:
			out = append(out, i)
		}

		inBlock = change
	}

	return out
}

// firstChange is the row of the first added or removed line of the hunk whose
// header is row hdr; a hunk with none stands for its header.
func (v *reviewView) firstChange(hdr int) int {
	for i := hdr + 1; i < len(v.rows); i++ {
		r := v.rows[i]
		if r.kind != rowLine && r.kind != rowPair {
			break
		}

		lines := v.res.Files[r.file].Hunks[r.hunk].Lines
		if lines[r.line].Kind != git.Context || (r.other >= 0 && lines[r.other].Kind != git.Context) {
			return i
		}
	}

	return hdr
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
		a.reviewLeaveTree(v, true)

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

	st := a.store.State()

	if box, shown := a.drawerFull(st); v.split && shown && !a.drawerLayoutOf(box, v, drawerTree(st)).split {
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

// reviewTree is tab: it opens a hidden tree and gives it the keyboard, gives a
// shown tree the keyboard, and hides a tree that has it.
func (a *App) reviewTree() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if a.drawer.tree {
		// dispatch logs its own failures.
		_ = a.dispatch(core.SetReviewTree{Open: false})
		a.drawer.tree = false

		return nil
	}

	if !tab.Review.Tree {
		if err := a.dispatch(core.SetReviewTree{Open: true}); err != nil {
			return nil
		}
	}

	a.drawerTake()
	a.drawer.tree, v.treeSel = true, 0

	if v.cursor >= 0 && v.cursor < len(v.rows) {
		v.treeSel = v.rows[v.cursor].file
	}

	return nil
}

// reviewLeaveTree gives the diff the keyboard back; the tree stays open. With
// jump the cursor first goes to the selected file's header.
func (a *App) reviewLeaveTree(v *reviewView, jump bool) {
	if jump {
		a.reviewJumpFile(v, v.treeSel)
	}

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
// its old number on a removed line of a hunk, the hunk's new start on a hunk
// header, the first new line after the block on a ghost line of the full view
// (the last line when the block ends the file) and 1 on a file header.
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
	case rowFull:
		line = r.line + 1
	case rowGhost, rowFold:
		line = r.edit
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

	// An editor reads +{command} and a leading dash as options, and the pane
	// starts elsewhere when the root is gone; the absolute path is neither.
	path := filepath.Join(v.root, f.Path)

	run := &paneRun{program: reviewEditor(a.getenv), args: []string{"+" + strconv.Itoa(editLine(f, r)), path}}

	cmd, opened := a.openPaneAt(core.KindShell, core.SplitVertical, tab.Review.Pane, v.root, run)
	if opened {
		a.closeDrawer(false)
	}

	return cmd
}

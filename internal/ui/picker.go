package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/ui/palette"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const (
	pickerOpenTimeout   = 5 * time.Second // the repository check and the worktree list
	pickerProbeTimeout  = 5 * time.Second // the new-worktree page's probes
	pickerCreateTimeout = 2 * time.Minute // git checks a whole tree out
	maxPickerChars      = 200
)

// noticePrunable is shown for enter on a worktree whose directory is gone.
const noticePrunable = "that worktree is gone; git worktree prune removes it"

const (
	formLabelW  = len("branch")
	formValueAt = 1 + 2 + formLabelW + 2 // a cell, the marker, the label and its gap: where a field's text starts inside the box
)

type pickerPage int

const (
	pageWorktrees pickerPage = iota
	pageNewWorktree
)

// pickerState is the worktree picker's share of the App.
type pickerState struct {
	cfg  core.WorktreesConfig
	seq  int     // counts requests; an answer for an earlier one is dropped
	open *picker // nil unless the picker shows
}

// pickerTree is one listed worktree with the texts its row draws.
type pickerTree struct {
	wt           git.Worktree
	label, short string // the branch, or 7 characters of a detached HEAD; the shortened path
}

// pickerMatch is a row the query keeps and the byte offsets of its label it matched.
type pickerMatch struct {
	tree int
	hit  map[int]bool
}

// picker is the open worktree picker. It owns every key until it closes.
type picker struct {
	seq   int
	tab   core.TabID
	repo  git.Repo
	trees []pickerTree
	main  string // the main checkout's path; "" when the list has none
	home  string

	page    pickerPage
	query   textinput.Model
	matches []pickerMatch
	sel     int // an index into matches, or len(matches) for the new worktree row
	top     int
	form    *worktreeForm // nil until the new-worktree page was first shown
}

// worktreeForm is the new-worktree page.
type worktreeForm struct {
	branch, base textinput.Model
	onBase       bool // the base field has the keyboard
	tools        []git.Tool
	err          string
	busy         bool // a creation is running
}

// pickerOpenMsg answers the repository check of a new agent request.
type pickerOpenMsg struct {
	seq   int
	tab   core.TabID
	pane  core.PaneID // the focused pane when it was asked
	cwd   string
	repo  git.Repo
	trees []git.Worktree
	err   error
}

// pickerPageMsg answers the new-worktree page's probes.
type pickerPageMsg struct {
	seq     int
	base    string // the default base branch, "" when there is none
	baseErr error
	tools   []git.Tool
}

// pickerCreatedMsg answers a worktree creation.
type pickerCreatedMsg struct {
	seq     int
	created git.Created
	err     error
}

// pickerSource lets fuzzy search a worktree's label and path together.
type pickerSource []pickerTree

func (s pickerSource) String(i int) string { return s[i].label + " " + s[i].short }
func (s pickerSource) Len() int            { return len(s) }

// newAgent implements agent.new. Whether the focused pane's cwd is in a
// repository is found off the update loop: in one the picker opens on its
// worktrees, elsewhere the agent opens at once.
func (a *App) newAgent() tea.Cmd {
	if !a.started {
		return nil
	}

	st := a.store.State()
	if a.panesFull(st) {
		return nil
	}

	cwd, tab, pane := a.focusedCwd(st), st.Tab().ID, st.Tab().Focus

	a.picker.seq++
	seq := a.picker.seq

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, pickerOpenTimeout)
		defer cancel()

		msg := pickerOpenMsg{seq: seq, tab: tab, pane: pane, cwd: cwd}
		if msg.repo, msg.err = git.Open(ctx, cwd); msg.err == nil {
			msg.trees, msg.err = git.ListWorktrees(ctx, msg.repo)
		}

		return msg
	}
}

// onPickerOpen opens the picker, or the agent when there is nothing to pick. An
// answer for an earlier request, another tab or pane, or under another modal is
// dropped.
func (a *App) onPickerOpen(msg pickerOpenMsg) tea.Cmd {
	st := a.store.State()
	if msg.seq != a.picker.seq || st.Tab().ID != msg.tab || st.Tab().Focus != msg.pane ||
		a.overlay != nil || a.confirm != nil || a.prompt != nil || a.copy != nil {
		return nil
	}

	if errors.Is(msg.err, context.Canceled) {
		return nil
	}

	home := a.getenv("HOME")

	trees, main := pickerTrees(msg.trees, home)
	if msg.err == nil && len(trees) > 0 {
		a.endLeader()

		p := &picker{seq: msg.seq, tab: msg.tab, repo: msg.repo, trees: trees, main: main, home: home, query: newPickerInput()}
		p.refilter()
		a.picker.open = p

		return nil
	}

	if msg.err != nil && !errors.Is(msg.err, git.ErrNotRepo) {
		a.log.Warn("new agent: list worktrees", "cwd", msg.cwd, "err", msg.err)
	}

	return a.openPaneIn(core.KindAgent, core.SplitVertical, msg.cwd)
}

// pickerTrees prepares the rows for list, skipping a bare repository's entry,
// and finds the main checkout their paths are shortened against.
func pickerTrees(list []git.Worktree, home string) (trees []pickerTree, main string) {
	for _, w := range list {
		if w.Main && !w.Bare {
			main = w.Path
		}
	}

	for _, w := range list {
		if !w.Bare {
			trees = append(trees, pickerTree{wt: w, label: worktreeLabel(w), short: shortPath(w.Path, main, home)})
		}
	}

	return trees, main
}

func worktreeLabel(w git.Worktree) string {
	switch {
	case w.Branch != "":
		return w.Branch
	case w.Head != "":
		return w.Head[:min(7, len(w.Head))]
	}

	return "(unknown)"
}

// shortPath is path relative to the main checkout when it lies inside it, else
// with ~ for the home directory.
func shortPath(path, main, home string) string {
	if rel, ok := within(main, path); ok {
		return rel
	}

	if rel, ok := within(home, path); ok {
		return "~/" + rel
	}

	return path
}

// within is path relative to dir when it lies strictly inside it.
func within(dir, path string) (string, bool) {
	if dir == "" {
		return "", false
	}

	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return rel, true
}

func newPickerInput() textinput.Model {
	in := textinput.New()
	// The picker draws the text itself and places the terminal's cursor.
	in.SetVirtualCursor(false)
	in.KeyMap.Paste.SetEnabled(false)
	in.CharLimit = maxPickerChars
	in.Focus()

	return in
}

// refilter rebuilds the rows from the query and selects the first.
func (p *picker) refilter() {
	p.matches = p.matches[:0:0]

	if q := p.query.Value(); q == "" {
		for i := range p.trees {
			p.matches = append(p.matches, pickerMatch{tree: i})
		}
	} else {
		for _, r := range fuzzy.FindFrom(q, pickerSource(p.trees)) {
			hit := make(map[int]bool, len(r.MatchedIndexes))
			for _, ix := range r.MatchedIndexes {
				hit[ix] = true
			}

			p.matches = append(p.matches, pickerMatch{tree: r.Index, hit: hit})
		}
	}

	p.sel, p.top = 0, 0
}

// move selects the row delta away, wrapping; the new worktree row is the last.
func (p *picker) move(delta int) {
	n := len(p.matches) + 1
	p.sel = ((p.sel+delta)%n + n) % n

	if p.sel < p.top {
		p.top = p.sel
	}

	if p.sel >= p.top+palette.MaxRows {
		p.top = p.sel - palette.MaxRows + 1
	}
}

// edit gives msg to the input that has the keyboard and keeps what depends on
// its text in step.
func (p *picker) edit(msg tea.Msg) {
	if p.page == pageWorktrees {
		before := p.query.Value()

		p.query, _ = p.query.Update(msg)
		if p.query.Value() != before {
			p.refilter()
		}

		return
	}

	in := &p.form.branch
	if p.form.onBase {
		in = &p.form.base
	}

	before := in.Value()

	*in, _ = in.Update(msg)
	if in.Value() != before {
		p.form.err = ""
	}
}

// pickerUpdate handles a message for the open picker and reports whether it
// took it: keys and pastes edit it, and the pointer reaches nothing under it.
// The picker closes once another tab shows.
func (a *App) pickerUpdate(msg tea.Msg) (tea.Cmd, bool) {
	p := a.picker.open
	if a.store.State().Tab().ID != p.tab {
		a.picker.open = nil

		return nil, false
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		a.notice = ""

		if p.page == pageNewWorktree {
			return a.pickerFormKey(p, msg), true
		}

		return a.pickerListKey(p, msg), true
	case tea.PasteMsg:
		p.edit(msg)

		return nil, true
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg:
		a.releaseForwarded()
		a.mouse.press = nil

		return nil, true
	}

	return nil, false
}

func (a *App) pickerListKey(p *picker, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		a.picker.open = nil
	case "down", "ctrl+n", "tab":
		p.move(1)
	case "up", "ctrl+p", "shift+tab":
		p.move(-1)
	case "enter":
		return a.pickerChoose(p)
	default:
		p.edit(msg)
	}

	return nil
}

// pickerChoose opens an agent in the selected worktree, or the new-worktree page.
func (a *App) pickerChoose(p *picker) tea.Cmd {
	if p.sel >= len(p.matches) {
		return a.pickerShowForm(p)
	}

	t := p.trees[p.matches[p.sel].tree]
	if t.wt.Prunable {
		a.notice = noticePrunable

		return nil
	}

	a.picker.open = nil

	return a.openPaneIn(core.KindAgent, core.SplitVertical, t.wt.Path)
}

// pickerShowForm shows the new-worktree page. The first time it also asks for
// the default base and the tools that need an ignore entry.
func (a *App) pickerShowForm(p *picker) tea.Cmd {
	p.page = pageNewWorktree
	if p.form != nil {
		return nil
	}

	p.form = &worktreeForm{branch: newPickerInput(), base: newPickerInput()}
	repo, seq, sibling := p.repo, p.seq, a.picker.cfg.Sibling()

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, pickerProbeTimeout)
		defer cancel()

		msg := pickerPageMsg{seq: seq}
		msg.base, msg.baseErr = git.DefaultBranch(ctx, repo)

		// Only the inside root puts a worktree where these tools look.
		if main, err := repo.MainRoot(); err == nil && !sibling {
			msg.tools, _ = git.DetectTools(main) // a check that failed is skipped; the others still count
		}

		return msg
	}
}

// onPickerPage fills the page from the probes' answer, keeping what was typed.
func (a *App) onPickerPage(msg pickerPageMsg) {
	p := a.picker.open
	if p == nil || p.seq != msg.seq || p.form == nil {
		return
	}

	if msg.baseErr != nil && !errors.Is(msg.baseErr, git.ErrNoBase) && !errors.Is(msg.baseErr, context.Canceled) {
		a.log.Debug("new worktree: default branch", "err", msg.baseErr)
	}

	if p.form.base.Value() == "" {
		p.form.base.SetValue(msg.base)
	}

	p.form.tools = msg.tools
}

func (a *App) pickerFormKey(p *picker, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.page = pageWorktrees
	case "tab", "shift+tab":
		p.form.onBase = !p.form.onBase
	case "enter":
		return a.pickerCreate(p)
	default:
		p.edit(msg)
	}

	return nil
}

// rootStyle maps [worktrees] root to the layout git creates.
func (a *App) rootStyle() git.RootStyle {
	if a.picker.cfg.Sibling() {
		return git.RootSibling
	}

	return git.RootInside
}

// pickerCreate starts creating the worktree the page describes.
func (a *App) pickerCreate(p *picker) tea.Cmd {
	f := p.form
	if f.busy {
		return nil
	}

	branch := strings.TrimSpace(f.branch.Value())
	if branch == "" {
		f.err = "enter a branch name"

		return nil
	}

	if _, err := git.WorktreeDir(branch); err != nil {
		f.err = worktreeReason(err)

		return nil
	}

	nw := git.NewWorktree{Branch: branch, Base: strings.TrimSpace(f.base.Value()), Root: a.rootStyle()}
	repo, seq := p.repo, p.seq
	f.busy, f.err = true, ""

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, pickerCreateTimeout)
		defer cancel()

		created, err := git.CreateWorktree(ctx, repo, nw)

		return pickerCreatedMsg{seq: seq, created: created, err: err}
	}
}

// onPickerCreated opens an agent in the new worktree, or shows why there is
// none and stays on the page. An answer for a picker that closed is dropped;
// the worktree it made stays.
func (a *App) onPickerCreated(msg pickerCreatedMsg) tea.Cmd {
	p := a.picker.open
	if p == nil || p.seq != msg.seq || p.form == nil {
		return nil
	}

	p.form.busy = false

	path := msg.created.Worktree.Path
	if path == "" {
		p.form.err = worktreeReason(msg.err)

		return nil
	}

	// A path with an error means the worktree exists but git could not list it.
	if msg.err != nil {
		a.log.Warn("new worktree", "path", path, "err", msg.err)
	}

	a.picker.open = nil

	ver := a.storeVer
	cmd := a.openPaneIn(core.KindAgent, core.SplitVertical, path)

	notice := "created " + shortPath(path, p.main, p.home)
	if a.storeVer == ver && a.notice != "" {
		notice += "; " + a.notice
	}

	a.notice = notice

	return cmd
}

// worktreeReason is err in a few words for the page.
func worktreeReason(err error) string {
	switch {
	case err == nil:
		return "the worktree was not created"
	case errors.Is(err, git.ErrExists):
		return "that path already exists"
	case errors.Is(err, git.ErrBadBranch):
		return "not a valid branch name"
	case errors.Is(err, git.ErrNoBase):
		return "no usable base branch; name one in the base field"
	case errors.Is(err, git.ErrBare):
		return `no main checkout; set root = "sibling" in [worktrees]`
	}

	first, _, _ := strings.Cut(err.Error(), "\n")

	return first
}

// worktreePreview is where a worktree for branch would go, and why it cannot.
// It follows git.CreateWorktree's layout without running git.
func (a *App) worktreePreview(repo git.Repo, branch string) (string, error) {
	name, err := git.WorktreeDir(branch)
	if err != nil {
		return "", err
	}

	main, mainErr := repo.MainRoot()

	switch {
	case !a.picker.cfg.Sibling():
		if mainErr != nil {
			return "", mainErr
		}

		return filepath.Join(main, ".worktrees", name), nil
	case mainErr == nil:
		return filepath.Join(main+".worktrees", name), nil
	}

	// Without a main checkout, /x/repo.git gives /x/repo.worktrees.
	base := strings.TrimSuffix(filepath.Base(repo.CommonDir), ".git")
	if base == "" {
		return "", mainErr
	}

	return filepath.Join(filepath.Dir(repo.CommonDir), base+".worktrees", name), nil
}

// agentsIn counts the agent panes whose repository root is path.
func (a *App) agentsIn(path string) int {
	n := 0

	for _, id := range a.store.State().AgentPanes() {
		if root, ok := a.paneRoot(id); ok && filepath.Clean(root) == filepath.Clean(path) {
			n++
		}
	}

	return n
}

// pickerBox is the box's left column, top row and width; ok is false when the
// terminal is too small to draw it.
func (a *App) pickerBox() (left, top, w int, ok bool) {
	w = min(maxOverlayWidth, a.width-4)
	if w < 2 || a.height < 2 {
		return 0, 0, 0, false
	}

	return (a.width - w) / 2, a.height / 4, w, true
}

// overlayPicker draws the picker over base, centred with its top at a quarter
// of the height and clipped to the body rows.
func (a *App) overlayPicker(base string, lk look) string {
	_, top, w, ok := a.pickerBox()
	if !ok {
		return base
	}

	p := a.picker.open
	pt := pickerPaint{ui: lk.theme.UI, inner: w - 2}

	lines := a.pickerListLines(p, pt)
	if p.page == pageNewWorktree {
		lines = a.pickerFormLines(p, pt)
	}

	return overlayLines(base, a.width, a.height, top, lines, w)
}

// pickerCursor places the terminal's cursor in the field that has the keyboard;
// nil when the box is clipped away.
func (a *App) pickerCursor() *tea.Cursor {
	p := a.picker.open

	left, top, w, ok := a.pickerBox()
	if !ok {
		return nil
	}

	inner := w - 2
	row, col := 1, 0

	if p.page == pageNewWorktree {
		in := p.form.branch
		if p.form.onBase {
			in, row = p.form.base, 2
		}

		_, c := fieldView(in, max(inner-formValueAt-1, 0), lipgloss.NewStyle())
		col = formValueAt + c
	} else {
		// A cell and the prompt come first.
		_, c := fieldView(p.query, inner-4, lipgloss.NewStyle())
		col = 3 + c
	}

	if top+row >= a.height-1 {
		return nil
	}

	return tea.NewCursor(left+1+col, top+row)
}

// fieldView draws an input's text in at most w cells, scrolled left so the
// cursor's cell stays inside, and returns the cursor's column in it.
func fieldView(in textinput.Model, w int, style lipgloss.Style) (string, int) {
	runes := []rune(in.Value())
	pos := min(in.Position(), len(runes))
	start := 0

	for start < pos && drawnWidth(string(runes[start:pos])) >= w {
		start++
	}

	return style.Render(ansi.Truncate(string(runes[start:]), max(w, 0), "")), drawnWidth(string(runes[start:pos]))
}

// pickerPaint draws the picker's box on the panel colour.
type pickerPaint struct {
	ui    theme.UI
	inner int // the cells between the side borders
}

// pickerSeg is a run of a row: text and its style.
type pickerSeg struct {
	text  string
	style lipgloss.Style
}

func (pt pickerPaint) on(fg theme.Hex) lipgloss.Style {
	return lipgloss.NewStyle().Background(pt.ui.Panel).Foreground(fg)
}

func (pt pickerPaint) border() lipgloss.Style { return pt.on(pt.ui.BorderFocus) }

// row puts content between the side borders, cut or padded to the inner width.
func (pt pickerPaint) row(content string, fill lipgloss.Style) string {
	content = ansi.Truncate(content, pt.inner, "")
	pad := fill.Render(strings.Repeat(" ", max(0, pt.inner-lipgloss.Width(content))))
	b := pt.border()

	return b.Render("│") + content + pad + b.Render("│")
}

// top is the top border with the title two cells from the corner.
func (pt pickerPaint) top(title string) string {
	b := pt.border()
	dash := min(1, pt.inner)
	title = ansi.Truncate(" "+title+" ", pt.inner-dash, "")

	return b.Render("╭"+strings.Repeat("─", dash)) + pt.on(pt.ui.Accent).Bold(true).Render(title) +
		b.Render(strings.Repeat("─", pt.inner-dash-lipgloss.Width(title))+"╮")
}

func (pt pickerPaint) divider() string {
	return pt.border().Render("├" + strings.Repeat("─", pt.inner) + "┤")
}

// bottom is the bottom border with count ending one dash before the corner;
// the count is left out when it does not fit.
func (pt pickerPaint) bottom(count string) string {
	b := pt.border()

	cw := lipgloss.Width(count)
	if count == "" || pt.inner < cw+1 {
		return b.Render("╰" + strings.Repeat("─", pt.inner) + "╯")
	}

	return b.Render("╰"+strings.Repeat("─", pt.inner-cw-1)) + pt.on(pt.ui.Muted).Render(count) + b.Render("─╯")
}

// hints is the key hint row: each key in Key bold and its label in Muted, the
// pairs three cells apart, after one cell of margin.
func (pt pickerPaint) hints(pairs [][2]string) string {
	fill := pt.on(pt.ui.Fg)

	var sb strings.Builder

	sb.WriteString(fill.Render(" "))

	for i, h := range pairs {
		if i > 0 {
			sb.WriteString(fill.Render("   "))
		}

		sb.WriteString(pt.on(pt.ui.Key).Bold(true).Render(h[0]))
		sb.WriteString(pt.on(pt.ui.Muted).Render(" " + h[1]))
	}

	return sb.String()
}

func segsWidth(segs []pickerSeg) int {
	w := 0

	for i, s := range segs {
		if i > 0 {
			w += 2
		}

		w += lipgloss.Width(s.text)
	}

	return w
}

// renderSegs joins segs two cells apart, the gap in gap's style.
func renderSegs(segs []pickerSeg, gap lipgloss.Style) string {
	var sb strings.Builder

	for i, s := range segs {
		if i > 0 {
			sb.WriteString(gap.Render("  "))
		}

		sb.WriteString(s.style.Render(s.text))
	}

	return sb.String()
}

func (a *App) pickerListLines(p *picker, pt pickerPaint) []string {
	ui := pt.ui
	fill := pt.on(ui.Fg)
	rows := len(p.matches) + 1

	query, _ := fieldView(p.query, pt.inner-4, fill)
	lines := []string{
		pt.top(a.titled(iconAgent, "New agent")),
		pt.row(fill.Render(" ")+pt.on(ui.Accent).Render("❯ ")+query, fill),
		pt.divider(),
	}

	for i := p.top; i < min(rows, p.top+palette.MaxRows); i++ {
		lines = append(lines, a.pickerRow(p, pt, i))
	}

	return append(lines,
		pt.divider(),
		pt.row(pt.hints([][2]string{{"up/down", "move"}, {"enter", "open"}, {"esc", "close"}}), fill),
		pt.bottom(" "+strconv.Itoa(p.sel+1)+" of "+strconv.Itoa(rows)+" "),
	)
}

// pickerRow draws row i: a worktree, or the new worktree row after the last.
func (a *App) pickerRow(p *picker, pt pickerPaint, i int) string {
	ui := pt.ui
	bg, selected := ui.Panel, i == p.sel

	if selected {
		bg = ui.Selection
	}

	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Background(bg).Foreground(fg) }
	base := on(ui.Fg)
	title := base

	if selected {
		title = base.Bold(true)
	}

	iconColour := ui.Muted
	if selected {
		iconColour = ui.Accent
	}

	if i == len(p.matches) {
		label, lead := "+ new worktree…", ""
		if a.icons {
			label, lead = "new worktree…", on(iconColour).Render(iconNewWorktree)+base.Render(" ")
		}

		return pt.row(base.Render(" ")+lead+title.Render(label), base)
	}

	m := p.matches[i]
	t := p.trees[m.tree]
	avail := pt.inner - 2 // a cell of margin on each side
	lead := ""

	if a.icons {
		colour := ui.Git
		if selected {
			colour = ui.Accent
		}

		lead = on(colour).Render(iconGit) + base.Render(" ")
		avail -= 2
	}

	var marks []pickerSeg

	if n := a.agentsIn(t.wt.Path); n > 0 {
		text := "● " + strconv.Itoa(n) + " agent"
		if n > 1 {
			text += "s"
		}

		marks = append(marks, pickerSeg{text, on(ui.Agent)})
	}

	if t.wt.Locked {
		marks = append(marks, pickerSeg{"locked", on(ui.Warning)})
	}

	if t.wt.Prunable {
		marks = append(marks, pickerSeg{"prunable", on(ui.Subtle)})
	}

	// The path goes first when the row is too narrow, then the label is cut.
	right := append(slices.Clone(marks), pickerSeg{t.short, on(ui.Muted)})
	if lipgloss.Width(t.label)+2+segsWidth(right) > avail {
		right = marks
	}

	room := avail
	if len(right) > 0 {
		room = max(avail-segsWidth(right)-2, 1)
	}

	branch, used := pickerLabel(t.label, room, m.hit, title, on(ui.Match).Bold(true))
	content := base.Render(" ") + lead + branch

	if len(right) > 0 {
		content += base.Render(strings.Repeat(" ", max(avail-used-segsWidth(right), 0))) + renderSegs(right, base)
	}

	return pt.row(content, base)
}

// pickerLabel draws text in room cells, cut with an ellipsis, with the runes at
// the byte offsets in hit emphasised; it returns the cells used.
func pickerLabel(text string, room int, hit map[int]bool, normal, match lipgloss.Style) (string, int) {
	cut := lipgloss.Width(text) > room
	limit := room

	if cut {
		limit = max(room-1, 0)
	}

	var sb, run strings.Builder

	used, runHit := 0, false
	flush := func() {
		if run.Len() == 0 {
			return
		}

		if runHit {
			sb.WriteString(match.Render(run.String()))
		} else {
			sb.WriteString(normal.Render(run.String()))
		}

		run.Reset()
	}

	for bi, r := range text {
		w := lipgloss.Width(string(r))
		if used+w > limit {
			break
		}

		used += w

		if hit[bi] != runHit {
			flush()

			runHit = hit[bi]
		}

		run.WriteRune(r)
	}

	flush()

	if cut && room > 0 {
		sb.WriteString(normal.Render("…"))

		used++
	}

	return sb.String(), used
}

func (a *App) pickerFormLines(p *picker, pt pickerPaint) []string {
	ui, f := pt.ui, p.form
	fill := pt.on(ui.Fg)
	valueW := max(pt.inner-formValueAt-1, 0)

	field := func(label, text string, focused bool) string {
		marker := fill.Render("  ")
		if focused {
			marker = pt.on(ui.Accent).Render("❯") + fill.Render(" ")
		}

		return pt.row(fill.Render(" ")+marker+pt.on(ui.Muted).Render(fmt.Sprintf("%-*s", formLabelW, label))+fill.Render("  ")+text, fill)
	}

	branch, _ := fieldView(f.branch, valueW, fill)
	base, _ := fieldView(f.base, valueW, fill)

	var path string

	if name := strings.TrimSpace(f.branch.Value()); name == "" {
		path = pt.on(ui.Subtle).Render("(from the branch name)")
	} else if dest, err := a.worktreePreview(p.repo, name); err != nil {
		path = pt.on(ui.Error).Render(worktreeReason(err))
	} else {
		path = pt.on(ui.Muted).Render(shortPath(dest, p.main, p.home))
	}

	var status string

	switch {
	case f.busy:
		status = pt.on(ui.Muted).Render("creating the worktree…")
	case f.err != "":
		status = pt.on(ui.Error).Render(f.err)
	}

	lines := []string{
		pt.top(a.titled(iconNewWorktree, "New worktree")),
		field("branch", branch, !f.onBase),
		field("base", base, f.onBase),
		field("path", path, false),
		pt.row(fill.Render(" ")+status, fill),
	}

	for _, t := range f.tools {
		lead, room := "", pt.inner-2 // a cell of margin on each side
		if a.icons {
			lead, room = pt.on(ui.Warning).Render(iconAlert)+fill.Render(" "), room-2
		}

		text := ansi.Truncate(t.Name+": "+t.Hint, max(room, 0), "…")
		lines = append(lines, pt.row(fill.Render(" ")+lead+pt.on(ui.Warning).Render(text), fill))
	}

	return append(lines,
		pt.divider(),
		pt.row(pt.hints([][2]string{{"tab", "next field"}, {"enter", "create"}, {"esc", "back"}}), fill),
		pt.bottom(""),
	)
}

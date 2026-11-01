package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/github"
)

const (
	// checkLogTimeout bounds one read of a job's log.
	checkLogTimeout = 30 * time.Second
	// actionsApp is the slug of the app whose check runs are Actions jobs.
	actionsApp = "github-actions"
	// logLead is how many rows stay above the first error when the page opens.
	logLead = 3
)

// The log page's texts.
const (
	noticeLogRunning = "the log is ready when the job ends"
	noticeNoLog      = "no log for %s; O opens its page"
	logLoading       = "loading the log…"
	logReadFailed    = "could not read the log: %s"
	logEarlier       = "earlier lines not loaded"

	summaryLoading    = "loading the summary…"
	summaryReadFailed = "could not read the summary: %s"
)

// checkPage is a job's log shown in the checks overlay. check is the row as it
// was when the page opened; seq numbers the read whose answer the page waits for.
type checkPage struct {
	check   checkRow
	seq     int
	loading bool
	loaded  bool // some answer arrived, so refreshing keeps the lines
	err     string
	log     github.JobLog
	open    map[int]bool // groups the user opened or closed, by index
	cursor  int          // a row of logRows
	top     int

	// A run that is not an Actions job shows its summary: sum is set, summary
	// is nil until the read answers, and chooser holds a line's several links.
	sum     bool
	summary *mdView
	chooser *linkChooser
}

// checkLogMsg answers one read of a job's log.
type checkLogMsg struct {
	view *checksView
	seq  int
	log  github.JobLog
	err  error
}

// checkSummaryMsg answers one read of a check run's summary. doc is the parsed
// summary; empty says the run wrote none.
type checkSummaryMsg struct {
	view  *checksView
	seq   int
	doc   *mdDoc
	empty bool
	err   error
}

// logRow is one shown row of the log page: a line of the log, or -1 for the
// row that says earlier lines were not loaded.
type logRow struct{ line int }

// logGroupOpen says whether group g shows its lines: as the user left it, else
// when it holds the first error.
func logGroupOpen(l github.JobLog, open map[int]bool, g int) bool {
	if v, ok := open[g]; ok {
		return v
	}

	return l.FirstError >= l.Groups[g].Head && l.FirstError < l.Groups[g].End
}

// logRows lists the rows shown: every line outside a group, and a group's head
// with, when it is open, its lines.
func logRows(l github.JobLog, open map[int]bool) []logRow {
	rows := make([]logRow, 0, len(l.Lines)+1)
	if l.Cut {
		rows = append(rows, logRow{line: -1})
	}

	for i, ln := range l.Lines {
		if g := ln.Group; g >= 0 && g < len(l.Groups) && i != l.Groups[g].Head && !logGroupOpen(l, open, g) {
			continue
		}

		rows = append(rows, logRow{line: i})
	}

	return rows
}

// rows is the page's shown rows.
func (p *checkPage) rows() []logRow { return logRows(p.log, p.open) }

// head reports the group a row is the head of.
func (p *checkPage) head(r logRow) (g int, ok bool) {
	if r.line < 0 {
		return 0, false
	}

	g = p.log.Lines[r.line].Group

	return g, g >= 0 && g < len(p.log.Groups) && p.log.Groups[g].Head == r.line
}

// fit holds the cursor and the top inside the rows, the cursor in view of a
// window of n rows.
func (p *checkPage) fit(total, n int) {
	n = max(n, 1)
	p.cursor = min(max(p.cursor, 0), max(total-1, 0))

	if p.cursor < p.top {
		p.top = p.cursor
	}

	if p.cursor >= p.top+n {
		p.top = p.cursor - n + 1
	}

	p.top = min(max(p.top, 0), max(total-n, 0))
}

// place puts the cursor on the first error with logLead rows above it, else on
// the last row at the end.
func (p *checkPage) place(n int) {
	rows := p.rows()
	p.cursor = max(len(rows)-1, 0)
	p.top = len(rows)

	for i, r := range rows {
		if r.line == p.log.FirstError && r.line >= 0 {
			p.cursor, p.top = i, i-logLead

			break
		}
	}

	p.fit(len(rows), n)
}

// move puts the cursor on row i.
func (p *checkPage) move(i, n int) {
	p.cursor = i
	p.fit(len(p.rows()), n)
}

// errorRow finds the next (dir 1) or previous (dir -1) shown error row from
// the cursor.
func (p *checkPage) errorRow(dir int) (int, bool) {
	rows := p.rows()

	for i := p.cursor + dir; i >= 0 && i < len(rows); i += dir {
		if rows[i].line >= 0 && p.log.Lines[rows[i].line].Kind == github.LogError {
			return i, true
		}
	}

	return 0, false
}

// fold opens or closes the group the cursor is on the head of.
func (p *checkPage) fold() {
	rows := p.rows()
	if p.cursor >= len(rows) {
		return
	}

	if g, ok := p.head(rows[p.cursor]); ok {
		p.open[g] = !logGroupOpen(p.log, p.open, g)
	}
}

// logPageRows is how many log rows the page shows at the terminal's size.
func (a *App) logPageRows() int {
	_, _, _, h, ok := logBox(a.width, a.height)
	if !ok {
		return 1
	}

	rows, _, _, _ := logSplit(h)

	return rows
}

// checksLog is the checks.log command: it opens the page of the job log of r,
// or says why there is none.
func (a *App) checksLog(v *checksView, r checkRow) tea.Cmd {
	switch {
	case r.id == 0:
		a.notice = fmt.Sprintf(noticeNoLog, printable(r.name))

		return nil
	case r.kind == checkRunning:
		a.notice = noticeLogRunning

		return nil
	case r.kind == checkSkipped:
		a.notice = fmt.Sprintf(noticeNoLog, printable(r.name))

		return nil
	case r.app != actionsApp:
		v.page = &checkPage{check: r, sum: true}

		return a.readCheckRun(v)
	}

	v.page = &checkPage{check: r, open: map[int]bool{}}

	return a.readJobLog(v)
}

// readCheckRun reads the summary of the page's run and parses it off the update
// loop, under the timeout and the app's context.
func (a *App) readCheckRun(v *checksView) tea.Cmd {
	p := v.page
	a.logSeq++
	p.seq, p.loading, p.err = a.logSeq, true, ""

	repo, ok := repoOfKey(v.repo)
	if !ok || a.reads == nil {
		p.loading, p.err = false, "not available"

		return nil
	}

	seq, id, parent, reads, tok := p.seq, p.check.id, a.ctx, a.reads, a.drawer.hl.HighlightLang

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, checkLogTimeout)
		defer cancel()

		out, err := reads.CheckRun(ctx, repo, id)
		if err != nil {
			return checkSummaryMsg{view: v, seq: seq, err: err}
		}

		if strings.TrimSpace(out.Summary) == "" {
			return checkSummaryMsg{view: v, seq: seq, empty: true}
		}

		return checkSummaryMsg{view: v, seq: seq, doc: parseMarkdown([]byte(out.Summary), "", tok)}
	}
}

// onCheckSummary keeps a summary's answer, dropping one for a page that closed,
// an overlay that closed or a read a newer one superseded. An empty summary
// goes back to the list with a notice.
func (a *App) onCheckSummary(msg checkSummaryMsg) {
	v := msg.view
	if a.checks != v || v == nil || v.page == nil || !v.page.sum || v.page.seq != msg.seq {
		return
	}

	p := v.page
	p.loading = false

	switch {
	case msg.err != nil:
		p.err = printable(msg.err.Error())
	case msg.empty:
		a.notice = fmt.Sprintf(noticeNoLog, printable(p.check.name))
		v.page = nil
	default:
		host := ""
		if r, ok := repoOfKey(v.repo); ok {
			host = r.Host
		}

		p.err, p.loaded, p.chooser = "", true, nil

		msg.doc.withPictures(docPictures{reg: a.pictures, host: host})

		old := p.summary
		p.summary = &mdView{doc: msg.doc}

		if old != nil {
			p.summary.top, p.summary.cursor = old.top, old.cursor
		}

		a.sizeSummary(p)
		p.summary.clamp()
	}
}

// moveSummary, pageSummary and endSummary move the summary's cursor; they do
// nothing while it is loading.
func (p *checkPage) moveSummary(d int) {
	if p.summary != nil {
		p.summary.move(d)
	}
}

func (p *checkPage) pageSummary(dir int) {
	if p.summary != nil {
		p.summary.page(dir)
	}
}

func (p *checkPage) endSummary(bottom bool) {
	if p.summary != nil {
		p.summary.end(bottom)
	}
}

// summaryRows is how many summary rows the page shows: its box has no status
// row, so the log's status row goes to the summary.
func summaryRows(h int) int {
	rows, _, status, _ := logSplit(h)
	if status {
		rows++
	}

	return rows
}

// sizeSummary sets the cells the summary draws in, keeping the cursor on its
// source line when the width changes. A box too small to draw leaves it alone.
func (a *App) sizeSummary(p *checkPage) {
	_, _, w, h, ok := logBox(a.width, a.height)
	if !ok || p.summary == nil {
		return
	}

	v, rows := p.summary, summaryRows(h)
	if v.w != 0 && (v.w != w-2 || v.icons != a.icons) {
		line := v.sourceLine()
		v.size(w-2, rows, a.icons)
		v.goSource(line)

		return
	}

	v.size(w-2, rows, a.icons)
}

// summaryEnter is enter on the summary: a fold head toggles, one link opens,
// several open the chooser. Only web links are targets; a summary has no base
// to resolve a path against.
func (a *App) summaryEnter(p *checkPage) tea.Cmd {
	v := p.summary
	if v == nil || v.fold() {
		return nil
	}

	targets := slices.DeleteFunc(v.targets(), func(t mdTarget) bool { return t.Path != "" || t.URL == "" })

	switch len(targets) {
	case 0:
		return nil
	case 1:
		return a.openURL(targets[0].URL)
	}

	p.chooser = &linkChooser{targets: targets}

	return nil
}

// summaryChooserKey runs a key while the page's link chooser has the keyboard.
func (a *App) summaryChooserKey(p *checkPage, key string) tea.Cmd {
	pick, done := p.chooser.key(key)
	if !done {
		return nil
	}

	p.chooser = nil

	if pick == nil {
		return nil
	}

	return a.openURL(pick.URL)
}

// readJobLog reads the log of the page's job off the update loop, under the
// timeout and the app's context.
func (a *App) readJobLog(v *checksView) tea.Cmd {
	p := v.page
	a.logSeq++
	p.seq, p.loading, p.err = a.logSeq, true, ""

	repo, ok := repoOfKey(v.repo)
	if !ok || a.reads == nil {
		p.loading, p.err = false, "not available"

		return nil
	}

	seq, id, parent, reads := p.seq, p.check.id, a.ctx, a.reads

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, checkLogTimeout)
		defer cancel()

		data, cut, err := reads.JobLog(ctx, repo, id)
		if err != nil {
			return checkLogMsg{view: v, seq: seq, err: err}
		}

		return checkLogMsg{view: v, seq: seq, log: github.ParseJobLog(data, cut)}
	}
}

// onCheckLog keeps a log's answer. One for a page that closed, an overlay that
// closed or a read a newer one superseded is dropped.
func (a *App) onCheckLog(msg checkLogMsg) {
	v := msg.view
	if a.checks != v || v == nil || v.page == nil || v.page.seq != msg.seq {
		return
	}

	p := v.page
	p.loading = false

	if msg.err != nil {
		p.err = printable(msg.err.Error())

		return
	}

	n := a.logPageRows()

	if !p.loaded {
		p.err, p.loaded, p.log, p.open = "", true, msg.log, map[int]bool{}
		p.place(n)

		return
	}

	// A refresh keeps the opened groups and the cursor's line, clamped to the new log.
	line, above := -1, p.cursor-p.top
	if rows := p.rows(); p.cursor < len(rows) {
		line = rows[p.cursor].line
	}

	p.err, p.log = "", msg.log
	line = min(line, len(p.log.Lines)-1)

	// A line a closed group hides lands on the nearest row above it.
	rows := p.rows()
	i := 0

	for k, r := range rows {
		if r.line <= line {
			i = k
		}
	}

	p.cursor, p.top = i, i-above
	p.fit(len(rows), n)
}

// logWheel scrolls the log page when the wheel is over its box and reports
// whether it took the event. The cursor moves only to stay in view.
func (a *App) logWheel(msg tea.MouseWheelMsg) bool {
	if a.checks == nil || a.checks.page == nil {
		return false
	}

	m := msg.Mouse()

	x, y, w, h, ok := logBox(a.width, a.height)
	if !ok || m.X < x || m.X >= x+w || m.Y < y || m.Y >= y+h {
		return false
	}

	p := a.checks.page

	if p.sum {
		if p.summary != nil && p.chooser == nil {
			switch m.Button {
			case tea.MouseWheelDown:
				p.summary.scroll(scrollStep)
			case tea.MouseWheelUp:
				p.summary.scroll(-scrollStep)
			}
		}

		return true
	}

	n, _, _, _ := logSplit(h)
	total := len(p.rows())

	switch m.Button {
	case tea.MouseWheelDown:
		p.top += scrollStep
	case tea.MouseWheelUp:
		p.top -= scrollStep
	}

	p.top = min(max(p.top, 0), max(total-n, 0))
	p.cursor = min(max(p.cursor, p.top), max(p.top+n-1, 0))
	p.fit(total, n)

	return true
}

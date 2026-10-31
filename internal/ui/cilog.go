package ui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/github"
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
}

// checkLogMsg answers one read of a job's log.
type checkLogMsg struct {
	view *checksView
	seq  int
	log  github.JobLog
	err  error
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
	case r.app != actionsApp:
		a.notice = fmt.Sprintf(noticeNoLog, printable(r.name))

		return nil
	}

	v.page = &checkPage{check: r, open: map[int]bool{}}

	return a.readJobLog(v)
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

	p.err, p.loaded, p.log, p.open = "", true, msg.log, map[int]bool{}
	p.place(a.logPageRows())
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

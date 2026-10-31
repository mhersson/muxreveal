package ui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/github"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const (
	// logMaxBox caps the log page's box.
	logMaxBox = 140
	// logMinBox is the narrowest box that is drawn.
	logMinBox = 24
	// logMinRows is how many log rows the box keeps before its other rows go.
	logMinRows = 3
	// logTab is the distance between tab stops.
	logTab = 4
)

// logBox is the log page's box on a width x height terminal. ok is false for a
// terminal too small to draw it; the page then stays open unseen.
func logBox(width, height int) (x, y, w, h int, ok bool) {
	w, h = min(logMaxBox, width-4), height-4
	if w < logMinBox || h < logMinRows {
		return 0, 0, 0, 0, false
	}

	return (width - w) / 2, 2, w, h, true
}

// logSplit splits a box of h rows: the log rows, and which of the blank rows,
// the status row and the hint row fit. The log keeps logMinRows rows while it
// can: the blank rows go first, then the status row, then the hint row.
func logSplit(h int) (rows int, blanks, status, hints bool) {
	switch {
	case h-6 >= logMinRows:
		return h - 6, true, true, true
	case h-4 >= logMinRows:
		return h - 4, false, true, true
	case h-3 >= logMinRows:
		return h - 3, false, false, true
	}

	return max(h-2, 1), false, false, false
}

// ansiColor is the theme colour of a foreground SGR colour 0-15; any other
// value, the default colour among them, has none.
func ansiColor(i int8, p theme.Palette) theme.Hex {
	switch i {
	case 0:
		return p.Surface1
	case 1, 9:
		return p.Red
	case 2, 10:
		return p.Green
	case 3, 11:
		return p.Yellow
	case 4, 12:
		return p.Blue
	case 5, 13:
		return p.Pink
	case 6, 14:
		return p.Teal
	case 7:
		return p.Subtext1
	case 8:
		return p.Surface2
	case 15:
		return p.Subtext0
	}

	return ""
}

// logTabs expands tabs to the next logTab column stop, for text that starts at
// column col, and drops every other control character.
func logTabs(s string, col int) string {
	if !strings.ContainsRune(s, '\t') {
		return printable(s)
	}

	var b strings.Builder

	for _, r := range s {
		if r == '\t' {
			n := logTab - col%logTab
			b.WriteString(strings.Repeat(" ", n))

			col += n

			continue
		}

		t := printable(string(r))
		b.WriteString(t)
		col += ansi.StringWidth(t)
	}

	return b.String()
}

// logStatus is the status row's words: the check's state, with the failed step
// for a failed job whose log holds an error, then the time it took and the job.
func (a *App) logStatus(p *checkPage, on func(theme.Hex) lipgloss.Style, ui theme.UI) string {
	c := p.check

	word := map[checkKind]string{checkFailed: "failed", checkPassed: "passed", checkSkipped: "skipped"}[c.kind]
	if c.kind == checkRunning {
		word = "running"
	}

	out := ""

	if step := logFailedStep(p.log); c.kind == checkFailed && p.loaded && step != "" {
		out = on(ui.Muted).Render(word+" at step ") + on(ui.Error).Bold(true).Render(printable(step))
	} else {
		out = on(ui.Muted).Render(word)
	}

	tail := "   "
	if c.duration != "" {
		tail += c.duration + "   "
	}

	return out + on(ui.Subtle).Render(tail+"job "+strconv.FormatInt(c.id, 10))
}

// logFailedStep is the title of the last group that begins before the first
// error; empty when the log has no error or no group before it.
func logFailedStep(l github.JobLog) string {
	if l.FirstError < 0 {
		return ""
	}

	for g := len(l.Groups) - 1; g >= 0; g-- {
		if l.Groups[g].Head < l.FirstError {
			return l.Groups[g].Title
		}
	}

	return ""
}

// logLine draws row r of the page cw cells wide on the box's inner width: the
// cursor mark, the time, the group's indent and the text, cut with … two cells
// short of the border.
func (a *App) logLine(lk look, p *checkPage, r logRow, cursor bool, inner int) string {
	ui, pal := lk.theme.UI, lk.theme.Palette
	bg := ui.Panel

	if cursor {
		bg = ui.Selection
	}

	on := func(fg theme.Hex, bold bool) lipgloss.Style {
		return lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(bold)
	}
	border := on(ui.BorderFocus, false)

	mark := " "
	if cursor {
		mark = "▎"
	}

	var body string

	switch {
	case r.line < 0:
		body = on(ui.Subtle, false).Render(logEarlier)
	default:
		body = a.logText(p, r.line, ui, pal, on)
	}

	cw := inner - 4
	body = ansi.Truncate(body, cw, "…")
	fill := strings.Repeat(" ", max(cw-ansi.StringWidth(body), 0))

	return border.Render("│") + on(ui.Accent, true).Render(mark) + on(ui.Fg, false).Render(" ") + body +
		on(ui.Fg, false).Render(fill+"  ") + border.Render("│")
}

// logText draws the text of line i: its time, its indent and its spans in the
// colours its kind and its own styling give.
func (a *App) logText(p *checkPage, i int, ui theme.UI, pal theme.Palette, on func(theme.Hex, bool) lipgloss.Style) string {
	ln := p.log.Lines[i]

	var b strings.Builder

	if !ln.At.IsZero() {
		b.WriteString(on(ui.Subtle, false).Render(ln.At.In(a.clock.Now().Location()).Format("15:04:05") + " "))
	}

	if g, head := p.head(logRow{line: i}); head {
		mark := "▸ "
		if logGroupOpen(p.log, p.open, g) {
			mark = "▾ "
		}

		b.WriteString(on(ui.Muted, true).Render(mark + logTabs(p.log.Groups[g].Title, 0)))

		return b.String()
	}

	if ln.Group >= 0 {
		b.WriteString(on(ui.Fg, false).Render("  "))
	}

	fg, bold, kinded := ui.Fg, false, true

	switch ln.Kind {
	case github.LogError:
		fg, bold = ui.Error, true
	case github.LogWarning:
		fg, bold = ui.Warning, true
	case github.LogNotice, github.LogCommand:
		fg = ui.Info
	case github.LogDebug:
		fg = ui.Subtle
	default:
		kinded = false
	}

	col := 0

	for _, s := range ln.Spans {
		text := logTabs(s.Text, col)
		col += ansi.StringWidth(text)

		sfg, sbold := fg, bold

		if !kinded {
			sfg, sbold = ui.Fg, s.Bold

			if c := ansiColor(s.Color, pal); c != "" {
				sfg = c
			}
		}

		b.WriteString(on(sfg, sbold).Render(text))
	}

	return b.String()
}

// logLines draws the box w x h: the title in the top border, the status row,
// the log rows and the hints.
func (a *App) logLines(lk look, w, h int) []string {
	ui, st := lk.theme.UI, lk.styles
	p := a.checks.page
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Background(ui.Panel).Foreground(fg) }

	title := " #" + strconv.Itoa(a.checks.number) + " " + printable(p.check.name) + " log "
	if a.icons {
		title = " " + iconLog + title
	}

	top, row, bottom := boxFrame(w, title, boxStyle{border: st.popupBorder, title: st.pushTitle, cut: "…"}, st)
	n, blanks, status, hints := logSplit(h)

	rows := p.rows()
	p.fit(len(rows), n)

	body := make([]string, 0, n)

	switch {
	case p.err != "":
		body = append(body, row(on(ui.Muted).Render(fmt.Sprintf(logReadFailed, p.err))))
	case p.loading && !p.loaded:
		body = append(body, row(on(ui.Muted).Render(logLoading)))
	default:
		for i := p.top; i < min(p.top+n, len(rows)); i++ {
			body = append(body, a.logLine(lk, p, rows[i], i == p.cursor, w-2))
		}
	}

	for len(body) < n {
		body = append(body, row(""))
	}

	out := []string{top}
	if status {
		out = append(out, row(a.logStatus(p, on, ui)))
	}

	if blanks {
		out = append(out, row(""))
	}

	out = append(out, body...)

	if blanks {
		out = append(out, row(""))
	}

	if hints {
		kt := a.keys.checks
		out = append(out, row(a.logHints(st, kt, w-5)))
	}

	return append(out, bottom)
}

// logHints is the page's hint row in cw cells: the close pair always stays and
// the others drop from the end before it does.
func (a *App) logHints(st styles, kt keyTable, cw int) string {
	lead := hintPairs([][2]string{
		{kt.hint("checks.next", "checks.prev"), "line"},
		{kt.hint("checks.error.next", "checks.error.prev"), "error"},
		{kt.hint("checks.fold"), "group"},
		{kt.hint("checks.browser"), "browser"},
	})

	closing := hintPairs([][2]string{{kt.hint("checks.close"), "checks"}})
	if len(closing) == 0 {
		return popupHints(st, lead, cw)
	}

	const gap = 4

	room := cw - ansi.StringWidth(closing[0][0]) - 1 - ansi.StringWidth(closing[0][1]) - gap
	hints := popupHints(st, lead, room)

	if hints != "" {
		hints += st.popup.Render(strings.Repeat(" ", gap))
	}

	return hints + popupHints(st, closing, cw)
}

// overlayCILog draws the log page over base; a terminal too small for the box
// draws nothing.
func (a *App) overlayCILog(f *frameLayers, base string, lk look) string {
	x, y, w, h, ok := logBox(a.width, a.height)
	if !ok {
		return base
	}

	return overlayAt(f, base, a.width, a.height, x, y, a.logLines(lk, w, h), nil)
}

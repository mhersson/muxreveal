package ui

import (
	"fmt"
	"image"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// convMinRows is how many view rows the box keeps before its blank rows, then
// its hint row, then its header go.
const convMinRows = 3

// convRows splits a box of h rows: the view's rows, and which of the three
// blank rows, the hint row and the header (the title, the meta row and the
// rule) fit. The view keeps convMinRows rows while it can.
func convRows(h int) (rows int, blanks, hint, header bool) {
	switch {
	case h-9 >= convMinRows:
		return h - 9, true, true, true
	case h-6 >= convMinRows:
		return h - 6, false, true, true
	case h-5 >= convMinRows:
		return h - 5, false, false, true
	}

	return max(h-2, 1), false, false, false
}

// convLines draws the box w x h: the title in the top border, the pull request's
// header, the view centred in the inner width and the hints. pics are the view's
// picture rows in the box's cells.
func (a *App) convLines(lk look, w, h int) (out []string, pics []picRow) {
	v := a.conv
	ui := lk.theme.UI
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(ui.Panel) }
	border := on(ui.BorderFocus)
	pad := func(n int) string { return on(ui.Fg).Render(strings.Repeat(" ", max(n, 0))) }

	rows, blanks, hint, header := convRows(h)
	inner := w - 2
	dx, _, vw, _ := convDocCells(w, h)
	off := dx - 1
	tw := vw - 4

	row := func(content string, cw int) string {
		return border.Render("│") + pad(off) + content + pad(inner-off-cw) + border.Render("│")
	}
	text := func(content string) string { return row(pad(2)+content, 2+ansi.StringWidth(ansi.Strip(content))) }

	title := " #" + strconv.Itoa(v.number) + " conversation "
	if a.icons {
		title = " " + iconConversation + title
	}

	title = ansi.Truncate(title, w-4, "")
	top := border.Render("╭─") + on(ui.Info).Bold(true).Render(title) +
		border.Render(strings.Repeat("─", w-3-ansi.StringWidth(title))+"╮")

	a.sizeConv()

	out = []string{top}
	if blanks {
		out = append(out, row("", 0))
	}

	if header {
		info, ok := prInfoByNumber(a.store.State(), v.repo, v.number)
		if !ok {
			info = v.pr
		}

		p := railPainter{bg: ui.Panel}

		out = append(out,
			text(p.line([]railRun{{text: prNumberText(v.number) + " " + printable(info.Title), fg: ui.Fg, bold: true}}, tw)),
			text(p.line(prMetaRunsOf(info, info.HeadBranch, info.BaseBranch, a.icons, ui, a.clock.Now()), tw)),
			text(border.Foreground(ui.Border).Render(strings.Repeat("─", max(tw, 0)))),
		)

		if blanks {
			out = append(out, row("", 0))
		}
	}

	first := len(out)

	switch {
	case v.err != "" && v.doc == nil:
		for _, l := range wrapRows(fmt.Sprintf(convReadFailed, v.err), tw, rows) {
			out = append(out, text(on(ui.Muted).Render(l)))
		}
	case v.doc == nil:
		out = append(out, text(on(ui.Muted).Render(convLoading)))
	default:
		drawn, viewPics := v.view.drawPictures(newMDPainter(lk.theme, ui.Panel).withQuery(v.query), true)
		for _, l := range drawn {
			out = append(out, row(l, vw))
		}

		for _, p := range viewPics {
			p.X += 1 + off
			p.Y += first
			pics = append(pics, p)
		}
	}

	for len(out)-first < rows {
		out = append(out, row("", 0))
	}

	if blanks {
		out = append(out, row("", 0))
	}

	if hint {
		hints := convHints(lk.styles, a.keys.reader, tw)
		if v.searching {
			hints = searchPromptRow(ui, tw, v.query)
		}

		out = append(out, row(pad(2)+hints, 2+ansi.StringWidth(ansi.Strip(hints))))
	}

	out = append(out, border.Render("╰"+strings.Repeat("─", w-2)+"╯"))

	if v.chooser != nil {
		if covered := a.chooserLines(v.chooser, ui, out); covered.Dx() > 0 {
			pics = dropCovered(pics, []image.Rectangle{covered})
		}
	}

	return out, pics
}

// convHints is the overlay's hint row in cw cells, with the keys as kt binds them: the
// pairs that do not fit drop from the end, and close always stays. A pair with no key
// is left out.
func convHints(st styles, kt keyTable, cw int) string {
	var lead, closing [][2]string

	for _, d := range []struct {
		label string
		ids   []string
	}{
		{"line", []string{"reader.line.next", "reader.line.prev"}},
		{"item", []string{"reader.section.next", "reader.section.prev"}},
		{"open", []string{"reader.open"}},
		{"search", []string{"reader.search"}},
		{"refresh", []string{"reader.refresh"}},
	} {
		if k := kt.hint(d.ids...); k != "" {
			lead = append(lead, [2]string{k, d.label})
		}
	}

	closeKey, _ := kt.first("reader.close")
	closing = append(closing, [2]string{closeKey, "close"})

	const gap = 4

	room := cw - ansi.StringWidth(closeKey+" close") - gap
	hints := popupHints(st, lead, room)

	if hints != "" {
		hints += st.popup.Render(strings.Repeat(" ", gap))
	}

	return hints + popupHints(st, closing, cw)
}

// convDocCells places the view in a box w x h, in the box's cells: the view's left
// cell and first row, its width and its rows.
func convDocCells(w, h int) (x, y, vw, rows int) {
	rows, blanks, _, header := convRows(h)
	vw = min(w-4, planMaxView)
	x, y = 1+(w-2-vw)/2, 1

	if blanks {
		y++
	}

	if header {
		y += 3
		if blanks {
			y++
		}
	}

	return x, y, vw, rows
}

// convDocRect is the screen cells of the overlay's view rows. ok is false for a
// terminal too small to draw the box.
func convDocRect(width, height int) (image.Rectangle, bool) {
	bx, by, w, h, ok := planBox(width, height)
	if !ok {
		return image.Rectangle{}, false
	}

	x, y, vw, rows := convDocCells(w, h)

	return image.Rect(bx+x, by+y, bx+x+vw, by+y+rows), true
}

// overlayConv draws the conversation overlay over base; a terminal too small for
// the box draws nothing.
func (a *App) overlayConv(f *frameLayers, base string, lk look) string {
	x, y, w, h, ok := planBox(a.width, a.height)
	if !ok {
		return base
	}

	lines, pics := a.convLines(lk, w, h)

	return overlayAt(f, base, a.width, a.height, x, y, lines, pics)
}

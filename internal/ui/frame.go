package ui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
)

// bodyRect is the area between the workspace bar on row 0 and the status bar
// on the last row.
func bodyRect(width, height int) core.Rect {
	return core.Rect{Y: 1, W: width, H: max(height-2, 0)}
}

// minFrame is the smallest body, in rows and columns, that has room for a frame.
const minFrame = 3

// innerRect is the body inside its frame: the left and right frame columns and
// the bottom frame row are outside it. A body under minFrame has no inner rect.
func innerRect(body core.Rect) core.Rect {
	if body.W < minFrame || body.H < minFrame {
		return core.Rect{}
	}

	return core.Rect{X: body.X + 1, Y: body.Y, W: body.W - 2, H: body.H - 1}
}

// contentRect is a pane rect without its top border row.
func contentRect(r core.Rect) core.Rect {
	return core.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}
}

// geometry is the pane rects (top border row included) of one View, computed
// once.
type geometry struct {
	rects map[core.PaneID]core.Rect
}

// layoutRects returns each visible pane's rect. A zoomed pane is the only one
// and fills the inner rect.
func (a *App) layoutRects() map[core.PaneID]core.Rect {
	return a.geometry().rects
}

func (a *App) geometry() geometry {
	st := a.store.State()

	return geometry{rects: a.layoutIn(st, st.Active)}
}

// tabRects lays tab i out in the inner rect with its own zoom: a zoomed tab's
// focused pane gets the whole inner rect.
func (a *App) tabRects(i int) map[core.PaneID]core.Rect {
	return a.layoutIn(a.store.State(), i)
}

func (a *App) layoutIn(st core.State, i int) map[core.PaneID]core.Rect {
	if i < 0 || i >= len(st.Tabs) {
		return nil
	}

	t := st.Tabs[i]
	inner := innerRect(bodyRect(a.width, a.height))

	if t.Zoomed {
		if _, ok := st.Panes[t.Focus]; ok {
			rects := map[core.PaneID]core.Rect{}
			if inner.W >= 1 && inner.H >= 1 {
				rects[t.Focus] = inner
			}

			return rects
		}
	}

	return core.Rects(t.Layout, inner)
}

// segment is a run of cells already styled, starting at column x.
type segment struct {
	x, w int
	text string
}

// frameAt draws the whole screen: the workspace bar, the body and the status
// bar, exactly height lines of exactly width cells. Height 1 is the status bar
// only and height 2 the two bars. An empty tab shows the welcome card.
func (a *App) frameAt(g geometry) string {
	if a.width <= 0 || a.height <= 0 {
		return ""
	}

	lk := a.current()
	st := a.store.State()
	lines := make([]string, 0, a.height)

	if a.height >= 2 {
		lines = append(lines, a.workspaceBar(a.width))
	}

	if bodyH := a.height - 2; bodyH > 0 {
		if st.Tab().Layout == nil {
			lines = append(lines, strings.Split(renderBody(a.width, bodyH, lk.styles, lk.sample, a.welcomeHints()), "\n")...)
		} else {
			lines = append(lines, a.frameBody(g, st, lk)...)
		}
	}

	lines = append(lines, a.statusBar(a.width, st))

	return strings.Join(lines, "\n")
}

// frameBody draws the body rows between the two bars: the frames, titles and
// pane content.
func (a *App) frameBody(g geometry, st core.State, lk look) []string {
	rows := make([][]segment, a.height)
	add := func(y int, s segment) {
		if y >= 0 && y < len(rows) && s.w > 0 {
			rows[y] = append(rows[y], s)
		}
	}

	grid := newBorderGrid(a.width, a.height)
	focus := st.Tab().Focus

	for _, r := range g.rects {
		grid.addBox(r, ownNone)
	}

	if r, ok := g.rects[focus]; ok {
		grid.addBox(r, kindOwner(st.Panes[focus].Kind))
	}

	for id, r := range g.rects {
		for _, s := range a.topBorder(grid, st, id, r, id == focus, lk.styles) {
			add(r.Y, s)
		}
	}

	for id, r := range g.rects {
		c := contentRect(r)
		var lines []string
		fit := false

		if p := a.panes[id]; p != nil {
			lines = p.lines()
			cols, prows := p.size()
			fit = cols != c.W || prows != c.H
		}

		for i := range c.H {
			text := ""
			if i < len(lines) {
				text = lines[i]
			}

			if fit || i >= len(lines) {
				text = fitWidth(text, c.W, lk.styles.body)
			}

			add(c.Y+i, segment{c.X, c.W, text})
		}
	}

	grid.runs(lk.styles, add)

	lines := make([]string, 0, a.height-2)

	var sb strings.Builder

	for _, segs := range rows[1 : a.height-1] {
		slices.SortFunc(segs, func(p, q segment) int { return p.x - q.x })
		sb.Reset()

		x := 0

		for _, s := range segs {
			if s.x < x {
				continue
			}

			sb.WriteString(spaces(lk.styles.body, s.x-x))
			sb.WriteString(s.text)
			x = s.x + s.w
		}

		sb.WriteString(spaces(lk.styles.body, a.width-x))
		lines = append(lines, sb.String())
	}

	return lines
}

// welcomeHints lists the commands that make sense with no pane open, with
// their full key sequences.
func (a *App) welcomeHints() []hint {
	ids := []string{"palette.open", "agent.new", "pane.split.down", "pane.split.right", "theme.pick", "app.quit"}
	hints := make([]hint, 0, len(ids))
	for _, id := range ids {
		key, ok := a.keymap.keyFor(id)
		c, found := a.reg.get(id)
		if ok && found {
			hints = append(hints, hint{key: key, title: c.title})
		}
	}
	return hints
}

// spaces is n body-coloured blank cells.
func spaces(body lipgloss.Style, n int) string {
	if n <= 0 {
		return ""
	}
	return body.Render(strings.Repeat(" ", n))
}

// fitWidth makes s exactly w cells: a guest row that does not match its rect
// (before the pane is resized) is cut or padded rather than breaking the frame.
func fitWidth(s string, w int, body lipgloss.Style) string {
	got := ansi.StringWidth(s)
	switch {
	case got == w:
		return s
	case got > w:
		return ansi.Truncate(s, w, "")
	}
	return s + spaces(body, w-got)
}

// printable drops control characters that guest or filesystem text may carry.
func printable(t string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, t)
}

// cursorView places the focused pane's cursor on screen; nil when the guest
// hides it, it falls outside the pane, or an overlay, a confirm or the which-key popup is open.
func (a *App) cursorView() *tea.Cursor { return a.cursorAt(a.geometry()) }

func (a *App) cursorAt(g geometry) *tea.Cursor {
	if a.overlay != nil || a.popup || a.confirm != nil {
		return nil
	}
	st := a.store.State()
	p := a.panes[st.Tab().Focus]
	r, ok := g.rects[st.Tab().Focus]
	if p == nil || !ok {
		return nil
	}
	pos, visible := p.cursor()
	c := contentRect(r)
	if !visible || pos.X < 0 || pos.Y < 0 || pos.X >= c.W || pos.Y >= c.H {
		return nil
	}
	return tea.NewCursor(c.X+pos.X, c.Y+pos.Y)
}

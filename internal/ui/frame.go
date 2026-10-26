package ui

import (
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
)

const (
	separatorGlyph = "│"
	zoomTag        = " zoom "
)

// bodyRect is the area above the status bar.
func bodyRect(width, height int) core.Rect {
	return core.Rect{W: width, H: height - 1}
}

// contentRect is a pane rect without its header row.
func contentRect(r core.Rect) core.Rect {
	return core.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}
}

// geometry is the pane rects (header row included) and the separators of one
// View, computed once.
type geometry struct {
	rects map[core.PaneID]core.Rect
	seps  []core.Rect
}

// layoutRects returns each visible pane's rect. A zoomed pane is the only one
// and fills the body.
func (a *App) layoutRects() map[core.PaneID]core.Rect {
	return a.geometry().rects
}

func (a *App) geometry() geometry {
	st := a.store.State()
	body := bodyRect(a.width, a.height)
	if st.Zoomed {
		if _, ok := st.Panes[st.Focus]; ok {
			g := geometry{rects: map[core.PaneID]core.Rect{}}
			if body.W >= 1 && body.H >= 1 {
				g.rects[st.Focus] = body
			}
			return g
		}
	}
	return geometry{rects: core.Rects(st.Layout, body), seps: core.Separators(st.Layout, body)}
}

// segment is a run of cells already styled, starting at column x.
type segment struct {
	x, w int
	text string
}

// frameAt draws the body and the status bar: exactly height lines of exactly
// width cells. An empty layout shows the welcome card.
func (a *App) frameAt(g geometry) string {
	if a.width <= 0 || a.height <= 0 {
		return ""
	}
	lk := a.current()
	bar := barState{leader: a.keymap.leader, pending: a.pending, notice: a.notice}
	st := a.store.State()
	if st.Layout == nil {
		hints := a.hints()
		full := make([]hint, len(hints))
		for i, h := range hints {
			full[i] = hint{key: a.keymap.leader + " " + h.key, title: h.title}
		}
		return shell(a.width, a.height, a.theme, lk.styles, lk.sample, full, bar)
	}

	rows := make([][]segment, max(a.height-1, 0))
	add := func(y int, s segment) {
		if y >= 0 && y < len(rows) && s.w > 0 {
			rows[y] = append(rows[y], s)
		}
	}
	for id, r := range g.rects {
		hdr := a.header(id, r, lk.styles)
		add(r.Y, segment{r.X, r.W, hdr})
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
	if !st.Zoomed {
		for _, s := range g.seps {
			for y := s.Y; y < s.Y+s.H; y++ {
				add(y, segment{s.X, 1, lk.styles.separator.Render(separatorGlyph)})
			}
		}
	}

	lines := make([]string, 0, a.height)
	var sb strings.Builder
	for _, segs := range rows {
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
	lines = append(lines, statusBar(a.width, a.theme, lk.styles, bar))
	return strings.Join(lines, "\n")
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

// header draws the one-row pane header: " title · cwd" and, when zoomed, a
// right-aligned zoom tag. The title is the guest's OSC title, else the kind.
func (a *App) header(id core.PaneID, r core.Rect, st styles) string {
	s := a.store.State()
	meta := s.Panes[id]
	title := "shell"
	if meta.Kind == core.KindAgent {
		title = "claude"
	}
	if p := a.panes[id]; p != nil {
		if t := cleanTitle(p.title()); t != "" {
			title = t
		}
	}
	text := " " + title
	if meta.Cwd != "" {
		text += " · " + filepath.Base(meta.Cwd)
	}
	style := st.paneHeader
	if id == s.Focus {
		style = st.paneHeaderFocus
	}
	tag := ""
	if s.Zoomed && r.W > len(zoomTag) {
		tag = zoomTag
	}
	text = ansi.Truncate(text, r.W-len(tag), "")
	gap := r.W - lipgloss.Width(text) - len(tag)
	return style.Render(text + strings.Repeat(" ", gap) + tag)
}

// cleanTitle drops control characters a guest may put in its title.
func cleanTitle(t string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, t)
}

// cursorView places the focused pane's cursor on screen; nil when the guest
// hides it, it falls outside the pane, or an overlay or the which-key popup is open.
func (a *App) cursorView() *tea.Cursor { return a.cursorAt(a.geometry()) }

func (a *App) cursorAt(g geometry) *tea.Cursor {
	if a.overlay != nil || a.popup {
		return nil
	}
	st := a.store.State()
	p := a.panes[st.Focus]
	r, ok := g.rects[st.Focus]
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

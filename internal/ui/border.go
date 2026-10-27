package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
)

// Border directions a cell's line leaves in.
const (
	dirU uint8 = 1 << iota
	dirD
	dirL
	dirR
)

// Who owns a border cell's colour: the focused pane's kind wins over a neighbour.
const (
	ownNone uint8 = iota
	ownAgent
	ownShell
)

// borderGrid collects the directions of every border cell so that panes that
// share an edge draw one connected line.
type borderGrid struct {
	w, h int
	dirs []uint8
	own  []uint8
}

func newBorderGrid(w, h int) *borderGrid {
	return &borderGrid{w: w, h: h, dirs: make([]uint8, w*h), own: make([]uint8, w*h)}
}

// addBox adds the border around a pane rect, on columns r.X-1..r.X+r.W and rows
// r.Y..r.Y+r.H, and sets the colour owner of every cell on it to own unless
// own is ownNone.
func (g *borderGrid) addBox(r core.Rect, own uint8) {
	x1, x2, y1, y2 := r.X-1, r.X+r.W, r.Y, r.Y+r.H
	set := func(x, y int, d uint8) {
		if x < 0 || y < 0 || x >= g.w || y >= g.h {
			return
		}
		i := y*g.w + x
		g.dirs[i] |= d
		if own != ownNone {
			g.own[i] = own
		}
	}
	for x := x1; x <= x2; x++ {
		d := dirL | dirR
		switch x {
		case x1:
			d = dirR
		case x2:
			d = dirL
		}
		set(x, y1, d)
		set(x, y2, d)
	}
	for y := y1; y <= y2; y++ {
		d := dirU | dirD
		switch y {
		case y1:
			d = dirD
		case y2:
			d = dirU
		}
		set(x1, y, d)
		set(x2, y, d)
	}
}

// straight reports whether (x, y) is a plain horizontal run.
func (g *borderGrid) straight(x, y int) bool { return g.dirs[y*g.w+x] == dirL|dirR }

// clear removes the border from cells [x0, x1) of row y: text sits there.
func (g *borderGrid) clear(y, x0, x1 int) {
	for x := x0; x < x1; x++ {
		g.dirs[y*g.w+x] = 0
	}
}

// runs emits the remaining border cells, one styled string per run of cells
// with the same owner.
func (g *borderGrid) runs(st styles, add func(y int, s segment)) {
	var sb strings.Builder
	for y := range g.h {
		x := 0
		for x < g.w {
			i := y*g.w + x
			if g.dirs[i] == 0 {
				x++
				continue
			}
			start, own := x, g.own[i]
			sb.Reset()
			for x < g.w && g.dirs[y*g.w+x] != 0 && g.own[y*g.w+x] == own {
				sb.WriteString(borderGlyph(g.dirs[y*g.w+x]))
				x++
			}
			add(y, segment{start, x - start, st.borderRun(own, sb.String())})
		}
	}
}

// borderGlyph is the rounded box-drawing character for the directions a cell
// connects.
func borderGlyph(d uint8) string {
	switch d {
	case dirU | dirD:
		return "│"
	case dirR | dirD:
		return "╭"
	case dirL | dirD:
		return "╮"
	case dirR | dirU:
		return "╰"
	case dirL | dirU:
		return "╯"
	case dirL | dirR | dirD:
		return "┬"
	case dirL | dirR | dirU:
		return "┴"
	case dirU | dirD | dirR:
		return "├"
	case dirU | dirD | dirL:
		return "┤"
	case dirU | dirD | dirL | dirR:
		return "┼"
	default:
		return "─"
	}
}

func kindOwner(k core.PaneKind) uint8 {
	if k == core.KindAgent {
		return ownAgent
	}
	return ownShell
}

// titleRoomMin is the room, in cells, a title needs to be shown at all.
const titleRoomMin = 4

// topBorder returns the title and tags set into the top border of the pane at
// r, and removes the border cells they cover from the grid. The title uses the
// plain run of border that starts after the left corner and the tags the one
// that ends before the right corner, so neither covers a junction.
func (a *App) topBorder(grid *borderGrid, s core.State, id core.PaneID, r core.Rect, focused bool, st styles) []segment {
	meta := s.Panes[id]
	kindStyle, titleStyle, cwdStyle := st.kind(meta.Kind), st.titleMuted, st.cwdSubtle
	if focused {
		titleStyle, cwdStyle = kindStyle, st.cwdFocus
	}

	left, right := r.X, r.X+r.W
	for left < right && grid.straight(left, r.Y) {
		left++
	}
	rightStart := right
	for rightStart > r.X && grid.straight(rightStart-1, r.Y) {
		rightStart--
	}
	whole := left == right

	var segs []segment

	// Tags sit right to left with one border cell between them and before the corner.
	tagEnd := right - 1
	place := func(tag string, style lipgloss.Style) {
		tw := lipgloss.Width(tag)
		x := tagEnd - tw
		if x < rightStart {
			return
		}
		segs = append(segs, segment{x, tw, style.Render(tag)})
		grid.clear(r.Y, x, x+tw)
		if whole {
			left = x
		}
		tagEnd = x - 1
	}

	if meta.Kind == core.KindAgent {
		if tag, style, ok := st.badge(meta.State, a.spin, a.icons); ok {
			place(tag, style)
		}
	}

	_, focusExists := s.Panes[s.Tab().Focus]
	if s.Tab().Zoomed && focusExists {
		tag := " zoom "
		if a.icons {
			tag = " " + iconZoom + " zoom "
		}
		place(tag, st.tagZoom)
	}

	if p := a.panes[id]; p != nil {
		if n := p.linesAbove(); n > 0 {
			arrow := "\u2191"
			if a.icons {
				arrow = iconScroll
			}
			place(fmt.Sprintf(" %s %d ", arrow, n), st.tagScroll)
		}
	}

	title := "shell"
	if meta.Kind == core.KindAgent {
		title = "claude"
	}
	if p := a.panes[id]; p != nil {
		if t := printable(p.title()); t != "" {
			title = t
		}
	}
	cwd := ""
	if meta.Cwd != "" {
		cwd = printable(filepath.Base(meta.Cwd))
	}

	lead := 2
	if a.icons {
		lead = 4
	}
	room := left - r.X - lead - 1
	if room < titleRoomMin {
		return segs
	}
	tw := ansi.StringWidth(title)
	cw := ansi.StringWidth(cwd)
	showCwd := cwd != "" && tw+3+cw <= room
	if tw > room {
		title = ansi.Truncate(title, room, "…")
		tw = ansi.StringWidth(title)
	}

	var sb strings.Builder
	sb.WriteString(st.body.Render(" "))
	if a.icons {
		sb.WriteString(kindStyle.Render(kindIcon(meta.Kind)))
		sb.WriteString(st.body.Render(" "))
	}
	sb.WriteString(titleStyle.Render(title))
	w := lead - 1 + tw + 1
	if showCwd {
		sb.WriteString(st.dot.Render(" · "))
		sb.WriteString(cwdStyle.Render(cwd))
		w += 3 + cw
	}
	sb.WriteString(st.body.Render(" "))
	grid.clear(r.Y, r.X+1, r.X+1+w)

	return append(segs, segment{r.X + 1, w, sb.String()})
}

func kindIcon(k core.PaneKind) string {
	if k == core.KindAgent {
		return iconAgent
	}
	return iconShell
}

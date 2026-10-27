package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
)

// Who owns a box's colour: the focused pane's kind, or nobody (Border).
const (
	ownNone uint8 = iota
	ownAgent
	ownShell
)

// drawBox adds the rounded box around a pane rect: corners, sides and bottom,
// and the top border with its title and tag segments set in. Every pane has its
// own box, so neighbours touch without shared cells.
func drawBox(add func(y int, s segment), r core.Rect, top []segment, own uint8, st styles) {
	if r.W < minFrame || r.H < minFrame {
		return
	}

	right := r.X + r.W - 1
	bottom := r.Y + r.H - 1

	line := func(x, w int, glyphs string) segment { return segment{x, w, st.borderRun(own, glyphs)} }

	// The top border is runs of corner and dashes between the title and tag
	// segments; the last run ends in the other corner.
	pos, prefix := r.X, "╭"
	run := func(end int, tail string) {
		if end > pos {
			dashes := end - pos - utf8.RuneCountInString(prefix+tail)
			add(r.Y, line(pos, end-pos, prefix+strings.Repeat("─", dashes)+tail))
		}
	}

	slices.SortFunc(top, func(p, q segment) int { return p.x - q.x })

	for _, s := range top {
		if s.x < max(pos, r.X+1) || s.x+s.w > right {
			continue
		}

		run(s.x, "")
		add(r.Y, s)
		pos, prefix = s.x+s.w, ""
	}

	run(right+1, "╮")

	for y := r.Y + 1; y < bottom; y++ {
		add(y, line(r.X, 1, "│"))
		add(y, line(right, 1, "│"))
	}

	add(bottom, line(r.X, r.W, "╰"+strings.Repeat("─", r.W-2)+"╯"))
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
// r: the title after the left corner and a dash, the tags before the right
// corner. The cells between them stay border.
func (a *App) topBorder(s core.State, id core.PaneID, r core.Rect, focused bool, st styles) []segment {
	meta := s.Panes[id]
	kindStyle, titleStyle := st.kind(meta.Kind), st.titleMuted
	if focused {
		titleStyle = kindStyle
	}

	// left is where the title must end; the first cell after the corner is a dash.
	left, rightStart := r.X+r.W-1, r.X+1

	var segs []segment

	// Tags sit right to left with one border cell between them and before the corner.
	tagEnd := r.X + r.W - 2
	place := func(tag string, style lipgloss.Style) {
		tw := lipgloss.Width(tag)
		x := tagEnd - tw
		if x < rightStart {
			return
		}
		segs = append(segs, segment{x, tw, style.Render(tag)})
		left = x
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

	lead := 2
	if a.icons {
		lead = 4
	}
	room := left - (r.X + 1) - lead - 1
	if room < titleRoomMin {
		return segs
	}
	tw := ansi.StringWidth(title)
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
	sb.WriteString(st.body.Render(" "))

	return append(segs, segment{r.X + 2, w, sb.String()})
}

func kindIcon(k core.PaneKind) string {
	if k == core.KindAgent {
		return iconAgent
	}
	return iconShell
}

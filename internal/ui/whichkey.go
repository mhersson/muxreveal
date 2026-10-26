package ui

import (
	"image"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// whichKeyMsg fires after the leader delay; seq ties it to one leader press.
type whichKeyMsg struct{ seq int }

// hint is one key and the title of the command it runs.
type hint struct{ key, title string }

// whichKeyPopup draws the bordered hint list titled with the leader key.
func whichKeyPopup(leader string, hints []hint, st styles) []string {
	keyW := 0
	for _, h := range hints {
		keyW = max(keyW, lipgloss.Width(h.key))
	}
	title := " " + leader + " "
	// inner must fit the title segment: one dash plus the title.
	inner := 1 + lipgloss.Width(title)
	rows := make([]string, len(hints))
	for i, h := range hints {
		rows[i] = st.popup.Render(" ") + st.popupKey.Render(h.key) +
			st.popup.Render(strings.Repeat(" ", keyW-lipgloss.Width(h.key)+2)) +
			st.popup.Render(h.title+" ")
		inner = max(inner, lipgloss.Width(rows[i]))
	}
	b := lipgloss.RoundedBorder()
	lines := make([]string, 0, len(rows)+2)
	lines = append(lines, st.popupBorder.Render(b.TopLeft+b.Top)+st.popupTitle.Render(title)+
		st.popupBorder.Render(strings.Repeat(b.Top, inner-1-lipgloss.Width(title))+b.TopRight))
	for _, r := range rows {
		pad := st.popup.Render(strings.Repeat(" ", inner-lipgloss.Width(r)))
		lines = append(lines, st.popupBorder.Render(b.Left)+r+pad+st.popupBorder.Render(b.Right))
	}
	return append(lines, st.popupBorder.Render(b.BottomLeft+strings.Repeat(b.Bottom, inner)+b.BottomRight))
}

// overlayWhichKey draws the popup over base at the bottom-right, above the
// status bar row. The popup is clipped to the terminal, never wrapped.
func overlayWhichKey(base string, width, height int, leader string, hints []hint, st styles) string {
	if width <= 0 || height < 2 || len(hints) == 0 {
		return base
	}
	lines := whichKeyPopup(leader, hints, st)
	if len(lines) > height-1 {
		lines = lines[:height-1]
	}
	w := 0
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
		w = max(w, lipgloss.Width(lines[i]))
	}
	popup := lipgloss.NewLayer(strings.Join(lines, "\n")).
		X(width - w).Y(height - 1 - len(lines)).Z(1)
	comp := lipgloss.NewCompositor(lipgloss.NewLayer(base), popup)
	if comp.Bounds() != image.Rect(0, 0, width, height) {
		return base
	}
	return comp.Render()
}

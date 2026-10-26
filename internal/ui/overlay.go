package ui

import (
	"fmt"
	"image"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/palette"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// maxOverlayWidth caps the palette overlay width.
const maxOverlayWidth = 60

type overlayKind int

const (
	overlayCommands overlayKind = iota
	overlayThemes
)

// overlay is an open palette. It owns every key until it closes.
type overlay struct {
	kind  overlayKind
	items []palette.Item
	pal   palette.Model
}

// look is the theme with its derived styles and highlighted sample. The app
// draws with the committed look, or a preview look while the theme picker moves.
type look struct {
	theme  theme.Theme
	styles styles
	sample []string
}

func newLook(th theme.Theme) (look, error) {
	sample, err := highlight(welcomeSample, "go", th)
	if err != nil {
		return look{}, fmt.Errorf("highlight welcome sample: %w", err)
	}
	return look{theme: th, styles: newStyles(th), sample: sample}, nil
}

// current is the look to draw with.
func (a *App) current() look {
	if a.preview != nil {
		return *a.preview
	}
	return look{theme: a.theme, styles: a.styles, sample: a.sample}
}

// openOverlay replaces any open overlay and ends a pending leader sequence.
func (a *App) openOverlay(kind overlayKind, title string, items []palette.Item) *overlay {
	a.pending, a.popup = false, false
	a.preview = nil
	a.overlay = &overlay{kind: kind, items: items, pal: palette.New(title, items, a.theme)}
	return a.overlay
}

func (a *App) closeOverlay() {
	a.overlay, a.preview = nil, nil
}

func (a *App) openCommandPalette() {
	cmds := a.reg.all()
	items := make([]palette.Item, len(cmds))
	for i, c := range cmds {
		hint, _ := a.keymap.keyFor(c.id)
		items[i] = palette.Item{ID: c.id, Title: c.title, Hint: hint}
	}
	a.openOverlay(overlayCommands, "Commands", items)
}

func (a *App) openThemePicker() {
	all := theme.All()
	items := make([]palette.Item, len(all))
	for i, th := range all {
		items[i] = palette.Item{ID: th.Name, Title: th.Title}
	}
	a.openOverlay(overlayThemes, "Theme", items).pal.Select(a.theme.Name)
}

// updateOverlay forwards a message to the open overlay's palette.
func (a *App) updateOverlay(msg tea.Msg) tea.Cmd {
	pal, cmd := a.overlay.pal.Update(msg)
	a.overlay.pal = pal
	return cmd
}

// chosen closes the overlay and applies the confirmed item.
func (a *App) chosen(id string) tea.Cmd {
	// Chosen messages arrive from goroutines; one meant for an earlier
	// overlay must not act on this one.
	if a.overlay == nil || !slices.ContainsFunc(a.overlay.items, func(it palette.Item) bool { return it.ID == id }) {
		return nil
	}
	kind := a.overlay.kind
	a.closeOverlay()
	switch kind {
	case overlayCommands:
		c, ok := a.reg.get(id)
		if !ok {
			a.log.Error("chosen command missing", "command", id)
			return nil
		}
		return c.run(a)
	case overlayThemes:
		// dispatch logs its own failures.
		_ = a.dispatch(core.SetTheme{Name: id})
	}
	return nil
}

// highlighted previews the theme picker's selection without touching the store.
// Messages arrive from goroutines, so a late one for another item is dropped.
func (a *App) highlighted(id string) {
	if a.overlay == nil || a.overlay.kind != overlayThemes {
		return
	}
	if it, ok := a.overlay.pal.Selected(); !ok || it.ID != id {
		return
	}
	th, err := theme.ByName(id)
	if err != nil {
		a.log.Warn("preview theme", "name", id, "err", err)
		return
	}
	lk, err := newLook(th)
	if err != nil {
		a.log.Error("preview theme", "name", id, "err", err)
		return
	}
	a.preview = &lk
	a.overlay.pal.SetTheme(th)
}

// overlayOver draws the palette over base, centred horizontally with its top
// at a quarter of the height. It is clipped to the body rows, never wrapped.
func overlayOver(base string, width, height int, pal palette.Model) string {
	if width < 2 || height < 2 {
		return base
	}
	pw := min(maxOverlayWidth, width)
	return overlayLines(base, width, height, height/4, strings.Split(pal.View(pw), "\n"), pw)
}

// overlayLines draws lines pw cells wide over base, centred horizontally with
// the top at row y, clipped to the body rows.
func overlayLines(base string, width, height, y int, lines []string, pw int) string {
	if width < 2 || height < 2 {
		return base
	}
	if room := height - 1 - y; len(lines) > room {
		lines = lines[:max(room, 0)]
	}
	if len(lines) == 0 {
		return base
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, pw, "")
	}
	layer := lipgloss.NewLayer(strings.Join(lines, "\n")).X((width - pw) / 2).Y(y).Z(1)
	comp := lipgloss.NewCompositor(lipgloss.NewLayer(base), layer)
	if comp.Bounds() != image.Rect(0, 0, width, height) {
		return base
	}
	return comp.Render()
}

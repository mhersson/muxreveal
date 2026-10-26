package ui

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/muxreveal/internal/core"
)

// handleKey routes a key press: the open overlay first, then the leader state
// machine, then the focused pane.
func (a *App) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if a.overlay != nil {
		return a.updateOverlay(msg)
	}
	a.notice = ""
	if cmd, ok := a.runLeader(msg); ok {
		return cmd
	}
	a.sendToFocused(func(p *pane) { p.sendKey(uv.KeyPressEvent(msg)) })
	return nil
}

// handlePaste sends pasted text to the open overlay, else to the focused pane.
func (a *App) handlePaste(msg tea.PasteMsg) tea.Cmd {
	if a.overlay != nil {
		return a.updateOverlay(msg)
	}
	a.sendToFocused(func(p *pane) { p.sendPaste(msg.Content) })
	return nil
}

// sendToFocused runs f on the focused pane; with none it does nothing.
func (a *App) sendToFocused(f func(*pane)) {
	if p, ok := a.panes[a.store.State().Focus]; ok {
		f(p)
	}
}

// reportFocusChange moves focus reports from the old pane to the new one while
// the outer terminal is focused.
func (a *App) reportFocusChange(from, to core.PaneID) {
	if from == to || !a.outerFocused {
		return
	}
	if p, ok := a.panes[from]; ok {
		p.sendFocus(false)
	}
	if p, ok := a.panes[to]; ok {
		p.sendFocus(true)
	}
}

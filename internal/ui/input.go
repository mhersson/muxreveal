package ui

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/muxreveal/internal/core"
)

// handleKey routes a key press: an open confirm first, then an open prompt, then
// copy mode, then the open overlay, then the leader state machine, then the rail while it
// has the keyboard, then the focused pane.
func (a *App) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if a.confirm != nil {
		return a.handleConfirmKey(msg)
	}

	if a.prompt != nil {
		return a.handlePromptKey(msg)
	}

	if a.copy != nil {
		return a.handleCopyKey(msg)
	}

	if a.overlay != nil {
		return a.updateOverlay(msg)
	}

	a.notice = ""
	if cmd, ok := a.runLeader(msg); ok {
		return cmd
	}

	if a.rail.focused {
		return a.handleRailKey(msg)
	}

	a.keyToFocused(msg)

	return nil
}

// handlePaste sends pasted text to the open prompt or overlay, else to the
// focused pane; an open confirm or copy mode drops it. A paste ends a pending leader sequence.
func (a *App) handlePaste(msg tea.PasteMsg) tea.Cmd {
	if a.confirm != nil {
		return nil
	}

	if a.prompt != nil {
		return a.updatePrompt(msg)
	}

	if a.copy != nil {
		return nil
	}

	if a.overlay != nil {
		return a.updateOverlay(msg)
	}

	a.endLeader()

	if a.rail.focused {
		a.railPaste(msg.Content)

		return nil
	}

	a.sendToFocused(func(p *pane) {
		a.clearSelectionOf(p.id)
		p.sendPaste(msg.Content)
	})

	return nil
}

// keyToFocused sends a key to the focused pane; typing ends that pane's selection.
func (a *App) keyToFocused(msg tea.KeyPressMsg) {
	a.sendToFocused(func(p *pane) {
		a.clearSelectionOf(p.id)
		a.answerDialog(p, msg.String())
		p.sendKey(uv.KeyPressEvent(msg))
	})
}

// sendToFocused runs f on the focused pane; with none it does nothing.
func (a *App) sendToFocused(f func(*pane)) {
	if p, ok := a.panes[a.store.State().Tab().Focus]; ok {
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

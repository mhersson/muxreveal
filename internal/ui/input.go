package ui

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/muxreveal/internal/core"
)

// handleKey routes a key press: an open confirm first, then an open prompt, then
// copy mode, then the open overlay, checks or worktree picker, then the leader state
// machine, then the rail while it has the keyboard, then the review drawer while
// it has it, then the focused pane.
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

	if a.composeShown() {
		return a.handleComposeKey(msg)
	}

	if a.merge != nil {
		return a.handleMergeKey(msg)
	}

	if a.checks != nil {
		return a.handleChecksKey(msg)
	}

	if a.prList != nil {
		return a.handlePRListKey(msg)
	}

	if a.picker.open != nil {
		return a.handlePickerKey(msg)
	}

	a.notice = ""
	if cmd, ok := a.runLeader(msg); ok {
		return cmd
	}

	if a.rail.focused {
		return a.handleRailKey(msg)
	}

	if a.drawerHasKeys() {
		return a.handleDrawerKey(msg)
	}

	a.keyToFocused(msg)

	return nil
}

// handlePaste sends pasted text to the open prompt, overlay or picker, else to the rail's
// or the drawer's search while one has the keyboard, else to the focused pane; an
// open confirm or copy mode drops it. A paste ends a pending leader sequence.
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

	if a.composeShown() {
		return a.handleComposePaste(msg)
	}

	if a.checks != nil || a.merge != nil || a.prList != nil {
		return nil
	}

	if a.picker.open != nil {
		a.picker.open.edit(msg)

		return nil
	}

	a.endLeader()

	if a.rail.focused {
		a.railPaste(msg.Content)

		return nil
	}

	if a.drawerHasKeys() {
		a.drawerPaste(msg.Content)

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

// modalOpen reports whether any modal owns the keyboard: a confirm, prompt,
// copy mode, the palette or another overlay, the compose box, the merge box,
// the checks, the pull request list, the worktree picker or the drawer's
// worktree list or link chooser.
func (a *App) modalOpen() bool { return a.composeShown() || a.otherModalOpen() }

// otherModalOpen is modalOpen without the compose box.
func (a *App) otherModalOpen() bool {
	return a.confirm != nil || a.prompt != nil || a.copy != nil || a.overlay != nil || a.merge != nil ||
		a.checks != nil || a.prList != nil || a.picker.open != nil || a.drawer.list != nil || a.drawer.links != nil
}

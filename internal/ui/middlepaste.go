package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
)

// middlePasteMsg carries a middle-click's paste text with its pane, so a read
// that outlives its pane is dropped.
type middlePasteMsg struct {
	id   core.PaneID
	p    *pane
	text string
}

// middlePaste reads the paste source off the update loop and delivers it to p.
func (a *App) middlePaste(p *pane) tea.Cmd {
	id := p.id

	return a.clip.paste(func(text string) tea.Msg {
		return middlePasteMsg{id: id, p: p, text: text}
	})
}

// onMiddlePaste pastes unless the pane is gone, the text is empty or a state
// that ignores the mouse opened while the read ran. It does not call
// mouseBlocked, which would release held buttons.
func (a *App) onMiddlePaste(msg middlePasteMsg) {
	p, ok := a.livePane(msg.id, msg.p)
	if !ok || msg.text == "" || a.modalOpen() || a.pending {
		return
	}

	a.clearSelectionOf(p.id)
	p.sendPaste(msg.text)
}

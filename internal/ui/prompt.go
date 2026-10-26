package ui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
)

// maxPromptChars bounds the text, so an input drawn in a bar cannot outgrow it.
const maxPromptChars = 32

// prompt is a single-line text input. It is a model only and draws nothing: its
// owner decides where it appears. While open it owns every key and paste.
type prompt struct {
	input textinput.Model
	tab   core.TabID // the tab it is about; it closes when that tab does
	done  func(a *App, value string) tea.Cmd
}

// openPrompt shows a prompt pre-filled with value, with the cursor at its end,
// and ends a pending leader sequence. enter calls done and closes it.
func (a *App) openPrompt(tab core.TabID, value string, done func(a *App, value string) tea.Cmd) {
	in := textinput.New()
	// The owner places the terminal's own cursor.
	in.SetVirtualCursor(false)
	in.KeyMap.Paste.SetEnabled(false)
	in.CharLimit = maxPromptChars
	in.SetValue(value)
	in.CursorEnd()
	in.Focus()

	a.pending, a.popup = false, false
	a.prompt = &prompt{input: in, tab: tab, done: done}
}

// handlePromptKey edits the open prompt; enter saves it and esc drops it.
func (a *App) handlePromptKey(msg tea.KeyPressMsg) tea.Cmd {
	p := a.prompt

	switch msg.String() {
	case "enter":
		a.prompt = nil

		return p.done(a, p.input.Value())
	case "esc":
		a.prompt = nil

		return nil
	}

	return a.updatePrompt(msg)
}

// updatePrompt forwards a message to the open prompt's input.
func (a *App) updatePrompt(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd

	a.prompt.input, cmd = a.prompt.input.Update(msg)

	return cmd
}

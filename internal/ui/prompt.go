package ui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
)

// maxPromptChars bounds the text, so an input drawn in a bar cannot outgrow it.
const maxPromptChars = 32

// promptKind says what a prompt edits and so where it is drawn.
type promptKind int

const (
	promptRename promptKind = iota // the active tab's name, in the workspace bar
	promptSearch                   // a copy-mode query, in the status bar
)

// prompt is a single-line text input. It is a model only and draws nothing: its
// owner decides where it appears. While open it owns every key and paste.
type prompt struct {
	input textinput.Model
	kind  promptKind
	tab   core.TabID  // promptRename: the tab it is about; it closes when that tab does
	pane  core.PaneID // promptSearch: the pane it searches; it closes when that pane does
	done  func(a *App, value string) tea.Cmd
}

// openPrompt shows a rename prompt pre-filled with value, with the cursor at
// its end, and ends a pending leader sequence. enter calls done and closes it.
func (a *App) openPrompt(tab core.TabID, value string, done func(a *App, value string) tea.Cmd) {
	a.showPrompt(&prompt{kind: promptRename, tab: tab, done: done}, value)
}

// showPrompt opens p with its input pre-filled with value.
func (a *App) showPrompt(p *prompt, value string) {
	in := textinput.New()
	// The owner places the terminal's own cursor.
	in.SetVirtualCursor(false)
	in.KeyMap.Paste.SetEnabled(false)
	in.CharLimit = maxPromptChars
	in.SetValue(value)
	in.CursorEnd()
	in.Focus()

	a.pending, a.popup = false, false
	p.input = in
	a.prompt = p
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

// promptCursor places the terminal cursor in the input of the open prompt; nil
// when its segment is shortened out of view.
func (a *App) promptCursor() *tea.Cursor {
	if a.prompt.kind == promptSearch {
		if x, ok := a.searchCursorX(); ok {
			return tea.NewCursor(x, a.height-1)
		}

		return nil
	}

	if x, ok := a.renameCursorX(); ok {
		return tea.NewCursor(x, 0)
	}

	return nil
}

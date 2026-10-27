package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
)

// handleRailKey takes a key while the rail has the keyboard; keys it does not use are dropped.
func (a *App) handleRailKey(msg tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd

	if a.rail.searching {
		a.railTypeKey(msg)
	} else {
		cmd = a.railNavKey(msg)
	}

	return cmd
}

func (a *App) railNavKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		a.railMove(1)
	case "k", "up":
		a.railMove(-1)
	case "/":
		a.railSetSearch(true, a.rail.query)
	case "enter":
		return a.railJump(a.rail.sel)
	case "esc":
		if a.rail.query != "" {
			a.railSetSearch(false, "")
		} else {
			a.railRelease()
		}
	}

	return nil
}

func (a *App) railTypeKey(msg tea.KeyPressMsg) {
	switch key := msg.String(); {
	case key == "enter":
		a.railSetSearch(false, a.rail.query)
	case key == "esc":
		a.railSetSearch(false, "")
	case key == "backspace":
		if r := []rune(a.rail.query); len(r) > 0 {
			a.railSetSearch(true, string(r[:len(r)-1]))
		}
	case msg.Text != "":
		a.railSetSearch(true, a.rail.query+printable(msg.Text))
	}
}

// railPaste appends pasted text to the search while it is typing; otherwise it is dropped.
func (a *App) railPaste(text string) {
	if a.rail.searching {
		a.railSetSearch(true, a.rail.query+printable(text))
	}
}

// railJump makes an agent's tab active, focuses its pane, flashes its frame and
// hands it the keyboard; the rail stays open. A pane that is gone does nothing.
func (a *App) railJump(id core.PaneID) tea.Cmd {
	st := a.store.State()

	ti, ok := st.TabOf(id)
	if !ok || id == "" {
		return nil
	}

	var cmds []tea.Cmd

	if ti != st.Active {
		cmds = append(cmds, a.act(core.SelectTab{Index: ti}))
	}

	cmds = append(cmds, a.act(core.FocusPane{ID: id}))

	a.railRelease()
	a.rail.flashPane, a.rail.flashUntil = id, a.clock.Now().Add(railFlashFor)
	a.rail.flashSeq++
	seq := a.rail.flashSeq

	cmds = append(cmds, a.tick(railFlashFor, func(time.Time) tea.Msg { return railFlashMsg{seq: seq} }))

	return tea.Batch(cmds...)
}

// railFlashMsg ends a jump's flash unless a later jump replaced it.
type railFlashMsg struct{ seq int }

func (a *App) onRailFlash(msg railFlashMsg) {
	if msg.seq == a.rail.flashSeq {
		a.rail.flashPane, a.rail.flashUntil = "", time.Time{}
	}
}

// railPulseMsg flips the blocked bar between its two colours.
type railPulseMsg struct{}

// railPulseCmd arms the pulse tick while the rail shows and some agent is blocked.
func (a *App) railPulseCmd(st core.State) tea.Cmd {
	if a.rail.pulsing || !a.railShows(st.Rail) || !anyBlocked(st) {
		return nil
	}

	a.rail.pulsing = true

	return a.tick(railPulseEvery, func(time.Time) tea.Msg { return railPulseMsg{} })
}

// onRailPulse flips the bar; the end of the update re-arms the tick while it is still needed.
func (a *App) onRailPulse() {
	a.rail.pulsing = false

	if st := a.store.State(); a.railShows(st.Rail) && anyBlocked(st) {
		a.rail.pulseDim = !a.rail.pulseDim
	} else {
		a.rail.pulseDim = false
	}
}

// onRailWheel moves the selection one card per notch.
func (a *App) onRailWheel(delta int) {
	a.railMove(delta)
}

// railClick gives the rail the keyboard, selects the card under the pointer and
// jumps on a double click.
func (a *App) railClick(x, y int) tea.Cmd {
	a.rail.focused = true

	v, ok := a.railViewOf(a.store.State())
	if !ok {
		return nil
	}

	i, onCard := v.cardAt(x, y)
	if !onCard {
		a.mouse.click = clickState{}

		return nil
	}

	a.railSelect(v.shown[i].pane)

	if a.countClick(x, y) == 2 {
		return a.railJump(a.rail.sel)
	}

	return nil
}

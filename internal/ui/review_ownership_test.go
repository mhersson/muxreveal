package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
)

// sideBySide is a rig with a drawer on p1, which is focused and shown on the
// left, over an agent pane p2 that the drawer covers.
func sideBySide(t *testing.T) (*drawerRig, *reviewView) {
	t.Helper()

	r := newKeysRig(t, keysRepo(t))
	r.run(r.openPane(core.KindAgent, core.SplitVertical))
	openIn(t, r.App, core.FocusPane{ID: "p1"})

	v := r.opened(t)

	if !r.drawer.focused || r.focus() != "p1" {
		t.Fatalf("setup: drawer has the keyboard = %v, focus = %s, want the drawer and p1", r.drawer.focused, r.focus())
	}

	return r, v
}

func TestOverlaysWhileTheDrawerHasTheKeyboard(t *testing.T) {
	r, v := sideBySide(t)

	cases := []struct {
		name        string
		open, close []tea.KeyPressMsg
		closed      func() bool
	}{
		{"copy mode", []tea.KeyPressMsg{keyLeader, keyRune('[')}, []tea.KeyPressMsg{keyEsc}, func() bool { return r.copy == nil }},
		{"confirm", []tea.KeyPressMsg{keyLeader, keyRune('q')}, []tea.KeyPressMsg{keyRune('n')}, func() bool { return r.confirm == nil }},
		{"palette", []tea.KeyPressMsg{keyLeader, keyRune(':')}, []tea.KeyPressMsg{keyEsc}, func() bool { return r.overlay == nil }},
	}

	for _, c := range cases {
		r.keys(c.open...)

		at := v.cursor

		r.keys(keyRune('j'))

		if v.cursor != at {
			t.Fatalf("with %s open the drawer took j", c.name)
		}

		if c.name == "copy mode" && (r.copy == nil || r.copy.pane != "p1") {
			t.Fatalf("copy mode is on %v, want it on the shown pane p1", r.copy)
		}

		r.keys(c.close...)
		r.until(t, c.name+" to close", c.closed)

		if !r.drawer.focused || r.store.State().Tab().Review == nil {
			t.Fatalf("closing %s left the drawer open = %v with the keyboard = %v, want both", c.name, r.store.State().Tab().Review != nil, r.drawer.focused)
		}

		r.keys(keyRune('j'))

		if v.cursor != at+1 {
			t.Errorf("after %s the drawer's cursor went %d -> %d, want it to move down", c.name, at, v.cursor)
		}
	}
}

// Copy mode on a pane the drawer covers would have no owner on screen, so the
// drawer closes without a slide first, as for any action that focuses a covered pane.
func TestCopyModeOnACoveredPaneClosesTheDrawer(t *testing.T) {
	r, _ := sideBySide(t)

	// The agent pane is the focused one when the drawer opens on it.
	r.drawerRelease()
	openIn(t, r.App, core.FocusPane{ID: "p2"})
	r.settle()

	if r.store.State().Tab().Review == nil || !r.drawer.focused {
		t.Fatalf("setup: drawer open = %v, has the keyboard = %v, want the drawer to have taken the keyboard from the covered p2",
			r.store.State().Tab().Review != nil, r.drawer.focused)
	}

	r.leader('[')

	if r.copy == nil || r.copy.pane != "p2" {
		t.Fatalf("copy mode = %v, want it on p2", r.copy)
	}

	if r.store.State().Tab().Review != nil || r.drawer.closing || r.drawer.focused {
		t.Errorf("drawer open = %v, closing = %v, has the keyboard = %v, want it closed at once", r.store.State().Tab().Review != nil, r.drawer.closing, r.drawer.focused)
	}

	r.keys(keyEsc)

	if r.copy != nil {
		t.Error("esc did not end copy mode")
	}
}

func TestKeyboardOwnerWhenPanesClose(t *testing.T) {
	t.Run("the drawer's pane closes while the drawer has the keyboard", func(t *testing.T) {
		r, _ := sideBySide(t)

		r.run(r.closePane("p1"))
		r.settle()

		st := r.store.State()
		if st.Tab().Review != nil || r.drawer.focused || r.focus() != "p2" {
			t.Fatalf("drawer open = %v, has the keyboard = %v, focus = %s, want it closed with the keyboard on p2", st.Tab().Review != nil, r.drawer.focused, r.focus())
		}

		r.keys(keyRune('x'))
		eventually(t, "x to reach p2", func() bool { return r.sentTo("p2") == "x" })
	})

	t.Run("the operator closes the drawer's pane with leader x", func(t *testing.T) {
		r, _ := sideBySide(t)

		r.leader('x')

		st := r.store.State()
		if _, ok := st.Panes["p1"]; ok || st.Tab().Review != nil || r.drawer.focused {
			t.Fatalf("p1 exists = %v, drawer open = %v, has the keyboard = %v, want the pane gone and the drawer closed", ok, st.Tab().Review != nil, r.drawer.focused)
		}
	})

	t.Run("another pane closes", func(t *testing.T) {
		dir := keysRepo(t)
		r := newKeysRig(t, dir)
		r.openShell(t, dir, core.SplitVertical)
		openIn(t, r.App, core.FocusPane{ID: "p1"})

		below := r.openShell(t, dir, core.SplitHorizontal)

		openIn(t, r.App, core.FocusPane{ID: "p1"})
		r.opened(t)

		r.run(r.closePane(below))
		r.settle()

		st := r.store.State()
		if st.Tab().Review == nil || st.Tab().Review.Pane != "p1" || !r.drawer.focused || r.focus() != "p1" {
			t.Errorf("drawer open = %v, has the keyboard = %v, focus = %s, want the drawer on p1 to keep the keyboard", st.Tab().Review != nil, r.drawer.focused, r.focus())
		}
	})

	t.Run("the focused pane closes and the drawer is not on it", func(t *testing.T) {
		dir := keysRepo(t)
		r := newKeysRig(t, dir)
		r.openShell(t, dir, core.SplitVertical)
		openIn(t, r.App, core.FocusPane{ID: "p1"})

		below := r.openShell(t, dir, core.SplitHorizontal)

		openIn(t, r.App, core.FocusPane{ID: "p1"})
		r.opened(t)
		r.keys(keyEsc)

		openIn(t, r.App, core.FocusPane{ID: below})
		r.run(r.closePane(below))
		r.settle()

		st := r.store.State()
		if st.Tab().Review == nil || r.drawer.focused || r.focus() != "p1" {
			t.Errorf("drawer open = %v, has the keyboard = %v, focus = %s, want the drawer open and p1 to have the keyboard", st.Tab().Review != nil, r.drawer.focused, r.focus())
		}
	})
}

// A tab whose focused pane is under its drawer gives that drawer the keyboard
// when the tab shows again.
func TestTabSwitchToACoveredFocusGivesTheDrawerTheKeyboard(t *testing.T) {
	dir := keysRepo(t)
	r := newKeysRig(t, dir)
	r.openShell(t, dir, core.SplitVertical)
	r.opened(t)

	if r.focus() != "p2" || !r.drawer.focused {
		t.Fatalf("setup: focus = %s, drawer has the keyboard = %v, want the covered p2 and the drawer", r.focus(), r.drawer.focused)
	}

	r.leader('c')

	if r.drawer.focused {
		t.Fatal("a new tab left the keyboard with the first tab's drawer")
	}

	r.leader('1')

	st := r.store.State()
	if st.Active != 0 || st.Tab().Review == nil || !r.drawer.focused || r.drawer.closing || r.focus() != "p2" {
		t.Errorf("tab %d, drawer open = %v, has the keyboard = %v, closing = %v, focus = %s, want the first tab's drawer to have the keyboard",
			st.Active, st.Tab().Review != nil, r.drawer.focused, r.drawer.closing, r.focus())
	}
}

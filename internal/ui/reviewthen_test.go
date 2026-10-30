package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestAfterLoadActionNeedsTheKeyboard(t *testing.T) {
	t.Parallel()

	t.Run("the drawer lost the keyboard before the load", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)
		r.typed("a")
		r.keys(keyEsc)

		if r.drawer.focused {
			t.Fatal("esc left the drawer focused")
		}

		r.until(t, "the PR to load", r.inPRMode(7))
		r.settle()

		if r.confirm != nil {
			t.Errorf("the late approve opened a confirm: %+v", *r.confirm)
		}

		if want := "#7 loaded; press a to approve"; r.notice != want {
			t.Errorf("notice = %q, want %q", r.notice, want)
		}
	})

	t.Run("another confirm is open at the load", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)
		r.typed("a")

		other := &confirm{prompt: "Quit MuxReveal?", yes: func(*App) tea.Cmd { return nil }}
		r.openConfirm(*other)

		r.until(t, "the PR to load", r.inPRMode(7))
		r.settle()

		if r.confirm == nil || r.confirm.approve != nil || r.confirm.prompt != other.prompt {
			t.Errorf("confirm = %+v, want the quit confirm left alone", r.confirm)
		}

		if want := "#7 loaded; press a to approve"; r.notice != want {
			t.Errorf("notice = %q, want %q", r.notice, want)
		}
	})

	t.Run("a merge asked for under a modal is dropped with its notice", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)
		r.typed("M")
		r.keys(keyEsc)

		r.until(t, "the PR to load", r.inPRMode(7))
		r.settle()

		if r.merge != nil {
			t.Error("the late merge opened the box")
		}

		if want := "#7 loaded; press M to merge"; r.notice != want {
			t.Errorf("notice = %q, want %q", r.notice, want)
		}
	})

	t.Run("leader g m takes an unfocused drawer's keyboard", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)
		r.keys(keyEsc)

		if r.drawer.focused {
			t.Fatal("esc left the drawer focused")
		}

		r.leader('g')
		r.typed("m")
		r.until(t, "the box", func() bool { return r.merge != nil })

		if !r.drawer.focused {
			t.Error("the merge left the keyboard off the drawer")
		}
	})
}

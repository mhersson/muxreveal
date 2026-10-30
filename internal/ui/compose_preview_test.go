package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
)

var keyCtrlP = tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}

// previewRig is a compose box on a sized screen whose ticks are recorded.
type previewRig struct {
	*composeRig
	v *composeView
}

func newPreviewRig(t *testing.T, w, h int) *previewRig {
	t.Helper()

	r := &previewRig{composeRig: newComposeRig(t)}
	r.v = r.mustOpen(t)
	sized(t, r.App, w, h)

	return r
}

func (r *previewRig) setBody(text string) {
	r.v.body = newTextArea(text, maxComposeBody)
	r.v.body.Start()
	r.v.focus(composeBody)
}

// msgOf runs cmd and returns the one message it answers.
func msgOf(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()

	if cmd == nil {
		t.Fatal("no command")
	}

	return cmd()
}

func listBody(n int) string {
	var b strings.Builder

	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "- item %d\n", i)
	}

	return strings.TrimSuffix(b.String(), "\n")
}

// assertToggle checks that word in the body box's border is the shown one
// (bold Accent) or the other (Subtle).
func assertToggle(t *testing.T, r *previewRig, scene, word string, shown bool) {
	t.Helper()

	lines := strings.Split(scene, "\n")
	buf := uv.NewScreenBuffer(120, len(lines))
	uv.NewStyledString(scene).Draw(buf, buf.Bounds())

	for y, l := range lines {
		plain := ansi.Strip(l)
		i := strings.Index(plain, "Write │ Preview")

		if i < 0 {
			continue
		}

		x := ansi.StringWidth(plain[:i])
		if word == "Preview" {
			x += ansi.StringWidth("Write │ ")
		}

		ui := r.current().theme.UI
		want, bold := ui.Subtle, false

		if shown {
			want, bold = ui.Accent, true
		}

		c := buf.CellAt(x, y)

		got, gotBold := hexOf(c.Style.Fg), c.Style.Attrs&uv.AttrBold != 0
		if got != string(want) || gotBold != bold {
			t.Errorf("%s: foreground %s bold %v, want %s bold %v", word, got, gotBold, want, bold)
		}

		return
	}

	t.Errorf("no border with the toggle for %s", word)
}

func TestComposePreviewToggle(t *testing.T) {
	for _, from := range []composeField{composeTitle, composeBase, composeDraft, composeBody} {
		t.Run(from.String(), func(t *testing.T) {
			r := newPreviewRig(t, 120, 40)
			r.setBody("one\ntwo\nthree")
			r.v.focus(from)
			r.v.body.row, r.v.body.col = 2, 1

			r.churn(t, press(r.App, keyCtrlP))

			if !r.v.preview.on || r.v.field != composeBody {
				t.Fatalf("on = %v, field = %v, want Preview on the body", r.v.preview.on, r.v.field)
			}

			scene := composeScene(t, r.composeRig, 120, 40)

			if !strings.Contains(ansi.Strip(scene), "Write │ Preview") {
				t.Errorf("the border lacks the toggle:\n%s", ansi.Strip(scene))
			}

			assertToggle(t, r, scene, "Write", false)
			assertToggle(t, r, scene, "Preview", true)

			r.churn(t, press(r.App, keyCtrlP))

			if r.v.preview.on || r.v.body.row != 2 || r.v.body.col != 1 {
				t.Errorf("on = %v, cursor (%d, %d), want Write with the cursor kept", r.v.preview.on, r.v.body.row, r.v.body.col)
			}

			scene = composeScene(t, r.composeRig, 120, 40)
			assertToggle(t, r, scene, "Write", true)
			assertToggle(t, r, scene, "Preview", false)
		})
	}
}

func TestComposePreviewDebounce(t *testing.T) {
	r := newPreviewRig(t, 120, 40)
	r.setBody("")
	r.ticks.delays = nil

	var due []tea.Msg

	for _, c := range "abc" {
		due = append(due, msgOf(t, press(r.App, keyRune(c))))
	}

	if want := []time.Duration{composePreviewDelay, composePreviewDelay, composePreviewDelay}; !slices.Equal(r.ticks.delays, want) {
		t.Fatalf("tick delays = %v, want %v", r.ticks.delays, want)
	}

	if composePreviewDelay != 150*time.Millisecond {
		t.Errorf("delay = %v, want 150ms", composePreviewDelay)
	}

	for i := range 2 {
		if cmd := r.step(due[i]); cmd != nil {
			t.Errorf("the tick of revision %d started a parse", i+1)
		}
	}

	cmd := r.step(due[2])
	if cmd == nil {
		t.Fatal("the latest tick started no parse")
	}

	rendered, ok := msgOf(t, cmd).(composeRenderedMsg)
	if !ok || rendered.v != r.v || rendered.rev != r.v.bodyRev {
		t.Fatalf("answer = %#v, want the latest revision of this box", rendered)
	}

	// An answer for an older revision is dropped.
	stale := composeRenderedMsg{v: r.v, rev: rendered.rev - 1, doc: parseMarkdown([]byte("old"), "", nil)}
	r.step(stale)

	if r.v.preview.doc != nil {
		t.Error("a stale answer was kept")
	}

	r.step(rendered)

	if r.v.preview.doc != rendered.doc {
		t.Error("the latest answer was not kept")
	}
}

func TestComposePreviewImmediate(t *testing.T) {
	r := newPreviewRig(t, 120, 40)
	r.setBody("first body")

	press(r.App, keyRune('x'))

	r.churn(t, press(r.App, keyCtrlP))

	doc := r.v.preview.doc
	if doc == nil {
		t.Fatal("ctrl+p parsed nothing")
	}

	r.churn(t, press(r.App, keyCtrlP))
	press(r.App, keyRune('y'))

	cmd := press(r.App, keyCtrlP)
	if cmd == nil {
		t.Fatal("ctrl+p with a newer body started no parse")
	}

	if r.v.preview.doc != doc {
		t.Error("the last doc did not stay meanwhile")
	}

	if _, ok := msgOf(t, cmd).(composeRenderedMsg); !ok {
		t.Error("the parse is not a rendered answer")
	}

	// A body no newer than the last parse starts none.
	r.churn(t, cmd)
	r.churn(t, press(r.App, keyCtrlP))

	if cmd := press(r.App, keyCtrlP); cmd != nil {
		t.Error("ctrl+p parsed an unchanged body again")
	}
}

func TestComposePreviewScroll(t *testing.T) {
	r := newPreviewRig(t, 120, 40)
	r.setBody(listBody(60))
	r.v.body.row = 39

	r.churn(t, press(r.App, keyCtrlP))

	text := r.text()
	lay := r.v.preview.doc.layout(composePreviewW(120), r.icons)
	top := r.v.preview.top
	rows := r.v.preview.rows

	if rows == 0 {
		t.Fatal("the preview does not know its rows")
	}

	if src := lay.Source[top]; src.First > 40 || src.Last < 40 || top == 0 {
		t.Errorf("top = %d holds source %v, want line 40", top, src)
	}

	if !strings.Contains(text, "item 40") || strings.Contains(text, "item 39") {
		t.Errorf("source line 40 is not the first line shown:\n%s", text)
	}

	press(r.App, keyDown)
	r.View()

	if r.v.preview.top != top+1 {
		t.Errorf("down: top = %d, want %d", r.v.preview.top, top+1)
	}

	press(r.App, keyUp)
	r.View()

	if r.v.preview.top != top {
		t.Errorf("up: top = %d, want %d", r.v.preview.top, top)
	}

	press(r.App, tea.KeyPressMsg{Code: tea.KeyPgDown})
	r.View()

	if want := min(top+rows-1, len(lay.Lines)-rows); r.v.preview.top != want {
		t.Errorf("pgdown: top = %d, want %d", r.v.preview.top, want)
	}

	down := r.v.preview.top

	press(r.App, tea.KeyPressMsg{Code: tea.KeyPgUp})
	r.View()

	if want := max(down-(rows-1), 0); r.v.preview.top != want {
		t.Errorf("pgup: top = %d, want %d", r.v.preview.top, want)
	}

	if r.v.body.row != 39 {
		t.Errorf("scrolling moved the text cursor to row %d", r.v.body.row)
	}

	// Past the end the box stays full.
	for range 10 {
		press(r.App, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}

	r.View()

	if r.v.preview.top != len(lay.Lines)-rows || !strings.Contains(r.text(), "item 60") {
		t.Errorf("end: top = %d of %d lines with %d rows, want a full box ending on item 60", r.v.preview.top, len(lay.Lines), rows)
	}

	// A cursor near the end clamps the same way.
	r2 := newPreviewRig(t, 120, 40)
	r2.setBody(listBody(60))
	r2.v.body.row = 58
	r2.churn(t, press(r2.App, keyCtrlP))
	r2.View()

	if want := len(r2.v.preview.doc.layout(composePreviewW(120), r2.icons).Lines) - r2.v.preview.rows; r2.v.preview.top != want {
		t.Errorf("near the end: top = %d, want %d", r2.v.preview.top, want)
	}
}

func TestComposePreviewDrops(t *testing.T) {
	setup := func(t *testing.T) *previewRig {
		t.Helper()

		r := newPreviewRig(t, 120, 40)
		r.churn(t, press(r.App, keyCtrlP))

		return r
	}

	t.Run("text", func(t *testing.T) {
		r := setup(t)
		before, rev := r.v.body.Value(), r.v.bodyRev

		for _, k := range []tea.KeyPressMsg{keyRune('z'), keyEnter, {Code: tea.KeyBackspace}, {Code: tea.KeyDelete}, keyLeft} {
			if cmd := press(r.App, k); cmd != nil {
				t.Errorf("%v raised a command", k)
			}
		}

		if cmd := r.handleComposePaste(tea.PasteMsg{Content: "pasted"}); cmd != nil {
			t.Error("the paste raised a command")
		}

		if r.v.body.Value() != before || r.v.bodyRev != rev || r.v.body.row != 0 || r.v.body.col != 0 {
			t.Errorf("the body changed: rev %d -> %d", rev, r.v.bodyRev)
		}
	})

	t.Run("tab", func(t *testing.T) {
		r := setup(t)
		press(r.App, keyTab)

		if r.v.field != composeTitle || !r.v.preview.on {
			t.Errorf("field = %v, on = %v, want tab to the title with Preview still on", r.v.field, r.v.preview.on)
		}
	})

	t.Run("ctrl+e", func(t *testing.T) {
		r := setup(t)
		press(r.App, keyCtrlE)

		if r.notice != noticeEditorNoRunDir {
			t.Errorf("notice = %q, want ctrl+e to act as in Write", r.notice)
		}
	})

	t.Run("ctrl+s", func(t *testing.T) {
		r := setup(t)
		press(r.App, keyCtrlS)

		if !r.v.busy {
			t.Error("ctrl+s did not create")
		}
	})

	t.Run("esc", func(t *testing.T) {
		r := setup(t)
		press(r.App, keyEsc)

		if r.compose != nil {
			t.Error("esc did not close the box")
		}
	})
}

func TestComposePreviewEditor(t *testing.T) {
	r := newEditorRig(t)
	sized(t, r.App, 120, 40)
	r.churn(t, press(r.App, keyCtrlP))

	r.ctrlE(t)
	r.edit(t, "# Fresh heading\n\nnew words")
	r.exit(t)

	if !r.v.preview.on || !r.composeShown() {
		t.Fatalf("on = %v, shown = %v, want Preview on after the editor", r.v.preview.on, r.composeShown())
	}

	if r.v.preview.parsed != r.v.bodyRev {
		t.Errorf("parsed revision %d, body revision %d, want the new body parsed at once", r.v.preview.parsed, r.v.bodyRev)
	}

	if text := r.text(); !strings.Contains(text, "Fresh heading") || !strings.Contains(text, "new words") {
		t.Errorf("the preview does not show the edited body:\n%s", text)
	}
}

func TestComposePreviewLate(t *testing.T) {
	r := newPreviewRig(t, 120, 40)
	cmd := press(r.App, keyCtrlP)
	late := msgOf(t, cmd)

	// The box closes before the answer lands.
	press(r.App, keyEsc)
	r.step(late)

	if r.compose != nil {
		t.Fatal("an answer reopened the box")
	}

	if r.v.preview.doc != nil {
		t.Error("an answer for a closed box was kept")
	}

	// A new box ignores the old one's answers, parsed and due alike.
	v2 := r.mustOpen(t)

	if v2 == r.v {
		t.Fatal("the same box opened again")
	}

	r.step(late)
	r.step(composeRenderDueMsg{v: r.v, rev: r.v.bodyRev})

	if v2.preview.doc != nil || v2.preview.parsed != 0 {
		t.Error("another box's answer was kept")
	}

	if cmd := r.step(composeRenderDueMsg{v: r.v, rev: r.v.bodyRev}); cmd != nil {
		t.Error("a tick of a closed box started a parse")
	}
}

func TestComposePreviewHints(t *testing.T) {
	r := newPreviewRig(t, 120, 40)

	if text := r.text(); !strings.Contains(text, "ctrl+p preview") || strings.Contains(text, "ctrl+p write") {
		t.Errorf("Write hints lack ctrl+p preview:\n%s", text)
	}

	r.churn(t, press(r.App, keyCtrlP))

	if text := r.text(); !strings.Contains(text, "ctrl+p write") || strings.Contains(text, "ctrl+p preview") {
		t.Errorf("Preview hints lack ctrl+p write:\n%s", text)
	}
}

func TestComposePreviewGolden(t *testing.T) {
	body := "# Release notes\n\nThis fixes the `parser` and adds:\n\n- faster startup\n- better errors\n\n```\nfunc main() {\n\trun()\n}\n```\n\nSee the docs."

	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"compose-preview", 120, 40},
		{"compose-preview-80x24", 80, 24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newPreviewRig(t, tc.w, tc.h)
			r.setBody(body)
			r.churn(t, press(r.App, keyCtrlP))

			t.Run("catppuccin-mocha/icons=true", func(t *testing.T) {
				r.icons = true
				golden.RequireEqual(t, []byte(composeScene(t, r.composeRig, tc.w, tc.h)))
			})

			for _, icons := range []bool{true, false} {
				t.Run(fmt.Sprintf("text/icons=%v", icons), func(t *testing.T) {
					r.icons = icons
					golden.RequireEqual(t, []byte(ansi.Strip(composeScene(t, r.composeRig, tc.w, tc.h))))
				})
			}
		})
	}
}

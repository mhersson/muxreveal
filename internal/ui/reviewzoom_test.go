package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
)

// zoomLayout puts r's app at w x h with the rail open and two panes in
// master-stack, the drawer open on the master (p1) and holding the keyboard.
func zoomLayout(t *testing.T, r *drawerRig, w, h int) {
	t.Helper()

	r.icons = true
	sized(t, r.App, w, h)
	openIn(t, r.App, core.SetRail{Open: true})
	r.openShell(t, t.TempDir(), core.SplitVertical)
	openIn(t, r.App, core.SetLayoutMode{Mode: core.ModeMaster, Area: r.bodyArea()}, core.FocusPane{ID: "p1"})
	r.drawerTake()
	r.settle()
}

// zoomRig is a keys rig at 270x75 with the drawer open on the master.
func zoomRig(t *testing.T) *drawerRig {
	t.Helper()

	r := newKeysRig(t, keysRepo(t))
	r.opened(t)
	zoomLayout(t, r, 270, 75)

	st := r.store.State()
	half, _ := r.drawerHalf(st)

	rects := r.geometry().rects
	if covered(rects["p1"], half) || !covered(rects["p2"], half) {
		t.Fatalf("setup: master %v and stack %v against the half drawer %v, want the master visible and the stack covered", rects["p1"], rects["p2"], half)
	}

	return r
}

func (r *drawerRig) zoomed() bool { return r.view().zoom }

func TestReviewZoom(t *testing.T) {
	t.Parallel()

	r := zoomRig(t)
	st := r.store.State()
	half, _ := r.drawerHalf(st)
	body := r.bodyAreaIn(st.Rail)

	r.keys(keyRune('z'))

	full, ok := r.drawerFull(r.store.State())
	if !ok || full != body {
		t.Fatalf("zoomed drawer = %v, want the body %v", full, body)
	}

	for id, rect := range r.geometry().rects {
		if !covered(rect, full) {
			t.Errorf("pane %s at %v is not covered by the zoomed drawer", id, rect)
		}
	}

	top := strings.SplitN(r.drawn(t), "\n", 2)[0]
	tag := " " + iconZoom + " zoom "

	if i, j := strings.Index(top, tag), strings.Index(top, "branch"); i < 0 || j < i {
		t.Errorf("top border %q: want the zoom tag left of the mode tag", top)
	}

	r.keys(keyRune('z'))

	if got, _ := r.drawerFull(r.store.State()); got != half {
		t.Errorf("drawer after z again = %v, want the half %v", got, half)
	}

	if r.zoomed() {
		t.Error("zoom is still set after z again")
	}
}

func TestReviewZoomLeaderZ(t *testing.T) {
	t.Parallel()

	r := zoomRig(t)

	r.leader('z')

	if !r.zoomed() || r.store.State().Tab().Zoomed {
		t.Errorf("leader z with the drawer: drawer zoomed %v, pane zoomed %v, want the drawer only", r.zoomed(), r.store.State().Tab().Zoomed)
	}

	r.keys(keyEsc)
	r.leader('z')

	if r.zoomed() || !r.store.State().Tab().Zoomed {
		t.Errorf("leader z with a pane: drawer zoomed %v, pane zoomed %v, want the pane only", r.zoomed(), r.store.State().Tab().Zoomed)
	}
}

func TestReviewZoomPalette(t *testing.T) {
	t.Parallel()

	r := zoomRig(t)
	r.keys(keyEsc)

	if r.drawerHasKeys() {
		t.Fatal("setup: the drawer kept the keyboard after esc")
	}

	c, ok := r.reg.get("review.zoom")
	if !ok {
		t.Fatal("no review.zoom command")
	}

	r.run(c.run(r.App))

	if !r.drawerHasKeys() || !r.zoomed() {
		t.Errorf("after the palette command: drawer has the keyboard %v, zoomed %v, want both", r.drawerHasKeys(), r.zoomed())
	}
}

func TestReviewZoomEverywhere(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T) *drawerRig
		tree  bool
	}{
		{"diff", zoomRig, false},
		{"tree", func(t *testing.T) *drawerRig {
			r := zoomRig(t)
			r.keys(keyTab)

			return r
		}, true},
		{"page", func(t *testing.T) *drawerRig {
			m := newPageRig(t, nil)
			zoomLayout(t, m.drawerRig, 270, 75)
			m.openPage(t, "README.md", false)

			return m.drawerRig
		}, false},
		{"rendered file", func(t *testing.T) *drawerRig {
			m := newMDRig(t)
			zoomLayout(t, m.drawerRig, 270, 75)
			m.onFile(t, planPath)
			m.press(keyRune('m'))

			return m.drawerRig
		}, false},
		{"pull request", func(t *testing.T) *drawerRig {
			p := newPRDrawer(t)
			p.poll()
			p.opened(t)
			zoomLayout(t, p.drawerRig, 270, 75)
			p.typed("p")
			p.until(t, "PR mode", p.inPRMode(7))

			return p.drawerRig
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tt.setup(t)
			r.keys(keyRune('z'))

			if !r.zoomed() || !r.drawerHasKeys() {
				t.Errorf("zoomed %v, drawer has the keyboard %v, want both", r.zoomed(), r.drawerHasKeys())
			}

			if r.drawer.tree != tt.tree {
				t.Errorf("tree focus = %v, want %v", r.drawer.tree, tt.tree)
			}
		})
	}
}

func TestReviewZoomLeaves(t *testing.T) {
	t.Parallel()

	const (
		toPane = "pane"
		toRail = "rail"
		toTab  = "other tab"
		toCopy = "copy mode"
	)

	tests := []struct {
		name  string
		leave func(t *testing.T, r *drawerRig)
		to    string
	}{
		{"esc", func(t *testing.T, r *drawerRig) { r.keys(keyEsc) }, toPane},
		{"leader h", func(t *testing.T, r *drawerRig) { r.leader('h') }, toPane},
		{"click on the rail", func(t *testing.T, r *drawerRig) {
			rail := r.railRect()
			clickRun(r.App, rail.X+1, rail.Y+1)
		}, toRail},
		{"leader a", func(t *testing.T, r *drawerRig) { r.leader('a') }, toRail},
		{"click on another tab", func(t *testing.T, r *drawerRig) {
			for x := range r.width {
				if h := r.hitTab(x); h.area == hitTab && h.tab == 1 {
					clickRun(r.App, x, 0)

					return
				}
			}

			t.Fatal("no tab 2 in the bar")
		}, toTab},
		{"leader 2", func(t *testing.T, r *drawerRig) { r.leader('2') }, toTab},
		{"leader [", func(t *testing.T, r *drawerRig) { r.leader('[') }, toCopy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := zoomRig(t)
			first := r.view()

			openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: t.TempDir()}, core.SelectTab{Index: 0})
			r.settle()

			r.keys(keyRune('z'))

			if !first.zoom {
				t.Fatal("setup: z did not zoom")
			}

			tt.leave(t, r)

			if first.zoom {
				t.Error("the drawer is still zoomed")
			}

			st := r.store.State()
			if tt.to != toTab && st.Tab().Review == nil {
				t.Error("the drawer closed")
			}

			switch tt.to {
			case toPane:
				if r.drawerHasKeys() || st.Tab().Focus != "p1" {
					t.Errorf("drawer has the keyboard %v, focus %s, want p1 with the drawer letting go", r.drawerHasKeys(), st.Tab().Focus)
				}
			case toRail:
				if !r.rail.focused || r.drawer.focused {
					t.Errorf("rail focused %v, drawer focused %v, want the rail", r.rail.focused, r.drawer.focused)
				}
			case toTab:
				if st.Active != 1 || r.drawer.focused {
					t.Errorf("active tab %d, drawer focused %v, want tab 1 and no drawer keyboard", st.Active, r.drawer.focused)
				}
			case toCopy:
				if r.copy == nil || st.Tab().Focus != "p1" {
					t.Errorf("copy mode %v, focus %s, want copy mode on p1", r.copy != nil, st.Tab().Focus)
				}
			}
		})
	}
}

func TestReviewZoomClose(t *testing.T) {
	t.Parallel()

	r := zoomRig(t)
	body := r.bodyAreaIn(r.store.State().Rail)

	r.keys(keyRune('z'))
	r.leader('d')

	if !r.zoomed() || !r.drawer.closing {
		t.Fatalf("zoomed %v, closing %v, want a close slide with zoom kept", r.zoomed(), r.drawer.closing)
	}

	if full, _ := r.drawerFull(r.store.State()); full != body {
		t.Errorf("sliding out from %v, want the body %v", full, body)
	}

	r.finishSlide()

	if r.store.State().Tab().Review != nil {
		t.Fatal("the drawer did not close")
	}

	r.leader('d')
	r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil })
	r.finishSlide()

	half, _ := r.drawerHalf(r.store.State())
	if full, _ := r.drawerFull(r.store.State()); full != half || r.zoomed() {
		t.Errorf("new drawer at %v zoomed %v, want the half %v unzoomed", full, r.zoomed(), half)
	}
}

func TestReviewZoomSmall(t *testing.T) {
	t.Parallel()

	r := newKeysRig(t, keysRepo(t))
	r.opened(t)
	zoomLayout(t, r, 80, 24)

	st := r.store.State()
	if half, _ := r.drawerHalf(st); half != r.bodyAreaIn(st.Rail) {
		t.Fatalf("setup: half drawer %v is not the body %v", half, r.bodyAreaIn(st.Rail))
	}

	r.keys(keyRune('z'))

	if !r.zoomed() || r.drawer.closing || !r.drawer.slideFrom.IsZero() {
		t.Errorf("zoomed %v, sliding %v, want the tag and no slide", r.zoomed(), !r.drawer.slideFrom.IsZero())
	}

	if top := strings.SplitN(r.drawn(t), "\n", 2)[0]; !strings.Contains(top, " "+iconZoom+" zoom ") {
		t.Errorf("top border %q lacks the zoom tag", top)
	}

	r.keys(keyEsc)

	if !r.drawerHasKeys() || !r.zoomed() {
		t.Errorf("after esc: drawer has the keyboard %v, zoomed %v, want both kept", r.drawerHasKeys(), r.zoomed())
	}
}

func TestReviewZoomTagDropsFirst(t *testing.T) {
	t.Parallel()

	r := zoomRig(t)
	r.keys(keyRune('z'))

	st := r.store.State()
	d, _, ok := r.drawCtxFor(st)

	if !ok {
		t.Fatal("no drawer to draw")
	}

	d.lay.split, d.v.rowsSplit = true, true

	var all, splitOnly bool

	for w := 20; w <= 160; w++ {
		d.lay.box.W = w
		top := ansi.Strip(d.topBorder())
		zoom, split := strings.Contains(top, "zoom"), strings.Contains(top, "split")

		if zoom && !split {
			t.Fatalf("width %d keeps the zoom tag and drops the split tag: %q", w, top)
		}

		all = all || (zoom && split && strings.Contains(top, iconReview))
		splitOnly = splitOnly || (split && !zoom)
	}

	if !all || !splitOnly {
		t.Errorf("sweep saw all tags with a title %v, the split tag without the zoom tag %v, want both", all, splitOnly)
	}
}

func TestReviewZoomHints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T) *drawerRig
		after string
	}{
		{"branch diff", zoomRig, "}/{ file"},
		{"pull request", func(t *testing.T) *drawerRig {
			p := newPRDrawer(t)
			p.poll()
			p.opened(t)
			zoomLayout(t, p.drawerRig, 270, 75)
			p.typed("p")
			p.until(t, "PR mode", p.inPRMode(7))

			return p.drawerRig
		}, "]/[ hunk"},
		{"rendered file", func(t *testing.T) *drawerRig {
			m := newMDRig(t)
			zoomLayout(t, m.drawerRig, 270, 75)
			m.onFile(t, planPath)
			m.press(keyRune('m'))

			return m.drawerRig
		}, "]/[ change"},
		{"page", func(t *testing.T) *drawerRig {
			m := newPageRig(t, nil)
			zoomLayout(t, m.drawerRig, 270, 75)
			m.openPage(t, "README.md", false)

			return m.drawerRig
		}, "]/[ heading"},
		{"tree", func(t *testing.T) *drawerRig {
			r := zoomRig(t)
			r.keys(keyTab)

			return r
		}, "enter jump"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tt.setup(t)
			want := tt.after + "   z zoom"

			if got := r.drawn(t); !strings.Contains(got, want) {
				t.Errorf("hints lack %q:\n%s", want, got)
			}

			r.keys(keyRune('z'))

			if got := r.drawn(t); !strings.Contains(got, want) {
				t.Errorf("zoomed hints lack %q:\n%s", want, got)
			}
		})
	}
}

func TestReviewZoomClearsSelection(t *testing.T) {
	t.Parallel()

	routes := map[string]func(d *diffSel){
		"z":        func(d *diffSel) { d.keys(keyRune('z')) },
		"leader z": func(d *diffSel) { d.leader('z') },
		"palette": func(d *diffSel) {
			c, ok := d.reg.get("review.zoom")
			if !ok {
				panic("no review.zoom command")
			}

			d.run(c.run(d.App))
		},
	}

	for _, small := range []bool{false, true} {
		for name, route := range routes {
			t.Run(fmt.Sprintf("%s small=%v", name, small), func(t *testing.T) {
				t.Parallel()

				d := newDiffSel(t)
				if small {
					zoomLayout(t, d.drawerRig, 80, 24)
				}

				del, add := rowWith(t, d.v, "line 3"), rowWith(t, d.v, "edited 3")

				d.dragRows(t, del, d.code(t, del), add, d.code(t, add)+len("edited 3")-1)

				if d.text == nil {
					t.Fatal("setup: the drag selected nothing")
				}

				route(d)

				if !d.v.zoom {
					t.Fatal("the route did not zoom")
				}

				if d.text != nil || d.v.sel != nil {
					t.Errorf("selection %v, view selection %v after zoom, want none", d.text, d.v.sel)
				}

				d.keys(keyLeader, keyRune('y'))

				if got := clipboardWrites(d.clip); len(got) != 0 {
					t.Errorf("leader y wrote %q, want nothing", got)
				}
			})
		}
	}
}

func TestReviewZoomClearsOnTabChange(t *testing.T) {
	t.Parallel()

	r := zoomRig(t)
	first := r.view()

	r.keys(keyRune('z'))
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: t.TempDir()})
	r.drawerAfterUpdate()

	if first.zoom || !r.drawer.slideFrom.IsZero() {
		t.Errorf("left tab zoomed %v, sliding %v, want no zoom and no slide", first.zoom, !r.drawer.slideFrom.IsZero())
	}
}

func TestReviewZoomKeepsWidthWhileClosing(t *testing.T) {
	t.Parallel()

	r := zoomRig(t)
	r.keys(keyRune('z'))
	r.leader('d')
	r.uncoverFocus()

	if !r.zoomed() || !r.drawer.closing {
		t.Errorf("zoomed %v, closing %v, want the close slide to keep its width", r.zoomed(), r.drawer.closing)
	}
}

func TestReviewZoomWithoutDrawer(t *testing.T) {
	t.Parallel()

	r := newKeysRig(t, keysRepo(t))

	c, ok := r.reg.get("review.zoom")
	if !ok {
		t.Fatal("no review.zoom command")
	}

	r.run(c.run(r.App))

	if r.notice != noticeNoDrawer {
		t.Errorf("notice = %q, want %q", r.notice, noticeNoDrawer)
	}

	c, _ = r.reg.get("pane.zoom")
	r.run(c.run(r.App))

	if !r.store.State().Tab().Zoomed {
		t.Error("pane.zoom with no drawer did not zoom the pane")
	}
}

func TestReviewZoomRemap(t *testing.T) {
	t.Parallel()

	r := newKeysRig(t, keysRepo(t), func(o *Options) { o.Keys = drawerKeys("Z", "review.zoom", "z", "none") })
	r.opened(t)
	zoomLayout(t, r, 270, 75)

	r.keys(keyRune('z'))

	if r.zoomed() {
		t.Error("z zoomed after it was unbound")
	}

	if got := r.drawn(t); !strings.Contains(got, "Z zoom") {
		t.Errorf("hints lack %q:\n%s", "Z zoom", got)
	}

	r.keys(keyRune('Z'))

	if !r.zoomed() {
		t.Error("Z did not zoom")
	}
}

package ui

import (
	"slices"
	"testing"

	"github.com/mhersson/muxreveal/internal/core"
)

// readerHost opens one of the reading overlays and returns its app.
type readerHost struct {
	name string
	open func(t *testing.T, w, h int) *App
}

func readerHosts() []readerHost {
	return []readerHost{
		{"plan", func(t *testing.T, w, h int) *App {
			r := newPlanRig(t, w, h)
			r.filePlan(t, "plan.md", demoPlan(t))
			r.open(t)

			return r.a
		}},
		{"file", func(t *testing.T, w, h int) *App {
			r := newMDOpenRig(t, nil)
			sized(t, r.App, w, h)
			r.opens(t, "docs/a.md")

			return r.App
		}},
		{"conversation", func(t *testing.T, w, h int) *App {
			a, _ := convApp(t, true, w, h)
			convOpen(t, a)

			return a
		}},
	}
}

func readerOpen(a *App) bool { return a.plan != nil || a.conv != nil }

// readerMoved is the keys config the remap tests share.
func readerMoved() core.KeysConfig {
	return contextKeys("reader", "n", "reader.line.next", "j", "none", "q", "reader.close", "esc", "none")
}

func TestReaderRemappedKeys(t *testing.T) {
	t.Parallel()

	for _, h := range readerHosts() {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()

			t.Run("moves", func(t *testing.T) {
				t.Parallel()

				a := h.open(t, 120, 40)
				remapTables(t, a, readerMoved())

				v := a.readerView()
				start := v.cursor

				run(a, press(a, keyRune('j')))

				if v.cursor != start {
					t.Fatalf("j moved the cursor to %d from %d", v.cursor, start)
				}

				run(a, press(a, keyRune('n')))

				if v.cursor != start+1 {
					t.Fatalf("n moved the cursor to %d, want %d", v.cursor, start+1)
				}
			})

			t.Run("selection", func(t *testing.T) {
				t.Parallel()

				a := h.open(t, 120, 40)
				remapTables(t, a, readerMoved())
				fakeClip(a)

				rect, ok := planDocRect(a.width, a.height)
				if a.conv != nil {
					rect, ok = convDocRect(a.width, a.height)
				}

				if !ok {
					t.Fatal("no doc rect")
				}

				dragRun(a, rect.Min.X, rect.Min.Y, rect.Min.X+6, rect.Min.Y, 0)

				if a.text == nil {
					t.Fatal("the drag selected nothing")
				}

				run(a, press(a, keyRune('x')))

				if a.text == nil {
					t.Error("an unbound key cleared the selection")
				}

				run(a, press(a, keyRune('n')))

				if a.text != nil {
					t.Error("a bound key kept the selection")
				}
			})

			t.Run("prompt", func(t *testing.T) {
				t.Parallel()

				a := h.open(t, 120, 40)
				remapTables(t, a, readerMoved())

				run(a, press(a, keyRune('/')))
				run(a, press(a, keyRune('n')))

				query := func() (bool, string) {
					if a.plan != nil {
						return a.plan.searching, a.plan.query
					}

					return a.conv.searching, a.conv.query
				}

				if on, q := query(); !on || q != "n" {
					t.Fatalf("searching %v, query %q; want the prompt open on n", on, q)
				}

				run(a, press(a, keyEsc))

				if on, _ := query(); on || !readerOpen(a) {
					t.Fatalf("esc left the prompt open %v, overlay open %v", on, readerOpen(a))
				}
			})

			t.Run("close", func(t *testing.T) {
				t.Parallel()

				a := h.open(t, 120, 40)
				remapTables(t, a, readerMoved())

				run(a, press(a, keyEsc))

				if !readerOpen(a) {
					t.Fatal("esc closed the overlay though it is unbound")
				}

				run(a, press(a, keyRune('q')))

				if readerOpen(a) {
					t.Fatal("q left the overlay open")
				}
			})
		})
	}
}

func TestReaderChooserKeepsItsKeys(t *testing.T) {
	t.Parallel()

	r := linkPlan(t, 120, 40)
	remapTables(t, r.a, contextKeys("reader", "j", "none", "esc", "none", "q", "reader.close"))
	r.onLine(t, "first then second")

	r.page(r.enter())

	pv := r.a.plan
	if pv.chooser == nil {
		t.Fatal("enter opened no chooser")
	}

	r.send(planKey("j"))

	if pv.chooser.sel != 1 {
		t.Fatalf("j left the chooser on %d, want 1", pv.chooser.sel)
	}

	r.send(planKey("esc"))

	if r.a.plan == nil || pv.chooser != nil {
		t.Fatalf("esc: overlay open %v, chooser open %v; want only the chooser closed", r.a.plan != nil, pv.chooser != nil)
	}
}

func TestReaderTooSmall(t *testing.T) {
	t.Parallel()

	for _, h := range readerHosts() {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()

			a := h.open(t, 120, 40)
			remapTables(t, a, readerMoved())
			sized(t, a, 20, 8)

			for _, k := range []string{"esc", "j", "n"} {
				run(a, press(a, planKey(k)))

				if !readerOpen(a) {
					t.Fatalf("%s closed the overlay", k)
				}
			}

			run(a, press(a, keyRune('q')))

			if readerOpen(a) {
				t.Fatal("q left the overlay open")
			}
		})
	}
}

func TestDefaultOverlayKeys(t *testing.T) {
	t.Parallel()

	_, tables, err := resolveKeys(core.KeysConfig{}, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(tables.reader.binds, readerKeys) {
		t.Errorf("reader keys = %v, want %v", tables.reader.binds, readerKeys)
	}
}

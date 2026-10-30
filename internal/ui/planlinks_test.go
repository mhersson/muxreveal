package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
)

// planLinkDoc is a plan file under docs/superpowers/plans: a line with no link,
// one with a link, one with three targets, a link to a page and a fold head with
// a link. Relative links reach the worktree root with ../../../.
const planLinkDoc = `# Links

plain words only

one [site](https://example.com/a) here

[first](https://example.com/1) then [second](../../b.md) then [third](../../../main.go)

[the b page](../../b.md)

<details>
<summary>More [site](https://example.com/f)</summary>

hidden text

</details>

[up](../../../../x.md) and [abs](/etc/hosts)
`

const planLinkPath = "docs/superpowers/plans/p.md"

// linkPlan opens a plan file holding planLinkDoc, beside docs/b.md and main.go.
func linkPlan(t *testing.T, w, h int) *planRig {
	t.Helper()

	r := newPlanRig(t, w, h)
	r.filePlan(t, planLinkPath, planLinkDoc)
	write(t, filepath.Join(r.wt, "docs/b.md"), "# Bee\n\nbee words\n\n[back](superpowers/plans/p.md)\n")
	write(t, filepath.Join(r.wt, "main.go"), "package main\n")
	chord(r.a, 'a')
	r.open(t)

	if r.a.plan == nil {
		t.Fatal("the overlay did not open")
	}

	return r
}

// onLine puts the shown view's cursor on the line holding sub.
func (r *planRig) onLine(t *testing.T, sub string) {
	t.Helper()

	v := r.a.plan.shown()
	v.layout()

	for i, l := range v.doc.text() {
		if strings.Contains(l, sub) {
			v.cursor = i
			v.clamp()

			return
		}
	}

	t.Fatalf("no line holds %q", sub)
}

// page delivers the page answers cmd holds.
func (r *planRig) page(cmd tea.Cmd) {
	for _, m := range flatten(cmd) {
		if pm, ok := m.(planPageMsg); ok {
			r.a.Update(pm)
		}
	}
}

func (r *planRig) send(msg tea.Msg) tea.Cmd {
	_, cmd := r.a.Update(msg)

	return cmd
}

func (r *planRig) enter() tea.Cmd { return r.send(planKey("enter")) }

func TestPlanEnterLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		line     string
		wantURLs []string
		chooser  []string
		folded   bool // the fold toggles
	}{
		{"no target", "plain words only", nil, nil, false},
		{"one link", "one site here", []string{"https://example.com/a"}, nil, false},
		{"three targets", "first then second", nil, []string{"first", "second", "third"}, false},
		{"fold head", "More", nil, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := linkPlan(t, 120, 40)
			r.onLine(t, tt.line)

			before := len(r.a.plan.shown().layout().Lines)
			r.page(r.enter())

			if got := r.opener.urls(); !slices.Equal(got, tt.wantURLs) {
				t.Errorf("the opener got %q, want %q", got, tt.wantURLs)
			}

			if r.a.plan == nil {
				t.Fatal("the overlay closed")
			}

			var got []string

			if c := r.a.plan.chooser; c != nil {
				for _, tg := range c.targets {
					got = append(got, tg.Text)
				}
			}

			if !slices.Equal(got, tt.chooser) {
				t.Errorf("chooser lists %q, want %q", got, tt.chooser)
			}

			if folded := len(r.a.plan.shown().layout().Lines) != before; folded != tt.folded {
				t.Errorf("fold toggled = %v, want %v", folded, tt.folded)
			}

			if r.a.plan.chooser != nil && !strings.Contains(r.shown(t), "Links") {
				t.Error("the chooser is not drawn")
			}
		})
	}
}

func TestPlanLinkChooser(t *testing.T) {
	t.Parallel()

	t.Run("j and k move, enter follows and closes", func(t *testing.T) {
		t.Parallel()

		r := linkPlan(t, 120, 40)
		r.onLine(t, "first then second")
		r.enter()
		r.keys("j", "j", "j", "k", "k", "k")

		if sel := r.a.plan.chooser.sel; sel != 0 {
			t.Fatalf("selection = %d after j j j k k k, want 0", sel)
		}

		r.keys("j")
		r.page(r.enter())

		if r.a.plan.chooser != nil {
			t.Error("enter left the chooser open")
		}

		if len(r.a.plan.pages) != 1 {
			t.Errorf("%d pages, want the second target as one", len(r.a.plan.pages))
		}
	})

	t.Run("enter on a url follows it", func(t *testing.T) {
		t.Parallel()

		r := linkPlan(t, 120, 40)
		r.onLine(t, "first then second")
		r.enter()
		r.page(r.enter())

		if got := r.opener.urls(); !slices.Equal(got, []string{"https://example.com/1"}) {
			t.Errorf("the opener got %q", got)
		}

		if r.a.plan == nil || r.a.plan.chooser != nil {
			t.Error("the overlay should stay open with the chooser closed")
		}
	})

	t.Run("esc closes only the chooser", func(t *testing.T) {
		t.Parallel()

		r := linkPlan(t, 120, 40)
		r.onLine(t, "first then second")
		r.enter()
		r.keys("esc")

		if r.a.plan == nil || r.a.plan.chooser != nil {
			t.Fatalf("overlay open %v, chooser open %v", r.a.plan != nil, r.a.plan != nil && r.a.plan.chooser != nil)
		}
	})

	t.Run("other keys and a paste are dropped", func(t *testing.T) {
		t.Parallel()

		r := linkPlan(t, 120, 40)
		r.onLine(t, "first then second")
		r.enter()

		cur := r.a.plan.shown().cursor
		r.keys("x", "/", "]", "G", "tab", "leader")
		r.a.Update(tea.PasteMsg{Content: "zzz"})

		pv := r.a.plan
		if pv.chooser == nil || pv.searching || pv.query != "" || pv.shown().cursor != cur || pv.chooser.sel != 0 {
			t.Errorf("state changed: chooser %v searching %v query %q cursor %d (was %d)", pv.chooser != nil, pv.searching, pv.query, pv.shown().cursor, cur)
		}
	})

	t.Run("a box too short for one row draws nothing and esc closes it", func(t *testing.T) {
		t.Parallel()

		r := linkPlan(t, 120, 40)
		r.onLine(t, "first then second")
		r.enter()
		r.a.Update(tea.WindowSizeMsg{Width: 120, Height: 8})

		if strings.Contains(r.shown(t), "Links") {
			t.Error("the chooser is drawn in a box with no room")
		}

		r.keys("esc")

		if r.a.plan == nil || r.a.plan.chooser != nil {
			t.Error("esc did not close the chooser alone")
		}
	})
}

func TestPlanPages(t *testing.T) {
	t.Parallel()

	r := linkPlan(t, 120, 40)
	r.onLine(t, "the b page")

	planCursor := r.a.plan.view.cursor
	r.page(r.enter())

	pv := r.a.plan
	if len(pv.pages) != 1 || pv.pages[0].path != filepath.Join(r.wt, "docs/b.md") {
		t.Fatalf("pages = %v", pv.pages)
	}

	screen := r.shown(t)
	if !strings.Contains(screen, "docs/b.md") || strings.Contains(screen, "plan file") || !strings.Contains(screen, "Bee") {
		t.Errorf("page not shown with its title and no tag:\n%s", screen)
	}

	// The keys act on the page.
	r.keys("/")
	r.keys("b", "e", "e", "enter")

	if pv.last != "bee" || pv.shown().cursor == 0 {
		t.Errorf("search did not act on the page: last %q cursor %d", pv.last, pv.shown().cursor)
	}

	r.keys("n")

	if pv.view.cursor != planCursor {
		t.Error("a key moved the plan under the page")
	}

	r.keys("]")

	r.a.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelDown})

	if pv.view.top != 0 {
		t.Error("the wheel scrolled the plan under the page")
	}

	r.onLine(t, "back")
	r.page(r.enter())

	if len(pv.pages) != 2 || !strings.Contains(r.shown(t), "docs/superpowers/plans/p.md") {
		t.Fatalf("a link inside a page did not push a page: %d pages", len(pv.pages))
	}

	r.keys("esc")

	if len(pv.pages) != 1 || r.a.plan != pv {
		t.Fatalf("esc did not pop one page: %d pages", len(pv.pages))
	}

	r.keys("esc")

	if len(pv.pages) != 0 || r.a.plan != pv || pv.view.cursor != planCursor {
		t.Fatalf("esc did not return to the plan: %d pages, cursor %d (was %d)", len(pv.pages), pv.view.cursor, planCursor)
	}

	if !strings.Contains(r.shown(t), "plan file") {
		t.Error("the tag did not come back")
	}

	r.keys("esc")

	if r.a.plan != nil {
		t.Error("a further esc did not close the overlay")
	}
}

func TestPlanLinkEditor(t *testing.T) {
	t.Parallel()

	t.Run("a file inside the root opens an editor", func(t *testing.T) {
		t.Parallel()

		r := linkPlan(t, 120, 40)
		r.onLine(t, "first then second")
		r.enter()
		r.keys("j", "j")

		base := r.st.count()

		for _, m := range flatten(r.enter()) {
			if le, ok := m.(linkEditMsg); ok {
				r.a.Update(le)
			}
		}

		if r.a.plan != nil {
			t.Fatal("the overlay stayed open")
		}

		if got := r.st.count() - base; got != 1 {
			t.Fatalf("%d editors started, want 1", got)
		}

		argv := r.st.cmds[base].Argv
		if got, want := argv[len(argv)-2:], []string{"+1", filepath.Join(r.wt, "main.go")}; !slices.Equal(got, want) {
			t.Errorf("the editor got %q, want %q", got, want)
		}
	})

	t.Run("a path outside the root is refused", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct{ name, line, want string }{
			{"up", "up and", "x.md"},
			{"abs", "up and", "/etc/hosts"},
		} {
			r := linkPlan(t, 120, 40)
			base := r.st.count()

			r.onLine(t, tt.line)

			// The parser joins an absolute link onto the file's folder, so the
			// target is built as an already resolved one.
			target := mdTarget{Path: "/etc/hosts"}

			if tt.name == "up" {
				r.enter()
				target = r.a.plan.chooser.targets[0]

				r.keys("esc")
			}

			r.page(r.a.planFollow(target))

			if r.a.plan == nil || len(r.a.plan.pages) != 0 || r.st.count() != base {
				t.Fatalf("%s: overlay open %v, editors %d", tt.name, r.a.plan != nil, r.st.count()-base)
			}

			if !strings.HasPrefix(r.a.notice, "not opening ") || !strings.HasSuffix(r.a.notice, ": outside the worktree") || !strings.Contains(r.a.notice, tt.want) {
				t.Errorf("%s: notice = %q", tt.name, r.a.notice)
			}
		}
	})
}

func TestPlanPageLate(t *testing.T) {
	t.Parallel()

	ask := func(t *testing.T, r *planRig) planPageMsg {
		t.Helper()

		r.onLine(t, "the b page")

		for _, m := range flatten(r.enter()) {
			if pm, ok := m.(planPageMsg); ok {
				return pm
			}
		}

		t.Fatal("no page read raised")

		return planPageMsg{}
	}

	t.Run("after esc closed the overlay", func(t *testing.T) {
		t.Parallel()

		r := linkPlan(t, 120, 40)
		msg := ask(t, r)
		r.keys("esc")
		r.a.Update(msg)

		if r.a.plan != nil {
			t.Error("a late page reopened the overlay")
		}
	})

	t.Run("after a second follow", func(t *testing.T) {
		t.Parallel()

		r := linkPlan(t, 120, 40)
		first := ask(t, r)
		second := ask(t, r)

		r.a.Update(first)

		if len(r.a.plan.pages) != 0 {
			t.Fatal("the first answer pushed a page")
		}

		r.a.Update(second)

		if len(r.a.plan.pages) != 1 {
			t.Fatalf("pages = %d, want 1", len(r.a.plan.pages))
		}
	})
}

func TestPlanLinksGolden(t *testing.T) {
	for _, name := range []string{"plan-links", "plan-page"} {
		t.Run("catppuccin-mocha/icons=true/"+name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(planLinkScene(t, true, name).View().Content))
		})
	}
}

func TestPlanLinksText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, name := range []string{"plan-links", "plan-page"} {
			t.Run(map[bool]string{true: "icons=true/", false: "icons=false/"}[icons]+name, func(t *testing.T) {
				t.Parallel()
				golden.RequireEqual(t, []byte(ansi.Strip(planLinkScene(t, icons, name).View().Content)))
			})
		}
	}
}

func planLinkScene(t *testing.T, icons bool, name string) *App {
	t.Helper()

	r := newPlanRig(t, 100, 30)
	r.a.icons = icons
	r.filePlan(t, planLinkPath, planLinkDoc)
	write(t, filepath.Join(r.wt, "docs/b.md"), "# Bee\n\nbee words and [a link](https://example.com/b)\n\n## More\n\nmore bee words\n")
	chord(r.a, 'a')
	r.open(t)

	if name == "plan-links" {
		r.onLine(t, "first then second")
		r.enter()
	} else {
		r.onLine(t, "the b page")
		r.page(r.enter())
	}

	return r.a
}

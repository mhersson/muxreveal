package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/md"
	"github.com/mhersson/muxreveal/internal/ui/palette"
)

func TestReadLocalMarkdown(t *testing.T) {
	t.Parallel()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(root, "in.md"), "inside\n")
	write(t, filepath.Join(root, "sub", "deep.md"), "deep\n")
	write(t, filepath.Join(root, "big.md"), strings.Repeat("x", 5<<20))
	write(t, filepath.Join(outside, "secret.md"), "secret\n")

	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}

	if err := os.Symlink("in.md", filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		want    string // the text read; "" when an error is wanted
		wantLen int
		err     error
		anyErr  bool // an error that is none of the sentinels
	}{
		{name: "a relative file", path: "in.md", want: "inside\n"},
		{name: "an absolute file", path: filepath.Join(root, "sub", "deep.md"), want: "deep\n"},
		{name: "a symlink that stays inside", path: "alias.md", want: "inside\n"},
		{name: "a path with dots that stays inside", path: "sub/../in.md", want: "inside\n"},
		{name: "a relative path out of the worktree", path: "../outside.md", err: errOutsideWorktree},
		{name: "an absolute path elsewhere", path: filepath.Join(outside, "secret.md"), err: errOutsideWorktree},
		{name: "the root's parent", path: filepath.Dir(root), err: errOutsideWorktree},
		{name: "a symlink to a file outside", path: "link.md", anyErr: true},
		{name: "a folder", path: "sub", err: errIsFolder},
		{name: "the root", path: root, err: errIsFolder},
		{name: "a missing file", path: "nope.md", err: fs.ErrNotExist},
		{name: "a file past the cap", path: "big.md", wantLen: md.MaxBytes + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := readLocalMarkdown(context.Background(), root, tt.path)

			switch {
			case tt.err != nil:
				if !errors.Is(err, tt.err) || got != nil {
					t.Errorf("read = %q, %v, want error %v and nothing read", got, err, tt.err)
				}
			case tt.anyErr:
				if err == nil || got != nil || errors.Is(err, errOutsideWorktree) {
					t.Errorf("read = %q, %v, want a read error and nothing read", got, err)
				}
			case tt.wantLen > 0:
				if err != nil || len(got) != tt.wantLen {
					t.Errorf("read %d bytes, %v, want %d", len(got), err, tt.wantLen)
				}
			default:
				if err != nil || string(got) != tt.want {
					t.Errorf("read = %q, %v, want %q", got, err, tt.want)
				}
			}
		})
	}
}

func TestMarkdownNotice(t *testing.T) {
	t.Parallel()

	home := "/home/me"
	root := "/home/me/code/repo"

	tests := []struct {
		name string
		path string
		err  error
		want string
	}{
		{"outside, under home", "/home/me/other/x.md", errOutsideWorktree, "not opening ~/other/x.md: outside the worktree"},
		{"outside, elsewhere", "/etc/hosts", errOutsideWorktree, "not opening /etc/hosts: outside the worktree"},
		{"missing", root + "/docs/gone.md", &fs.PathError{Op: "stat", Path: "docs/gone.md", Err: fs.ErrNotExist}, "not opening docs/gone.md: no such file"},
		{"a folder", root + "/docs", errIsFolder, "not opening docs: it is a folder"},
		{"another reason", root + "/link.md", &fs.PathError{Op: "statat", Path: "link.md", Err: errors.New("path escapes from parent")}, "not opening link.md: path escapes from parent"},
		{"a control character", root + "/a\x1b[31m.md", errIsFolder, "not opening a[31m.md: it is a folder"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := markdownNotice(root, tt.path, home, tt.err); got != tt.want {
				t.Errorf("notice = %q, want %q", got, tt.want)
			}
		})
	}
}

const pageDoc = `# Alpha

intro text

## Beta

- item one
- item two

<details>
<summary>More</summary>

hidden text

</details>

## Gamma

last words
`

// pageRig is a drawer on a repository with markdown files: README.md, docs/a.md
// and an ignored notes/b.md.
type pageRig struct {
	*drawerRig
	v    *reviewView
	dir  string
	root string
	st   *starts
}

// newPageRig opens a drawer on a repository; extra adds files to the worktree.
func newPageRig(t *testing.T, extra map[string]string) *pageRig {
	t.Helper()

	dir := newRepoDir(t)
	write(t, filepath.Join(dir, "README.md"), "# Readme\n\nhello there\n")
	write(t, filepath.Join(dir, "docs/a.md"), pageDoc)
	write(t, filepath.Join(dir, ".gitignore"), "notes/\n")
	write(t, filepath.Join(dir, "main.go"), numberedLines("line", 40))
	commitEverything(t, dir, "init")
	write(t, filepath.Join(dir, "notes/b.md"), "# Ignored\n")
	write(t, filepath.Join(dir, "main.go"), editedLines(40, 3, 30))

	for name, text := range extra {
		write(t, filepath.Join(dir, name), text)
	}

	return openPageRig(t, dir)
}

func openPageRig(t *testing.T, dir string) *pageRig {
	t.Helper()

	r := newKeysRig(t, dir, func(o *Options) {
		o.Getenv = func(k string) string {
			return map[string]string{"SHELL": "/bin/testsh", "EDITOR": "ed", "HOME": "/home/test"}[k]
		}
	})
	st := &starts{}
	r.start = st.start
	r.icons = true

	m := &pageRig{drawerRig: r, dir: dir, st: st}
	m.v = r.opened(t)
	m.root = rootOf(t, dir)

	return m
}

// flow feeds the messages of cmd, and of what Update returns for them, to Update,
// except those hold names, which it hands back unsent.
func (r *drawerRig) flow(cmd tea.Cmd, hold func(tea.Msg) bool) (held []tea.Msg) {
	queue := flatten(cmd)

	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]

		switch {
		case m == nil:
		case hold != nil && hold(m):
			held = append(held, m)
		default:
			queue = append(queue, flatten(r.step(m))...)
		}
	}

	return held
}

func holdLists(m tea.Msg) bool {
	_, ok := m.(mdListMsg)

	return ok
}

func holdPages(m tea.Msg) bool {
	_, ok := m.(mdPageMsg)

	return ok
}

func (r *drawerRig) tabID() core.TabID { return r.store.State().Tab().ID }

// typeKeys presses a rune for each character of s.
func (r *drawerRig) typeKeys(s string) {
	for _, c := range s {
		r.press(keyRune(c))
	}
}

// mdOpen runs the md.open command and returns the list answers it held.
func (r *drawerRig) mdOpen() []tea.Msg {
	c, ok := r.reg.get("md.open")
	if !ok {
		panic("md.open is not a command")
	}

	cmd := c.run(r.App)
	_, after := r.Update(struct{}{})

	return r.flow(tea.Batch(cmd, after), holdLists)
}

// openPage shows rel as a page, pushed or as the only one.
func (m *pageRig) openPage(t *testing.T, rel string, push bool) *mdPage {
	t.Helper()

	m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, rel), push), nil)

	p := m.v.page()
	if p == nil || p.rel != rel {
		t.Fatalf("page = %+v, want %s (notice %q)", p, rel, m.notice)
	}

	return p
}

// overlayIDs are the ids of the items in the open palette.
func overlayIDs(a *App) []string {
	var out []string

	if a.overlay != nil {
		for _, it := range a.overlay.items {
			out = append(out, it.ID)
		}
	}

	return out
}

// pageLine is the layout line of the page that holds sub.
func pageLine(t *testing.T, p *mdPage, sub string) int {
	t.Helper()

	p.view.layout()

	for i, l := range p.view.doc.text() {
		if strings.Contains(l, sub) {
			return i
		}
	}

	t.Fatalf("no line holds %q", sub)

	return 0
}

func TestMarkdownOpen(t *testing.T) {
	t.Parallel()

	m := newPageRig(t, nil)
	m.showTree(t)

	title := func() string { return strings.SplitN(m.drawn(t), "\n", 2)[0] }
	before := title()

	if m.layout(t).treeW == 0 {
		t.Fatal("the tree is not open before md.open")
	}

	for _, held := range m.mdOpen() {
		m.flow(m.step(held), nil)
	}

	if want := []string{"README.md", "docs/a.md", "notes/b.md"}; !slices.Equal(overlayIDs(m.App), want) {
		t.Fatalf("palette lists %v, want %v", overlayIDs(m.App), want)
	}

	box := ansi.Strip(m.overlay.pal.View(60))
	for _, want := range []string{iconMarkdown + " Markdown files", "enter open"} {
		if !strings.Contains(box, want) {
			t.Errorf("the palette lacks %q:\n%s", want, box)
		}
	}

	m.typeKeys("docs/a")
	m.press(keyEnter)

	p := m.v.page()
	if p == nil || p.rel != "docs/a.md" || p.path != filepath.Join(m.root, "docs", "a.md") || len(m.v.pages) != 1 {
		t.Fatalf("pages = %+v, want docs/a.md only (notice %q)", m.v.pages, m.notice)
	}

	if m.overlay != nil || !m.drawer.focused {
		t.Errorf("overlay open = %v, drawer has the keyboard = %v, want the page to hold it", m.overlay != nil, m.drawer.focused)
	}

	got := m.drawn(t)
	if tag := " " + iconMarkdown + " docs/a.md "; !strings.Contains(strings.SplitN(got, "\n", 2)[0], tag) {
		t.Errorf("top border = %q, lacks the tag %q", strings.SplitN(got, "\n", 2)[0], tag)
	}

	if !strings.Contains(got, "Alpha") || strings.Contains(got, "main.go") {
		t.Errorf("the drawer shows:\n%s\nwant the page and none of the diff", got)
	}

	if lay := m.layout(t); lay.treeW != 0 || lay.headH != 0 {
		t.Errorf("layout under a page: tree %d, header %d, want none", lay.treeW, lay.headH)
	}

	// The title is the diff's.
	if a, b := titlePart(title()), titlePart(before); a != b {
		t.Errorf("title = %q, was %q", a, b)
	}
}

// titlePart is the title of a drawer's top border: the text up to the tags.
func titlePart(top string) string {
	if i := strings.Index(top, " "+iconMarkdown+" "); i >= 0 {
		top = top[:i]
	}

	if i := strings.Index(top, " "+iconBranchMode+" "); i >= 0 {
		top = top[:i]
	}

	return strings.TrimRight(top, "─ ")
}

func TestMarkdownOpenNoDrawer(t *testing.T) {
	t.Parallel()

	t.Run("a pane in a repository", func(t *testing.T) {
		t.Parallel()

		dir := newRepoDir(t)
		write(t, filepath.Join(dir, "README.md"), "# Readme\n")
		write(t, filepath.Join(dir, "docs/a.md"), pageDoc)
		commitEverything(t, dir, "init")

		r := newKeysRig(t, dir)
		if r.store.State().Tab().Review != nil {
			t.Fatal("a drawer is open at the start")
		}

		held := r.mdOpen()
		r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil && r.view() != nil && r.view().root != "" })

		for _, m := range held {
			r.flow(r.step(m), nil)
		}

		// The list may be asked for after the drawer knows its root.
		r.until(t, "the files to be listed", func() bool { return r.overlay != nil })

		if want := []string{"README.md", "docs/a.md"}; !slices.Equal(overlayIDs(r.App), want) {
			t.Errorf("palette lists %v, want %v", overlayIDs(r.App), want)
		}

		if !r.drawer.focused {
			t.Error("the new drawer lacks the keyboard")
		}
	})

	t.Run("a pane outside a repository", func(t *testing.T) {
		t.Parallel()

		r := newKeysRig(t, t.TempDir())
		r.notice = ""

		held := r.mdOpen()

		r.until(t, "the notice", func() bool { return r.notice == noticeNotRepo })

		if len(held) != 0 || r.overlay != nil || r.store.State().Tab().Review != nil || r.drawer.focused {
			t.Errorf("held %d answers, overlay %v, drawer %v, want nothing", len(held), r.overlay != nil, r.store.State().Tab().Review != nil)
		}
	})
}

func TestMarkdownOpenNotices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*mdListMsg)
		want   string
	}{
		{"no files", func(m *mdListMsg) { m.files = nil }, "no markdown files in this worktree"},
		{"a listing error", func(m *mdListMsg) { m.files, m.err = nil, errors.New("boom") }, "markdown files: boom"},
	}

	m := newPageRig(t, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			held := m.mdOpen()
			if len(held) != 1 {
				t.Fatalf("md.open held %d answers, want 1", len(held))
			}

			msg, ok := held[0].(mdListMsg)
			if !ok {
				t.Fatalf("held %T", held[0])
			}

			tt.mutate(&msg)

			m.notice = ""
			m.flow(m.step(msg), nil)

			if m.notice != tt.want || m.overlay != nil {
				t.Errorf("notice = %q, overlay open = %v, want %q and none", m.notice, m.overlay != nil, tt.want)
			}
		})
	}
}

func TestMarkdownOpenLate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		after func(m *pageRig)
		// undo runs after the late answer, to put back what after changed.
		undo func(m *pageRig)
	}{
		{"the drawer closed", func(m *pageRig) { m.closeDrawer(false) }, nil},
		{"a retarget", func(m *pageRig) { m.flow(m.reviewRetarget(m.tabID(), m.v, 0), nil) }, nil},
		{"the compose box opened", func(m *pageRig) { m.compose = &composeView{} }, func(m *pageRig) { m.compose = nil }},
		{"a palette opened", func(m *pageRig) { m.openCommandPalette() }, func(m *pageRig) { m.closeOverlay() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newPageRig(t, nil)

			held := m.mdOpen()
			if len(held) != 1 {
				t.Fatalf("md.open held %d answers, want 1", len(held))
			}

			tt.after(m)
			m.flow(m.step(held[0]), nil)

			if m.overlay != nil && m.overlay.kind == overlayMarkdown {
				t.Errorf("the late list opened the palette: %v", overlayIDs(m.App))
			}

			if tt.undo != nil {
				tt.undo(m)
			}
		})
	}

	t.Run("a second md.open", func(t *testing.T) {
		t.Parallel()

		m := newPageRig(t, nil)
		first, second := m.mdOpen(), m.mdOpen()

		m.flow(m.step(first[0]), nil)

		if m.overlay != nil {
			t.Fatal("the first list opened after a second md.open")
		}

		m.flow(m.step(second[0]), nil)

		if m.overlay == nil || m.overlay.kind != overlayMarkdown {
			t.Error("the second list did not open")
		}
	})

	t.Run("a page read after b", func(t *testing.T) {
		t.Parallel()

		m := newPageRig(t, nil)
		held := m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "docs/a.md"), false), holdPages)

		m.press(keyRune('b'))
		m.flow(m.step(held[0]), nil)

		if len(m.v.pages) != 0 {
			t.Errorf("a page read opened after b: %v", m.v.pages)
		}
	})

	t.Run("a page read after a retarget", func(t *testing.T) {
		t.Parallel()

		m := newPageRig(t, nil)
		held := m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "docs/a.md"), false), holdPages)

		m.flow(m.reviewRetarget(m.tabID(), m.v, 0), nil)
		m.flow(m.step(held[0]), nil)

		if len(m.v.pages) != 0 {
			t.Errorf("a page read opened after a retarget: %v", m.v.pages)
		}
	})

	t.Run("a page read after another", func(t *testing.T) {
		t.Parallel()

		m := newPageRig(t, nil)
		first := m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "docs/a.md"), false), holdPages)
		second := m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "README.md"), false), holdPages)

		m.flow(m.step(second[0]), nil)
		m.flow(m.step(first[0]), nil)

		if p := m.v.page(); len(m.v.pages) != 1 || p.rel != "README.md" {
			t.Errorf("pages = %v, want README.md only", m.v.pages)
		}
	})
}

func TestMarkdownOpenPageErrors(t *testing.T) {
	t.Parallel()

	m := newPageRig(t, nil)
	write(t, filepath.Join(m.dir, "docs/link.md"), "x\n")

	if err := os.Remove(filepath.Join(m.dir, "docs/link.md")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/etc/hostname", filepath.Join(m.dir, "docs/link.md")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}

	tests := []struct {
		name, path, want string
	}{
		{"a missing file", "docs/gone.md", "not opening docs/gone.md: no such file"},
		{"a folder", "docs", "not opening docs: it is a folder"},
		{"a path out of the worktree", "../x.md", "not opening ~/x.md: outside the worktree"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(m.root, tt.path)
			if strings.HasPrefix(tt.path, "../") {
				path = "/home/test/x.md"
			}

			m.notice = ""
			m.flow(m.openMarkdownPage(m.tabID(), path, false), nil)

			if m.notice != tt.want || len(m.v.pages) != 0 {
				t.Errorf("notice = %q, pages %d, want %q and none", m.notice, len(m.v.pages), tt.want)
			}
		})
	}

	t.Run("a symlink out of the worktree", func(t *testing.T) {
		m.notice = ""
		m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "docs/link.md"), false), nil)

		if !strings.HasPrefix(m.notice, "not opening docs/link.md: ") || len(m.v.pages) != 0 {
			t.Errorf("notice = %q, pages %d, want a refusal and no page", m.notice, len(m.v.pages))
		}
	})
}

func TestMarkdownPageKeys(t *testing.T) {
	t.Parallel()

	long := new(strings.Builder)
	for i := range 120 {
		fmt.Fprintf(long, "paragraph %d\n\n", i)
	}

	m := newPageRig(t, map[string]string{"docs/long.md": long.String()})

	type snap struct {
		pages, rows, cursor, lines int
		split, tree                bool
		fullOn                     int
		list                       bool
	}

	snapOf := func() snap {
		p := m.v.page()

		return snap{len(m.v.pages), len(m.v.rows), p.view.cursor, len(p.view.layout().Lines), m.v.split, m.drawer.tree, len(m.v.fullOn), m.drawer.list != nil}
	}

	t.Run("lines", func(t *testing.T) {
		p := m.openPage(t, "docs/a.md", false)

		m.press(keyRune('j'))
		m.press(keyRune('j'))

		if p.view.cursor != 2 {
			t.Errorf("after j j: cursor = %d, want 2", p.view.cursor)
		}

		m.press(keyRune('k'))
		m.press(tea.KeyPressMsg{Code: tea.KeyDown})
		m.press(tea.KeyPressMsg{Code: tea.KeyUp})

		if p.view.cursor != 1 {
			t.Errorf("after k down up: cursor = %d, want 1", p.view.cursor)
		}
	})

	t.Run("headings", func(t *testing.T) {
		p := m.openPage(t, "docs/a.md", false)
		beta, gamma := pageLine(t, p, "Beta"), pageLine(t, p, "Gamma")

		m.press(keyRune(']'))

		if p.view.cursor != beta {
			t.Errorf("] from the top: cursor = %d, want Beta's %d", p.view.cursor, beta)
		}

		m.press(keyRune(']'))

		if p.view.cursor != gamma {
			t.Errorf("] again: cursor = %d, want Gamma's %d", p.view.cursor, gamma)
		}

		m.press(keyRune('['))

		if p.view.cursor != beta {
			t.Errorf("[: cursor = %d, want Beta's %d", p.view.cursor, beta)
		}
	})

	t.Run("pages and ends", func(t *testing.T) {
		p := m.openPage(t, "docs/long.md", false)
		last := len(p.view.layout().Lines) - 1
		half := p.view.h / 2

		if last < p.view.h*2 {
			t.Fatalf("the long page has %d lines for a window of %d", last+1, p.view.h)
		}

		m.press(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

		if p.view.cursor != half {
			t.Errorf("ctrl+d: cursor = %d, want half a window, %d", p.view.cursor, half)
		}

		m.press(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})

		if p.view.cursor != 0 {
			t.Errorf("ctrl+u: cursor = %d, want 0", p.view.cursor)
		}

		m.press(keyRune('G'))

		if p.view.cursor != last {
			t.Errorf("G: cursor = %d, want %d", p.view.cursor, last)
		}

		m.press(keyRune('g'))

		if p.view.cursor != 0 {
			t.Errorf("g: cursor = %d, want 0", p.view.cursor)
		}
	})

	t.Run("enter on a fold head and elsewhere", func(t *testing.T) {
		p := m.openPage(t, "docs/a.md", false)
		before := len(p.view.layout().Lines)

		m.press(keyEnter)

		if got := len(p.view.layout().Lines); got != before || len(m.v.pages) != 1 {
			t.Errorf("enter off a fold: %d lines (was %d), %d pages, want no change", got, before, len(m.v.pages))
		}

		folds := p.view.layout().Folds
		if len(folds) == 0 {
			t.Fatal("the page has no fold")
		}

		p.view.cursor = folds[0].Line
		p.view.clamp()
		m.press(keyEnter)

		if got := len(p.view.layout().Lines); got == before {
			t.Errorf("enter on the fold head left %d lines", got)
		}

		m.press(keyEnter)

		if got := len(p.view.layout().Lines); got != before {
			t.Errorf("enter on the fold head again: %d lines, want %d", got, before)
		}
	})

	t.Run("search", func(t *testing.T) {
		p := m.openPage(t, "docs/a.md", false)

		m.press(keyRune('/'))
		m.typeKeys("gamma")
		m.press(keyEnter)

		if want := pageLine(t, p, "Gamma"); p.view.cursor != want || m.v.search != nil {
			t.Errorf("after /gamma: cursor = %d, search open = %v, want %d and closed", p.view.cursor, m.v.search != nil, want)
		}

		m.press(keyRune('/'))
		m.typeKeys("zzz")
		m.notice = ""
		m.press(keyEnter)

		if m.notice != "not found: zzz" {
			t.Errorf("notice = %q, want not found: zzz", m.notice)
		}
	})

	t.Run("r reads the file again", func(t *testing.T) {
		p := m.openPage(t, "docs/a.md", false)
		write(t, filepath.Join(m.dir, "docs/a.md"), strings.Replace(pageDoc, "intro text", "brand new intro", 1))

		m.press(keyRune('r'))

		if got := strings.Join(p.view.doc.text(), "\n"); !strings.Contains(got, "brand new intro") {
			t.Errorf("the page after r:\n%s\nwant the new text", got)
		}

		write(t, filepath.Join(m.dir, "docs/a.md"), pageDoc)
	})

	t.Run("other keys are dropped", func(t *testing.T) {
		m.openPage(t, "docs/a.md", false)

		before := snapOf()

		for _, k := range []tea.KeyPressMsg{keyRune('f'), keyRune('u'), {Code: tea.KeyTab}, keyRune('w'), keyRune('m'), keyRune('c'), keyRune('}'), keyRune(','), keyRune('x')} {
			m.notice = ""
			m.press(k)

			if got := snapOf(); got != before || m.notice != "" {
				t.Errorf("%s: state %+v (was %+v), notice %q, want it dropped", k.String(), got, before, m.notice)
			}
		}
	})
}

func TestMarkdownPageStack(t *testing.T) {
	t.Parallel()

	m := newPageRig(t, nil)
	m.v.cursor = 3
	diffCursor := m.v.cursor

	first := m.openPage(t, "docs/a.md", false)
	second := m.openPage(t, "README.md", true)

	if len(m.v.pages) != 2 || m.v.page() != second || m.v.pages[0] != first {
		t.Fatalf("pages = %v, want a.md under README.md", m.v.pages)
	}

	first.view.cursor = 2

	m.press(keyEsc)

	if len(m.v.pages) != 1 || m.v.page() != first || first.view.cursor != 2 || !m.drawer.focused {
		t.Fatalf("after esc: %d pages, top %v, drawer has the keyboard %v, want a.md alone with its cursor", len(m.v.pages), m.v.page(), m.drawer.focused)
	}

	m.press(keyEsc)

	if len(m.v.pages) != 0 || m.v.cursor != diffCursor || !m.drawer.focused || m.store.State().Tab().Review == nil {
		t.Fatalf("after the second esc: %d pages, diff cursor %d (was %d), keyboard %v, want the diff as it was", len(m.v.pages), m.v.cursor, diffCursor, m.drawer.focused)
	}

	if got := m.drawn(t); !strings.Contains(got, "main.go") {
		t.Errorf("the drawer after the last pop:\n%s\nwant the diff", got)
	}

	t.Run("md.open over a stack replaces it", func(t *testing.T) {
		m.openPage(t, "docs/a.md", false)
		m.openPage(t, "README.md", true)

		for _, held := range m.mdOpen() {
			m.flow(m.step(held), nil)
		}

		m.flow(m.step(palette.ChosenMsg{ID: "docs/a.md"}), nil)

		if len(m.v.pages) != 1 || m.v.page().rel != "docs/a.md" {
			t.Errorf("pages = %v, want docs/a.md alone", m.v.pages)
		}
	})

	for _, key := range []rune{'b', 't', 'p'} {
		t.Run(string(key)+" drops the stack", func(t *testing.T) {
			m.openPage(t, "docs/a.md", false)
			m.openPage(t, "README.md", true)

			m.press(keyRune(key))

			if len(m.v.pages) != 0 {
				t.Errorf("%c left %d pages", key, len(m.v.pages))
			}
		})
	}
}

func TestMarkdownPageReload(t *testing.T) {
	t.Parallel()

	// The cursor keeps its source line, so the edit keeps every line where it was.
	newDoc := strings.Replace(pageDoc, "intro text", "added above", 1)

	tests := []struct {
		name  string
		batch git.Batch
		pr    bool
		reads bool
	}{
		{"the page's path", git.Batch{Paths: []string{"docs/a.md"}}, false, true},
		{"another path", git.Batch{Paths: []string{"main.go", "docs/other.md"}}, false, false},
		{"an overflow", git.Batch{Overflow: true}, false, true},
		{"a removed parent", git.Batch{Paths: []string{"docs"}}, false, true},
		{"the page's path in pull request mode", git.Batch{Paths: []string{"docs/a.md"}}, true, true},
		{"an overflow in pull request mode", git.Batch{Overflow: true}, true, true},
		{"a head change only", git.Batch{Git: true}, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newPageRig(t, nil)
			p := m.openPage(t, "docs/a.md", false)
			p.view.cursor = pageLine(t, p, "Gamma")
			p.view.clamp()

			old := p.view.doc

			write(t, filepath.Join(m.dir, "docs/a.md"), newDoc)

			if tt.pr {
				m.v.pr = core.ReviewPR{Repo: "o/r", Number: 7, Root: m.root}
			}

			cmd := m.batch(m.root, tt.batch)

			m.v.pr = core.ReviewPR{}

			var reloads []tea.Msg

			for _, msg := range flatten(cmd) {
				if pm, ok := msg.(mdPageMsg); ok && pm.reload {
					reloads = append(reloads, msg)
				}
			}

			if got := len(reloads) > 0; got != tt.reads {
				t.Fatalf("the batch read the page again = %v, want %v", got, tt.reads)
			}

			for _, msg := range reloads {
				m.flow(m.step(msg), nil)
			}

			changed := p.view.doc != old

			if changed != tt.reads {
				t.Fatalf("the page's doc changed = %v, want %v", changed, tt.reads)
			}

			if !tt.reads {
				return
			}

			text := strings.Join(p.view.doc.text(), "\n")
			if !strings.Contains(text, "added above") {
				t.Errorf("the page lacks the new text:\n%s", text)
			}

			if line := p.view.doc.text()[p.view.cursor]; !strings.Contains(line, "Gamma") {
				t.Errorf("the cursor is on %q, want it on Gamma", line)
			}
		})
	}

	t.Run("a page under another reads again when it shows", func(t *testing.T) {
		t.Parallel()

		m := newPageRig(t, nil)
		low := m.openPage(t, "docs/a.md", false)
		m.openPage(t, "README.md", true)

		old := low.view.doc

		write(t, filepath.Join(m.dir, "docs/a.md"), newDoc)

		if reads := m.flow(m.batch(m.root, git.Batch{Paths: []string{"docs/a.md"}}), holdPages); len(reads) != 0 {
			t.Fatalf("a covered page was read again at once: %v", reads)
		}

		m.press(keyEsc)

		if m.v.page() != low || low.view.doc == old {
			t.Errorf("the page under was not read again once it showed (doc changed %v)", low.view.doc != old)
		}
	})

	t.Run("an answer for a page that is gone", func(t *testing.T) {
		t.Parallel()

		m := newPageRig(t, nil)
		m.openPage(t, "docs/a.md", false)

		held := m.flow(m.batch(m.root, git.Batch{Paths: []string{"docs/a.md"}}), holdPages)
		if len(held) != 1 {
			t.Fatalf("%d reads held, want 1", len(held))
		}

		m.press(keyEsc)
		m.flow(m.step(held[0]), nil)

		if len(m.v.pages) != 0 {
			t.Errorf("a late reload brought back a page: %v", m.v.pages)
		}
	})
}

func TestMarkdownPageDropped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		drop func(m *pageRig) *reviewView // the view that should hold no page after
	}{
		{"a retarget", func(m *pageRig) *reviewView {
			m.flow(m.reviewRetarget(m.tabID(), m.v, 0), nil)

			return m.v
		}},
		{"reviewReset", func(m *pageRig) *reviewView {
			return m.reviewReset(m.tabID(), m.v.pane)
		}},
		{"closing the drawer", func(m *pageRig) *reviewView {
			m.closeDrawer(false)

			return m.v
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newPageRig(t, nil)
			m.openPage(t, "docs/a.md", false)
			m.openPage(t, "README.md", true)

			if v := tt.drop(m); len(v.pages) != 0 {
				t.Errorf("%d pages left", len(v.pages))
			}
		})
	}
}

func TestMarkdownPageMouse(t *testing.T) {
	t.Parallel()

	long := new(strings.Builder)
	for i := range 120 {
		fmt.Fprintf(long, "paragraph %d\n\n", i)
	}

	m := newPageRig(t, map[string]string{"docs/long.md": long.String()})
	p := m.openPage(t, "docs/long.md", false)
	rect := mustRect(t, m.drawerRig)

	click := func(row int) {
		m.Update(tea.MouseClickMsg{X: rect.X + 6, Y: rect.Y + 1 + row, Button: tea.MouseLeft})
	}

	click(4)

	if p.view.cursor != 4 {
		t.Errorf("a click on row 4: cursor = %d, want 4", p.view.cursor)
	}

	wheel := func(b tea.MouseButton) {
		m.Update(tea.MouseWheelMsg{X: rect.X + 6, Y: rect.Y + 5, Button: b})
	}

	wheel(tea.MouseWheelDown)

	if p.view.top != wheelRows {
		t.Errorf("a wheel notch down: top = %d, want %d", p.view.top, wheelRows)
	}

	wheel(tea.MouseWheelDown)
	wheel(tea.MouseWheelUp)

	if p.view.top != wheelRows {
		t.Errorf("down, down, up: top = %d, want %d", p.view.top, wheelRows)
	}

	if p.view.cursor < p.view.top || p.view.cursor >= p.view.top+p.view.h {
		t.Errorf("the cursor %d left the window at %d", p.view.cursor, p.view.top)
	}

	click(2)

	if want := p.view.top + 2; p.view.cursor != want {
		t.Errorf("a click on row 2 of a scrolled page: cursor = %d, want %d", p.view.cursor, want)
	}

	if m.drawer.tree || len(m.v.pages) != 1 {
		t.Error("the mouse changed more than the page")
	}
}

func TestMarkdownPageEdit(t *testing.T) {
	t.Parallel()

	m := newPageRig(t, nil)
	p := m.openPage(t, "docs/a.md", false)
	p.view.cursor = pageLine(t, p, "Beta")
	p.view.clamp()

	m.keys(keyRune('e'))

	if m.st.count() != 1 {
		t.Fatalf("e started %d processes, want 1", m.st.count())
	}

	argv := m.st.cmds[0].Argv
	if got, want := argv[len(argv)-2:], []string{"+5", filepath.Join(m.root, "docs", "a.md")}; !slices.Equal(got, want) {
		t.Errorf("the editor got %q, want %q", got, want)
	}

	if m.store.State().Tab().Review != nil || m.drawer.focused || len(m.v.pages) != 0 {
		t.Error("e left the drawer open")
	}
}

// mdPageScene is a drawer on a branch with a markdown page over its diff.
func mdPageScene(th string, icons bool) reviewScene {
	return reviewScene{
		theme: th, icons: icons, w: 200, h: 52, branch: true, focused: true, res: mdResult(),
		tune: func(t *testing.T, a *App, v *reviewView) {
			t.Helper()

			doc := parseMarkdown([]byte(planWith(nil)), "", nil)
			p := &mdPage{path: "/w/p1/" + planPath, rel: planPath, view: mdView{doc: doc}}

			v.pages = []*mdPage{p}
			p.resize(120, 40, icons)
			p.view.cursor = pageLine(t, p, "Steps")
		},
	}
}

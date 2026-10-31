package ui

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/ui/palette"
)

const (
	// mdListTimeout bounds the listing of a worktree's markdown files.
	mdListTimeout = 5 * time.Second

	noticeNoMarkdown = "no markdown files in this worktree"
)

// mdOpenFile is the markdown file the centred overlay shows: repo is the
// repository's printable name and path the file's absolute path.
type mdOpenFile struct {
	repo, path string
}

// mdPick names the worktree a markdown list was made for: its root and the
// repository's printable name.
type mdPick struct {
	root, repo string
}

// mdListMsg answers the listing of a worktree's markdown files. dir is the
// directory md.open listed from and seq the serial of the ask.
type mdListMsg struct {
	seq             int
	dir, root, repo string
	files           []string
	err             error
}

// mdFileMsg answers the read and parse of a picked file.
type mdFileMsg struct {
	seq              int
	root, repo, path string
	doc              *mdDoc
	err              error
}

// mdOpenDir is the directory md.open lists from: what the open drawer shows
// while it has the keyboard, else the focused pane's followed place.
func (a *App) mdOpenDir(st core.State) string {
	tab := st.Tab()
	if tab.Review != nil && a.drawerHasKeys() {
		return a.drawerShows(st, tab)
	}

	return st.Panes[tab.Focus].FollowDir()
}

// mdOpenCommand is md.open: it lists the markdown files of the repository the
// operator is looking at in the palette. It leaves any drawer as it is.
func (a *App) mdOpenCommand() tea.Cmd {
	dir := a.mdOpenDir(a.store.State())
	if dir == "" {
		a.notice = noticeNotRepo

		return nil
	}

	a.planSeq++

	seq, parent := a.planSeq, a.ctx

	return func() tea.Msg {
		msg := mdListMsg{seq: seq, dir: dir}

		ctx, cancel := context.WithTimeout(parent, reviewOpenTimeout)
		defer cancel()

		repo, err := git.Open(ctx, dir)
		if err != nil {
			msg.err = err
			if !usableDir(dir) {
				msg.err = errWorktreeRemoved{root: dir}
			}

			return msg
		}

		msg.root, msg.repo = repo.Root, printable(repo.Name())

		lctx, lcancel := context.WithTimeout(parent, mdListTimeout)
		defer lcancel()

		msg.files, msg.err = git.MarkdownFiles(lctx, repo.Root)

		return msg
	}
}

// onMDList opens the palette on the files listed. An answer after a newer ask,
// after the place md.open lists from changed, or under the compose box or
// another modal above the drawer is dropped.
func (a *App) onMDList(msg mdListMsg) {
	if msg.seq != a.planSeq || a.mdOpenDir(a.store.State()) != msg.dir || a.composeShown() || a.aboveDrawerModalOpen() {
		return
	}

	var gone errWorktreeRemoved

	switch {
	case errors.Is(msg.err, context.Canceled):
		return
	case errors.As(msg.err, &gone):
		a.notice = noticeRemoved + shortPath(gone.root, "", a.getenv("HOME"))

		return
	case errors.Is(msg.err, git.ErrNotRepo):
		a.notice = noticeNotRepo

		return
	case msg.err != nil:
		a.log.Warn("markdown files", "dir", msg.dir, "err", msg.err)
		a.notice = "markdown files: " + printable(msg.err.Error())

		return
	case len(msg.files) == 0:
		a.notice = noticeNoMarkdown

		return
	}

	items := make([]palette.Item, len(msg.files))
	for i, f := range msg.files {
		items[i] = palette.Item{ID: f, Title: printable(f)}
	}

	o := a.openOverlay(overlayMarkdown, a.titled(iconMarkdown, "Markdown files"), items)
	o.pal = o.pal.WithEnterLabel("open")
	o.pick = &mdPick{root: msg.root, repo: msg.repo}
}

// pickMarkdown reads the file picked from the list, off the update loop; the
// overlay shows when the answer lands.
func (a *App) pickMarkdown(pick *mdPick, rel string) tea.Cmd {
	if pick == nil {
		return nil
	}

	a.planSeq++

	path := filepath.Join(pick.root, filepath.FromSlash(rel))
	msg := mdFileMsg{seq: a.planSeq, root: pick.root, repo: pick.repo, path: path}
	parent, tok := a.ctx, a.drawer.hl.HighlightLang

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, mdReadTimeout)
		defer cancel()

		src, err := readLocalMarkdown(ctx, msg.root, msg.path)
		if err != nil {
			msg.err = err

			return msg
		}

		msg.doc = parseMarkdown(src, filepath.Dir(msg.path), tok)

		return msg
	}
}

// onMDFile shows the file once it was read. An answer for an ask a newer one
// superseded, or one that lands under the compose box or another modal, is dropped.
func (a *App) onMDFile(msg mdFileMsg) {
	if msg.seq != a.planSeq || a.composeShown() || a.aboveDrawerModalOpen() {
		return
	}

	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			a.notice = markdownNotice(msg.root, msg.path, a.getenv("HOME"), msg.err)
		}

		return
	}

	msg.doc.withPictures(docPictures{reg: a.pictures, root: msg.root})
	a.endLeader()
	a.plan = &planView{
		file: &mdOpenFile{repo: msg.repo, path: msg.path}, root: msg.root, base: filepath.Dir(msg.path),
		view: mdView{doc: msg.doc},
	}
	a.sizePlan()
}

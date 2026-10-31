package ui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
)

// convReadTimeout bounds one read of a pull request's conversation.
const convReadTimeout = 30 * time.Second

// The conversation overlay's texts.
const (
	convLoading    = "loading the conversation…"
	convReadFailed = "could not read the conversation: %s"
)

// convView is the open conversation overlay: one pull request's description and
// timeline as a markdown view. It is UI state and is never saved. seq numbers
// the read whose answer it waits for; readAt is the update time of the pull
// request the shown conversation is as of, which a newer poll reads again.
type convView struct {
	repo    string
	number  int
	pr      core.PRInfo
	view    mdView
	doc     *convDoc
	seq     int
	loading bool
	err     string
	readAt  time.Time
}

// convReadMsg answers one read of a conversation.
type convReadMsg struct {
	view *convView
	seq  int
	doc  *convDoc
	pr   core.PRInfo
	err  error
}

// openConversation opens the overlay on pull request number of repo (keyed
// "host/owner/name"), which shows at once and fills when the read answers. pr is
// what the caller knows of the pull request.
func (a *App) openConversation(repo string, number int, pr core.PRInfo) tea.Cmd {
	if a.reads == nil {
		a.notice = noticePRsOff

		return nil
	}

	a.endLeader()
	a.conv = &convView{repo: repo, number: number, pr: pr}
	a.prs.dirty = true
	a.sizeConv()

	return a.readConversation(a.conv)
}

// closeConv closes the overlay and drops any read still on its way.
func (a *App) closeConv() {
	if ts := a.text; ts != nil && a.conv != nil && ts.view == &a.conv.view {
		a.clearTextSel()
	}

	a.conv = nil
	a.prs.dirty = true
}

// readConversation reads and parses the conversation of v off the update loop,
// under the timeout and the app's context. An answer of an older read is stale.
func (a *App) readConversation(v *convView) tea.Cmd {
	v.seq++
	v.loading, v.err = true, ""

	repo, ok := repoOfKey(v.repo)
	if !ok || a.reads == nil {
		v.loading, v.err = false, "not available"

		return nil
	}

	if info, ok := prInfoByNumber(a.store.State(), v.repo, v.number); ok && info.UpdatedAt.After(v.readAt) {
		v.readAt = info.UpdatedAt
	}

	if v.doc != nil {
		a.pictures.retry(docPictures{reg: a.pictures, host: repo.Host}, v.view.layout())
	}

	var (
		seq, number = v.seq, v.number
		parent      = a.ctx
		reads       = a.reads
		now         = a.clock.Now()
		base        = a.prLink(core.ReviewPR{Repo: v.repo, Number: v.number})
		tok, hl     = a.drawer.hl.HighlightLang, a.drawer.hl.Highlight
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, convReadTimeout)
		defer cancel()

		c, err := reads.Conversation(ctx, repo, number)
		if err != nil {
			return convReadMsg{view: v, seq: seq, err: err}
		}

		return convReadMsg{view: v, seq: seq, doc: parseConversation(c, base, tok, hl, now), pr: prInfoOf(c.PR)}
	}
}

// onConvRead keeps a conversation's answer. One for an overlay that closed or a
// read a newer one superseded is dropped; a failed read keeps the old conversation
// off the screen and says why.
func (a *App) onConvRead(msg convReadMsg) {
	v := msg.view
	if a.conv != v || v == nil || v.seq != msg.seq {
		return
	}

	v.loading = false

	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			v.err = printable(msg.err.Error())
		}

		return
	}

	v.err = ""

	if msg.pr.Number != 0 {
		v.pr = msg.pr
		if msg.pr.UpdatedAt.After(v.readAt) {
			v.readAt = msg.pr.UpdatedAt
		}
	}

	host := ""
	if r, ok := repoOfKey(v.repo); ok {
		host = r.Host
	}

	msg.doc.withPictures(docPictures{reg: a.pictures, host: host})

	// The cursor keeps its item and its offset in it, and the top its distance
	// from the cursor.
	mv := &v.view

	var (
		id         string
		off, above int
	)

	if v.doc != nil {
		mv.layout()
		id, off = v.doc.itemAt(mv.cursor)
		above = mv.cursor - mv.top
	}

	v.doc, mv.doc = msg.doc, msg.doc
	mv.layout()

	if line := msg.doc.lineOf(id, off); id != "" && line >= 0 {
		mv.cursor, mv.top = line, line-above
	}

	a.sizeConv()
	mv.clamp()
}

// convPoll reads the open conversation again when the poll of its repository
// lists the pull request updated since the conversation was read.
func (a *App) convPoll(st core.State, repo string) tea.Cmd {
	v := a.conv
	if v == nil || v.repo != repo {
		return nil
	}

	if info, ok := prInfoByNumber(st, repo, v.number); ok && info.UpdatedAt.After(v.readAt) {
		return a.readConversation(v)
	}

	return nil
}

// sizeConv sets the cells the view draws in, keeping the cursor on its item when
// the width changes. A box too small to draw leaves the view as it was.
func (a *App) sizeConv() {
	v := a.conv
	if v == nil {
		return
	}

	_, _, w, h, ok := planBox(a.width, a.height)
	if !ok {
		return
	}

	rows, _, _, _ := convRows(h)
	vw := min(w-4, planMaxView)
	mv := &v.view

	if mv.w != 0 && (mv.w != vw || mv.icons != a.icons) && v.doc != nil {
		mv.layout()
		id, off := v.doc.itemAt(mv.cursor)
		above := mv.cursor - mv.top

		mv.size(vw, rows, a.icons)
		mv.layout()

		if line := v.doc.lineOf(id, off); id != "" && line >= 0 {
			mv.cursor, mv.top = line, line-above
		}

		mv.clamp()

		return
	}

	mv.size(vw, rows, a.icons)
}

// handleConvKey answers the open conversation overlay; any other key is
// dropped. A box too small to draw answers esc alone.
func (a *App) handleConvKey(msg tea.KeyPressMsg) tea.Cmd {
	a.notice = ""
	key := bindingKey(msg)

	if key == "esc" {
		a.closeConv()

		return nil
	}

	if _, _, _, _, ok := planBox(a.width, a.height); !ok {
		return nil
	}

	v := a.conv
	mv := &v.view

	a.sizeConv()

	switch key {
	case "j", "down":
		mv.move(1)
	case "k", "up":
		mv.move(-1)
	case "ctrl+d":
		mv.page(1)
	case "ctrl+u":
		mv.page(-1)
	case "ctrl+f", "pgdown":
		mv.pageFull(1)
	case "ctrl+b", "pgup":
		mv.pageFull(-1)
	case "g":
		mv.end(false)
	case "G":
		mv.end(true)
	case "]":
		a.convItem(1)
	case "[":
		a.convItem(-1)
	case "enter":
		mv.fold()
	case "r":
		return a.readConversation(v)
	}

	return nil
}

// convItem puts the cursor on the first line of the next (dir 1) or the current
// item or the previous one (dir -1).
func (a *App) convItem(dir int) {
	v := a.conv
	if v.doc == nil {
		return
	}

	mv := &v.view
	mv.layout()

	if line, ok := v.doc.itemLine(mv.cursor, dir); ok {
		mv.cursor = line
		mv.clamp()
	}
}

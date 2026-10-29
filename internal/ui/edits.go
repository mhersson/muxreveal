package ui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
)

// editState is what the hooks' edit handling keeps between events.
type editState struct {
	seq     uint64                                           // counts the edit hooks in the order they reach the update loop
	last    map[core.PaneID]lastEdit                         // each agent's latest resolved edit
	resolve func(context.Context, string) (git.Place, error) // git.EditPlace; tests replace it
}

// lastEdit is the place an agent's latest edit resolved to. The same path
// again, as a PostToolUse after its PreToolUse, reuses it without git.
type lastEdit struct {
	path  string
	place git.Place
}

// editPlaceMsg answers noteEdit: the place of path, or why it has none. The
// pane's session is the one when the hook arrived, so an answer that comes after
// a session change records nothing.
type editPlaceMsg struct {
	pane    core.PaneID
	session core.SessionID
	path    string
	seq     uint64
	at      time.Time
	place   git.Place
	err     error
}

// noteEdit records the worktree the file an agent just edited belongs to. The
// order is the one the hooks arrive in, whenever git answers. A path resolves
// off the update loop, under the probe limit.
func (a *App) noteEdit(m core.Pane, id core.PaneID, path string) tea.Cmd {
	a.edits.seq++

	msg := editPlaceMsg{pane: id, session: m.Session, path: path, seq: a.edits.seq, at: a.clock.Now()}

	if l, ok := a.edits.last[id]; ok && l.path == path {
		msg.place = l.place
		a.onEditPlace(msg)

		return nil
	}

	ctx, sem, resolve := a.ctx, a.probeSem, a.edits.resolve

	return func() tea.Msg {
		ctx, done, err := admit(ctx, sem)
		if err != nil {
			msg.err = err

			return msg
		}
		defer done()

		msg.place, msg.err = resolve(ctx, path)

		return msg
	}
}

// onEditPlace remembers a resolved place and hands it to the core. An edit that
// does not count, or fails to resolve, changes nothing and raises no notice.
func (a *App) onEditPlace(msg editPlaceMsg) {
	if msg.err != nil {
		// Not counted is the normal case, and its error names the edited path: not logged.
		switch {
		case errors.Is(msg.err, git.ErrNotCounted):
		case errors.Is(msg.err, context.Canceled):
			a.log.Debug("resolve edit place", "pane", msg.pane, "err", msg.err)
		default:
			a.log.Warn("resolve edit place", "pane", msg.pane, "err", msg.err)
		}

		return
	}

	if m, ok := a.store.State().Panes[msg.pane]; !ok || m.Kind != core.KindAgent {
		return
	}

	a.edits.last[msg.pane] = lastEdit{path: msg.path, place: msg.place}

	// dispatch logs its own failures.
	_ = a.dispatch(core.NoteEdit{ID: msg.pane, Session: msg.session, Edit: core.Edit{
		Root: msg.place.Root, Common: msg.place.Common, Repo: msg.place.Repo, Seq: msg.seq, At: msg.at,
	}})
}

// dropEdits forgets the cached edit of every pane that closed or stopped being an agent.
func (a *App) dropEdits(st core.State) {
	for id := range a.edits.last {
		if m, ok := st.Panes[id]; !ok || m.Kind != core.KindAgent {
			delete(a.edits.last, id)
		}
	}
}

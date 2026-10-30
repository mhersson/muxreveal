package git

import (
	"context"
	"fmt"
)

// TurnList is the turns that changed a worktree, oldest first.
type TurnList struct {
	Turns []Turn
	Live  bool // the last is the open current turn and differs from the working tree
}

// TurnList lists the session's turns in worktree that changed it: a closed turn
// whose end differs from its start, any turn with a marker, an open turn that is
// no longer current, and the open current turn while the working tree differs
// from its start.
func (e *Engine) TurnList(ctx context.Context, worktree, session string) (TurnList, error) {
	r, err := Open(ctx, worktree)
	if err != nil {
		return TurnList{}, err
	}

	l, _, err := e.turnList(ctx, r, session, "")

	return l, err
}

// TurnListAt is TurnList for a caller that already has the worktree's current
// tree id, such as the New side of a live branch diff: the open current turn
// is compared with it instead of a fresh working-tree snapshot.
func (e *Engine) TurnListAt(ctx context.Context, worktree, session, workingTree string) (TurnList, error) {
	r, err := Open(ctx, worktree)
	if err != nil {
		return TurnList{}, err
	}

	l, _, err := e.turnList(ctx, r, session, workingTree)

	return l, err
}

// turnList is TurnList on the opened repo r. A known working tree id replaces
// the snapshot; the working tree is returned when it had to be computed, which
// is only for an open current turn.
func (e *Engine) turnList(ctx context.Context, r Repo, session, known string) (TurnList, *WorkTree, error) {
	turns, err := Turns(ctx, r, session)
	if err != nil {
		return TurnList{}, nil, err
	}

	var (
		list TurnList
		wt   *WorkTree
	)

	for _, t := range turns {
		switch {
		case t.EndCommit != "":
			if !t.Missing && !t.EndMissing && t.EndTree == t.Tree {
				continue
			}
		case t.Current && !t.Missing:
			tree := known

			if tree == "" {
				w, err := e.workingTree(ctx, r)
				if err != nil {
					return TurnList{}, nil, fmt.Errorf("turns: %w", err)
				}

				wt, tree = &w, w.Tree
			}

			if tree == t.Tree {
				continue
			}

			list.Live = true
		}

		list.Turns = append(list.Turns, t)
	}

	return list, wt, nil
}

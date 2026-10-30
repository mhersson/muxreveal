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

	l, _, err := e.turnList(ctx, r, session)

	return l, err
}

// turnList is TurnList on the opened repo r. It also returns the working tree
// when it had to compute it, which is only for an open current turn.
func (e *Engine) turnList(ctx context.Context, r Repo, session string) (TurnList, *WorkTree, error) {
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
			w, err := e.workingTree(ctx, r)
			if err != nil {
				return TurnList{}, nil, fmt.Errorf("turns: %w", err)
			}

			wt = &w

			if w.Tree == t.Tree {
				continue
			}

			list.Live = true
		}

		list.Turns = append(list.Turns, t)
	}

	return list, wt, nil
}

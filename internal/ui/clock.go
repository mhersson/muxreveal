package ui

import "time"

// syncMaxHold is how long a DEC 2026 synchronized update may hold a pane's
// redraw; tuios and tmux use the same bound.
const syncMaxHold = time.Second

// clock is the only source of time for the sync hold.
type clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

package ui

import "time"

// syncMaxHold is how long a DEC 2026 synchronized update may hold a pane's
// redraw; tuios and tmux use the same bound.
const syncMaxHold = time.Second

// clock is the app's source of time, so tests can inject a fake one.
type clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

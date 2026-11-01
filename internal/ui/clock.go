package ui

import "time"

// syncMaxHold is how long a DEC 2026 synchronized update may hold a pane's
// redraw; tuios and tmux use the same bound.
const syncMaxHold = time.Second

// clock is where the app reads the time, so tests can inject a fake one; only
// log timings read the wall clock.
type clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

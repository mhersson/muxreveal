// Package harness adapts external coding-agent CLIs to MuxReveal.
package harness

import "github.com/mhersson/muxreveal/internal/core"

// Harness adapts one agent CLI: how to launch it, how to hook it, and how to
// read what its hooks report.
type Harness interface {
	// ID is saved with each agent pane, e.g. "claude-code".
	ID() string
	// Program is the foreground process name of a running agent, e.g. "claude".
	Program() string
	// Launch returns the agent argv. An empty settings omits the hooks file; an
	// empty resume starts a fresh session.
	Launch(settings string, resume core.SessionID) []string
	// Settings returns the hooks file whose handlers run `exe hook <ID()>`.
	Settings(exe string) ([]byte, error)
	// ParseEvent reads one hook payload as received on stdin.
	ParseEvent(payload []byte) (Event, error)
}

// Event is one hook payload reduced to what the app needs.
type Event struct {
	Pane       core.PaneID     `json:"pane"`
	Name       string          `json:"name"` // the harness's hook event name, for logs
	Session    core.SessionID  `json:"session,omitempty"`
	Transcript string          `json:"transcript,omitempty"`
	State      core.AgentState `json:"state,omitempty"` // AgentUnknown leaves the state as it is
	Start      bool            `json:"start,omitempty"` // Session is now the pane's current session
	End        bool            `json:"end,omitempty"`   // the session ended
	Cwd        string          `json:"cwd,omitempty"`   // the agent's working directory

	// TurnStart marks a prompt submission: the agent starts working once the
	// hook is answered, so the server holds the answer until Done.
	TurnStart bool `json:"turn_start,omitempty"`
	// Done releases the held answer. The server sets it on a held event; it is
	// safe to call more than once and concurrently. Nil when nothing is held.
	Done func() `json:"-"`
}

// ByID returns the harness saved under id.
func ByID(id string) (Harness, bool) {
	if id == (Claude{}).ID() {
		return Claude{}, true
	}

	return nil, false
}

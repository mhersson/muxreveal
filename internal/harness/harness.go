// Package harness adapts external coding-agent CLIs to MuxReveal.
package harness

import (
	"context"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
)

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
	// CustomName reads the name the user gave the session from its transcript
	// file; empty when it has none. The name is untrusted text. It does file IO.
	CustomName(ctx context.Context, transcript string) (string, error)
}

// Event is one hook or statusline payload reduced to what the app needs.
type Event struct {
	Pane       core.PaneID     `json:"pane"`
	Name       string          `json:"name"` // the harness's hook event name, or "statusline", for logs
	Session    core.SessionID  `json:"session,omitempty"`
	Transcript string          `json:"transcript,omitempty"`
	State      core.AgentState `json:"state,omitempty"` // AgentUnknown leaves the state as it is
	Start      bool            `json:"start,omitempty"` // Session is now the pane's current session
	End        bool            `json:"end,omitempty"`   // the session ended
	Cwd        string          `json:"cwd,omitempty"`   // the agent's working directory

	// Action is the agent's current action, short and printable. ActionSet true
	// replaces the pane's action with it (empty clears it); false leaves it.
	Action    string `json:"action,omitempty"`
	ActionSet bool   `json:"action_set,omitempty"`

	// Status marks a status update from the agent's statusline, not a hook. It
	// carries Session and the fields below, and leaves state, action and cwd alone.
	Status bool `json:"status,omitempty"`
	// Model is the model's display name, printable and cut like an action; empty
	// when the agent did not report one.
	Model string `json:"model,omitempty"`
	// Context is the percentage of the context window in use, as the agent
	// reports it; it means something only when ContextKnown is true.
	Context      float64 `json:"context,omitempty"`
	ContextKnown bool    `json:"context_known,omitempty"`
	// Window is the context window size in tokens and InputTokens the input
	// tokens used so far; 0 when not reported.
	Window      int `json:"window,omitempty"`
	InputTokens int `json:"input_tokens,omitempty"`

	// TurnStart marks a prompt submission: the agent starts working once the
	// hook is answered, so the server holds the answer until Done.
	TurnStart bool `json:"turn_start,omitempty"`
	// Done releases the held answer. The server sets it on a held event; it is
	// safe to call more than once and concurrently. Nil when nothing is held.
	Done func() `json:"-"`
	// Deadline is when the turn snapshot must be done, stamped by the server
	// as it reads a held turn start; zero when unset.
	Deadline time.Time `json:"-"`
	// Snapped is closed when the turn's snapshot (or its marker) is finished,
	// whether or not it succeeded. Nil when no snapshot is taken for the event.
	Snapped <-chan struct{} `json:"-"`
}

// ByID returns the harness saved under id.
func ByID(id string) (Harness, bool) {
	if id == (Claude{}).ID() {
		return Claude{}, true
	}

	return nil, false
}

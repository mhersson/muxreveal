package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mhersson/muxreveal/internal/core"
)

// Claude is the Claude Code adapter.
type Claude struct{}

const hookTimeout = 5 // seconds; the hook command answers in milliseconds

// claudeEvents returns the hook events MuxReveal registers.
func claudeEvents() []string {
	return []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "PostToolUseFailure",
		"ElicitationResult", "PreToolUse", "PermissionRequest", "Elicitation", "Notification",
		"Stop", "StopFailure",
	}
}

func (Claude) ID() string      { return "claude-code" }
func (Claude) Program() string { return "claude" }

func (Claude) Launch(settings string, resume core.SessionID) []string {
	argv := []string{"claude"}
	if settings != "" {
		argv = append(argv, "--settings", settings)
	}
	if resume != "" {
		argv = append(argv, "--resume", string(resume))
	}
	return argv
}

// Settings uses the exec form so no shell parses exe.
func (c Claude) Settings(exe string) ([]byte, error) {
	if !filepath.IsAbs(exe) {
		return nil, fmt.Errorf("harness: hook executable %q is not an absolute path", exe)
	}
	type handler struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Timeout int      `json:"timeout"`
	}
	type group struct {
		Hooks []handler `json:"hooks"`
	}
	h := handler{Type: "command", Command: exe, Args: []string{"hook", c.ID()}, Timeout: hookTimeout}
	events := claudeEvents()
	hooks := make(map[string][]group, len(events))
	for _, name := range events {
		hooks[name] = []group{{Hooks: []handler{h}}}
	}
	out, err := json.Marshal(struct {
		Hooks map[string][]group `json:"hooks"`
	}{hooks})
	if err != nil {
		return nil, fmt.Errorf("harness: encode settings: %w", err)
	}
	return out, nil
}

// claudePayload holds the only fields read; decoding skips the rest, so a large
// tool_response never reaches the Event.
type claudePayload struct {
	Name             string `json:"hook_event_name"`
	SessionID        string `json:"session_id"`
	TranscriptPath   string `json:"transcript_path"`
	Cwd              string `json:"cwd"`
	Source           string `json:"source"`
	ToolName         string `json:"tool_name"`
	NotificationType string `json:"notification_type"`
}

func (Claude) ParseEvent(payload []byte) (Event, error) {
	var p claudePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Event{}, fmt.Errorf("harness: parse claude-code payload: %w", err)
	}
	if p.Name == "" {
		return Event{}, errors.New("harness: claude-code payload has no hook_event_name")
	}
	ev := Event{
		Name:       p.Name,
		Session:    core.SessionID(p.SessionID),
		Transcript: p.TranscriptPath,
		Cwd:        p.Cwd,
		TurnStart:  p.Name == "UserPromptSubmit",
	}
	switch p.Name {
	case "SessionStart":
		ev.Start = p.SessionID != ""
		if p.Source != "compact" {
			ev.State = core.AgentIdle
		}
	case "SessionEnd":
		ev.End = true
	case "UserPromptSubmit", "PostToolUse", "PostToolUseFailure", "ElicitationResult":
		ev.State = core.AgentWorking
	case "PreToolUse":
		ev.State = core.AgentWorking
		if p.ToolName == "AskUserQuestion" {
			ev.State = core.AgentBlocked
		}
	case "PermissionRequest", "Elicitation":
		ev.State = core.AgentBlocked
	case "Notification":
		switch p.NotificationType {
		case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
			ev.State = core.AgentBlocked
		case "idle_prompt":
			ev.State = core.AgentIdle
		}
	case "Stop", "StopFailure":
		ev.State = core.AgentIdle
	default:
		return Event{}, fmt.Errorf("harness: unknown claude-code event %q", p.Name)
	}
	return ev, nil
}

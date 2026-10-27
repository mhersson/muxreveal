package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

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
	// Lenient: a payload with an odd shape here must not drop the state.
	ToolInput json.RawMessage `json:"tool_input"`
	Message   json.RawMessage `json:"message"`
}

// maxActionRunes bounds the action shown on a card.
const maxActionRunes = 120

// targetKeys are the tool_input fields that name what a tool works on, in order.
var targetKeys = []string{"file_path", "notebook_path", "command", "pattern", "url", "query", "description"}

// printable keeps the printable runes of s (no control, format or other
// non-printing runes) and trims surrounding spaces.
func printable(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}

		return -1
	}, s))
}

// clip makes s an action: printable runes only, at most maxActionRunes.
func clip(s string) string {
	s = printable(s)
	if len(s) <= maxActionRunes {
		return s
	}

	i := 0
	for pos := range s {
		if i == maxActionRunes {
			return s[:pos]
		}

		i++
	}

	return s
}

// jsonString returns raw as a string, or "" when it is not a JSON string.
func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}

	return s
}

// pathKeys are the targetKeys that hold file paths; only these are shown
// relative to the working directory.
var pathKeys = map[string]bool{"file_path": true, "notebook_path": true}

// relativeTo shows an absolute path strictly inside the absolute directory cwd
// relative to it; any other path stays as sent, never in a "../" form.
func relativeTo(cwd, path string) string {
	if !filepath.IsAbs(cwd) || !filepath.IsAbs(path) {
		return path
	}

	rel, err := filepath.Rel(filepath.Clean(cwd), filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}

	return rel
}

// toolTarget is "<tool> <target>"; the target is the first non-empty target
// field, reduced to its first line, with file paths relative to cwd. A
// tool_input that is not an object leaves the tool name alone.
func toolTarget(tool, cwd string, fields map[string]json.RawMessage) string {
	for _, k := range targetKeys {
		t := printable(firstLine(jsonString(fields[k])))
		if t == "" {
			continue
		}

		if pathKeys[k] {
			t = relativeTo(cwd, t)
		}

		return tool + " " + t
	}

	return tool
}

// objectFields reads a tool_input leniently; anything but an object gives nil.
func objectFields(input json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return nil
	}

	return fields
}

// toolAction is the PreToolUse action. AskUserQuestion shows its first
// question when it has one.
func toolAction(tool, cwd string, input json.RawMessage) string {
	fields := objectFields(input)
	if tool == "AskUserQuestion" {
		if q := firstQuestion(fields["questions"]); q != "" {
			return clip(firstLine(q))
		}

		return clip(tool)
	}

	return clip(toolTarget(tool, cwd, fields))
}

// firstQuestion returns the text of the first entry of a questions list: a
// string, or an object with a string "question".
func firstQuestion(raw json.RawMessage) string {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return ""
	}

	if q := jsonString(list[0]); q != "" {
		return q
	}

	var obj map[string]json.RawMessage
	if json.Unmarshal(list[0], &obj) != nil {
		return ""
	}

	return jsonString(obj["question"])
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	line, _, _ = strings.Cut(line, "\r")

	return line
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
		ev.ActionSet = true

		ev.Start = p.SessionID != ""
		if p.Source != "compact" {
			ev.State = core.AgentIdle
		}
	case "SessionEnd":
		ev.End, ev.ActionSet = true, true
	case "UserPromptSubmit":
		ev.State = core.AgentWorking
		ev.Action, ev.ActionSet = "thinking", true
	case "PostToolUse", "PostToolUseFailure":
		ev.State, ev.ActionSet = core.AgentWorking, true
		ev.Action = clip(toolTarget(printable(p.ToolName), p.Cwd, objectFields(p.ToolInput)))
	case "ElicitationResult":
		ev.State = core.AgentWorking
	case "PreToolUse":
		ev.State, ev.ActionSet = core.AgentWorking, true
		if p.ToolName == "AskUserQuestion" {
			ev.State = core.AgentBlocked
		}

		ev.Action = toolAction(p.ToolName, p.Cwd, p.ToolInput)
	case "PermissionRequest":
		ev.State, ev.ActionSet = core.AgentBlocked, true
		ev.Action = permissionAction(p.ToolName, p.Cwd, p.ToolInput)
	case "Elicitation":
		ev.State, ev.ActionSet = core.AgentBlocked, true
		ev.Action = clip(firstLine(jsonString(p.Message)))
	case "Notification":
		switch p.NotificationType {
		case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
			ev.State, ev.ActionSet = core.AgentBlocked, true
			ev.Action = clip(firstLine(jsonString(p.Message)))
		case "idle_prompt":
			ev.State = core.AgentIdle
		}
	case "Stop", "StopFailure":
		ev.State, ev.ActionSet = core.AgentIdle, true
	default:
		return Event{}, fmt.Errorf("harness: unknown claude-code event %q", p.Name)
	}

	return ev, nil
}

// permissionAction is "Allow <tool> <target>?".
func permissionAction(tool, cwd string, input json.RawMessage) string {
	return clip("Allow " + toolTarget(printable(tool), cwd, objectFields(input)) + "?")
}

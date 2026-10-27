package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mhersson/muxreveal/internal/core"
)

const (
	testSession    = core.SessionID("11111111-2222-4333-8444-555555555555")
	testTranscript = "/home/user/.claude/projects/-work/11111111-2222-4333-8444-555555555555.jsonl"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseEvent(t *testing.T) {
	tests := []struct {
		fixture string
		name    string
		state   core.AgentState
		start   bool
		end     bool
	}{
		{"session_start_startup", "SessionStart", core.AgentIdle, true, false},
		{"session_start_resume", "SessionStart", core.AgentIdle, true, false},
		{"session_start_clear", "SessionStart", core.AgentIdle, true, false},
		{"session_start_fork", "SessionStart", core.AgentIdle, true, false},
		{"session_start_compact", "SessionStart", core.AgentUnknown, true, false},
		{"session_end", "SessionEnd", core.AgentUnknown, false, true},
		{"user_prompt_submit", "UserPromptSubmit", core.AgentWorking, false, false},
		{"post_tool_use", "PostToolUse", core.AgentWorking, false, false},
		{"post_tool_use_failure", "PostToolUseFailure", core.AgentWorking, false, false},
		{"elicitation_result", "ElicitationResult", core.AgentWorking, false, false},
		{"pre_tool_use_bash", "PreToolUse", core.AgentWorking, false, false},
		{"pre_tool_use_ask", "PreToolUse", core.AgentBlocked, false, false},
		{"permission_request", "PermissionRequest", core.AgentBlocked, false, false},
		{"elicitation", "Elicitation", core.AgentBlocked, false, false},
		{"notification_permission_prompt", "Notification", core.AgentBlocked, false, false},
		{"notification_elicitation_dialog", "Notification", core.AgentBlocked, false, false},
		{"notification_elicitation_url_dialog", "Notification", core.AgentBlocked, false, false},
		{"notification_agent_needs_input", "Notification", core.AgentBlocked, false, false},
		{"notification_idle_prompt", "Notification", core.AgentIdle, false, false},
		{"notification_auth_success", "Notification", core.AgentUnknown, false, false},
		{"stop", "Stop", core.AgentIdle, false, false},
		{"stop_failure", "StopFailure", core.AgentIdle, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			ev, err := Claude{}.ParseEvent(readFixture(t, tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			want := Event{
				Name:       tt.name,
				Session:    testSession,
				Transcript: testTranscript,
				State:      tt.state,
				Start:      tt.start,
				End:        tt.end,
			}
			if ev != want {
				t.Errorf("event = %+v, want %+v", ev, want)
			}
		})
	}
}

func TestParseEventSessionStartWithoutSession(t *testing.T) {
	ev, err := Claude{}.ParseEvent([]byte(`{"hook_event_name":"SessionStart","source":"startup"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Start || ev.Session != "" || ev.State != core.AgentIdle {
		t.Errorf("event = %+v, want no Start, no session, idle", ev)
	}
}

func TestParseEventErrors(t *testing.T) {
	for _, fixture := range []string{"invalid", "unknown_event", "missing_event"} {
		t.Run(fixture, func(t *testing.T) {
			if _, err := (Claude{}).ParseEvent(readFixture(t, fixture)); err == nil {
				t.Error("want an error")
			}
		})
	}
	if _, err := (Claude{}).ParseEvent(nil); err == nil {
		t.Error("empty payload: want an error")
	}
}

func TestParseEventIgnoresLargeFields(t *testing.T) {
	payload := `{"session_id":"s","transcript_path":"/t","hook_event_name":"PostToolUse","tool_name":"Bash",` +
		`"tool_response":{"stdout":"` + strings.Repeat("x", 5<<20) + `"}}`
	ev, err := Claude{}.ParseEvent([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if ev.State != core.AgentWorking || ev.Session != "s" || ev.Transcript != "/t" {
		t.Errorf("event = %+v", ev)
	}
	enc, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if len(enc) > 256 {
		t.Errorf("encoded event is %d bytes", len(enc))
	}
}

func TestLaunch(t *testing.T) {
	tests := []struct {
		settings string
		resume   core.SessionID
		want     []string
	}{
		{"", "", []string{"claude"}},
		{"/run/1-claude.json", "", []string{"claude", "--settings", "/run/1-claude.json"}},
		{"", testSession, []string{"claude", "--resume", string(testSession)}},
		{"/run/1-claude.json", testSession, []string{"claude", "--settings", "/run/1-claude.json", "--resume", string(testSession)}},
	}
	for _, tt := range tests {
		if got := (Claude{}).Launch(tt.settings, tt.resume); !slices.Equal(got, tt.want) {
			t.Errorf("Launch(%q, %q) = %q, want %q", tt.settings, tt.resume, got, tt.want)
		}
	}
}

func TestIdentity(t *testing.T) {
	if got := (Claude{}).ID(); got != "claude-code" {
		t.Errorf("ID = %q", got)
	}
	if got := (Claude{}).Program(); got != "claude" {
		t.Errorf("Program = %q", got)
	}
}

type settingsFile struct {
	Hooks map[string][]struct {
		Matcher *string `json:"matcher"`
		Hooks   []struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Timeout int      `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func TestSettings(t *testing.T) {
	const exe = "/opt/my tools/muxreveal"
	raw, err := Claude{}.Settings(exe)
	if err != nil {
		t.Fatal(err)
	}
	var s settingsFile
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	events := []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "PostToolUseFailure",
		"ElicitationResult", "PreToolUse", "PermissionRequest", "Elicitation", "Notification",
		"Stop", "StopFailure",
	}
	if len(s.Hooks) != len(events) {
		t.Errorf("%d events registered, want %d", len(s.Hooks), len(events))
	}
	for _, name := range events {
		groups := s.Hooks[name]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Errorf("%s: groups = %+v, want one group with one handler", name, groups)
			continue
		}
		if groups[0].Matcher != nil {
			t.Errorf("%s: has a matcher", name)
		}
		h := groups[0].Hooks[0]
		if h.Type != "command" || h.Command != exe || !slices.Equal(h.Args, []string{"hook", "claude-code"}) || h.Timeout != 5 {
			t.Errorf("%s: handler = %+v", name, h)
		}
		// Every registered event must be one ParseEvent accepts.
		payload := `{"hook_event_name":"` + name + `"}`
		if _, err := (Claude{}).ParseEvent([]byte(payload)); err != nil {
			t.Errorf("%s: registered but not parsed: %v", name, err)
		}
	}
}

func TestSettingsRejectsBadExe(t *testing.T) {
	for _, exe := range []string{"", "muxreveal", "./muxreveal", "bin/muxreveal"} {
		if _, err := (Claude{}).Settings(exe); err == nil {
			t.Errorf("Settings(%q): want an error", exe)
		}
	}
}

func TestByID(t *testing.T) {
	h, ok := ByID("claude-code")
	if !ok || h.ID() != "claude-code" {
		t.Errorf("ByID(claude-code) = %v, %v", h, ok)
	}
	if h, ok := ByID("codex"); ok || h != nil {
		t.Errorf("ByID(codex) = %v, %v", h, ok)
	}
}

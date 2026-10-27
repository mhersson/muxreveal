package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

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
		action  string
		set     bool
	}{
		{"session_start_startup", "SessionStart", core.AgentIdle, true, false, "", true},
		{"session_start_resume", "SessionStart", core.AgentIdle, true, false, "", true},
		{"session_start_clear", "SessionStart", core.AgentIdle, true, false, "", true},
		{"session_start_fork", "SessionStart", core.AgentIdle, true, false, "", true},
		{"session_start_compact", "SessionStart", core.AgentUnknown, true, false, "", true},
		{"session_end", "SessionEnd", core.AgentUnknown, false, true, "", true},
		{"user_prompt_submit", "UserPromptSubmit", core.AgentWorking, false, false, "thinking", true},
		{"post_tool_use", "PostToolUse", core.AgentWorking, false, false, "Bash ls", true},
		{"post_tool_use_failure", "PostToolUseFailure", core.AgentWorking, false, false, "Bash false", true},
		{"elicitation_result", "ElicitationResult", core.AgentWorking, false, false, "", false},
		{"pre_tool_use_bash", "PreToolUse", core.AgentWorking, false, false, "Bash ls", true},
		{"pre_tool_use_ask", "PreToolUse", core.AgentBlocked, false, false, "AskUserQuestion", true},
		{"permission_request", "PermissionRequest", core.AgentBlocked, false, false, "Allow Bash rm x?", true},
		{"elicitation", "Elicitation", core.AgentBlocked, false, false, "", true},
		{"notification_permission_prompt", "Notification", core.AgentBlocked, false, false, "m", true},
		{"notification_elicitation_dialog", "Notification", core.AgentBlocked, false, false, "m", true},
		{"notification_elicitation_url_dialog", "Notification", core.AgentBlocked, false, false, "m", true},
		{"notification_agent_needs_input", "Notification", core.AgentBlocked, false, false, "m", true},
		{"notification_idle_prompt", "Notification", core.AgentIdle, false, false, "", false},
		{"notification_auth_success", "Notification", core.AgentUnknown, false, false, "", false},
		{"stop", "Stop", core.AgentIdle, false, false, "", true},
		{"stop_failure", "StopFailure", core.AgentIdle, false, false, "", true},
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
				Cwd:        "/work",
				TurnStart:  tt.name == "UserPromptSubmit",
				Action:     tt.action,
				ActionSet:  tt.set,
			}
			if !reflect.DeepEqual(ev, want) {
				t.Errorf("event = %+v, want %+v", ev, want)
			}
		})
	}
}

// payload builds a hook payload from raw JSON members after the event name.
func payload(event, rest string) []byte {
	if rest != "" {
		rest = "," + rest
	}

	return []byte(`{"hook_event_name":"` + event + `"` + rest + `}`)
}

func TestParseEventAction(t *testing.T) {
	long := strings.Repeat("a", 200)
	cjk := strings.Repeat("\u6f22", 200) // 200 CJK runes of 3 bytes each
	emoji := strings.Repeat("\U0001F600", 200)

	tests := []struct {
		name    string
		event   string
		rest    string
		state   core.AgentState
		action  string
		set     bool
		wantLen int // wanted rune count when > 0
	}{
		{"file_path", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":"internal/core/modes.go","command":"x"}`, core.AgentWorking, "Edit internal/core/modes.go", true, 0},
		{"notebook_path", "PreToolUse", `"tool_name":"NotebookEdit","tool_input":{"notebook_path":"a.ipynb","pattern":"p"}`, core.AgentWorking, "NotebookEdit a.ipynb", true, 0},
		{"command", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"go test ./...","description":"run"}`, core.AgentWorking, "Bash go test ./...", true, 0},
		{"pattern", "PreToolUse", `"tool_name":"Grep","tool_input":{"pattern":"foo","url":"u"}`, core.AgentWorking, "Grep foo", true, 0},
		{"url", "PreToolUse", `"tool_name":"WebFetch","tool_input":{"url":"https://x.dev","query":"q"}`, core.AgentWorking, "WebFetch https://x.dev", true, 0},
		{"query", "PreToolUse", `"tool_name":"WebSearch","tool_input":{"query":"go vt","description":"d"}`, core.AgentWorking, "WebSearch go vt", true, 0},
		{"description", "PreToolUse", `"tool_name":"Task","tool_input":{"description":"explore"}`, core.AgentWorking, "Task explore", true, 0},
		{"no target", "PreToolUse", `"tool_name":"TodoWrite","tool_input":{"todos":[]}`, core.AgentWorking, "TodoWrite", true, 0},
		{"unknown tool generic rule", "PreToolUse", `"tool_name":"mcp__x__y","tool_input":{"url":"u"}`, core.AgentWorking, "mcp__x__y u", true, 0},
		{"unknown tool no target", "PreToolUse", `"tool_name":"mcp__x__y","tool_input":{"a":1}`, core.AgentWorking, "mcp__x__y", true, 0},
		{"empty first target falls through", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"","description":"d"}`, core.AgentWorking, "Bash d", true, 0},
		{"non-string target skipped", "PreToolUse", `"tool_name":"Bash","tool_input":{"file_path":5,"command":"ls"}`, core.AgentWorking, "Bash ls", true, 0},
		{"missing tool_input", "PreToolUse", `"tool_name":"Bash"`, core.AgentWorking, "Bash", true, 0},
		{"tool_input string", "PreToolUse", `"tool_name":"Bash","tool_input":"ls"`, core.AgentWorking, "Bash", true, 0},
		{"tool_input array", "PreToolUse", `"tool_name":"Bash","tool_input":["ls"]`, core.AgentWorking, "Bash", true, 0},
		{"tool_input null", "PreToolUse", `"tool_name":"Bash","tool_input":null`, core.AgentWorking, "Bash", true, 0},
		{"tool_input number", "PreToolUse", `"tool_name":"Bash","tool_input":7`, core.AgentWorking, "Bash", true, 0},
		{"multi-line command keeps first line", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"echo a\necho b"}`, core.AgentWorking, "Bash echo a", true, 0},
		{"crlf command keeps first line", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"echo a\r\necho b"}`, core.AgentWorking, "Bash echo a", true, 0},
		{"escape sequences removed", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"\u001b[31mred\u001b[0m \u009b2J\u0007x"}`, core.AgentWorking, "Bash [31mred[0m 2Jx", true, 0},
		{"format characters removed", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"a\u200bb\u202ec\ufeffd"}`, core.AgentWorking, "Bash abcd", true, 0},
		{"tab removed", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"a\tb"}`, core.AgentWorking, "Bash ab", true, 0},
		{"control characters in the tool name", "PreToolUse", `"tool_name":"Ba\u001bsh\n","tool_input":{"command":"ls"}`, core.AgentWorking, "Bash ls", true, 0},
		{"60 KiB command", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"` + strings.Repeat("x", 60<<10) + `"}`, core.AgentWorking, "", true, 120},
		{"long target cut", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"` + long + `"}`, core.AgentWorking, "", true, 120},
		{"CJK cut at 120 runes", "PreToolUse", `"tool_name":"Read","tool_input":{"file_path":"` + cjk + `"}`, core.AgentWorking, "Read " + strings.Repeat("\u6f22", 115), true, 120},
		{"emoji cut at 120 runes", "PreToolUse", `"tool_name":"Read","tool_input":{"file_path":"` + emoji + `"}`, core.AgentWorking, "Read " + strings.Repeat("\U0001F600", 115), true, 120},
		{"ask first question", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which db?","header":"DB"},{"question":"Other"}]}`, core.AgentBlocked, "Which db?", true, 0},
		{"ask question with newline", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which\ndb?"}]}`, core.AgentBlocked, "Which", true, 0},
		{"ask empty questions", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":[]}`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"ask no questions", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{}`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"ask questions not a list", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":"why"}`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"ask question not a string", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":5}]}`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"ask question entry is a number", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":[3]}`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"ask tool_input string", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":"x"`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"permission with target", "PermissionRequest", `"tool_name":"Bash","tool_input":{"command":"rm x"}`, core.AgentBlocked, "Allow Bash rm x?", true, 0},
		{"permission without target", "PermissionRequest", `"tool_name":"Bash"`, core.AgentBlocked, "Allow Bash?", true, 0},
		{"permission tool_input array", "PermissionRequest", `"tool_name":"Bash","tool_input":[1]`, core.AgentBlocked, "Allow Bash?", true, 0},
		{"permission long command", "PermissionRequest", `"tool_name":"Bash","tool_input":{"command":"` + strings.Repeat("y", 60<<10) + `"}`, core.AgentBlocked, "", true, 120},
		{"permission prompt message", "Notification", `"notification_type":"permission_prompt","message":"Claude needs permission"`, core.AgentBlocked, "Claude needs permission", true, 0},
		{"agent needs input message", "Notification", `"notification_type":"agent_needs_input","message":"a\u001b[2Jb\nc"`, core.AgentBlocked, "a[2Jb", true, 0},
		{"blocking notification without message clears", "Notification", `"notification_type":"elicitation_dialog"`, core.AgentBlocked, "", true, 0},
		{"blocking notification message not a string", "Notification", `"notification_type":"permission_prompt","message":{"a":1}`, core.AgentBlocked, "", true, 0},
		{"idle notification leaves the action", "Notification", `"notification_type":"idle_prompt","message":"waiting"`, core.AgentIdle, "", false, 0},
		{"other notification leaves the action", "Notification", `"notification_type":"auth_success","message":"ok"`, core.AgentUnknown, "", false, 0},
		{"elicitation message", "Elicitation", `"message":"Enter token"`, core.AgentBlocked, "Enter token", true, 0},
		{"elicitation long message", "Elicitation", `"message":"` + long + `"`, core.AgentBlocked, "", true, 120},
		{"prompt", "UserPromptSubmit", `"prompt":"do the thing"`, core.AgentWorking, "thinking", true, 0},
		{"stop clears", "Stop", ``, core.AgentIdle, "", true, 0},
		{"stop failure clears", "StopFailure", ``, core.AgentIdle, "", true, 0},
		{"session start clears", "SessionStart", `"session_id":"s","source":"startup"`, core.AgentIdle, "", true, 0},
		{"compact session start clears", "SessionStart", `"session_id":"s","source":"compact"`, core.AgentUnknown, "", true, 0},
		{"session end clears", "SessionEnd", ``, core.AgentUnknown, "", true, 0},
		{"post tool use replaces the action", "PostToolUse", `"tool_name":"Bash","tool_input":{"command":"ls"}`, core.AgentWorking, "Bash ls", true, 0},
		{"post tool use failure replaces the action", "PostToolUseFailure", `"tool_name":"Bash"`, core.AgentWorking, "Bash", true, 0},
		{"post tool use relative file", "PostToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/b/x/y.go"}`, core.AgentWorking, "Edit x/y.go", true, 0},
		{"post tool use failure relative file", "PostToolUseFailure", `"cwd":"/a/b","tool_name":"Read","tool_input":{"file_path":"/a/b/y.go"}`, core.AgentWorking, "Read y.go", true, 0},
		{"pre inside the cwd", "PreToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/b/x/y.go"}`, core.AgentWorking, "Edit x/y.go", true, 0},
		{"pre notebook inside the cwd", "PreToolUse", `"cwd":"/a/b","tool_name":"NotebookEdit","tool_input":{"notebook_path":"/a/b/n.ipynb"}`, core.AgentWorking, "NotebookEdit n.ipynb", true, 0},
		{"pre unclean path inside the cwd", "PreToolUse", `"cwd":"/a/b/","tool_name":"Edit","tool_input":{"file_path":"/a/b/./x//y.go"}`, core.AgentWorking, "Edit x/y.go", true, 0},
		{"pre outside the cwd", "PreToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/c/y.go"}`, core.AgentWorking, "Edit /a/c/y.go", true, 0},
		{"pre parent of the cwd", "PreToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/y.go"}`, core.AgentWorking, "Edit /a/y.go", true, 0},
		{"pre equal to the cwd", "PreToolUse", `"cwd":"/a/b","tool_name":"Read","tool_input":{"file_path":"/a/b"}`, core.AgentWorking, "Read /a/b", true, 0},
		{"pre relative target", "PreToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"x/y.go"}`, core.AgentWorking, "Edit x/y.go", true, 0},
		{"pre sibling sharing the cwd prefix", "PreToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/bc/x"}`, core.AgentWorking, "Edit /a/bc/x", true, 0},
		{"pre empty cwd", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":"/a/b/x"}`, core.AgentWorking, "Edit /a/b/x", true, 0},
		{"pre relative cwd", "PreToolUse", `"cwd":"a/b","tool_name":"Edit","tool_input":{"file_path":"/a/b/x"}`, core.AgentWorking, "Edit /a/b/x", true, 0},
		{"pre command is not relativised", "PreToolUse", `"cwd":"/a/b","tool_name":"Bash","tool_input":{"command":"/a/b/x"}`, core.AgentWorking, "Bash /a/b/x", true, 0},
		{"pre pattern is not relativised", "PreToolUse", `"cwd":"/a/b","tool_name":"Grep","tool_input":{"pattern":"/a/b/x"}`, core.AgentWorking, "Grep /a/b/x", true, 0},
		{"pre url is not relativised", "PreToolUse", `"cwd":"/a/b","tool_name":"WebFetch","tool_input":{"url":"/a/b/x"}`, core.AgentWorking, "WebFetch /a/b/x", true, 0},
		{"permission inside the cwd", "PermissionRequest", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/b/x/y.go"}`, core.AgentBlocked, "Allow Edit x/y.go?", true, 0},
		{"permission outside the cwd", "PermissionRequest", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/c/y.go"}`, core.AgentBlocked, "Allow Edit /a/c/y.go?", true, 0},
		{"permission equal to the cwd", "PermissionRequest", `"cwd":"/a/b","tool_name":"Read","tool_input":{"file_path":"/a/b"}`, core.AgentBlocked, "Allow Read /a/b?", true, 0},
		{"permission relative target", "PermissionRequest", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"x"}`, core.AgentBlocked, "Allow Edit x?", true, 0},
		{"permission sibling sharing the cwd prefix", "PermissionRequest", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/bc/x"}`, core.AgentBlocked, "Allow Edit /a/bc/x?", true, 0},
		{"permission empty cwd", "PermissionRequest", `"tool_name":"Edit","tool_input":{"file_path":"/a/b/x"}`, core.AgentBlocked, "Allow Edit /a/b/x?", true, 0},
		{"elicitation result leaves", "ElicitationResult", ``, core.AgentWorking, "", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := Claude{}.ParseEvent(payload(tt.event, tt.rest))
			if err != nil {
				t.Fatal(err)
			}

			if ev.State != tt.state || ev.ActionSet != tt.set {
				t.Errorf("state, set = %v, %v, want %v, %v", ev.State, ev.ActionSet, tt.state, tt.set)
			}

			if tt.wantLen == 0 && ev.Action != tt.action {
				t.Errorf("action = %q, want %q", ev.Action, tt.action)
			}

			if n := utf8.RuneCountInString(ev.Action); n > 120 || (tt.wantLen > 0 && n != tt.wantLen) {
				t.Errorf("action has %d runes, want %d", n, max(tt.wantLen, 120))
			}

			if !utf8.ValidString(ev.Action) {
				t.Errorf("action %q is not valid UTF-8", ev.Action)
			}

			for _, r := range ev.Action {
				if !unicode.IsPrint(r) {
					t.Errorf("action %q holds non-printable %U", ev.Action, r)
				}
			}

			if !ev.ActionSet && ev.Action != "" {
				t.Errorf("action %q on an event that leaves it", ev.Action)
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

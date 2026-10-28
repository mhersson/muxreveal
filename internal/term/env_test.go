package term

import (
	"slices"
	"strings"
	"testing"
)

func TestPaneEnv(t *testing.T) {
	tests := []struct {
		name string
		base []string
		want []string
	}{
		{"empty", nil, []string{"TERM=xterm-256color", "COLORTERM=truecolor"}},
		{
			"strips and keeps",
			[]string{"TERM=dumb", "HOME=/h", "TMUX=/t,1,0", "COLORTERM=24bit", "TMUX_PANE=%1", "PATH=/bin"},
			[]string{"HOME=/h", "PATH=/bin", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"duplicates collapse",
			[]string{"TERM=a", "X=1", "TERM=b"},
			[]string{"X=1", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"pane identity dropped",
			[]string{"MUXREVEAL_PANE=p1", "MUXREVEAL_SOCK=/s", "MUXREVEAL_OTHER=1"},
			[]string{"MUXREVEAL_OTHER=1", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"similar names kept",
			[]string{"TERMINFO=/x", "TMUX_FOO=1"},
			[]string{"TERMINFO=/x", "TMUX_FOO=1", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PaneEnv(tt.base); !slices.Equal(got, tt.want) {
				t.Errorf("PaneEnv = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommands(t *testing.T) {
	lookup := func(shell string) func(string) string {
		return func(k string) string {
			if k == "SHELL" {
				return shell
			}

			return ""
		}
	}

	tests := []struct {
		name  string
		shell string
		shArg []string
		agent []string
	}{
		{
			"fish", "/usr/bin/fish",
			[]string{"/usr/bin/fish", "-l"},
			[]string{"/usr/bin/fish", "-l", "-i", "-c", `status job-control full; 'claude'; exec "$SHELL" -l`},
		},
		{
			"fallback", "",
			[]string{"/bin/sh", "-l"},
			[]string{"/bin/sh", "-l", "-i", "-c", `'claude'; exec "$SHELL" -l`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sh := ShellCommand(lookup(tt.shell), "/work", nil)
			if !slices.Equal(sh.Argv, tt.shArg) || sh.Dir != "/work" {
				t.Errorf("ShellCommand = %+v", sh)
			}

			ag := AgentCommand(lookup(tt.shell), "/work", []string{"claude"}, nil)
			if !slices.Equal(ag.Argv, tt.agent) || ag.Dir != "/work" {
				t.Errorf("AgentCommand = %+v", ag)
			}

			wantShell := tt.shArg[0]

			for _, c := range []Command{sh, ag} {
				var shells []string

				for _, kv := range c.Env {
					if strings.HasPrefix(kv, "SHELL=") {
						shells = append(shells, kv)
					}
				}

				if !slices.Equal(shells, []string{"SHELL=" + wantShell}) {
					t.Errorf("SHELL entries = %q, want only SHELL=%s", shells, wantShell)
				}

				if !slices.Contains(c.Env, "TERM=xterm-256color") {
					t.Errorf("Env lacks TERM: %q", c.Env)
				}
			}
		})
	}
}

func TestCommandsExtraEnv(t *testing.T) {
	t.Setenv("MUXREVEAL_PANE", "inherited")

	env := func(string) string { return "/bin/sh" }
	extra := []string{"MUXREVEAL_PANE=p2", "MUXREVEAL_SOCK=/run/1.sock"}

	for name, c := range map[string]Command{
		"shell": ShellCommand(env, "/work", extra),
		"agent": AgentCommand(env, "/work", []string{"claude"}, extra),
	} {
		var panes []string

		for _, kv := range c.Env {
			if strings.HasPrefix(kv, "MUXREVEAL_PANE=") {
				panes = append(panes, kv)
			}
		}

		if !slices.Equal(panes, []string{"MUXREVEAL_PANE=p2"}) {
			t.Errorf("%s: MUXREVEAL_PANE entries = %q, want only p2", name, panes)
		}

		if !slices.Contains(c.Env, "MUXREVEAL_SOCK=/run/1.sock") {
			t.Errorf("%s: Env lacks extra entry: %q", name, c.Env)
		}
	}
}

func TestAgentCommandQuoting(t *testing.T) {
	env := func(string) string { return "/bin/sh" }
	c := AgentCommand(env, "/work", []string{"claude", "--settings", "/tmp/a b/it's.json", "$(x)"}, nil)

	want := `'claude' '--settings' '/tmp/a b/it'\''s.json' '$(x)'; exec "$SHELL" -l`
	if got := c.Argv[len(c.Argv)-1]; got != want {
		t.Errorf("script = %q, want %q", got, want)
	}
}

func TestEditorCommand(t *testing.T) {
	sh := func(string) string { return "/bin/sh" }
	fish := func(string) string { return "/usr/bin/fish" }

	tests := []struct {
		name   string
		env    func(string) string
		editor string
		args   []string
		want   string
	}{
		{"a plain editor", sh, "vi", []string{"+7", "a.go"}, `vi '+7' 'a.go'; exec "$SHELL" -l`},
		{"an editor with arguments is read by the shell", sh, "code -w", []string{"+7", "a.go"}, `code -w '+7' 'a.go'; exec "$SHELL" -l`},
		{"a quote and a space stay one argument", sh, "vi", []string{"+7", "it's a file.go"}, `vi '+7' 'it'\''s a file.go'; exec "$SHELL" -l`},
		{"shell syntax in an argument is not run", sh, "vi", []string{"$(x)", "a;b"}, `vi '$(x)' 'a;b'; exec "$SHELL" -l`},
		{"fish gets job control first", fish, "vi", []string{"a.go"}, `status job-control full; vi 'a.go'; exec "$SHELL" -l`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := EditorCommand(tt.env, "/work", tt.editor, tt.args, []string{"MUXREVEAL_PANE=p2"})

			wantSh := tt.env("SHELL")
			if len(c.Argv) != 5 || c.Argv[0] != wantSh || c.Argv[1] != "-l" || c.Argv[2] != "-i" || c.Argv[3] != "-c" {
				t.Fatalf("Argv = %q, want %s -l -i -c <script>", c.Argv, wantSh)
			}

			if got := c.Argv[4]; got != tt.want {
				t.Errorf("script = %q, want %q", got, tt.want)
			}

			if c.Dir != "/work" {
				t.Errorf("Dir = %q, want /work", c.Dir)
			}

			if !slices.Contains(c.Env, "SHELL="+wantSh) || !slices.Contains(c.Env, "MUXREVEAL_PANE=p2") {
				t.Errorf("Env lacks SHELL or the extra entry: %q", c.Env)
			}
		})
	}
}

// TestAgentCommandPinned pins, byte for byte, the Command AgentCommand returns.
// It runs nothing, so unlike the tests that start a shell it holds on every platform.
func TestAgentCommandPinned(t *testing.T) {
	t.Setenv("SHELL", "/inherited/shell")
	t.Setenv("MUXREVEAL_PANE", "p9")
	t.Setenv("MUXREVEAL_SOCK", "/inherited.sock")
	t.Setenv("TERM", "dumb")
	t.Setenv("COLORTERM", "16color")

	const (
		tail   = `; exec "$SHELL" -l`
		quoted = `'claude' '--settings' '/tmp/a b/it'\''s.json' '$(x)'`
	)

	argv := []string{"claude", "--settings", "/tmp/a b/it's.json", "$(x)"}
	fishScript := `status job-control full; ` + quoted + tail

	tests := []struct {
		name  string
		shell string // what $SHELL says; "" for unset
		argv  []string
		sh    string // the shell that runs
		want  string
	}{
		{"sh", "/bin/sh", argv, "/bin/sh", quoted + tail},
		{"bash", "/bin/bash", argv, "/bin/bash", quoted + tail},
		{"zsh", "/bin/zsh", argv, "/bin/zsh", quoted + tail},
		{"fish gets job control first", "/usr/bin/fish", argv, "/usr/bin/fish", fishScript},
		{"fish at another path", "/opt/homebrew/bin/fish", argv, "/opt/homebrew/bin/fish", fishScript},
		{"an unset SHELL runs sh", "", argv, "/bin/sh", quoted + tail},
		{"one word", "/bin/zsh", []string{"claude"}, "/bin/zsh", `'claude'` + tail},
	}

	extra := []string{"MUXREVEAL_PANE=p2", "MUXREVEAL_SOCK=/run/1.sock"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := func(k string) string {
				if k == "SHELL" {
					return tt.shell
				}

				return ""
			}

			c := AgentCommand(env, "/work", tt.argv, extra)

			if want := []string{tt.sh, "-l", "-i", "-c", tt.want}; !slices.Equal(c.Argv, want) {
				t.Errorf("Argv = %q, want %q", c.Argv, want)
			}

			if c.Dir != "/work" {
				t.Errorf("Dir = %q, want /work", c.Dir)
			}

			// The inherited identity, shell and terminal values are dropped; the pane's
			// terminal values, the shell that runs and the extra entries end the list, in this order.
			wantTail := []string{"TERM=xterm-256color", "COLORTERM=truecolor", "SHELL=" + tt.sh, "MUXREVEAL_PANE=p2", "MUXREVEAL_SOCK=/run/1.sock"}
			if len(c.Env) < len(wantTail) || !slices.Equal(c.Env[len(c.Env)-len(wantTail):], wantTail) {
				t.Fatalf("Env ends with %q, want %q", c.Env[max(len(c.Env)-len(wantTail), 0):], wantTail)
			}

			for _, kv := range c.Env[:len(c.Env)-len(wantTail)] {
				for _, key := range []string{"SHELL=", "MUXREVEAL_PANE=", "MUXREVEAL_SOCK=", "TERM=", "COLORTERM="} {
					if strings.HasPrefix(kv, key) {
						t.Errorf("Env keeps the inherited %q", kv)
					}
				}
			}
		})
	}
}

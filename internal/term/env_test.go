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
		{"fish", "/usr/bin/fish", []string{"/usr/bin/fish", "-l"},
			[]string{"/usr/bin/fish", "-l", "-i", "-c", `'claude'; exec "$SHELL" -l`}},
		{"fallback", "", []string{"/bin/sh", "-l"},
			[]string{"/bin/sh", "-l", "-i", "-c", `'claude'; exec "$SHELL" -l`}},
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

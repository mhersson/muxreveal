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
			[]string{"/usr/bin/fish", "-l", "-i", "-c", `claude; exec "$SHELL" -l`}},
		{"fallback", "", []string{"/bin/sh", "-l"},
			[]string{"/bin/sh", "-l", "-i", "-c", `claude; exec "$SHELL" -l`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sh := ShellCommand(lookup(tt.shell), "/work")
			if !slices.Equal(sh.Argv, tt.shArg) || sh.Dir != "/work" {
				t.Errorf("ShellCommand = %+v", sh)
			}

			ag := AgentCommand(lookup(tt.shell), "/work")
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

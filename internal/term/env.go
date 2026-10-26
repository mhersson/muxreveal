package term

import (
	"os"
	"slices"
	"strings"
)

// PaneEnv returns base with the terminal variables replaced by the values a
// pane emulator provides.
func PaneEnv(base []string) []string {
	out := make([]string, 0, len(base)+2)

	for _, kv := range base {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "TERM", "COLORTERM", "TMUX", "TMUX_PANE":
		default:
			out = append(out, kv)
		}
	}

	return append(out, "TERM=xterm-256color", "COLORTERM=truecolor")
}

// ShellCommand is a login shell in dir.
func ShellCommand(env func(string) string, dir string) Command {
	sh := shellPath(env)

	return Command{Argv: []string{sh, "-l"}, Dir: dir, Env: shellEnv(sh)}
}

// AgentCommand runs claude in an interactive login shell, then stays in a shell
// when the agent exits.
func AgentCommand(env func(string) string, dir string) Command {
	sh := shellPath(env)

	return Command{
		Argv: []string{sh, "-l", "-i", "-c", `claude; exec "$SHELL" -l`},
		Dir:  dir,
		Env:  shellEnv(sh),
	}
}

// shellEnv is the pane environment with SHELL set to the resolved shell, so
// the agent command's `exec "$SHELL"` works when SHELL was unset.
func shellEnv(sh string) []string {
	env := slices.DeleteFunc(PaneEnv(os.Environ()), func(kv string) bool {
		return strings.HasPrefix(kv, "SHELL=")
	})

	return append(env, "SHELL="+sh)
}

func shellPath(env func(string) string) string {
	if sh := env("SHELL"); sh != "" {
		return sh
	}

	return "/bin/sh"
}

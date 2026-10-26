package term

import (
	"os"
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
	return Command{Argv: []string{shellPath(env), "-l"}, Dir: dir, Env: PaneEnv(os.Environ())}
}

// AgentCommand runs claude in an interactive login shell, then stays in a shell
// when the agent exits.
func AgentCommand(env func(string) string, dir string) Command {
	return Command{
		Argv: []string{shellPath(env), "-l", "-i", "-c", `claude; exec "$SHELL" -l`},
		Dir:  dir,
		Env:  PaneEnv(os.Environ()),
	}
}

func shellPath(env func(string) string) string {
	if sh := env("SHELL"); sh != "" {
		return sh
	}

	return "/bin/sh"
}

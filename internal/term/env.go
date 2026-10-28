package term

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// PaneEnv returns base with the terminal variables replaced by the values a
// pane emulator provides and the inherited pane identity removed.
func PaneEnv(base []string) []string {
	out := make([]string, 0, len(base)+2)

	for _, kv := range base {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "TERM", "COLORTERM", "TMUX", "TMUX_PANE", "MUXREVEAL_PANE", "MUXREVEAL_SOCK":
		default:
			out = append(out, kv)
		}
	}

	return append(out, "TERM=xterm-256color", "COLORTERM=truecolor")
}

// ShellCommand is a login shell in dir. extraEnv is appended to the pane
// environment, so it wins over inherited entries.
func ShellCommand(env func(string) string, dir string, extraEnv []string) Command {
	sh := shellPath(env)

	return Command{Argv: []string{sh, "-l"}, Dir: dir, Env: shellEnv(sh, extraEnv)}
}

// AgentCommand runs argv in an interactive login shell, then stays in a shell
// when the agent exits. Every argument is single-quoted, so nothing in argv is
// interpreted by the shell. argv must not be empty.
func AgentCommand(env func(string) string, dir string, argv, extraEnv []string) Command {
	return runThenShell(env, dir, strings.Join(quoteAll(argv), " "), extraEnv)
}

// EditorCommand runs editor with args in an interactive login shell, then stays
// in a shell when the editor exits. The shell reads editor as git reads
// $EDITOR, so a value such as `code -w` runs code with -w; every argument is
// single-quoted, so nothing in args is interpreted. editor comes from the
// operator's own environment and must not be empty.
func EditorCommand(env func(string) string, dir, editor string, args, extraEnv []string) Command {
	return runThenShell(env, dir, strings.Join(append([]string{editor}, quoteAll(args)...), " "), extraEnv)
}

// runThenShell runs the shell command line in an interactive login shell, then
// replaces that shell with a login shell.
func runThenShell(env func(string) string, dir, line string, extraEnv []string) Command {
	sh := shellPath(env)

	script := line + `; exec "$SHELL" -l`
	if filepath.Base(sh) == "fish" {
		// fish -c runs commands without job control, so the program would not
		// lead the terminal's foreground group.
		script = "status job-control full; " + script
	}

	return Command{
		Argv: []string{sh, "-l", "-i", "-c", script},
		Dir:  dir,
		Env:  shellEnv(sh, extraEnv),
	}
}

func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = quote(a)
	}

	return out
}

// quote is POSIX single-quoting; fish reads it alike for arguments without backslashes.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellEnv is the pane environment with SHELL set to the resolved shell, so
// the agent command's `exec "$SHELL"` works when SHELL was unset.
func shellEnv(sh string, extra []string) []string {
	env := slices.DeleteFunc(PaneEnv(os.Environ()), func(kv string) bool {
		return strings.HasPrefix(kv, "SHELL=")
	})

	return append(append(env, "SHELL="+sh), extra...)
}

func shellPath(env func(string) string) string {
	if sh := env("SHELL"); sh != "" {
		return sh
	}

	return "/bin/sh"
}

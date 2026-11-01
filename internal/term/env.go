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
		case "TERM", "COLORTERM", "TMUX", "TMUX_PANE", "AIM_PANE", "AIM_SOCK":
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
// when the agent exits. argv reaches the shell as separate arguments after a
// fixed script, so nothing in it is parsed. argv must not be empty.
func AgentCommand(env func(string) string, dir string, argv, extraEnv []string) Command {
	return runThenShell(env, dir, "", argv, extraEnv)
}

// EditorCommand runs editor with args in an interactive login shell, then stays
// in a shell when the editor exits. The shell reads editor as git reads
// $EDITOR, so a value such as `code -w` runs code with -w; args reach the shell
// as separate arguments after a fixed script, so nothing in them is parsed.
// editor comes from the operator's own environment and must not be empty.
func EditorCommand(env func(string) string, dir, editor string, args, extraEnv []string) Command {
	return runThenShell(env, dir, editor, args, extraEnv)
}

// EditorOnlyCommand is EditorCommand without the trailing shell: the command
// ends when the editor does.
func EditorOnlyCommand(env func(string) string, dir, editor string, args, extraEnv []string) Command {
	return runIn(env, dir, editor, args, extraEnv, "")
}

// runThenShell runs program, or args[0] when program is empty, with args in an
// interactive login shell, then replaces that shell with a login shell. The
// script holds only fixed text and the operator's program; args are never part
// of it, because no quoting is safe in every shell (fish reads a backslash in
// single quotes as an escape).
func runThenShell(env func(string) string, dir, program string, args, extraEnv []string) Command {
	return runIn(env, dir, program, args, extraEnv, `; exec "$SHELL" -l`)
}

// runIn is runThenShell with the script's tail given.
func runIn(env func(string) string, dir, program string, args, extraEnv []string, tail string) Command {
	sh := shellPath(env)
	isFish := filepath.Base(sh) == "fish"

	positional := `"$@"`
	if isFish {
		positional = "$argv"
	}

	script := strings.TrimSpace(program+" "+positional) + tail
	if isFish {
		// fish -c runs commands without job control, so the program would not
		// lead the terminal's foreground group.
		script = "status job-control full; " + script
	}

	argv := []string{sh, "-l", "-i", "-c", script}
	if !isFish {
		// sh -c takes the next argument as $0; fish -c passes them all to $argv.
		argv = append(argv, filepath.Base(sh))
	}

	return Command{
		Argv: append(argv, args...),
		Dir:  dir,
		Env:  shellEnv(sh, extraEnv),
	}
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

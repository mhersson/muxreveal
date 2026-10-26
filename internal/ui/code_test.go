package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHighlight(t *testing.T) {
	mocha := mustTheme(t, "catppuccin-mocha")

	t.Run("go", func(t *testing.T) {
		lines, err := highlight("func f() {}", "go", mocha)
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != 1 {
			t.Fatalf("got %d lines, want 1", len(lines))
		}
		if got := ansi.Strip(lines[0]); got != "func f() {}" {
			t.Errorf("text = %q", got)
		}
		// The shipped style colours declaration keywords Red.
		if !strings.Contains(lines[0], "38;2;243;139;168") {
			t.Errorf("no Red SGR for func in %q", lines[0])
		}
	})

	t.Run("keyword is Mauve", func(t *testing.T) {
		lines, err := highlight("return", "go", mocha)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(lines[0], "38;2;203;166;247") {
			t.Errorf("no Mauve SGR in %q", lines[0])
		}
	})

	t.Run("unknown language is plain Fg", func(t *testing.T) {
		lines, err := highlight("func f() {}", "no-such-lang", mocha)
		if err != nil {
			t.Fatal(err)
		}
		if got := ansi.Strip(strings.Join(lines, "\n")); got != "func f() {}" {
			t.Errorf("text = %q", got)
		}
		// Text is #cdd6f4 = 205;214;244
		if !strings.Contains(lines[0], "38;2;205;214;244") {
			t.Errorf("no Fg SGR in %q", lines[0])
		}
		if strings.Contains(lines[0], "38;2;203;166;247") {
			t.Errorf("fallback was highlighted: %q", lines[0])
		}
	})

	t.Run("multiple lines and tabs", func(t *testing.T) {
		lines, err := highlight("a\n\treturn 1\n", "go", mocha)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(lines, "\n")
		if strings.Contains(joined, "\t") {
			t.Errorf("tab survived: %q", joined)
		}
		if got := ansi.Strip(lines[1]); !strings.HasPrefix(got, "    return") {
			t.Errorf("line 1 = %q", got)
		}
	})
}

// Ported from tuios internal/linediff/linediff.go at commit
// aaaa128cd35b38ba8aa08a5f69e688f01385ec3e (github.com/Gaurav-Gosain/tuios).
// Copyright (c) 2025 Gaurav Gosain. MIT License; see THIRD_PARTY.md.

// Package linediff splits texts into lines and finds a shortest line edit
// script between two of them. It imports only the standard library.
package linediff

import "strings"

// Op is one line of an edit script: Op is ' ' for a kept line, '-' for a line
// removed from a, '+' for a line added from b.
type Op struct {
	Op   byte
	Line string
}

// Split is s as lines, without a final empty line for a trailing newline. An
// empty s has no lines.
func Split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Replace is the script that removes every line of a and then adds every line
// of b. It is a correct diff of any two texts, for when Diff would cost too
// much.
func Replace(a, b []string) []Op {
	ops := make([]Op, 0, len(a)+len(b))
	for _, l := range a {
		ops = append(ops, Op{'-', l})
	}
	for _, l := range b {
		ops = append(ops, Op{'+', l})
	}
	return ops
}

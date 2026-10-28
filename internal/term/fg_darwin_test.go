package term

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Claude's native install runs claude as a symlink to a file named after its
// version. A symlink to sleep stands in for it: p_comm reports "sleep".
func TestForegroundSymlinkedAgent(t *testing.T) {
	for _, name := range []string{"sh", "bash", "zsh", "fish"} {
		t.Run(name, func(t *testing.T) {
			sh, err := exec.LookPath(name)
			if err != nil {
				t.Skipf("%s not installed", name)
			}

			shellTestEnv(t)
			t.Setenv("SHELL", sh)

			link := filepath.Join(t.TempDir(), "claude")
			if err := os.Symlink("/bin/sleep", link); err != nil {
				t.Fatal(err)
			}

			p := startCommand(t, AgentCommand(os.Getenv, t.TempDir(), []string{link, "30"}, nil))

			waitForeground(t, p, "claude")

			pgrp, err := foregroundPgrp(p.pty)
			if err != nil {
				t.Fatal(err)
			}

			if comm, err := kinfoComm(pgrp); err != nil || comm != "sleep" {
				t.Fatalf("p_comm = %q, %v; want sleep, else this no longer tests the symlink case", comm, err)
			}
		})
	}
}

// procargs builds a kern.procargs2 buffer: argc, the exec path, pad NULs, each
// argument NUL-terminated, then an environment string, as the kernel lays it out.
func procargs(argc uint32, path string, pad int, argv ...string) []byte {
	b := binary.NativeEndian.AppendUint32(nil, argc)
	b = append(append(b, path...), 0)
	b = append(b, make([]byte, pad)...)

	for _, a := range append(argv, "SHELL=/bin/zsh") {
		b = append(append(b, a...), 0)
	}

	return b
}

func TestArgv0Name(t *testing.T) {
	// raw is argc 1 and then s with no terminator added.
	raw := func(s string) []byte { return append(binary.NativeEndian.AppendUint32(nil, 1), s...) }

	tests := []struct {
		name string
		buf  []byte
		want string
		ok   bool
	}{
		{"bare name", procargs(2, "/Users/u/.local/bin/claude", 3, "claude", "--resume"), "claude", true},
		{"full path", procargs(1, "/tmp/x/claude", 0, "/tmp/x/claude"), "claude", true},
		{"long padding", procargs(1, "/bin/sleep", 64, "sleep"), "sleep", true},
		{"login shell", procargs(1, "/bin/zsh", 2, "-zsh"), "zsh", true},
		{"login shell by path", procargs(1, "/opt/homebrew/bin/fish", 1, "-/opt/homebrew/bin/fish"), "fish", true},
		{"no argv", procargs(0, "/bin/sleep", 4), "", false},
		{"short buffer", []byte{1, 0}, "", false},
		{"exec path unterminated", raw("/bin/sleep"), "", false},
		{"argv0 unterminated", raw("/bin/sleep\x00\x00\x00sleep"), "", false},
		{"bare dash", procargs(1, "/bin/sh", 0, "-"), "", false},
		{"root", procargs(1, "/bin/sh", 0, "/"), "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := argv0Name(tt.buf)
			if got != tt.want || ok != tt.ok {
				t.Errorf("argv0Name = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

package term

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Foreground is the name of the terminal's foreground process group leader:
// the base name of its argv[0], else its p_comm. p_comm names a symlink's
// target, and Claude's native install runs as a file named after its version.
func (p *Process) Foreground() (string, error) {
	pgrp, err := foregroundPgrp(p.pty)
	if err != nil {
		return "", err
	}

	// Fails for another user's process (sudo) or one that has exited.
	if b, err := unix.SysctlRaw("kern.procargs2", pgrp); err == nil {
		if name, ok := argv0Name(b); ok {
			return name, nil
		}
	}

	return kinfoComm(pgrp)
}

// argv0Name is the base name of argv[0] in a kern.procargs2 buffer (argc as a
// uint32, the exec path, NUL padding, then argv), without a login shell's
// leading "-", which Linux's comm does not have either. An empty argv[0] cannot
// be told from the padding, as in ps.
func argv0Name(b []byte) (string, bool) {
	if len(b) < 4 || binary.NativeEndian.Uint32(b) == 0 {
		return "", false
	}

	b = b[4:]

	end := bytes.IndexByte(b, 0)
	if end < 0 {
		return "", false
	}

	b = bytes.TrimLeft(b[end:], "\x00")

	end = bytes.IndexByte(b, 0)
	if end < 0 {
		return "", false
	}

	name := filepath.Base(strings.TrimPrefix(string(b[:end]), "-"))
	if name == "." || name == "/" {
		return "", false
	}

	return name, true
}

// kinfoComm is the kinfo_proc p_comm of pid.
func kinfoComm(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("read process name: %w", err)
	}

	comm := kp.Proc.P_comm[:]
	if i := bytes.IndexByte(comm, 0); i >= 0 {
		comm = comm[:i]
	}

	return string(comm), nil
}

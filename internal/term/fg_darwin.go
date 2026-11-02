package term

import (
	"bytes"
	"fmt"

	"golang.org/x/sys/unix"
)

// Foreground is the name of the terminal's foreground process group leader:
// the base name of its argv[0] (of its script for node or bun), else its
// p_comm. p_comm names a symlink's target, and Claude's native install runs as
// a file named after its version.
func (p *Process) Foreground() (string, error) {
	pgrp, err := foregroundPgrp(p.pty)
	if err != nil {
		return "", err
	}

	// Fails for another user's process (sudo) or one that has exited.
	if b, err := unix.SysctlRaw("kern.procargs2", pgrp); err == nil {
		if name, ok := procargsName(b); ok {
			return name, nil
		}
	}

	return kinfoComm(pgrp)
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

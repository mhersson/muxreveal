package term

import (
	"bytes"
	"fmt"

	"golang.org/x/sys/unix"
)

// Foreground is the name of the terminal's foreground process group leader.
func (p *Process) Foreground() (string, error) {
	pgrp, err := foregroundPgrp(p.pty)
	if err != nil {
		return "", err
	}

	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pgrp)
	if err != nil {
		return "", fmt.Errorf("read process name: %w", err)
	}

	comm := kp.Proc.P_comm[:]
	if i := bytes.IndexByte(comm, 0); i >= 0 {
		comm = comm[:i]
	}

	return string(comm), nil
}

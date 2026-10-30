//go:build linux || darwin

package ui

import (
	"os/exec"
	"syscall"
)

// startDetached runs cmd in its own session, without a controlling terminal,
// and with stdin, stdout and stderr on /dev/null.
func startDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	return cmd.Run()
}

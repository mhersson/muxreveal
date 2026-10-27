//go:build unix

package git

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup starts the command in its own process group so a kill
// reaches every child git spawns.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(p *os.Process) error {
	err := syscall.Kill(-p.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}

	return err
}

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

// setSession starts the command in a new session, which has no controlling
// terminal and is its own process group. Setpgid must stay unset: Go runs setsid
// then setpgid, and setpgid on a session leader fails.
func setSession(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func killProcessGroup(p *os.Process) error {
	err := syscall.Kill(-p.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}

	return err
}

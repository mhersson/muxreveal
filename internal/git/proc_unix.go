//go:build unix

package git

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// rekillFor bounds how long killProcessGroup re-sends the kill.
const rekillFor = 100 * time.Millisecond

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

// killProcessGroup kills the group, then re-sends the kill until no live member
// is left: on XNU a child forked during a group kill can miss the signal.
func killProcessGroup(p *os.Process) error {
	err := syscall.Kill(-p.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}

	if err != nil {
		return err
	}

	// kill fails once the group is gone (ESRCH) or, on macOS, holds only zombies (EPERM).
	for deadline := time.Now().Add(rekillFor); time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)

		if syscall.Kill(-p.Pid, syscall.SIGKILL) != nil {
			break
		}
	}

	return nil
}

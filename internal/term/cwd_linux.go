package term

import (
	"fmt"
	"os"
	"strconv"
)

// Cwd is the working directory of the terminal's foreground process group,
// which follows a `cd` in the shell or whatever it is running.
func (p *Process) Cwd() (string, error) {
	pgrp, err := foregroundPgrp(p.pty)
	if err != nil {
		return "", err
	}

	dir, err := os.Readlink("/proc/" + strconv.Itoa(pgrp) + "/cwd")
	if err != nil {
		return "", fmt.Errorf("read cwd: %w", err)
	}

	return dir, nil
}

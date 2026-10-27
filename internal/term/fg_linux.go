package term

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Foreground is the name of the terminal's foreground process group leader.
func (p *Process) Foreground() (string, error) {
	pgrp, err := foregroundPgrp(p.pty)
	if err != nil {
		return "", err
	}

	b, err := os.ReadFile("/proc/" + strconv.Itoa(pgrp) + "/comm")
	if err != nil {
		return "", fmt.Errorf("read comm: %w", err)
	}

	return strings.TrimSpace(string(b)), nil
}

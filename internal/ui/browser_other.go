//go:build !linux && !darwin

package ui

import (
	"errors"
	"os/exec"
)

var errNoBrowser = errors.New("no way to open a browser on this platform")

func browserArgv(string) ([]string, error) { return nil, errNoBrowser }

func startDetached(*exec.Cmd) error { return errNoBrowser }

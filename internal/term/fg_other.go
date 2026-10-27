//go:build !linux && !darwin

package term

import "errors"

// Foreground is not implemented on this platform.
func (p *Process) Foreground() (string, error) { return "", errors.ErrUnsupported }

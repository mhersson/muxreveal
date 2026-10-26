//go:build !linux

package term

import "errors"

// Cwd is not implemented outside Linux.
func (p *Process) Cwd() (string, error) { return "", errors.ErrUnsupported }

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// openLog creates dir if needed and opens a fresh aim.log in it. The
// previous run's log is kept as aim.log.1, replacing any older one.
func openLog(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("log dir: %w", err)
	}

	path := filepath.Join(dir, "aim.log")
	if err := os.Rename(path, path+".1"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("rotate log: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}

	return f, nil
}

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/mhersson/muxreveal/internal/harness"
	"github.com/mhersson/muxreveal/internal/store"
)

// eventBuffer is how many hook events may wait for the update loop.
const eventBuffer = 256

// hookRuntime is what agents need to report through hooks. Settings and Sock
// are empty, and Notice says why, when the setup failed.
type hookRuntime struct {
	Settings, Sock string
	Notice         string
	Events         <-chan harness.Event

	stop func()
}

// Stop ends the socket server and removes this instance's files; it is safe on
// a runtime whose setup failed.
func (h hookRuntime) Stop() {
	if h.stop != nil {
		h.stop()
	}
}

// startHooks prepares <stateDir>/run: it sweeps files of dead instances, writes
// this instance's settings file and serves its socket. A failure leaves nothing
// behind and yields a runtime with a notice, so agents start without hooks.
func startHooks(logger *slog.Logger, stateDir string) hookRuntime {
	h, err := setupHooks(logger, stateDir)
	if err != nil {
		logger.Warn("agent hooks are off", "err", err)

		return hookRuntime{Notice: "agent hooks are off: " + err.Error()}
	}

	return h
}

func setupHooks(logger *slog.Logger, stateDir string) (hookRuntime, error) {
	runDir := filepath.Join(stateDir, "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return hookRuntime{}, fmt.Errorf("run dir: %w", err)
	}

	if err := os.Chmod(runDir, 0o700); err != nil {
		return hookRuntime{}, fmt.Errorf("run dir mode: %w", err)
	}

	sweepRunDir(logger, runDir, pidAlive)

	exe, err := os.Executable()
	if err != nil {
		return hookRuntime{}, fmt.Errorf("executable path: %w", err)
	}

	pid := os.Getpid()
	settingsPath := filepath.Join(runDir, strconv.Itoa(pid)+"-claude.json")
	sockPath := filepath.Join(runDir, strconv.Itoa(pid)+".sock")

	settings, err := harness.Claude{}.Settings(exe)
	if err != nil {
		return hookRuntime{}, err
	}

	if err := store.WriteAtomic(settingsPath, settings); err != nil {
		return hookRuntime{}, fmt.Errorf("settings file: %w", err)
	}

	srv, err := harness.Listen(sockPath)
	if err != nil {
		_ = os.Remove(settingsPath)

		return hookRuntime{}, err
	}

	srv.Log = logger

	events := make(chan harness.Event, eventBuffer)
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	wg.Go(func() {
		if err := srv.Serve(ctx, events); err != nil {
			logger.Error("hook server", "err", err)
		}
	})

	stop := sync.OnceFunc(func() {
		cancel()

		if err := srv.Close(); err != nil {
			logger.Warn("close hook server", "err", err)
		}

		wg.Wait()

		if err := os.Remove(settingsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("remove settings file", "err", err)
		}
	})

	return hookRuntime{Settings: settingsPath, Sock: sockPath, Events: events, stop: stop}, nil
}

// sweepRunDir removes the socket and settings file of instances that are gone.
// Other names, and files of live instances, stay.
func sweepRunDir(logger *slog.Logger, dir string, alive func(pid int) bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Warn("read run dir", "err", err)

		return
	}

	for _, e := range entries {
		pid, ok := runFilePid(e.Name())
		if !ok || alive(pid) {
			continue
		}

		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("remove stale run file", "name", e.Name(), "err", err)
		}
	}
}

// runFilePid extracts the pid from "<pid>.sock" and "<pid>-claude.json".
func runFilePid(name string) (int, bool) {
	for _, suffix := range []string{".sock", "-claude.json"} {
		if digits, ok := strings.CutSuffix(name, suffix); ok {
			pid, err := strconv.Atoi(digits)

			return pid, err == nil && pid > 0 && digits == strconv.Itoa(pid)
		}
	}

	return 0, false
}

// pidAlive reports whether a process with pid exists; a process of another
// user answers EPERM and counts as alive.
func pidAlive(pid int) bool {
	return !errors.Is(unix.Kill(pid, 0), unix.ESRCH)
}

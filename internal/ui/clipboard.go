package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	nativeWriteTimeout = 2 * time.Second
	// nativeWaitDelay bounds how long Wait lingers on I/O after the context
	// ends; wl-copy and xclip fork a child that keeps serving the selection.
	nativeWaitDelay = 100 * time.Millisecond
)

type clipboardConfig struct{ OSC52, Native bool }

// clipboard writes copied text to the system clipboard through OSC 52 and,
// on a local session, through native tools. It also reads the primary
// selection back for a middle-click paste.
type clipboard struct {
	cfg     clipboardConfig
	getenv  func(string) string
	goos    string
	log     *slog.Logger
	timeout time.Duration
	run     func(ctx context.Context, argv []string, stdin string, stdout io.Writer) error

	// last is the latest non-empty text copy was given; only the update loop
	// touches it.
	last string

	mu      sync.Mutex
	missing map[string]bool
}

func newClipboard(cfg clipboardConfig, getenv func(string) string, goos string, log *slog.Logger) *clipboard {
	return &clipboard{
		cfg:     cfg,
		getenv:  getenv,
		goos:    goos,
		log:     log,
		timeout: nativeWriteTimeout,
		run:     runNative,
		missing: map[string]bool{},
	}
}

// copy returns a Cmd that sends text to the clipboard, or nil when there is
// nothing to send.
func (c *clipboard) copy(text string) tea.Cmd {
	if text == "" {
		return nil
	}

	c.last = text

	var cmds []tea.Cmd
	if c.cfg.OSC52 {
		cmds = append(cmds, tea.SetClipboard(text))
		if c.goos == "linux" {
			cmds = append(cmds, tea.SetPrimaryClipboard(text))
		}
	}

	if c.cfg.Native {
		if argvs := nativeCommands(c.getenv, c.goos); len(argvs) > 0 {
			cmds = append(cmds, func() tea.Msg {
				c.writeNative(argvs, text)

				return nil
			})
		}
	}

	return tea.Batch(cmds...)
}

func (c *clipboard) writeNative(argvs [][]string, text string) {
	for _, argv := range argvs {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		err := c.run(ctx, argv, text, nil)

		cancel()

		if err != nil {
			c.logFailure("write", argv, err)
		}
	}
}

// paste returns a Cmd that delivers the text to paste: the primary selection
// when a native tool can read it, else the last copy.
func (c *clipboard) paste(deliver func(text string) tea.Msg) tea.Cmd {
	last := c.last

	var argv []string
	if c.cfg.Native {
		argv = pasteCommand(c.getenv, c.goos)
	}

	if argv == nil {
		return func() tea.Msg { return deliver(last) }
	}

	return func() tea.Msg {
		text, err := c.readNative(argv)
		if err == nil {
			return deliver(text)
		}

		c.logFailure("read", argv, err)

		if errors.Is(err, exec.ErrNotFound) {
			return deliver(last)
		}

		return deliver("")
	}
}

func (c *clipboard) readNative(argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	var buf bytes.Buffer
	if err := c.run(ctx, argv, "", &buf); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (c *clipboard) logFailure(op string, argv []string, err error) {
	if errors.Is(err, exec.ErrNotFound) {
		c.mu.Lock()
		seen := c.missing[argv[0]]
		c.missing[argv[0]] = true
		c.mu.Unlock()

		if seen {
			return
		}
	}

	c.log.Warn("native clipboard "+op+" failed", "cmd", strings.Join(argv, " "), "err", err)
}

// nativeCommands returns the tools to write with, in order; none on an SSH
// session or when no display is known.
func nativeCommands(getenv func(string) string, goos string) [][]string {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return nil
	}

	switch {
	case goos == "darwin":
		return [][]string{{"pbcopy"}}
	case getenv("WAYLAND_DISPLAY") != "":
		return [][]string{{"wl-copy"}, {"wl-copy", "--primary"}}
	case getenv("DISPLAY") != "":
		return [][]string{
			{"xclip", "-selection", "clipboard"},
			{"xclip", "-selection", "primary"},
		}
	}

	return nil
}

// pasteCommand returns the tool to read the primary selection with; nil on an
// SSH session or when no display is known.
func pasteCommand(getenv func(string) string, goos string) []string {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return nil
	}

	switch {
	case goos == "darwin":
		return []string{"pbpaste"}
	case getenv("WAYLAND_DISPLAY") != "":
		return []string{"wl-paste", "--primary", "--no-newline", "--type", "text"}
	case getenv("DISPLAY") != "":
		return []string{"xclip", "-selection", "primary", "-o"}
	}

	return nil
}

// runNative writes pass a nil stdout so a forked selection server cannot hold
// a pipe open past the context; reads pass a buffer, and their tools do not fork.
func runNative(ctx context.Context, argv []string, stdin string, stdout io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = stdout

	cmd.WaitDelay = nativeWaitDelay
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", argv[0], err)
	}

	return nil
}

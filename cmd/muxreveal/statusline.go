package main

import (
	"context"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/harness"
)

// statuslineBudget bounds the whole statusline command: Claude runs it often
// and shows nothing from it, so a slow MuxReveal must never hold it up.
const statuslineBudget = time.Second

// newStatuslineCmd is run by Claude Code as its status line. It always
// succeeds and prints nothing, so Claude's own status line stays empty.
func newStatuslineCmd(getenv func(string) string) *cobra.Command {
	return &cobra.Command{
		Use:    "statusline",
		Short:  "Report an agent's status line update to the running MuxReveal",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), statuslineBudget)
			defer cancel()

			runStatusline(ctx, cmd.InOrStdin(), getenv)

			return nil
		},
	}
}

// runStatusline forwards one statusline payload to the MuxReveal socket. It
// writes nothing to stdout or stderr and drops the update on any failure.
func runStatusline(ctx context.Context, stdin io.Reader, getenv func(string) string) {
	// Read in a goroutine so a stdin that never closes cannot outlast the
	// budget; the process exits right after, which ends the reader.
	type result struct {
		data []byte
		err  error
	}

	read := make(chan result, 1)

	go func() {
		data, err := io.ReadAll(io.LimitReader(stdin, maxHookPayload))
		// Drain the rest so Claude's write never fails with EPIPE.
		_, _ = io.Copy(io.Discard, stdin)

		read <- result{data, err}
	}()

	var payload []byte

	select {
	case r := <-read:
		if r.err != nil {
			return
		}

		payload = r.data
	case <-ctx.Done():
		return
	}

	sock, pane := getenv("MUXREVEAL_SOCK"), getenv("MUXREVEAL_PANE")
	if sock == "" || pane == "" {
		return
	}

	ev, err := harness.Claude{}.ParseStatus(payload)
	if err != nil {
		return
	}

	ev.Pane = core.PaneID(pane)
	_ = harness.Send(ctx, sock, ev)
}

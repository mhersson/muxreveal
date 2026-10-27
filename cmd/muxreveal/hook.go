package main

import (
	"context"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/harness"
)

const (
	// hookBudget bounds the whole hook command so an agent is never held up.
	hookBudget = 2 * time.Second
	// maxHookPayload bounds how much of a hook payload is read from stdin.
	maxHookPayload = 16 << 20
)

// newHookCmd is run by the agent for every hook event. It always succeeds
// quietly: a failing or noisy hook would disturb the agent it reports on.
func newHookCmd(getenv func(string) string) *cobra.Command {
	return &cobra.Command{
		Use:    "hook <harness>",
		Short:  "Report an agent hook event to the running MuxReveal",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), hookBudget)
			defer cancel()

			runHook(ctx, args[0], cmd.InOrStdin(), getenv)

			return nil
		},
	}
}

// runHook forwards one hook payload to the MuxReveal socket. It writes nothing
// to stdout or stderr and drops the event on any failure.
func runHook(ctx context.Context, id string, stdin io.Reader, getenv func(string) string) {
	// Read in a goroutine so a stdin that never closes cannot outlast the
	// budget; the process exits right after, which ends the reader.
	type result struct {
		data []byte
		err  error
	}

	read := make(chan result, 1)

	go func() {
		data, err := io.ReadAll(io.LimitReader(stdin, maxHookPayload))
		// Drain the rest so the agent's write never fails with EPIPE.
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

	h, ok := harness.ByID(id)
	if !ok {
		return
	}

	ev, err := h.ParseEvent(payload)
	if err != nil {
		return
	}

	ev.Pane = core.PaneID(pane)
	_ = harness.Send(ctx, sock, ev)
}

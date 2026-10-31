package ui

import (
	"context"

	"github.com/mhersson/muxreveal/internal/github"
)

// prReads is every GitHub read the pull request views make beyond the poller.
// cmd/muxreveal fills it from its client cache, picking the client by host.
type prReads interface {
	MyPRs(ctx context.Context, host string) (github.MyPRs, error)
	JobLog(ctx context.Context, r github.Repo, jobID int64) (data []byte, cut bool, err error)
	CheckRun(ctx context.Context, r github.Repo, id int64) (github.CheckRunOutput, error)
}

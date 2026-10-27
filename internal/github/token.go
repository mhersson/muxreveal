package github

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/auth"
)

// TokenOptions configure Token.
type TokenOptions struct {
	FromEnvOrConfig func(host string) string // nil = auth.TokenFromEnvOrConfig's token
	GH              string                   // the gh binary; "" = "gh" on PATH
}

// Token returns the token for host from the environment or gh's config, else
// from `gh auth token`. Nothing found is ErrNoToken. gh's output is never
// copied into an error.
func Token(ctx context.Context, host string, o TokenOptions) (string, error) {
	from := o.FromEnvOrConfig
	if from == nil {
		from = func(h string) string {
			t, _ := auth.TokenFromEnvOrConfig(h)

			return t
		}
	}

	if t := from(host); t != "" {
		return t, nil
	}

	noToken := fmt.Errorf("%w for %s: run gh auth login --hostname %s", ErrNoToken, host, host)

	gh := o.GH
	if gh == "" {
		gh = "gh"
	}

	path, err := exec.LookPath(gh)
	if host == "" || err != nil || strings.HasPrefix(host, "-") {
		return "", noToken
	}

	cmd := exec.CommandContext(ctx, path, "auth", "token", "--secure-storage", "--hostname", host)
	// A killed gh can leave a child holding the pipe open.
	cmd.WaitDelay = time.Second

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%w for %s: gh did not answer: %w", ErrNoToken, host, ctx.Err())
		}

		return "", noToken
	}

	if t := strings.TrimSpace(string(out)); t != "" {
		return t, nil
	}

	return "", noToken
}

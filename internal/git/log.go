package git

import (
	"context"
	"fmt"
	"strings"
)

const logLimit = 250

// Commit is one entry of a branch's history.
type Commit struct{ OID, Subject, Body string }

// Log returns the commits in base..HEAD, oldest first. When there are more than
// 250 it keeps the newest 250.
func Log(ctx context.Context, r Repo, base string) ([]Commit, error) {
	if strings.HasPrefix(base, "-") {
		return nil, fmt.Errorf("git log: invalid base %q", base)
	}

	if _, err := run(ctx, call{dir: r.Root, args: []string{"rev-parse", "--verify", "--quiet", base + "^{commit}"}, read: true}); err != nil {
		return nil, fmt.Errorf("resolve base %q: %w", base, err)
	}

	// -n applies before --reverse, so the newest commits are the ones kept.
	out, err := run(ctx, call{
		dir:  r.Root,
		args: []string{"log", "-z", "--reverse", "--format=%H%n%s%n%b", "-n", fmt.Sprint(logLimit), base + "..HEAD", "--"},
		read: true,
	})
	if err != nil {
		return nil, fmt.Errorf("list commits since %q: %w", base, err)
	}

	var commits []Commit

	for rec := range strings.SplitSeq(string(out), "\x00") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}

		oid, rest, _ := strings.Cut(rec, "\n")
		subject, body, _ := strings.Cut(rest, "\n")
		commits = append(commits, Commit{OID: oid, Subject: subject, Body: strings.TrimRight(body, "\n")})
	}

	return commits, nil
}

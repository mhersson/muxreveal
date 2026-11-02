package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// staleAge is how long a session's newest turn must be past for its refs to go.
const staleAge = 14 * 24 * time.Hour

// ErrNotAimRef is returned by DeleteRefs for a name outside refs/aim/ or a ref
// listed without its value.
var ErrNotAimRef = errors.New("git: ref is not under refs/aim/ or has no old value")

var prRefRE = regexp.MustCompile(`^refs/aim/pr/([1-9][0-9]*)/(head|base)$`)

// Ref is a ref name and the object it pointed at when it was listed.
type Ref struct{ Name, OID string }

type turnEntry struct {
	session string
	n       int
	ref     Ref
}

type prEntry struct {
	number int
	ref    Ref
}

// AimRefs is the turn and pull request refs under refs/aim/, as listed.
type AimRefs struct {
	turns []turnEntry
	prs   []prEntry
}

// Sweep is the refs Stale picked.
type Sweep struct{ Turns, PRs []Ref }

// ListAimRefs reads the names and values of refs/aim/ in one call, without
// reading any commit. Names AIM does not own are dropped.
func ListAimRefs(ctx context.Context, r Repo) (AimRefs, error) {
	out, err := run(ctx, call{
		dir:  r.CommonDir,
		args: []string{"for-each-ref", "--format=%(refname)%00%(objectname)", refPrefix},
		read: true,
	})
	if err != nil {
		return AimRefs{}, fmt.Errorf("list aim refs: %w", err)
	}

	var l AimRefs

	for line := range strings.SplitSeq(string(out), "\n") {
		name, oid, ok := strings.Cut(line, "\x00")
		if !ok {
			continue
		}

		ref := Ref{Name: name, OID: oid}

		if rec, ok := parseTurnRef(name); ok {
			l.turns = append(l.turns, turnEntry{session: rec.session, n: rec.N, ref: ref})

			continue
		}

		if m := prRefRE.FindStringSubmatch(name); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				l.prs = append(l.prs, prEntry{number: n, ref: ref})
			}
		}
	}

	return l, nil
}

// Stale picks the turn refs of sessions that are not held and whose newest turn
// in the repository, across worktrees and both layouts, is over staleAge old,
// and the pull request refs that are not shown. held gets the session as the
// ref spells it, in lower case.
func (l AimRefs) Stale(now time.Time, held func(session string) bool, shown func(number int) bool) Sweep {
	newest := make(map[string]time.Time)

	for _, t := range l.turns {
		at := time.UnixMilli(int64(t.n))
		if at.After(newest[t.session]) {
			newest[t.session] = at
		}
	}

	var s Sweep

	for _, t := range l.turns {
		if !held(t.session) && now.Sub(newest[t.session]) > staleAge {
			s.Turns = append(s.Turns, t.ref)
		}
	}

	for _, p := range l.prs {
		if !shown(p.number) {
			s.PRs = append(s.PRs, p.ref)
		}
	}

	byName := func(a, b Ref) int { return strings.Compare(a.Name, b.Name) }
	slices.SortFunc(s.Turns, byName)
	slices.SortFunc(s.PRs, byName)

	return s
}

// DeleteRefs removes refs in one transaction, each only while it still holds
// the listed value. It refuses, and deletes nothing, when a name is outside
// refs/aim/ or a value is empty.
func DeleteRefs(ctx context.Context, r Repo, refs []Ref) (int, error) {
	if len(refs) == 0 {
		return 0, nil
	}

	var stdin bytes.Buffer

	for _, ref := range refs {
		if !strings.HasPrefix(ref.Name, refPrefix) || ref.OID == "" {
			return 0, fmt.Errorf("delete %q: %w", ref.Name, ErrNotAimRef)
		}

		fmt.Fprintf(&stdin, "delete %s %s\n", ref.Name, ref.OID)
	}

	wctx, cancel, err := refWriteContext(ctx)
	if err != nil {
		return 0, err
	}
	defer cancel()

	if _, err := run(wctx, call{dir: r.CommonDir, args: []string{"update-ref", "--stdin"}, env: isolatedEnv(), stdin: &stdin}); err != nil {
		return 0, fmt.Errorf("delete aim refs: %w", err)
	}

	return len(refs), nil
}

package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	refPrefix      = "refs/muxreveal/"
	maxSkippedTags = 20

	// refWriteBudget bounds writing a ref and its commit once the tree exists;
	// a killed update-ref leaves its lock file behind.
	refWriteBudget = 2 * time.Second
)

// ErrBadSession means a session id is not a UUID.
var ErrBadSession = errors.New("git: session id is not a UUID")

var (
	sessionRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	turnRefRE = regexp.MustCompile(`^refs/muxreveal/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/([0-9]+)$`)
)

// refWriteContext returns the context for a ref write: the caller's error when
// it is already done, else one that survives its cancellation for refWriteBudget.
func refWriteContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), refWriteBudget)

	return c, cancel, nil
}

// SnapshotOptions tune one snapshot.
type SnapshotOptions struct {
	TempDir      string // where the temporary index is created
	IndexCache   string // keeps a refreshed index copy per worktree; empty = none
	MaxUntracked int64  // bytes; 0 = 50 MiB, negative = no cap
	Unchanged    bool   // reuse the previous turn's tree without staging
}

// Turn is one recorded turn of a session.
type Turn struct {
	N       int
	Commit  string
	Tree    string
	Missing bool // a "no snapshot" marker
	Skipped []string
	Time    time.Time // committer time
}

// Snapshot records the working tree of the worktree containing dir as the
// session's next turn under refs/muxreveal/<session>/<n>. The user's index,
// refs, stash and files are only read.
func Snapshot(ctx context.Context, dir, session string, opts SnapshotOptions) (Turn, error) {
	session, err := normalizeSession(session)
	if err != nil {
		return Turn{}, err
	}

	r, err := Open(ctx, dir)
	if err != nil {
		return Turn{}, err
	}

	turns, err := Turns(ctx, r, session)
	if err != nil {
		return Turn{}, err
	}

	var wt WorkTree

	reused := false

	if opts.Unchanged {
		for _, t := range slices.Backward(turns) {
			if !t.Missing {
				wt = WorkTree{Tree: t.Tree, Skipped: t.Skipped}
				reused = true

				break
			}
		}
	}

	if !reused {
		wt, err = WorkingTree(ctx, r, TreeOptions{TempDir: opts.TempDir, IndexCache: opts.IndexCache, MaxUntracked: opts.MaxUntracked})
		if err != nil {
			return Turn{}, fmt.Errorf("snapshot %s: %w", r.Root, err)
		}
	}

	return writeTurn(ctx, r, session, turns, wt.Tree, wt.Skipped, false)
}

// MarkMissing records that a turn has no snapshot: a commit of the empty tree
// with the trailer "Snapshot: none", so later turns keep their numbers.
func MarkMissing(ctx context.Context, dir, session string) (Turn, error) {
	session, err := normalizeSession(session)
	if err != nil {
		return Turn{}, err
	}

	r, err := Open(ctx, dir)
	if err != nil {
		return Turn{}, err
	}

	turns, err := Turns(ctx, r, session)
	if err != nil {
		return Turn{}, err
	}

	wctx, cancel, err := refWriteContext(ctx)
	if err != nil {
		return Turn{}, err
	}
	defer cancel()

	// mktree writes the empty tree in whatever hash the repository uses.
	out, err := run(wctx, call{dir: r.Root, args: []string{"mktree"}, stdin: bytes.NewReader(nil)})
	if err != nil {
		return Turn{}, fmt.Errorf("mark missing: %w", err)
	}

	return writeTurn(ctx, r, session, turns, strings.TrimSpace(string(out)), nil, true)
}

// writeTurn commits tree as the next turn and creates its ref. A ref that
// already exists (another writer took the number) is retried once.
func writeTurn(ctx context.Context, r Repo, session string, turns []Turn, tree string, skipped []string, none bool) (Turn, error) {
	parent, err := headCommit(ctx, r.Root)
	if err != nil && !errors.Is(err, errUnborn) {
		return Turn{}, err
	}

	for attempt := 0; ; attempt++ {
		wctx, cancel, err := refWriteContext(ctx)
		if err != nil {
			return Turn{}, err
		}

		n := nextTurn(turns)

		turn, err := writeRef(wctx, r, session, n, tree, parent, skipped, none)

		cancel()

		if err == nil {
			return turn, nil
		}

		if attempt > 0 || ctx.Err() != nil {
			return Turn{}, err
		}

		updateErr := err

		turns, err = Turns(ctx, r, session)
		if err != nil {
			return Turn{}, fmt.Errorf("%w (then %w)", updateErr, err)
		}

		if nextTurn(turns) == n {
			return Turn{}, updateErr
		}
	}
}

// writeRef commits tree as turn n and creates its ref.
func writeRef(ctx context.Context, r Repo, session string, n int, tree, parent string, skipped []string, none bool) (Turn, error) {
	now := time.Now().Truncate(time.Second)

	commit, err := commitTurn(ctx, r, session, n, tree, parent, skipped, none, now)
	if err != nil {
		return Turn{}, err
	}

	ref := fmt.Sprintf("%s%s/%d", refPrefix, session, n)

	_, err = run(ctx, call{
		dir:  r.Root,
		args: []string{"update-ref", "--no-deref", "-m", "muxreveal turn", ref, commit, ""},
		env:  isolatedEnv(),
	})
	if err != nil {
		return Turn{}, fmt.Errorf("write %s: %w", ref, err)
	}

	return Turn{N: n, Commit: commit, Tree: tree, Missing: none, Skipped: skipped, Time: now}, nil
}

func commitTurn(ctx context.Context, r Repo, session string, n int, tree, parent string, skipped []string, none bool, at time.Time) (string, error) {
	var msg strings.Builder

	fmt.Fprintf(&msg, "muxreveal turn %d\n\nSession: %s\nWorktree: %s\n", n, session, quoteValue(r.Root))

	if none {
		msg.WriteString("Snapshot: none\n")
	}

	if len(skipped) > 0 {
		fmt.Fprintf(&msg, "Skipped-Count: %d\n", len(skipped))

		for _, p := range skipped[:min(len(skipped), maxSkippedTags)] {
			fmt.Fprintf(&msg, "Skipped: %s\n", quoteValue(p))
		}
	}

	date := strconv.FormatInt(at.Unix(), 10) + " +0000"
	env := append(isolatedEnv(),
		"GIT_AUTHOR_NAME=MuxReveal", "GIT_COMMITTER_NAME=MuxReveal",
		"GIT_AUTHOR_EMAIL=muxreveal@localhost", "GIT_COMMITTER_EMAIL=muxreveal@localhost",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date,
	)

	args := []string{"commit-tree", "--no-gpg-sign", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}

	out, err := run(ctx, call{dir: r.Root, args: args, env: env, stdin: strings.NewReader(msg.String())})
	if err != nil {
		return "", fmt.Errorf("commit turn %d: %w", n, err)
	}

	return strings.TrimSpace(string(out)), nil
}

func nextTurn(turns []Turn) int {
	if len(turns) == 0 {
		return 1
	}

	return turns[len(turns)-1].N + 1
}

func normalizeSession(session string) (string, error) {
	if !sessionRE.MatchString(session) {
		return "", fmt.Errorf("session %q: %w", session, ErrBadSession)
	}

	return strings.ToLower(session), nil
}

// quoteValue makes s safe as a one-line trailer value: Go-quoted when it has
// characters a trailer would mangle (newlines, edge spaces, quotes, bad UTF-8).
func quoteValue(s string) string {
	q := strconv.Quote(s)
	if s != "" && q == `"`+s+`"` && strings.TrimSpace(s) == s {
		return s
	}

	return q
}

func unquoteValue(s string) string {
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}

	return s
}

const turnFormat = "%(refname)%00%(objectname)%00%(tree)%00%(committerdate:unix)%00" +
	"%(trailers:key=Snapshot,valueonly,separator=%x1f)%00" +
	"%(trailers:key=Skipped,valueonly,separator=%x1f)%00" +
	"%(trailers:key=Worktree,valueonly,separator=%x1f)"

type turnRecord struct {
	Turn

	session  string
	worktree string
}

// listRefs reads every turn ref under pattern in one for-each-ref call.
func listRefs(ctx context.Context, r Repo, pattern string) ([]turnRecord, error) {
	out, err := run(ctx, call{
		dir:  r.CommonDir,
		args: []string{"for-each-ref", "--format=" + turnFormat, pattern},
		read: true,
	})
	if err != nil {
		return nil, fmt.Errorf("list turns: %w", err)
	}

	var recs []turnRecord

	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 7 {
			continue
		}

		m := turnRefRE.FindStringSubmatch(f[0])
		if m == nil {
			continue
		}

		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}

		secs, _ := strconv.ParseInt(f[3], 10, 64)
		rec := turnRecord{
			Turn: Turn{
				N: n, Commit: f[1], Tree: f[2], Time: time.Unix(secs, 0),
				Missing: strings.TrimSpace(f[4]) == "none",
			},
			session:  m[1],
			worktree: unquoteValue(strings.TrimSpace(f[6])),
		}

		if f[5] != "" {
			for p := range strings.SplitSeq(f[5], "\x1f") {
				rec.Skipped = append(rec.Skipped, unquoteValue(p))
			}
		}

		recs = append(recs, rec)
	}

	return recs, nil
}

// Turns lists the session's turns in ascending order.
func Turns(ctx context.Context, r Repo, session string) ([]Turn, error) {
	session, err := normalizeSession(session)
	if err != nil {
		return nil, err
	}

	recs, err := listRefs(ctx, r, refPrefix+session+"/")
	if err != nil {
		return nil, err
	}

	slices.SortFunc(recs, func(a, b turnRecord) int { return a.N - b.N })

	turns := make([]Turn, len(recs))
	for i, rec := range recs {
		turns[i] = rec.Turn
	}

	return turns, nil
}

// PruneWorktree deletes every turn ref whose commit names worktree and returns
// how many it removed. worktree is a worktree root as Repo.Root gives it, the
// form the commits record; a symlinked spelling does not match.
func PruneWorktree(ctx context.Context, r Repo, worktree string) (int, error) {
	recs, err := listRefs(ctx, r, refPrefix)
	if err != nil {
		return 0, err
	}

	want := filepath.Clean(worktree)

	return deleteRefs(ctx, r, recs, func(rec turnRecord) bool { return filepath.Clean(rec.worktree) == want })
}

// PruneSession deletes every turn ref of the session and returns how many.
func PruneSession(ctx context.Context, r Repo, session string) (int, error) {
	session, err := normalizeSession(session)
	if err != nil {
		return 0, err
	}

	recs, err := listRefs(ctx, r, refPrefix+session+"/")
	if err != nil {
		return 0, err
	}

	return deleteRefs(ctx, r, recs, func(turnRecord) bool { return true })
}

// deleteRefs removes the matching refs in one transaction, each only if it
// still points at the commit that was read.
func deleteRefs(ctx context.Context, r Repo, recs []turnRecord, match func(turnRecord) bool) (int, error) {
	var stdin bytes.Buffer

	count := 0

	for _, rec := range recs {
		if match(rec) {
			fmt.Fprintf(&stdin, "delete %s%s/%d %s\n", refPrefix, rec.session, rec.N, rec.Commit)

			count++
		}
	}

	if count == 0 {
		return 0, nil
	}

	wctx, cancel, err := refWriteContext(ctx)
	if err != nil {
		return 0, err
	}
	defer cancel()

	_, err = run(wctx, call{dir: r.CommonDir, args: []string{"update-ref", "--stdin"}, env: isolatedEnv(), stdin: &stdin})
	if err != nil {
		return 0, fmt.Errorf("prune turns: %w", err)
	}

	return count, nil
}

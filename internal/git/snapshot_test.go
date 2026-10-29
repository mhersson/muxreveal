package git

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	sessA = "11111111-2222-4333-8444-555555555555"
	sessB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

// seedRepo makes a repository with one commit holding a.txt, b.txt and
// dir/c.txt.
func seedRepo(t *testing.T) string {
	t.Helper()

	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	writeFile(t, dir, "b.txt", "b\n")
	writeFile(t, dir, "dir/c.txt", "c\n")
	commitAll(t, dir, "init")

	return dir
}

func openRepo(t *testing.T, dir string) Repo {
	t.Helper()

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	return r
}

// snap takes a snapshot and fails when it leaves anything in its temp dir.
func snap(t *testing.T, dir, session string, opts SnapshotOptions) Turn {
	t.Helper()

	tmp := t.TempDir()
	opts.TempDir = tmp

	turn, err := Snapshot(context.Background(), dir, session, opts)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	assertEmptyDir(t, tmp)

	return turn
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		t.Errorf("temporary file left behind: %s", e.Name())
	}
}

// gitEnvOut runs git with extra environment and returns trimmed stdout.
func gitEnvOut(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(), env...)

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}

	return strings.TrimSpace(string(out))
}

// scratchTree builds the tree of the files on disk the plain way: an empty
// index and `git add -A`.
func scratchTree(t *testing.T, dir string) string {
	t.Helper()

	env := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "scratch-index")}
	gitEnvOut(t, dir, env, "add", "-A")

	return gitEnvOut(t, dir, env, "write-tree")
}

// treeEntries lists a tree as path -> "mode sha".
func treeEntries(t *testing.T, dir, tree string) map[string]string {
	t.Helper()

	cmd := exec.Command("git", "ls-tree", "-r", "-z", tree)
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ls-tree: %v", err)
	}

	entries := map[string]string{}

	for rec := range bytes.SplitSeq(out, []byte{0}) {
		meta, path, ok := strings.Cut(string(rec), "\t")
		if !ok {
			continue
		}

		f := strings.Fields(meta)
		entries[path] = f[0] + " " + f[2]
	}

	return entries
}

func paths(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}

	sort.Strings(out)

	return out
}

func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()

	for _, k := range keys {
		t.Setenv(k, "")

		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := seedRepo(t)
	ctx := context.Background()

	var turns []Turn

	writeFile(t, dir, "a.txt", "a1\n")
	turns = append(turns, snap(t, dir, sessA, SnapshotOptions{}))
	want1 := scratchTree(t, dir)

	writeFile(t, dir, "new.txt", "n\n")
	turns = append(turns, snap(t, dir, sessA, SnapshotOptions{}))
	want2 := scratchTree(t, dir)

	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}

	turns = append(turns, snap(t, dir, sessA, SnapshotOptions{}))
	want3 := scratchTree(t, dir)

	for i, want := range []string{want1, want2, want3} {
		if turns[i].N != i+1 || turns[i].Tree != want {
			t.Errorf("turn %d = %+v, want tree %s", i+1, turns[i], want)
		}
	}

	got, err := Turns(ctx, openRepo(t, dir), sessA)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("Turns = %+v, want 3", got)
	}

	head := gitOut(t, dir, "rev-parse", "HEAD")

	for i, g := range got {
		if g.N != i+1 || g.Commit != turns[i].Commit || g.Tree != turns[i].Tree || !g.Time.Equal(turns[i].Time) {
			t.Errorf("Turns[%d] = %+v, want %+v", i, g, turns[i])
		}

		if p := gitOut(t, dir, "rev-parse", g.Commit+"^"); p != head {
			t.Errorf("turn %d parent = %s, want HEAD %s", g.N, p, head)
		}

		if d := time.Since(g.Time); d < 0 || d > time.Minute {
			t.Errorf("turn %d time %v is not recent", g.N, g.Time)
		}
	}

	msg := gitOut(t, dir, "log", "-1", "--format=%B", turns[0].Commit)
	for _, line := range []string{"muxreveal turn 1", "Session: " + sessA, "Worktree: " + dir} {
		if !strings.Contains(msg, line) {
			t.Errorf("commit message lacks %q:\n%s", line, msg)
		}
	}

	if who := gitOut(t, dir, "log", "-1", "--format=%an <%ae> %cn <%ce>", turns[0].Commit); who !=
		"MuxReveal <muxreveal@localhost> MuxReveal <muxreveal@localhost>" {
		t.Errorf("identity = %q", who)
	}
}

func TestTurnsSortNumerically(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)

	for range 11 {
		snap(t, dir, sessA, SnapshotOptions{})
	}

	turns, err := Turns(context.Background(), openRepo(t, dir), sessA)
	if err != nil {
		t.Fatal(err)
	}

	for i, tn := range turns {
		if tn.N != i+1 {
			t.Fatalf("turn at index %d is %d; turns = %v", i, tn.N, turns)
		}
	}

	if len(turns) != 11 {
		t.Fatalf("got %d turns, want 11", len(turns))
	}
}

func TestSnapshotTreeContent(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	writeFile(t, dir, ".gitignore", "ignored.log\nbuild/\n")
	writeFile(t, dir, "mod.txt", "old\n")
	writeFile(t, dir, "gone.txt", "bye\n")
	writeFile(t, dir, "bin.dat", "\x00\x01\x02")
	commitAll(t, dir, "more")

	writeFile(t, dir, "mod.txt", "new\n")
	writeFile(t, dir, "bin.dat", "\x00\x01\x02\x03\xff")
	writeFile(t, dir, "crlf.txt", "one\r\ntwo\r\n")
	writeFile(t, dir, "untracked.txt", "u\n")
	writeFile(t, dir, "staged.txt", "s\n")
	gitOut(t, dir, "add", "staged.txt")
	writeFile(t, dir, "ignored.log", "x\n")
	writeFile(t, dir, "build/out.o", "x\n")
	writeFile(t, dir, "with space/file name.txt", "x\n")
	writeFile(t, dir, "new\nline.txt", "x\n")

	invalid := "bad\xffname.txt"
	invalidOK := fsAcceptsName(t, invalid)

	if invalidOK {
		writeFile(t, dir, invalid, "x\n")
	}

	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	// A nested repository that is not a submodule is left out.
	writeFile(t, dir, "nested/f.txt", "x\n")
	gitOut(t, filepath.Join(dir, "nested"), "init", "-q", "-b", "main")
	gitOut(t, filepath.Join(dir, "nested"), "add", "-A")
	gitOut(t, filepath.Join(dir, "nested"), "commit", "-q", "-m", "n")

	turn := snap(t, dir, sessA, SnapshotOptions{})
	got := treeEntries(t, dir, turn.Tree)

	wantPaths := []string{
		"a.txt", "b.txt", "dir/c.txt", "mod.txt", "bin.dat", "crlf.txt", "untracked.txt",
		"staged.txt", "link", "with space/file name.txt", "new\nline.txt", ".gitignore",
	}
	if invalidOK {
		wantPaths = append(wantPaths, invalid)
	}

	for _, p := range wantPaths {
		if _, ok := got[p]; !ok {
			t.Errorf("tree lacks %q; has %q", p, paths(got))
		}
	}

	for _, p := range []string{"gone.txt", "ignored.log", "build/out.o", "nested", "nested/f.txt"} {
		if _, ok := got[p]; ok {
			t.Errorf("tree has %q", p)
		}
	}

	if !strings.HasPrefix(got["link"], "120000 ") {
		t.Errorf("link = %q, want a symlink", got["link"])
	}

	for _, w := range []string{"mod.txt", "bin.dat", "crlf.txt"} {
		want := gitOut(t, dir, "hash-object", w)
		if !strings.HasSuffix(got[w], " "+want) {
			t.Errorf("%s = %q, want blob %s", w, got[w], want)
		}
	}
}

func TestSnapshotSubmoduleStaysAtRecordedCommit(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)

	sub := newRepo(t)
	writeFile(t, sub, "s.txt", "s\n")
	commitAll(t, sub, "sub")

	gitOut(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "sm")
	gitOut(t, dir, "commit", "-q", "-m", "add submodule")

	recorded := gitOut(t, dir, "rev-parse", "HEAD:sm")

	// The submodule moves on; the superproject has not recorded it.
	writeFile(t, filepath.Join(dir, "sm"), "s.txt", "moved\n")
	gitOut(t, filepath.Join(dir, "sm"), "commit", "-q", "-am", "moved")
	writeFile(t, dir, "x.txt", "x\n")

	got := treeEntries(t, dir, snap(t, dir, sessA, SnapshotOptions{}).Tree)

	if got["sm"] != "160000 "+recorded {
		t.Errorf("sm = %q, want gitlink at %s", got["sm"], recorded)
	}

	if _, ok := got["x.txt"]; !ok {
		t.Error("untracked file missing")
	}
}

func TestSnapshotUntrackedSizeCap(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)

	big := filepath.Join(dir, "big\nfile.bin")

	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.Truncate(60 << 20); err != nil {
		t.Fatal(err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "small.txt", "s\n")

	capped := snap(t, dir, sessA, SnapshotOptions{})
	if want := []string{"big\nfile.bin"}; !slices.Equal(capped.Skipped, want) {
		t.Errorf("Skipped = %q, want %q", capped.Skipped, want)
	}

	entries := treeEntries(t, dir, capped.Tree)
	if _, ok := entries["big\nfile.bin"]; ok {
		t.Error("big file in a capped tree")
	}

	if _, ok := entries["small.txt"]; !ok {
		t.Error("small file missing")
	}

	msg := gitOut(t, dir, "log", "-1", "--format=%B", capped.Commit)
	if !strings.Contains(msg, "Skipped-Count: 1") || !strings.Contains(msg, `Skipped: "big\nfile.bin"`) {
		t.Errorf("trailers do not name the skipped file:\n%s", msg)
	}

	turns, err := Turns(context.Background(), openRepo(t, dir), sessA)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(turns[0].Skipped, capped.Skipped) {
		t.Errorf("Turns Skipped = %q, want %q", turns[0].Skipped, capped.Skipped)
	}

	all := snap(t, dir, sessA, SnapshotOptions{MaxUntracked: -1})
	if len(all.Skipped) != 0 {
		t.Errorf("Skipped = %q with no cap", all.Skipped)
	}

	if _, ok := treeEntries(t, dir, all.Tree)["big\nfile.bin"]; !ok {
		t.Error("big file missing with no cap")
	}
}

// userState captures everything a snapshot must leave alone.
type userState struct {
	index      []byte
	indexMtime time.Time
	stash      string
	refs       string
	head       string
	status     string
	mtimes     map[string]time.Time
	gitDirTop  []string
}

func readOrEmpty(path string) string {
	b, _ := os.ReadFile(path)

	return string(b)
}

func captureState(t *testing.T, dir string) userState {
	t.Helper()

	gitDir := filepath.Join(dir, ".git")
	s := userState{
		stash:  gitOut(t, dir, "for-each-ref", "--format=%(objectname)", "refs/stash") + readOrEmpty(filepath.Join(gitDir, "logs", "refs", "stash")),
		refs:   gitOut(t, dir, "for-each-ref", "refs/heads", "refs/tags"),
		head:   gitOut(t, dir, "symbolic-ref", "-q", "HEAD"),
		status: gitEnvOut(t, dir, []string{"GIT_OPTIONAL_LOCKS=0"}, "status", "--porcelain=v2", "--ignored"),
		mtimes: map[string]time.Time{},
	}

	var err error

	if s.index, err = os.ReadFile(filepath.Join(gitDir, "index")); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(gitDir, "index"))
	if err != nil {
		t.Fatal(err)
	}

	s.indexMtime = info.ModTime()

	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(dir, p)

		if rel == ".git" {
			return filepath.SkipDir
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		s.mtimes[rel] = info.ModTime()

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = filepath.WalkDir(gitDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(gitDir, p)
		if d.IsDir() && (rel == "objects" || rel == "refs") {
			return filepath.SkipDir
		}

		s.gitDirTop = append(s.gitDirTop, rel)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func assertSameState(t *testing.T, before, after userState) {
	t.Helper()

	if !bytes.Equal(before.index, after.index) {
		t.Error("index bytes changed")
	}

	if !before.indexMtime.Equal(after.indexMtime) {
		t.Errorf("index mtime changed: %v -> %v", before.indexMtime, after.indexMtime)
	}

	for name, want := range map[string][2]string{
		"stash":  {before.stash, after.stash},
		"refs":   {before.refs, after.refs},
		"HEAD":   {before.head, after.head},
		"status": {before.status, after.status},
	} {
		if want[0] != want[1] {
			t.Errorf("%s changed:\n%s\n---\n%s", name, want[0], want[1])
		}
	}

	for p, mt := range before.mtimes {
		if got, ok := after.mtimes[p]; !ok || !got.Equal(mt) {
			t.Errorf("mtime of %q changed: %v -> %v", p, mt, got)
		}
	}

	if len(after.mtimes) != len(before.mtimes) {
		t.Errorf("worktree entries changed: %d -> %d", len(before.mtimes), len(after.mtimes))
	}

	if !slices.Equal(before.gitDirTop, after.gitDirTop) {
		t.Errorf("git dir files changed:\n%q\n%q", before.gitDirTop, after.gitDirTop)
	}
}

func TestSnapshotLeavesUserStateAlone(t *testing.T) {
	dir := seedRepo(t)

	// A stash entry seeded by hand, so no test runs git stash.
	stashed := gitOut(t, dir, "commit-tree", "-p", "HEAD", "-m", "mine", "HEAD^{tree}")
	gitOut(t, dir, "update-ref", "--create-reflog", "-m", "mine", "refs/stash", stashed)
	marks := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "hooks")

	hookScript := "#!/bin/sh\necho \"$0\" >> '" + filepath.Join(marks, "mark") + "'\nexit 1\n"
	for _, h := range []string{"pre-commit", "post-index-change", "reference-transaction", "post-commit", "commit-msg"} {
		for _, d := range []string{hooks, filepath.Join(dir, ".git", "hooks")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(d, h), []byte(hookScript), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	fsmon := filepath.Join(t.TempDir(), "fsmon")
	if err := os.WriteFile(fsmon, []byte("#!/bin/sh\necho fsmonitor >> '"+filepath.Join(marks, "mark")+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for k, v := range map[string]string{
		"commit.gpgsign":     "true",
		"gpg.program":        "/nonexistent/gpg",
		"core.hooksPath":     hooks,
		"core.fsmonitor":     fsmon,
		"user.useConfigOnly": "true",
	} {
		gitOut(t, dir, "config", k, v)
	}

	// Staged, intent-to-add, assume-unchanged and skip-worktree entries.
	writeFile(t, dir, "staged.txt", "s\n")
	gitOut(t, dir, "add", "staged.txt")
	writeFile(t, dir, "intent.txt", "i\n")
	gitOut(t, dir, "add", "-N", "intent.txt")
	gitOut(t, dir, "update-index", "--assume-unchanged", "a.txt")
	gitOut(t, dir, "update-index", "--skip-worktree", "b.txt")
	writeFile(t, dir, "a.txt", "assumed\n")
	writeFile(t, dir, "dir/c.txt", "modified\n")
	writeFile(t, dir, "untracked.txt", "u\n")

	unsetEnv(t, "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
		"EMAIL", "GIT_CONFIG_GLOBAL")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("HOME", t.TempDir())

	// Let the clock move so a rewritten file would show a new mtime.
	time.Sleep(20 * time.Millisecond)

	before := captureState(t, dir)
	_ = os.Remove(filepath.Join(marks, "mark"))
	turn := snap(t, dir, sessA, SnapshotOptions{})
	mark := snap(t, dir, sessA, SnapshotOptions{Unchanged: true})

	missing, err := MarkMissing(context.Background(), dir, sessA)
	if err != nil {
		t.Fatal(err)
	}

	if b, err := os.ReadFile(filepath.Join(marks, "mark")); err == nil {
		t.Errorf("a hook or the fsmonitor ran:\n%s", b)
	}

	assertSameState(t, before, captureState(t, dir))

	if turn.N != 1 || mark.N != 2 || missing.N != 3 {
		t.Errorf("turns = %d, %d, %d", turn.N, mark.N, missing.N)
	}

	for _, c := range []string{turn.Commit, missing.Commit} {
		if raw := gitOut(t, dir, "cat-file", "commit", c); strings.Contains(raw, "gpgsig") {
			t.Errorf("commit %s is signed", c)
		}
	}

	got := treeEntries(t, dir, turn.Tree)
	if _, ok := got["untracked.txt"]; !ok {
		t.Error("untracked file missing")
	}
}

func TestSnapshotUnbornHead(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, "f.txt", "f\n")
	gitOut(t, dir, "add", "f.txt")
	writeFile(t, dir, "g.txt", "g\n")

	turn := snap(t, dir, sessA, SnapshotOptions{})

	if got := gitOut(t, dir, "rev-list", "--parents", "-n1", turn.Commit); got != turn.Commit {
		t.Errorf("rev-list --parents = %q, want a commit without parents", got)
	}

	if want := scratchTree(t, dir); turn.Tree != want {
		t.Errorf("tree = %s, want %s", turn.Tree, want)
	}

	missing, err := MarkMissing(context.Background(), dir, sessB)
	if err != nil {
		t.Fatal(err)
	}

	if got := gitOut(t, dir, "rev-list", "--parents", "-n1", missing.Commit); got != missing.Commit {
		t.Errorf("marker has parents: %q", got)
	}
}

func TestSnapshotDetachedHead(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	gitOut(t, dir, "checkout", "-q", "--detach")
	writeFile(t, dir, "a.txt", "changed\n")

	turn := snap(t, dir, sessA, SnapshotOptions{})

	if got := gitOut(t, dir, "rev-parse", turn.Commit+"^"); got != gitOut(t, dir, "rev-parse", "HEAD") {
		t.Errorf("parent = %s", got)
	}

	if want := scratchTree(t, dir); turn.Tree != want {
		t.Errorf("tree = %s, want %s", turn.Tree, want)
	}
}

func TestSnapshotLinkedWorktree(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	wt := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-linked")
	gitOut(t, dir, "worktree", "add", "-q", "-b", "feat", wt)

	wt, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, wt, "only-here.txt", "x\n")
	writeFile(t, wt, "a.txt", "linked edit\n")

	turn := snap(t, wt, sessA, SnapshotOptions{})

	if want := scratchTree(t, wt); turn.Tree != want {
		t.Errorf("tree = %s, want %s", turn.Tree, want)
	}

	if got := gitOut(t, dir, "for-each-ref", "--format=%(refname)", "refs/muxreveal"); got != "refs/muxreveal/"+sessA+"/1" {
		t.Errorf("refs seen from the main checkout = %q", got)
	}

	if msg := gitOut(t, wt, "log", "-1", "--format=%B", turn.Commit); !strings.Contains(msg, "Worktree: "+wt) {
		t.Errorf("trailer does not name the linked worktree:\n%s", msg)
	}
}

func TestSnapshotLinkedWorktreeOfBareRepo(t *testing.T) {
	t.Parallel()
	src := seedRepo(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitOut(t, src, "clone", "-q", "--bare", src, bare)

	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, bare, "worktree", "add", "-q", "-b", "work", wt)

	wt, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, wt, "new.txt", "n\n")

	turn := snap(t, wt, sessA, SnapshotOptions{})

	if want := scratchTree(t, wt); turn.Tree != want {
		t.Errorf("tree = %s, want %s", turn.Tree, want)
	}

	if got := gitOut(t, bare, "for-each-ref", "--format=%(refname)", "refs/muxreveal"); got != "refs/muxreveal/"+sessA+"/1" {
		t.Errorf("refs in the bare repository = %q", got)
	}
}

func TestSnapshotSplitIndex(t *testing.T) {
	dir := seedRepo(t)
	gitOut(t, dir, "config", "core.splitIndex", "true")
	gitOut(t, dir, "update-index", "--split-index")
	writeFile(t, dir, "dir/c.txt", "touched\n")
	gitOut(t, dir, "add", "dir/c.txt") // a second index write leaves a delta over the shared file
	writeFile(t, dir, "a.txt", "edited\n")
	writeFile(t, dir, "u.txt", "u\n")

	shared, _ := filepath.Glob(filepath.Join(dir, ".git", "sharedindex.*"))
	if len(shared) == 0 {
		t.Fatal("setup did not produce a split index")
	}

	before := captureState(t, dir)
	logPath := fakeGit(t, "")
	turn := snap(t, dir, sessA, SnapshotOptions{})
	assertSameState(t, before, captureState(t, dir))

	if want := scratchTree(t, dir); turn.Tree != want {
		t.Errorf("tree = %s, want %s", turn.Tree, want)
	}

	if calls, _ := os.ReadFile(logPath); !strings.Contains(string(calls), "read-tree HEAD") {
		t.Errorf("a split index did not fall back to HEAD:\n%s", calls)
	}
}

func TestSnapshotUnusableIndex(t *testing.T) {
	t.Parallel()

	for name, content := range map[string][]byte{
		"garbage": []byte("this is not an index file at all, not even close"),
		"empty":   nil,
		"missing": nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := seedRepo(t)
			writeFile(t, dir, "u.txt", "u\n")

			indexPath := filepath.Join(dir, ".git", "index")
			if name == "missing" {
				if err := os.Remove(indexPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(indexPath, content, 0o644); err != nil {
				t.Fatal(err)
			}

			turn := snap(t, dir, sessA, SnapshotOptions{})
			if want := scratchTree(t, dir); turn.Tree != want {
				t.Errorf("tree = %s, want %s", turn.Tree, want)
			}
		})
	}
}

func TestSnapshotPathShapeChanges(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	writeFile(t, dir, "d/inner.txt", "i\n")
	commitAll(t, dir, "d")

	// A tracked file becomes a directory and a tracked directory becomes a file.
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "a.txt/b", "now a dir\n")

	if err := os.RemoveAll(filepath.Join(dir, "d")); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "d", "now a file\n")

	turn := snap(t, dir, sessA, SnapshotOptions{})
	got := treeEntries(t, dir, turn.Tree)

	if want := scratchTree(t, dir); turn.Tree != want {
		t.Errorf("tree = %s, want %s; has %q", turn.Tree, want, paths(got))
	}

	// An emptied directory in place of a tracked file removes the file.
	dir2 := seedRepo(t)
	if err := os.Remove(filepath.Join(dir2, "a.txt")); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir2, "a.txt"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, ok := treeEntries(t, dir2, snap(t, dir2, sessA, SnapshotOptions{}).Tree)["a.txt"]; ok {
		t.Error("a.txt survived as a file")
	}
}

func TestSnapshotUnchangedReusesPreviousTree(t *testing.T) {
	dir := seedRepo(t)

	if first := snap(t, dir, sessA, SnapshotOptions{Unchanged: true}); first.N != 1 || first.Tree != scratchTree(t, dir) {
		t.Fatalf("Unchanged on the first turn must take a full snapshot, got %+v", first)
	}

	writeFile(t, dir, "big.bin", "")

	if err := os.Truncate(filepath.Join(dir, "big.bin"), 60<<20); err != nil {
		t.Fatal(err)
	}

	second := snap(t, dir, sessA, SnapshotOptions{})
	logPath := fakeGit(t, "")

	// Marker in between: the reuse skips it and takes the real snapshot's tree.
	if _, err := MarkMissing(context.Background(), dir, sessA); err != nil {
		t.Fatal(err)
	}

	logBefore, _ := os.ReadFile(logPath)

	writeFile(t, dir, "ignored-by-reuse.txt", "x\n")

	fourth := snap(t, dir, sessA, SnapshotOptions{Unchanged: true})

	if fourth.N != 4 || fourth.Tree != second.Tree || !slices.Equal(fourth.Skipped, second.Skipped) {
		t.Errorf("fourth = %+v, want turn 4 with %s and skipped %q", fourth, second.Tree, second.Skipped)
	}

	logAfter, _ := os.ReadFile(logPath)

	calls := string(logAfter[len(logBefore):])
	for _, sub := range []string{"update-index", "write-tree", "ls-files"} {
		if strings.Contains(calls, sub) {
			t.Errorf("Unchanged ran %s:\n%s", sub, calls)
		}
	}
}

func TestMarkMissing(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	ctx := context.Background()

	snap(t, dir, sessA, SnapshotOptions{})

	m, err := MarkMissing(ctx, dir, sessA)
	if err != nil {
		t.Fatal(err)
	}

	if m.N != 2 || !m.Missing || m.Tree != gitOut(t, dir, "mktree") {
		t.Errorf("marker = %+v", m)
	}

	msg := gitOut(t, dir, "log", "-1", "--format=%B", m.Commit)
	for _, line := range []string{"muxreveal turn 2", "Session: " + sessA, "Worktree: " + dir, "Snapshot: none"} {
		if !strings.Contains(msg, line) {
			t.Errorf("marker message lacks %q:\n%s", line, msg)
		}
	}

	if after := snap(t, dir, sessA, SnapshotOptions{}); after.N != 3 || after.Missing {
		t.Errorf("snapshot after a marker = %+v, want turn 3", after)
	}

	turns, err := Turns(ctx, openRepo(t, dir), sessA)
	if err != nil {
		t.Fatal(err)
	}

	if len(turns) != 3 || turns[0].Missing || !turns[1].Missing || turns[2].Missing {
		t.Errorf("turns = %+v", turns)
	}
}

func TestBadSession(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	ctx := context.Background()
	tmp := t.TempDir()

	for _, s := range []string{"", "not-a-uuid", sessA + "x", "../../heads/main", "11111111-2222-4333-8444-55555555555*"} {
		if _, err := Snapshot(ctx, dir, s, SnapshotOptions{TempDir: tmp}); !errors.Is(err, ErrBadSession) {
			t.Errorf("Snapshot(%q) error = %v, want ErrBadSession", s, err)
		}

		if _, err := MarkMissing(ctx, dir, s); !errors.Is(err, ErrBadSession) {
			t.Errorf("MarkMissing(%q) error = %v, want ErrBadSession", s, err)
		}

		if _, err := Turns(ctx, openRepo(t, dir), s); !errors.Is(err, ErrBadSession) {
			t.Errorf("Turns(%q) error = %v, want ErrBadSession", s, err)
		}

		if _, err := PruneSession(ctx, openRepo(t, dir), s); !errors.Is(err, ErrBadSession) {
			t.Errorf("PruneSession(%q) error = %v, want ErrBadSession", s, err)
		}
	}

	assertEmptyDir(t, tmp)

	if refs := gitOut(t, dir, "for-each-ref", "refs/muxreveal"); refs != "" {
		t.Errorf("refs written: %s", refs)
	}
}

func TestSnapshotNotInRepo(t *testing.T) {
	t.Parallel()

	_, err := Snapshot(context.Background(), t.TempDir(), sessA, SnapshotOptions{TempDir: t.TempDir()})
	if !errors.Is(err, ErrNotRepo) {
		t.Errorf("error = %v, want ErrNotRepo", err)
	}
}

func TestSnapshotRetriesWhenTheRefExists(t *testing.T) {
	dir := seedRepo(t)

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}

	// A racing writer takes number 1 right before our update-ref, once.
	fakeGit(t, `case "$*" in
*update-ref*--no-deref*)
	if [ ! -e "$LOG.raced" ]; then
		: > "$LOG.raced"
		'`+realGit+`' update-ref refs/muxreveal/`+sessA+`/1 "$('`+realGit+`' rev-parse HEAD)"
	fi;;
esac`)

	turn := snap(t, dir, sessA, SnapshotOptions{})
	if turn.N != 2 {
		t.Errorf("turn = %d, want 2 after the retry", turn.N)
	}

	turns, err := Turns(context.Background(), openRepo(t, dir), sessA)
	if err != nil {
		t.Fatal(err)
	}

	if len(turns) != 2 || turns[1].Commit != turn.Commit {
		t.Errorf("turns = %+v", turns)
	}
}

func TestPrune(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	ctx := context.Background()

	wt := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-other")
	gitOut(t, dir, "worktree", "add", "-q", "-b", "other", wt)

	wt, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}

	snap(t, dir, sessA, SnapshotOptions{})
	snap(t, dir, sessA, SnapshotOptions{})
	snap(t, wt, sessB, SnapshotOptions{})

	if _, err := MarkMissing(ctx, wt, sessB); err != nil {
		t.Fatal(err)
	}

	r := openRepo(t, dir)

	n, err := PruneWorktree(ctx, r, wt)
	if err != nil || n != 2 {
		t.Fatalf("PruneWorktree = %d, %v; want 2", n, err)
	}

	if got, _ := Turns(ctx, r, sessB); len(got) != 0 {
		t.Errorf("session B turns left: %+v", got)
	}

	if got, _ := Turns(ctx, r, sessA); len(got) != 2 {
		t.Errorf("session A turns = %+v, want both kept", got)
	}

	snap(t, wt, sessB, SnapshotOptions{})

	n, err = PruneSession(ctx, r, sessA)
	if err != nil || n != 2 {
		t.Fatalf("PruneSession = %d, %v; want 2", n, err)
	}

	if got, _ := Turns(ctx, r, sessB); len(got) != 1 {
		t.Errorf("session B turns = %+v, want 1 kept", got)
	}

	if n, err := PruneSession(ctx, r, sessA); err != nil || n != 0 {
		t.Errorf("second PruneSession = %d, %v", n, err)
	}
}

func TestSnapshotCancelledLeavesNoTempFileOrRef(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	tmp := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Snapshot(ctx, dir, sessA, SnapshotOptions{TempDir: tmp}); err == nil {
		t.Fatal("cancelled context did not fail the snapshot")
	}

	assertEmptyDir(t, tmp)

	if refs := gitOut(t, dir, "for-each-ref", "refs/muxreveal"); refs != "" {
		t.Errorf("refs written: %s", refs)
	}
}

func TestTempIndexNameAndLockCleanup(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	idx, err := newTempIndex(tmp)
	if err != nil {
		t.Fatal(err)
	}

	base := filepath.Base(idx.path)
	if !strings.HasPrefix(base, strconv.Itoa(os.Getpid())+"-index-") {
		t.Errorf("name %q does not match <pid>-index-*", base)
	}

	if err := os.WriteFile(idx.path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	idx.remove()
	assertEmptyDir(t, tmp)
}

func TestSnapshotRefWriteSurvivesTheContextEnding(t *testing.T) {
	dir := seedRepo(t)
	r := openRepo(t, dir)

	// update-ref outlasts the deadline; the ref write must not be killed.
	fakeGit(t, `case "$*" in
*update-ref*) sleep 1.5;;
esac`)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	turn, err := Snapshot(ctx, dir, sessA, SnapshotOptions{TempDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if turn.N != 1 {
		t.Errorf("turn = %d, want 1", turn.N)
	}

	locks, err := filepath.Glob(filepath.Join(r.CommonDir, "refs", "muxreveal", "*", "*.lock"))
	if err != nil || len(locks) > 0 {
		t.Errorf("lock files %v (%v)", locks, err)
	}

	if next := snap(t, dir, sessA, SnapshotOptions{}); next.N != 2 {
		t.Errorf("second turn = %d, want 2", next.N)
	}
}

func TestWriteTurnWithDoneContextWritesNothing(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tree := gitOut(t, dir, "rev-parse", "HEAD^{tree}")

	if _, err := writeTurn(ctx, r, sessA, nil, tree, nil, false); !errors.Is(err, context.Canceled) {
		t.Errorf("writeTurn error = %v, want context.Canceled", err)
	}

	if _, err := MarkMissing(ctx, dir, sessA); err == nil {
		t.Error("MarkMissing with a done context succeeded")
	}

	if refs := gitOut(t, dir, "for-each-ref", "refs/muxreveal"); refs != "" {
		t.Errorf("refs written: %s", refs)
	}
}

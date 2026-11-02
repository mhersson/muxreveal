package git

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	sessC = "cccccccc-dddd-4eee-8fff-000000000000"
	sessD = "dddddddd-eeee-4fff-8000-111111111111"
	sessE = "eeeeeeee-ffff-4000-8111-222222222222"
	day   = 24 * time.Hour
)

var sweepNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) int { return int(sweepNow.Add(-d).UnixMilli()) }

// prRefs points refs/aim/pr/<n>/head and base at HEAD.
func prRefs(t *testing.T, dir string, n int) {
	t.Helper()

	head := gitOut(t, dir, "rev-parse", "HEAD")
	for _, leaf := range []string{"head", "base"} {
		gitOut(t, dir, "update-ref", "refs/aim/pr/"+strconv.Itoa(n)+"/"+leaf, head)
	}
}

// names lists the refs under prefix, sorted.
func names(t *testing.T, dir, prefix string) []string {
	t.Helper()

	var out []string

	for l := range strings.SplitSeq(gitOut(t, dir, "for-each-ref", "--format=%(refname)", prefix), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}

	slices.Sort(out)

	return out
}

func refNames(refs []Ref) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Name)
	}

	slices.Sort(out)

	return out
}

func sameNames(t *testing.T, what string, got []Ref, want ...string) {
	t.Helper()
	slices.Sort(want)

	if g := refNames(got); !slices.Equal(g, want) {
		t.Errorf("%s = %q, want %q", what, g, want)
	}
}

func TestStaleReadsTheSessionsNewestTurnInTheRepository(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	wt := linkedWorktree(t, dir, "other")
	r := openRepo(t, dir)

	snap(t, dir, sessA, SnapshotOptions{Turn: ago(20 * day)})
	snap(t, dir, sessB, SnapshotOptions{Turn: ago(20 * day)})
	snap(t, wt, sessB, SnapshotOptions{Turn: ago(time.Hour)})
	snap(t, dir, sessC, SnapshotOptions{Turn: ago(15 * day)})
	legacyTurnRef(t, dir, r.Root, sessC, 3)
	snap(t, wt, sessD, SnapshotOptions{Turn: ago(14 * day)})
	snap(t, dir, sessE, SnapshotOptions{Turn: ago(-time.Hour)})
	prRefs(t, dir, 5)
	prRefs(t, dir, 8)

	l, err := ListAimRefs(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	got := l.Stale(sweepNow, func(s string) bool { return s == sessA }, func(n int) bool { return n == 5 })

	sameNames(t, "turns", got.Turns, turnRef(sessC, turnKey(r.Root), ago(15*day), false), "refs/aim/"+sessC+"/3")
	sameNames(t, "prs", got.PRs, "refs/aim/pr/8/base", "refs/aim/pr/8/head")

	for _, ref := range slices.Concat(got.Turns, got.PRs) {
		if want := gitOut(t, dir, "rev-parse", ref.Name); ref.OID != want {
			t.Errorf("%s OID = %s, want %s", ref.Name, ref.OID, want)
		}
	}
}

func TestDeleteRefsCarriesTheListedValue(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)

	snap(t, dir, sessC, SnapshotOptions{Turn: ago(15 * day)})
	prRefs(t, dir, 8)

	l, err := ListAimRefs(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	got := l.Stale(sweepNow, func(string) bool { return false }, func(int) bool { return false })

	writeFile(t, dir, "a.txt", "moved\n")
	commitAll(t, dir, "next")
	gitOut(t, dir, "update-ref", "refs/aim/pr/8/head", gitOut(t, dir, "rev-parse", "HEAD"))

	if n, err := DeleteRefs(context.Background(), r, got.PRs); err == nil || n != 0 {
		t.Errorf("DeleteRefs(PRs) = %d, %v; want an error", n, err)
	}

	if got := names(t, dir, "refs/aim/pr/"); len(got) != 2 {
		t.Errorf("pr refs = %q, want both kept", got)
	}

	n, err := DeleteRefs(context.Background(), r, got.Turns)
	if err != nil || n != len(got.Turns) || n == 0 {
		t.Errorf("DeleteRefs(Turns) = %d, %v; want %d", n, err, len(got.Turns))
	}

	if got := names(t, dir, "refs/aim/turns/"); len(got) != 0 {
		t.Errorf("turn refs left: %q", got)
	}
}

func TestDeleteRefsRefusesRefsOutsideAim(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)
	prRefs(t, dir, 8)

	head := gitOut(t, dir, "rev-parse", "HEAD")
	before := names(t, dir, "refs/")

	cases := map[string][]Ref{
		"outside": {{Name: "refs/heads/main", OID: head}, {Name: "refs/aim/pr/8/head", OID: head}},
		"no oid":  {{Name: "refs/aim/pr/8/head"}},
	}

	for name, refs := range cases {
		n, err := DeleteRefs(context.Background(), r, refs)
		if n != 0 || !errors.Is(err, ErrNotAimRef) {
			t.Errorf("%s: DeleteRefs = %d, %v; want 0, ErrNotAimRef", name, n, err)
		}

		if got := names(t, dir, "refs/"); !slices.Equal(got, before) {
			t.Errorf("%s: refs = %q, want %q", name, got, before)
		}
	}

	if n, err := DeleteRefs(context.Background(), r, nil); n != 0 || err != nil {
		t.Errorf("DeleteRefs(nil) = %d, %v", n, err)
	}
}

func TestSweepLeavesOtherRefs(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)
	head := gitOut(t, dir, "rev-parse", "HEAD")

	for _, ref := range []string{
		"refs/heads/side", "refs/tags/v1", "refs/keep/x", "refs/aim/other/x", "refs/aim/pr/7/other",
		"refs/aim/turns/" + sessB + "/zzzz/1/start",
	} {
		gitOut(t, dir, "update-ref", ref, head)
	}

	snap(t, dir, sessB, SnapshotOptions{Turn: ago(20 * day)})
	snap(t, dir, sessC, SnapshotOptions{Turn: ago(15 * day)})
	prRefs(t, dir, 9)

	before := names(t, dir, "refs/")
	goneTurn := turnRef(sessC, turnKey(r.Root), ago(15*day), false)

	l, err := ListAimRefs(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	got := l.Stale(sweepNow, func(s string) bool { return s == sessB }, func(int) bool { return false })

	for _, half := range [][]Ref{got.Turns, got.PRs} {
		if _, err := DeleteRefs(context.Background(), r, half); err != nil {
			t.Fatal(err)
		}
	}

	var want []string

	for _, n := range before {
		if n != goneTurn && n != "refs/aim/pr/9/head" && n != "refs/aim/pr/9/base" {
			want = append(want, n)
		}
	}

	if got := names(t, dir, "refs/"); !slices.Equal(got, want) {
		t.Errorf("refs = %q, want %q", got, want)
	}
}

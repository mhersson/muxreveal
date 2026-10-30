package core

import (
	"reflect"
	"testing"
)

const repoKey = "github.com/o/r"

func prState(t *testing.T, acts ...Action) State {
	t.Helper()

	st := NewStore(InitialState(DefaultConfig()))
	for _, a := range acts {
		if err := st.Dispatch(a); err != nil {
			t.Fatalf("Dispatch(%T) error = %v", a, err)
		}
	}

	return st.State()
}

func TestSetRepoPRsReplacesOpenAndKeepsBranchLookups(t *testing.T) {
	merged := PRInfo{Number: 3, State: "MERGED", HeadBranch: "old"}

	s := prState(t,
		SetBranchPR{Repo: repoKey, Branch: "old", PR: merged, Found: true},
		SetRepoPRs{Repo: repoKey, DefaultBranch: "main", Open: []PRInfo{{Number: 1, HeadBranch: "a"}, {Number: 2, HeadBranch: "b"}}},
	)

	s = dispatch(t, s, SetRepoPRs{Repo: repoKey, DefaultBranch: "trunk", Open: []PRInfo{{Number: 4, HeadBranch: "c"}}, Err: "boom"})

	got := s.PRs[repoKey]
	if got.DefaultBranch != "trunk" || got.Err != "boom" {
		t.Errorf("repo = %+v, want default branch trunk and error boom", got)
	}

	if len(got.Open) != 1 || got.Open[0].Number != 4 {
		t.Errorf("Open = %+v, want only #4", got.Open)
	}

	if got.Branch["old"] != merged {
		t.Errorf("Branch = %+v, want the lookup kept", got.Branch)
	}
}

func TestSetRepoPRsKeepsReposApartAndLeavesTheCallersSliceAlone(t *testing.T) {
	open := []PRInfo{{Number: 1, HeadBranch: "a"}}
	s := prState(t,
		SetRepoPRs{Repo: repoKey, Open: open},
		SetRepoPRs{Repo: "github.com/o/other", Open: []PRInfo{{Number: 9, HeadBranch: "z"}}},
	)

	open[0].Number = 99

	if got := s.PRs[repoKey].Open[0].Number; got != 1 {
		t.Errorf("Open[0].Number = %d after the caller edited its slice, want 1", got)
	}

	if got := s.PRs["github.com/o/other"].Open[0].Number; got != 9 {
		t.Errorf("other repository changed: #%d, want #9", got)
	}
}

func TestSetRepoPRsRefusesNoRepo(t *testing.T) {
	st := NewStore(InitialState(DefaultConfig()))
	if err := st.Dispatch(SetRepoPRs{}); err == nil {
		t.Error("SetRepoPRs with no repository was accepted")
	}

	if err := st.Dispatch(SetBranchPR{Branch: "b", Found: true}); err == nil {
		t.Error("SetBranchPR with no repository was accepted")
	}

	if err := st.Dispatch(SetBranchPR{Repo: repoKey, Found: true}); err == nil {
		t.Error("SetBranchPR with no branch was accepted")
	}
}

func TestSetBranchPRRecordsAndClears(t *testing.T) {
	closed := PRInfo{Number: 5, State: "CLOSED", HeadBranch: "x"}

	s := prState(t, SetBranchPR{Repo: repoKey, Branch: "x", PR: closed, Found: true})
	if s.PRs[repoKey].Branch["x"] != closed {
		t.Fatalf("Branch = %+v, want #5 recorded", s.PRs[repoKey].Branch)
	}

	// A poll that arrives later keeps the lookup.
	s = dispatch(t, s, SetRepoPRs{Repo: repoKey, Open: nil})
	if s.PRs[repoKey].Branch["x"] != closed {
		t.Fatalf("Branch = %+v after a poll, want #5 kept", s.PRs[repoKey].Branch)
	}

	s = dispatch(t, s, SetBranchPR{Repo: repoKey, Branch: "x"})
	if _, ok := s.PRs[repoKey].Branch["x"]; ok {
		t.Errorf("Branch = %+v, want the lookup cleared", s.PRs[repoKey].Branch)
	}

	// Forgetting a branch nothing is known about records no repository.
	s = dispatch(t, State{}, SetBranchPR{Repo: repoKey, Branch: "x"})
	if _, ok := s.PRs[repoKey]; ok {
		t.Errorf("PRs = %+v, want no entry for a branch never looked up", s.PRs)
	}
}

func TestPRsAreSharedByCopiesButReplacedByActions(t *testing.T) {
	s := prState(t, SetRepoPRs{Repo: repoKey, Open: []PRInfo{{Number: 1, HeadBranch: "a"}}})
	store := NewStore(s)

	before := store.State()

	if err := store.Dispatch(SetRepoPRs{Repo: repoKey, Open: []PRInfo{{Number: 2, HeadBranch: "a"}}}); err != nil {
		t.Fatal(err)
	}

	if got := before.PRs[repoKey].Open[0].Number; got != 1 {
		t.Errorf("an earlier copy sees #%d, want #1", got)
	}

	if got := store.State().PRs[repoKey].Open[0].Number; got != 2 {
		t.Errorf("state holds #%d, want #2", got)
	}
}

func TestPRFor(t *testing.T) {
	lookup := PRInfo{Number: 3, State: "MERGED", HeadBranch: "feat"}
	fork := PRInfo{Number: 9, State: "OPEN", HeadBranch: "feat", CrossRepo: true}

	tests := []struct {
		name   string
		acts   []Action
		repo   string
		branch string
		want   int    // 0 for none
		state  string // the state wanted, when the case checks it
	}{
		{"nothing known", nil, repoKey, "feat", 0, ""},
		{
			"an open PR for the branch",
			[]Action{SetRepoPRs{Repo: repoKey, Open: []PRInfo{{Number: 1, HeadBranch: "other"}, {Number: 2, HeadBranch: "feat"}}}},
			repoKey, "feat", 2, "",
		},
		{
			"the newest of several open PRs, whatever their order",
			[]Action{SetRepoPRs{Repo: repoKey, Open: []PRInfo{{Number: 5, HeadBranch: "feat"}, {Number: 8, HeadBranch: "feat"}, {Number: 6, HeadBranch: "feat"}}}},
			repoKey, "feat", 8, "",
		},
		{
			"an open PR beats the lookup",
			[]Action{
				SetBranchPR{Repo: repoKey, Branch: "feat", PR: lookup, Found: true},
				SetRepoPRs{Repo: repoKey, Open: []PRInfo{{Number: 4, HeadBranch: "feat"}}},
			},
			repoKey, "feat", 4, "",
		},
		{
			"a merged lookup of the same PR beats a poll that still lists it open",
			[]Action{
				SetRepoPRs{Repo: repoKey, Open: []PRInfo{{Number: 3, HeadBranch: "feat", State: "OPEN"}}},
				SetBranchPR{Repo: repoKey, Branch: "feat", PR: lookup, Found: true},
			},
			repoKey, "feat", 3, "MERGED",
		},
		{
			"an open PR reusing the branch beats an older merged record",
			[]Action{
				SetBranchPR{Repo: repoKey, Branch: "feat", PR: lookup, Found: true},
				SetRepoPRs{Repo: repoKey, Open: []PRInfo{{Number: 9, HeadBranch: "feat", State: "OPEN"}}},
			},
			repoKey, "feat", 9, "OPEN",
		},
		{
			"the lookup when no open PR matches",
			[]Action{
				SetRepoPRs{Repo: repoKey, Open: []PRInfo{{Number: 1, HeadBranch: "other"}}},
				SetBranchPR{Repo: repoKey, Branch: "feat", PR: lookup, Found: true},
			},
			repoKey, "feat", 3, "",
		},
		{
			"a cross-repository open PR never matches",
			[]Action{SetRepoPRs{Repo: repoKey, Open: []PRInfo{fork}}},
			repoKey, "feat", 0, "",
		},
		{
			"a cross-repository PR does not hide the lookup",
			[]Action{
				SetRepoPRs{Repo: repoKey, Open: []PRInfo{fork}},
				SetBranchPR{Repo: repoKey, Branch: "feat", PR: lookup, Found: true},
			},
			repoKey, "feat", 3, "",
		},
		{
			"a cross-repository lookup never matches",
			[]Action{SetBranchPR{Repo: repoKey, Branch: "feat", PR: fork, Found: true}},
			repoKey, "feat", 0, "",
		},
		{
			"another repository's PRs do not match",
			[]Action{SetRepoPRs{Repo: "github.com/o/other", Open: []PRInfo{{Number: 1, HeadBranch: "feat"}}}},
			repoKey, "feat", 0, "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := prState(t, tt.acts...)

			pr, ok := s.PRFor(tt.repo, tt.branch)
			if ok != (tt.want != 0) || pr.Number != tt.want || (tt.state != "" && pr.State != tt.state) {
				t.Errorf("PRFor(%q, %q) = #%d, %v, want #%d %s", tt.repo, tt.branch, pr.Number, ok, tt.want, tt.state)
			}
		})
	}
}

func TestOpenPRForReadsOnlyThePollsOpenList(t *testing.T) {
	openLookup := PRInfo{Number: 11, State: "OPEN", HeadBranch: "feat"}
	merged := PRInfo{Number: 3, State: "MERGED", HeadBranch: "feat"}
	fork := PRInfo{Number: 9, State: "OPEN", HeadBranch: "feat", CrossRepo: true}

	tests := []struct {
		name string
		acts []Action
		want int // 0 for none
	}{
		{"nothing known", nil, 0},
		{
			"the newest open PR of the list",
			[]Action{SetRepoPRs{Repo: repoKey, Open: []PRInfo{{Number: 5, HeadBranch: "feat"}, {Number: 8, HeadBranch: "feat"}, {Number: 6, HeadBranch: "other"}}}},
			8,
		},
		{
			"a lookup is not the poll, even one that reads open",
			[]Action{SetBranchPR{Repo: repoKey, Branch: "feat", PR: openLookup, Found: true}},
			0,
		},
		{
			"a lookup does not stand in for an open PR that is missing",
			[]Action{
				SetBranchPR{Repo: repoKey, Branch: "feat", PR: merged, Found: true},
				SetRepoPRs{Repo: repoKey, Open: []PRInfo{{Number: 1, HeadBranch: "other"}}},
			},
			0,
		},
		{"a cross-repository PR never matches", []Action{SetRepoPRs{Repo: repoKey, Open: []PRInfo{fork}}}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, ok := prState(t, tt.acts...).OpenPRFor(repoKey, "feat")
			if ok != (tt.want != 0) || pr.Number != tt.want {
				t.Errorf("OpenPRFor = #%d, %v, want #%d", pr.Number, ok, tt.want)
			}
		})
	}
}

func TestSetRepoPRsDetails(t *testing.T) {
	t.Parallel()

	focused := &PRChecks{Number: 7, HeadOID: "abc", Checks: []CheckInfo{{Name: "ci", State: "success"}}}
	good := SetRepoPRs{
		Repo: repoKey, DefaultBranch: "main",
		Methods: []string{"merge", "squash"}, DeleteBranchOnMerge: true, Focused: focused,
	}

	tests := []struct {
		name        string
		next        SetRepoPRs
		wantMethods []string
		wantDelete  bool
		wantFocus   *PRChecks
	}{
		{"error keeps them", SetRepoPRs{Repo: repoKey, Err: "boom", Methods: []string{"merge", "squash"}, DeleteBranchOnMerge: true, Focused: focused}, []string{"merge", "squash"}, true, focused},
		{"good poll replaces them", SetRepoPRs{Repo: repoKey, Methods: []string{"rebase"}}, []string{"rebase"}, false, nil},
		{"nil focus clears it", SetRepoPRs{Repo: repoKey, Methods: []string{"merge", "squash"}, DeleteBranchOnMerge: true}, []string{"merge", "squash"}, true, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := dispatch(t, prState(t, good), tt.next)
			got := s.PRs[repoKey]

			if !reflect.DeepEqual(got.Methods, tt.wantMethods) || got.DeleteBranchOnMerge != tt.wantDelete || !reflect.DeepEqual(got.Focused, tt.wantFocus) {
				t.Errorf("repo = %+v, want methods %v, delete %v, focused %+v", got, tt.wantMethods, tt.wantDelete, tt.wantFocus)
			}
		})
	}
}

package core

import (
	"errors"
	"maps"
	"slices"
	"time"
)

// PRInfo is a pull request as the poll sees it. It mirrors the GitHub client's
// type so that core needs no import of it.
type PRInfo struct {
	Number                          int
	Title, URL, Author              string
	State                           string // OPEN, MERGED or CLOSED
	HeadBranch, HeadOID, BaseBranch string
	ReviewDecision, MergeState      string // GitHub's enums as given; "" when null
	Draft, CrossRepo                bool
	ChecksTotal, ChecksDone         int
	ChecksFailed                    int
	UpdatedAt                       time.Time
}

// RepoPRs is what is known of one repository's pull requests. State copies
// share it, so an action replaces a RepoPRs and never edits one in place.
type RepoPRs struct {
	DefaultBranch string
	Open          []PRInfo          // the last successful poll, newest update first
	Err           string            // the last poll's failure; "" after a success
	Branch        map[string]PRInfo // per branch, the pull request a lookup found
}

// SetRepoPRs records a poll of the repository keyed "host/owner/name": its
// default branch, its open pull requests and the poll's error, replacing all
// three. The branch lookups stay.
type SetRepoPRs struct {
	Repo          string
	DefaultBranch string
	Open          []PRInfo
	Err           string
}

func (a SetRepoPRs) apply(s *State) error {
	if a.Repo == "" {
		return errors.New("core: SetRepoPRs with an empty repository")
	}

	s.setRepoPRs(a.Repo, RepoPRs{
		DefaultBranch: a.DefaultBranch,
		Open:          slices.Clone(a.Open),
		Err:           a.Err,
		Branch:        s.PRs[a.Repo].Branch,
	})

	return nil
}

// SetBranchPR records the pull request a lookup found for a branch of the
// repository keyed "host/owner/name"; without Found it forgets the branch's.
type SetBranchPR struct {
	Repo, Branch string
	PR           PRInfo
	Found        bool
}

func (a SetBranchPR) apply(s *State) error {
	if a.Repo == "" || a.Branch == "" {
		return errors.New("core: SetBranchPR needs a repository and a branch")
	}

	r := s.PRs[a.Repo]

	if _, known := r.Branch[a.Branch]; !a.Found && !known {
		return nil
	}

	branch := maps.Clone(r.Branch)

	if a.Found {
		if branch == nil {
			branch = map[string]PRInfo{}
		}

		branch[a.Branch] = a.PR
	} else {
		delete(branch, a.Branch)
	}

	r.Branch = branch
	s.setRepoPRs(a.Repo, r)

	return nil
}

// setRepoPRs stores r under repo in a new map, since copies of the state share
// the old one.
func (s *State) setRepoPRs(repo string, r RepoPRs) {
	prs := maps.Clone(s.PRs)
	if prs == nil {
		prs = map[string]RepoPRs{}
	}

	prs[repo] = r
	s.PRs = prs
}

// PRFor returns the pull request of a branch of the repository keyed
// "host/owner/name": the newest same-repository open one, else the one a lookup
// found. Newest is the highest number, which is the order of creation.
func (s State) PRFor(repo, branch string) (PRInfo, bool) {
	r := s.PRs[repo]

	var best PRInfo

	found := false

	for _, p := range r.Open {
		if p.HeadBranch == branch && !p.CrossRepo && (!found || p.Number > best.Number) {
			best, found = p, true
		}
	}

	if found {
		return best, true
	}

	p, ok := r.Branch[branch]
	if !ok || p.CrossRepo {
		return PRInfo{}, false
	}

	return p, true
}

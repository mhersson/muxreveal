package github

import (
	"context"
	"time"

	graphql "github.com/cli/shurcooL-graphql"
)

const (
	requestedSearch = "is:open is:pr archived:false review-requested:@me"
	authoredSearch  = "is:open is:pr archived:false author:@me"
)

// SearchPR is a pull request found by a search, with the repository it is in.
type SearchPR struct {
	Repo Repo
	PR   PR
}

// MyPRs holds the open pull requests that ask for the viewer's review and
// the ones the viewer opened.
type MyPRs struct {
	Requested, Authored []SearchPR
}

type searchNode struct {
	PullRequest struct {
		pullNode
		Repository struct {
			Name  string
			Owner struct{ Login string }
		}
	} `graphql:"... on PullRequest"`
}

type searchResult struct {
	Nodes []searchNode
}

type myPRsQuery struct {
	Requested searchResult `graphql:"requested: search(query: $requested, type: ISSUE, first: 100)"`
	Authored  searchResult `graphql:"authored: search(query: $authored, type: ISSUE, first: 100)"`
}

// MyPRs searches the host for the open pull requests that ask for the
// viewer's review and the ones the viewer opened, at most 100 of each, in
// repositories that are not archived. A since that is not zero limits the
// review requests to pull requests updated on or after its UTC date.
func (c *Client) MyPRs(ctx context.Context, since time.Time) (MyPRs, error) {
	var q myPRsQuery

	requested := requestedSearch
	if !since.IsZero() {
		requested += " updated:>=" + since.UTC().Format(time.DateOnly)
	}

	vars := map[string]any{
		"requested": graphql.String(requested),
		"authored":  graphql.String(authoredSearch),
	}
	if err := c.query(ctx, "MyPRs", &q, vars); err != nil {
		return MyPRs{}, err
	}

	return MyPRs{
		Requested: c.searchPRs(q.Requested.Nodes),
		Authored:  c.searchPRs(q.Authored.Nodes),
	}, nil
}

func (c *Client) searchPRs(nodes []searchNode) []SearchPR {
	var out []SearchPR

	for _, n := range nodes {
		pr := n.PullRequest
		if pr.Number == 0 {
			continue
		}

		repo := Repo{Host: c.host, Owner: pr.Repository.Owner.Login, Name: pr.Repository.Name}
		out = append(out, SearchPR{Repo: repo, PR: pr.pr()})
	}

	return out
}

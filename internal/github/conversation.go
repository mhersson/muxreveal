package github

import (
	"context"
	"html"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	graphql "github.com/cli/shurcooL-graphql"
)

const (
	timelinePageSize = 100
	timelineMaxPages = 5
	signedImageHost  = "private-user-images.githubusercontent.com"
)

// Text is a markdown body and the signed URLs of its private images.
type Text struct {
	Markdown string
	Images   map[string]string // image source as written to the signed URL; nil when none
}

// ItemKind is the kind of a timeline item.
type ItemKind uint8

const (
	ItemComment ItemKind = iota
	ItemReview
	ItemCommits // a push: a run of commits with no other item between them
	ItemForcePush
	ItemReviewRequest
	ItemMerged
	ItemClosed
)

// ReviewComment is an inline comment of a review.
type ReviewComment struct {
	Path string
	Line int // 0 when unknown
	Hunk string
	Body Text
}

// TimelineItem is one entry of a pull request's conversation. Only the fields
// of its Kind are set, besides the common ones.
type TimelineItem struct {
	Kind ItemKind
	ID   string // the node id; the last commit's oid for ItemCommits
	// Author is a login ("ghost" for none), or a git author name when Named.
	Author string
	Bot    bool
	Named  bool
	At     time.Time

	Body     Text
	State    string // a review's: APPROVED, CHANGES_REQUESTED, COMMENTED or DISMISSED
	Comments []ReviewComment

	Commits            int    // ItemCommits
	Ref, Before, After string // ItemForcePush: abbreviated oids
	Reviewer           string // ItemReviewRequest: a login or org/slug
	Commit, Base       string // ItemMerged
}

// Conversation is a pull request's description and timeline.
type Conversation struct {
	PR      PR
	Created time.Time
	Body    Text
	Items   []TimelineItem // oldest first
	Cut     bool           // older items were not read
}

type actor struct {
	Login    string
	Typename string `graphql:"__typename"`
}

type commentNode struct {
	ID        string
	Author    *actor
	CreatedAt time.Time
	Body      string
	BodyHTML  string `graphql:"bodyHTML"`
}

type reviewNode struct {
	ID        string
	Author    *actor
	CreatedAt time.Time
	State     string
	Body      string
	BodyHTML  string `graphql:"bodyHTML"`
	Comments  struct {
		Nodes []struct {
			Path         string
			Line         *int
			OriginalLine *int
			DiffHunk     string
			Body         string
			BodyHTML     string `graphql:"bodyHTML"`
		}
	} `graphql:"comments(first: 100)"`
}

type reviewerNode struct {
	Typename  string                        `graphql:"__typename"`
	User      struct{ Login string }        `graphql:"... on User"`
	Bot       struct{ Login string }        `graphql:"... on Bot"`
	Mannequin struct{ Login string }        `graphql:"... on Mannequin"`
	Team      struct{ CombinedSlug string } `graphql:"... on Team"`
}

type timelineNode struct {
	Typename string `graphql:"__typename"`

	IssueComment commentNode `graphql:"... on IssueComment"`

	Review reviewNode `graphql:"... on PullRequestReview"`

	PRCommit struct {
		ID     string
		Commit struct {
			Oid           string
			CommittedDate time.Time
			Author        *struct {
				Name string
				User *struct{ Login string }
			}
		}
	} `graphql:"... on PullRequestCommit"`

	ForcePush struct {
		ID           string
		Actor        *actor
		CreatedAt    time.Time
		BeforeCommit *struct{ AbbreviatedOid string }
		AfterCommit  *struct{ AbbreviatedOid string }
		Ref          *struct{ Name string }
	} `graphql:"... on HeadRefForcePushedEvent"`

	ReviewRequest struct {
		ID        string
		Actor     *actor
		CreatedAt time.Time
		Reviewer  *reviewerNode `graphql:"requestedReviewer"`
	} `graphql:"... on ReviewRequestedEvent"`

	Merged struct {
		ID          string
		Actor       *actor
		CreatedAt   time.Time
		MergeCommit *struct{ AbbreviatedOid string } `graphql:"mergeCommit: commit"`
		MergeRef    string                           `graphql:"mergeRefName"`
	} `graphql:"... on MergedEvent"`

	Closed struct {
		ID        string
		Actor     *actor
		CreatedAt time.Time
	} `graphql:"... on ClosedEvent"`
}

type conversationQuery struct {
	Repository struct {
		PullRequest *struct {
			pullNode
			CreatedAt     time.Time
			Body          string
			BodyHTML      string `graphql:"bodyHTML"`
			TimelineItems struct {
				PageInfo struct {
					HasPreviousPage bool
					StartCursor     string
				}
				Nodes []timelineNode
			} `graphql:"timelineItems(last: 100, before: $cursor, itemTypes: [ISSUE_COMMENT, PULL_REQUEST_REVIEW, PULL_REQUEST_COMMIT, HEAD_REF_FORCE_PUSHED_EVENT, REVIEW_REQUESTED_EVENT, MERGED_EVENT, CLOSED_EVENT])"`
		} `graphql:"pullRequest(number: $number)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

// Conversation reads a pull request's description and its newest 500 timeline
// items. A pull request that does not exist is ErrNoPR.
func (c *Client) Conversation(ctx context.Context, r Repo, number int) (Conversation, error) {
	var (
		conv   Conversation
		pages  [][]timelineNode
		cursor *graphql.String
	)

	for page := range timelineMaxPages {
		var q conversationQuery

		vars := map[string]any{
			"owner": graphql.String(r.Owner), "name": graphql.String(r.Name),
			"number": graphql.Int(number), //nolint:gosec // pull request numbers fit int32
			"cursor": cursor,
		}
		if err := c.query(ctx, "Conversation", &q, vars); err != nil {
			return Conversation{}, err
		}

		pr := q.Repository.PullRequest
		if pr == nil {
			return Conversation{}, ErrNoPR
		}

		if page == 0 {
			conv.PR = pr.pr()
			conv.Created = pr.CreatedAt
			conv.Body = newText(pr.Body, pr.BodyHTML)
		}

		pages = append(pages, pr.TimelineItems.Nodes)

		info := pr.TimelineItems.PageInfo
		if !info.HasPreviousPage || info.StartCursor == "" {
			conv.Cut = info.HasPreviousPage

			break
		}

		if page == timelineMaxPages-1 {
			conv.Cut = true

			break
		}

		next := graphql.String(info.StartCursor)
		cursor = &next
	}

	var nodes []timelineNode
	for i := len(pages) - 1; i >= 0; i-- {
		nodes = append(nodes, pages[i]...)
	}

	conv.Items = timelineItems(nodes)

	return conv, nil
}

func timelineItems(nodes []timelineNode) []TimelineItem {
	var items []TimelineItem

	for _, n := range nodes {
		switch n.Typename {
		case "IssueComment":
			c := n.IssueComment
			it := TimelineItem{Kind: ItemComment, ID: c.ID, At: c.CreatedAt, Body: newText(c.Body, c.BodyHTML)}
			it.Author, it.Bot = authorOf(c.Author)
			items = append(items, it)
		case "PullRequestReview":
			rv := n.Review
			if rv.State == "PENDING" {
				continue
			}

			it := TimelineItem{
				Kind: ItemReview, ID: rv.ID, At: rv.CreatedAt, State: rv.State,
				Body: newText(rv.Body, rv.BodyHTML),
			}
			it.Author, it.Bot = authorOf(rv.Author)

			for _, c := range rv.Comments.Nodes {
				rc := ReviewComment{Path: c.Path, Hunk: c.DiffHunk, Body: newText(c.Body, c.BodyHTML)}

				switch {
				case c.Line != nil:
					rc.Line = *c.Line
				case c.OriginalLine != nil:
					rc.Line = *c.OriginalLine
				}

				it.Comments = append(it.Comments, rc)
			}

			items = append(items, it)
		case "PullRequestCommit":
			cm := n.PRCommit.Commit

			if last := len(items) - 1; last >= 0 && items[last].Kind == ItemCommits {
				items[last].Commits++
				setPushAuthor(&items[last], cm.Author)
				items[last].ID, items[last].At = cm.Oid, cm.CommittedDate

				continue
			}

			it := TimelineItem{Kind: ItemCommits, ID: cm.Oid, At: cm.CommittedDate, Commits: 1}
			setPushAuthor(&it, cm.Author)
			items = append(items, it)
		case "HeadRefForcePushedEvent":
			f := n.ForcePush
			it := TimelineItem{Kind: ItemForcePush, ID: f.ID, At: f.CreatedAt}
			it.Author, it.Bot = authorOf(f.Actor)

			if f.Ref != nil {
				it.Ref = f.Ref.Name
			}

			if f.BeforeCommit != nil {
				it.Before = f.BeforeCommit.AbbreviatedOid
			}

			if f.AfterCommit != nil {
				it.After = f.AfterCommit.AbbreviatedOid
			}

			items = append(items, it)
		case "ReviewRequestedEvent":
			rq := n.ReviewRequest
			it := TimelineItem{Kind: ItemReviewRequest, ID: rq.ID, At: rq.CreatedAt, Reviewer: reviewerName(rq.Reviewer)}
			it.Author, it.Bot = authorOf(rq.Actor)
			items = append(items, it)
		case "MergedEvent":
			m := n.Merged
			it := TimelineItem{Kind: ItemMerged, ID: m.ID, At: m.CreatedAt, Base: m.MergeRef}
			it.Author, it.Bot = authorOf(m.Actor)

			if m.MergeCommit != nil {
				it.Commit = m.MergeCommit.AbbreviatedOid
			}

			items = append(items, it)
		case "ClosedEvent":
			cl := n.Closed
			it := TimelineItem{Kind: ItemClosed, ID: cl.ID, At: cl.CreatedAt}
			it.Author, it.Bot = authorOf(cl.Actor)
			items = append(items, it)
		}
	}

	return items
}

// setPushAuthor names a push after its last commit's GitHub user, else the
// commit's git author name.
func setPushAuthor(it *TimelineItem, a *struct {
	Name string
	User *struct{ Login string }
},
) {
	it.Author, it.Named, it.Bot = "ghost", false, false

	switch {
	case a == nil:
	case a.User != nil && a.User.Login != "":
		it.Author = a.User.Login
	case a.Name != "":
		it.Author, it.Named = a.Name, true
	}
}

// authorOf returns an actor's login, with [bot] added for a bot as GitHub's
// page writes it, and whether it is a bot.
func authorOf(a *actor) (string, bool) {
	if a == nil || a.Login == "" {
		return "ghost", false
	}

	if a.Typename == "Bot" {
		return botLogin(a.Login), true
	}

	return a.Login, false
}

func botLogin(login string) string {
	if strings.HasSuffix(login, "[bot]") {
		return login
	}

	return login + "[bot]"
}

func reviewerName(r *reviewerNode) string {
	if r == nil {
		return "ghost"
	}

	var name string

	switch r.Typename {
	case "Team":
		name = r.Team.CombinedSlug
	case "Bot":
		if r.Bot.Login != "" {
			name = botLogin(r.Bot.Login)
		}
	case "Mannequin":
		name = r.Mannequin.Login
	default:
		name = r.User.Login
	}

	if name == "" {
		return "ghost"
	}

	return name
}

func newText(markdown, htmlBody string) Text {
	return Text{Markdown: markdown, Images: signedImages(markdown, htmlBody)}
}

var (
	mdImageRe = regexp.MustCompile(`!\[[^\]]*\]\(\s*<?([^)\s>]+)`)
	imgSrcRe  = regexp.MustCompile(`(?i)<img\b[^>]*?\ssrc\s*=\s*(?:"([^"]*)"|'([^']*)')`)
)

// signedImages maps each image source in markdown to the signed URL that the
// rendered html gives it on the private image host. A signed URL matches a
// source by the last path element, without its extension: the source's must
// equal the signed one's or end it after a '-'.
func signedImages(markdown, htmlBody string) map[string]string {
	var signed []string

	for _, m := range imgSrcRe.FindAllStringSubmatch(htmlBody, -1) {
		src := html.UnescapeString(m[1] + m[2])

		u, err := url.Parse(src)
		if err != nil || u.Scheme != "https" || u.Host != signedImageHost {
			continue
		}

		signed = append(signed, src)
	}

	if len(signed) == 0 {
		return nil
	}

	var sources []string

	for _, m := range mdImageRe.FindAllStringSubmatch(markdown, -1) {
		sources = append(sources, m[1])
	}

	for _, m := range imgSrcRe.FindAllStringSubmatch(markdown, -1) {
		sources = append(sources, html.UnescapeString(m[1]+m[2]))
	}

	var out map[string]string

	for _, src := range sources {
		key := imageKey(src)
		if key == "" {
			continue
		}

		for _, s := range signed {
			name := imageKey(s)
			if name == key || strings.HasSuffix(name, "-"+key) {
				if out == nil {
					out = map[string]string{}
				}

				out[src] = s

				break
			}
		}
	}

	return out
}

// imageKey is the last path element of an image URL without its extension.
func imageKey(src string) string {
	u, err := url.Parse(src)
	if err != nil {
		return ""
	}

	base := path.Base(u.Path)
	if base == "." || base == "/" {
		return ""
	}

	return strings.TrimSuffix(base, path.Ext(base))
}

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// maxCheckPages bounds how many pages of check runs one call reads.
const maxCheckPages = 5

// maxCheckBody bounds one response body.
const maxCheckBody = 8 << 20

// Check is one check run or commit status of a commit.
type Check struct {
	ID                     int64  // a run's id, which is its job id for an Actions job; 0 for a status
	App                    string // a run's app slug; "" for a status
	Name, Kind, URL        string // Kind "run" or "status"
	State                  CheckState
	Conclusion             string // GitHub's raw conclusion or status state
	StartedAt, CompletedAt time.Time
}

type checkRunsPage struct {
	CheckRuns []struct {
		ID  int64 `json:"id"`
		App struct {
			Slug string `json:"slug"`
		} `json:"app"`
		Name        string    `json:"name"`
		Status      string    `json:"status"`
		Conclusion  string    `json:"conclusion"`
		StartedAt   time.Time `json:"started_at"`
		CompletedAt time.Time `json:"completed_at"`
		HTMLURL     string    `json:"html_url"`
		DetailsURL  string    `json:"details_url"`
	} `json:"check_runs"`
}

type combinedStatus struct {
	Statuses []struct {
		Context   string    `json:"context"`
		State     string    `json:"state"`
		TargetURL string    `json:"target_url"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	} `json:"statuses"`
}

// Checks returns the check runs of commit sha in API order, then its commit
// statuses by name. Runs are read over at most five pages.
func (c *Client) Checks(ctx context.Context, r Repo, sha string) ([]Check, error) {
	if sha == "" {
		return nil, ErrNoSHA
	}

	base := fmt.Sprintf("repos/%s/%s/commits/%s", url.PathEscape(r.Owner), url.PathEscape(r.Name), url.PathEscape(sha))

	var checks []Check

	next := base + "/check-runs?per_page=100"

	for page := 0; page < maxCheckPages && next != ""; page++ {
		var body checkRunsPage

		link, err := c.getPage(ctx, next, &body)
		if err != nil {
			return nil, err
		}

		next = link

		for _, run := range body.CheckRuns {
			u := run.HTMLURL
			if u == "" {
				u = run.DetailsURL
			}

			conclusion := run.Conclusion
			if conclusion == "" {
				conclusion = run.Status
			}

			checks = append(checks, Check{
				ID: run.ID, App: run.App.Slug, Name: run.Name, Kind: "run", URL: u, State: runState(run.Status, run.Conclusion),
				Conclusion: conclusion, StartedAt: run.StartedAt, CompletedAt: run.CompletedAt,
			})
		}
	}

	var status combinedStatus
	if _, err := c.getPage(ctx, base+"/status?per_page=100", &status); err != nil {
		return nil, err
	}

	statuses := make([]Check, 0, len(status.Statuses))

	for _, s := range status.Statuses {
		st := statusState(s.State)

		ch := Check{
			Name: s.Context, Kind: "status", URL: s.TargetURL, State: st,
			Conclusion: s.State, StartedAt: s.CreatedAt,
		}
		if st != CheckPending {
			ch.CompletedAt = s.UpdatedAt
		}

		statuses = append(statuses, ch)
	}

	sort.SliceStable(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })

	return append(checks, statuses...), nil
}

// getPage decodes a GET into out and returns the next page's URL, which is
// empty at the last page. A next link to another origin is an error, so the
// token is never sent anywhere else.
func (c *Client) getPage(ctx context.Context, path string, out any) (string, error) {
	resp, err := c.restAPI.RequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", wrapAPI("github: GET "+pathOnly(path), redactURL(err), time.Now())
	}

	defer resp.Body.Close()

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxCheckBody)).Decode(out); err != nil {
		return "", fmt.Errorf("github: decode GET %s: %w", pathOnly(path), err)
	}

	next := nextLink(resp.Header.Get("Link"))
	if next == "" {
		return "", nil
	}

	nu, err := url.Parse(next)
	if err != nil || resp.Request == nil || !sameOrigin(nu, resp.Request.URL) {
		return "", errors.New("github: a next page link points to another origin")
	}

	return next, nil
}

func pathOnly(p string) string {
	before, _, _ := strings.Cut(p, "?")

	return before
}

func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

// nextLink returns the rel="next" target of a Link header, or "".
func nextLink(header string) string {
	for part := range strings.SplitSeq(header, ",") {
		target, params, ok := strings.Cut(part, ";")
		if !ok {
			continue
		}

		target = strings.TrimSpace(target)
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}

		for p := range strings.SplitSeq(params, ";") {
			if strings.ReplaceAll(strings.TrimSpace(p), " ", "") == `rel="next"` {
				return target[1 : len(target)-1]
			}
		}
	}

	return ""
}

func runState(status, conclusion string) CheckState {
	if status != "completed" {
		return CheckPending
	}

	switch conclusion {
	case "success":
		return CheckSuccess
	case "neutral", "skipped", "stale":
		return CheckSkipped
	default: // an unrecognised result must not read as green; the poll's counts use only the listed states
		return CheckFailure
	}
}

func statusState(state string) CheckState {
	switch state {
	case "success":
		return CheckSuccess
	case "pending":
		return CheckPending
	default: // an unrecognised state counts as failure on purpose
		return CheckFailure
	}
}

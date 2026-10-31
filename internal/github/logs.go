package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

const (
	// JobLogTail is how much of the end of a job log JobLog keeps.
	JobLogTail = 1 << 20

	// jobLogReadMax bounds what is read from a server that ignores Range.
	jobLogReadMax = 64 << 20
)

// ErrJobLogTooLarge is returned for a log of more than jobLogReadMax bytes
// from a server that ignores Range.
var ErrJobLogTooLarge = errors.New("github: job log too large")

// CheckRunOutput is the output a check run reports.
type CheckRunOutput struct{ Title, Summary string }

// headerKey keys the headers a request's context asks a headerTransport to set.
type headerKey struct{}

// withHeader returns ctx asking headerTransport to set key on every request
// made under it, redirect hops included.
func withHeader(ctx context.Context, key, value string) context.Context {
	h := http.Header{}
	if old, ok := ctx.Value(headerKey{}).(http.Header); ok {
		h = old.Clone()
	}

	h.Set(key, value)

	return context.WithValue(ctx, headerKey{}, h)
}

// headerTransport sets the headers withHeader put in the context. net/http
// copies only the first request's own headers to a redirect, so a header that
// must reach the redirect target rides in the context instead.
type headerTransport struct{ next http.RoundTripper }

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	h, ok := req.Context().Value(headerKey{}).(http.Header)
	if !ok {
		return t.next.RoundTrip(req)
	}

	out := req.Clone(req.Context())
	for k, v := range h {
		out.Header[k] = append([]string(nil), v...)
	}

	return t.next.RoundTrip(out)
}

// JobLog reads the last JobLogTail bytes of the log of Actions job jobID. cut
// reports that earlier bytes were left out. A log GitHub has no bytes for
// (416) is empty, not an error. The storage host GitHub redirects to gets no
// credentials, and no error holds its signed URL.
func (c *Client) JobLog(ctx context.Context, r Repo, jobID int64) (data []byte, cut bool, err error) {
	path := fmt.Sprintf("repos/%s/%s/actions/jobs/%d/logs", url.PathEscape(r.Owner), url.PathEscape(r.Name), jobID)
	ctx = withHeader(ctx, "Range", "bytes=-"+strconv.Itoa(JobLogTail))

	resp, err := c.restAPI.RequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		var he *api.HTTPError
		if errors.As(err, &he) && he.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			return nil, false, nil
		}

		return nil, false, wrapAPI("github: GET "+path, redactURL(err), time.Now())
	}

	defer resp.Body.Close()

	data, total, err := readTail(resp.Body, JobLogTail, jobLogReadMax)
	if err != nil {
		return nil, false, fmt.Errorf("github: read GET %s: %w", path, redactURL(err))
	}

	if resp.StatusCode == http.StatusPartialContent {
		if start, ok := contentRangeStart(resp.Header.Get("Content-Range")); ok {
			return data, start > 0, nil
		}

		return data, total >= JobLogTail, nil
	}

	return data, total > JobLogTail, nil
}

// readTail reads r keeping its last keep bytes; total counts every byte read.
// More than limit bytes is ErrJobLogTooLarge.
func readTail(r io.Reader, keep, limit int) (tail []byte, total int, err error) {
	buf := make([]byte, 0, min(2*keep, 64<<10))
	chunk := make([]byte, 32<<10)

	for {
		n, rerr := r.Read(chunk)
		buf = append(buf, chunk[:n]...)
		total += n

		if total > limit {
			return nil, total, ErrJobLogTooLarge
		}

		if len(buf) >= 2*keep {
			buf = append(buf[:0], buf[len(buf)-keep:]...)
		}

		if rerr == io.EOF {
			break
		}

		if rerr != nil {
			return nil, total, rerr
		}
	}

	if len(buf) > keep {
		buf = buf[len(buf)-keep:]
	}

	return buf, total, nil
}

// contentRangeStart parses the first byte of "bytes S-E/T".
func contentRangeStart(v string) (int64, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(v), "bytes ")
	if !ok {
		return 0, false
	}

	first, _, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, false
	}

	start, err := strconv.ParseInt(first, 10, 64)

	return start, err == nil && start >= 0
}

// redactURL removes the query from the request URL an error names, since a
// log redirect's query is a signed credential.
func redactURL(err error) error {
	var he *api.HTTPError
	if errors.As(err, &he) && he.RequestURL != nil {
		cp := *he
		u := *he.RequestURL
		u.RawQuery, u.Fragment, u.User = "", "", nil
		cp.RequestURL = &u

		return &cp
	}

	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}

	return err
}

// CheckRun reads the output of check run id: the title and summary a check
// reports when it is not an Actions job.
func (c *Client) CheckRun(ctx context.Context, r Repo, id int64) (CheckRunOutput, error) {
	var body struct {
		Output struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
		} `json:"output"`
	}

	path := fmt.Sprintf("repos/%s/%s/check-runs/%d", url.PathEscape(r.Owner), url.PathEscape(r.Name), id)
	if _, err := c.getPage(ctx, path, &body); err != nil {
		return CheckRunOutput{}, err
	}

	return CheckRunOutput{Title: body.Output.Title, Summary: body.Output.Summary}, nil
}

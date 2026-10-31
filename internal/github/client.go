package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

const defaultTimeout = 30 * time.Second

// Options configure a Client.
type Options struct {
	Token     string            // required
	Transport http.RoundTripper // nil = http.DefaultTransport
	Timeout   time.Duration     // per request; 0 = 30 s
}

// Client talks to one GitHub host. It is safe for concurrent use.
type Client struct {
	host    string
	restAPI *api.RESTClient
	gqlAPI  *api.GraphQLClient
}

// NewClient builds a client for host, which must be named. Host, token and
// transport are always passed, so go-gh resolves none of them and never logs
// requests.
func NewClient(host string, o Options) (*Client, error) {
	if host == "" {
		return nil, fmt.Errorf("%w: no host", ErrNotGitHub)
	}

	if o.Token == "" {
		return nil, ErrNoToken
	}

	base := o.Transport
	if base == nil {
		base = http.DefaultTransport
	}

	timeout := o.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	etags := newETagTransport(base)
	opts := api.ClientOptions{
		Host:         host,
		AuthToken:    o.Token,
		Transport:    headerTransport{next: etags},
		LogIgnoreEnv: true,
		EnableCache:  false,
		Timeout:      timeout,
	}

	restClient, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, fmt.Errorf("github: rest client: %w", err)
	}

	opts.Transport = httpErrorTransport{next: etags}

	gqlClient, err := api.NewGraphQLClient(opts)
	if err != nil {
		return nil, fmt.Errorf("github: graphql client: %w", err)
	}

	return &Client{host: host, restAPI: restClient, gqlAPI: gqlClient}, nil
}

// resetKey keys the resetHolder a query puts in its request context.
type resetKey struct{}

// resetHolder receives the reset (unix seconds) of an exhausted rate limit
// from the GraphQL transport; 0 means none was seen.
type resetHolder struct{ unix atomic.Int64 }

// httpErrorTransport turns a non-2xx GraphQL response into *api.HTTPError;
// shurcooL-graphql drops the status and headers that rate limits need. It also
// records the reset of an exhausted limit on any status, since GitHub reports
// a GraphQL primary limit as a 200.
type httpErrorTransport struct{ next http.RoundTripper }

func (t httpErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if h, ok := req.Context().Value(resetKey{}).(*resetHolder); ok && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if unix, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			h.unix.Store(unix)
		}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}

	defer resp.Body.Close()

	if resp.Request == nil {
		resp.Request = req
	}

	return nil, api.HandleHTTPError(resp)
}

// rest sends a JSON request and decodes a JSON response into out, which may
// be nil.
func (c *Client) rest(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader

	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github: encode %s %s: %w", method, path, err)
		}

		r = bytes.NewReader(b)
	}

	err := c.restAPI.DoWithContext(ctx, method, path, r, out)

	return wrapAPI(fmt.Sprintf("github: %s %s", method, path), err, time.Now())
}

// query runs a struct-tagged GraphQL query; name labels it in errors.
func (c *Client) query(ctx context.Context, name string, q any, vars map[string]any) error {
	h := new(resetHolder)
	err := c.gqlAPI.QueryWithContext(context.WithValue(ctx, resetKey{}, h), name, q, vars)

	var reset time.Time
	if unix := h.unix.Load(); unix != 0 {
		reset = time.Unix(unix, 0)
	}

	if err == nil {
		return nil
	}

	return fmt.Errorf("github: query %s: %w", name, classifyAt(redactURL(err), time.Now(), reset))
}

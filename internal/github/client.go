package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// NewClient builds a client for host. Host, token and transport are always
// passed, so go-gh resolves none of them and never logs requests.
func NewClient(host string, o Options) (*Client, error) {
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
		Transport:    etags,
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

// httpErrorTransport turns a non-2xx GraphQL response into *api.HTTPError;
// shurcooL-graphql drops the status and headers that rate limits need.
type httpErrorTransport struct{ next http.RoundTripper }

func (t httpErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
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
	err := c.gqlAPI.QueryWithContext(ctx, name, q, vars)

	return wrapAPI("github: query "+name, err, time.Now())
}

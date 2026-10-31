package github

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

const testToken = "ghp_testtoken0123456789"

// routeTransport sends requests for one API host to a local server and fails
// the test for any other host, so no request leaves the process.
type routeTransport struct {
	t      *testing.T
	host   string
	target *url.URL
	next   *http.Transport
	others map[string]*url.URL // host to server, for a redirect's target

	mu   sync.Mutex
	reqs []*http.Request
}

func (rt *routeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target := rt.target

	if req.URL.Host != rt.host {
		other, ok := rt.others[req.URL.Host]
		if !ok {
			rt.t.Errorf("request to unexpected host %q", req.URL.Host)

			return nil, http.ErrUseLastResponse
		}

		target = other
	}

	rt.mu.Lock()
	rt.reqs = append(rt.reqs, req.Clone(req.Context()))
	rt.mu.Unlock()

	out := req.Clone(req.Context())
	out.URL.Scheme = target.Scheme
	out.URL.Host = target.Host

	resp, err := rt.next.RoundTrip(out)
	if err != nil {
		return nil, err
	}

	resp.Request = req

	return resp, nil
}

// newTestClient returns a client for host whose requests reach h. The
// recorded requests keep their original URLs.
func newTestClient(t *testing.T, host string, h http.Handler) (*Client, func() []*http.Request) {
	t.Helper()

	return newTestClientWith(t, host, h, nil)
}

// newTestClientWith is newTestClient with extra servers, keyed by the host a
// redirect points at.
func newTestClientWith(t *testing.T, host string, h http.Handler, others map[string]http.Handler) (*Client, func() []*http.Request) {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	apiHost := host
	if host == "github.com" {
		apiHost = "api.github.com"
	}

	rt := &routeTransport{t: t, host: apiHost, target: target, next: &http.Transport{}, others: map[string]*url.URL{}}

	for oh, oHandler := range others {
		osrv := httptest.NewServer(oHandler)
		t.Cleanup(osrv.Close)

		ou, err := url.Parse(osrv.URL)
		if err != nil {
			t.Fatal(err)
		}

		rt.others[oh] = ou
	}

	t.Cleanup(rt.next.CloseIdleConnections)

	c, err := NewClient(host, Options{Token: testToken, Transport: rt})
	if err != nil {
		t.Fatal(err)
	}

	return c, func() []*http.Request {
		rt.mu.Lock()
		defer rt.mu.Unlock()

		return append([]*http.Request(nil), rt.reqs...)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

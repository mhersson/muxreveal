package github

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeOrigin struct {
	etag    string
	body    string
	status  int
	lastINM string
	calls   int
}

func (o *fakeOrigin) RoundTrip(req *http.Request) (*http.Response, error) {
	o.calls++
	o.lastINM = req.Header.Get("If-None-Match")

	h := http.Header{}
	status := o.status

	if status == 0 {
		status = http.StatusOK
	}

	if o.etag != "" {
		h.Set("ETag", o.etag)
	}

	if o.etag != "" && o.lastINM == o.etag && req.Method == http.MethodGet && status == http.StatusOK {
		return &http.Response{StatusCode: 304, Header: h, Body: http.NoBody, Request: req}, nil
	}

	h.Set("Content-Type", "application/json")

	return &http.Response{
		StatusCode: status, Status: fmt.Sprint(status), Header: h, Request: req,
		Body: io.NopCloser(strings.NewReader(o.body)),
	}, nil
}

func get(t *testing.T, rt http.RoundTripper, method, url string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	return resp, string(b)
}

func TestETag(t *testing.T) {
	const u = "https://api.github.com/repos/o/r"

	t.Run("a 304 serves the cached body as a 200", func(t *testing.T) {
		o := &fakeOrigin{etag: `"a"`, body: `{"x":1}`}
		rt := newETagTransport(o)

		get(t, rt, http.MethodGet, u)

		if o.lastINM != "" {
			t.Errorf("first request sent If-None-Match %q", o.lastINM)
		}

		resp, body := get(t, rt, http.MethodGet, u)
		if o.lastINM != `"a"` {
			t.Errorf("second request If-None-Match = %q", o.lastINM)
		}

		if resp.StatusCode != 200 || body != `{"x":1}` || resp.Header.Get("ETag") != `"a"` {
			t.Errorf("got %d %q etag %q", resp.StatusCode, body, resp.Header.Get("ETag"))
		}

		if resp.Request == nil {
			t.Error("synthesized response has no request")
		}
	})

	t.Run("a new etag replaces the entry", func(t *testing.T) {
		o := &fakeOrigin{etag: `"a"`, body: `1`}
		rt := newETagTransport(o)

		get(t, rt, http.MethodGet, u)

		o.etag, o.body = `"b"`, `2`

		_, body := get(t, rt, http.MethodGet, u)
		if body != "2" {
			t.Fatalf("body = %q", body)
		}

		get(t, rt, http.MethodGet, u)

		if o.lastINM != `"b"` {
			t.Errorf("If-None-Match = %q, want the new etag", o.lastINM)
		}
	})

	t.Run("a POST carries no If-None-Match", func(t *testing.T) {
		o := &fakeOrigin{etag: `"a"`, body: `1`}
		rt := newETagTransport(o)

		get(t, rt, http.MethodGet, u)
		get(t, rt, http.MethodPost, u)

		if o.lastINM != "" {
			t.Errorf("POST sent If-None-Match %q", o.lastINM)
		}
	})

	t.Run("a 304 without an entry is an error", func(t *testing.T) {
		rt := newETagTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 304, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
		}))

		req, _ := http.NewRequest(http.MethodGet, u, nil)
		if resp, err := rt.RoundTrip(req); err == nil {
			_ = resp.Body.Close()

			t.Fatal("no error")
		}
	})

	t.Run("a 2 MiB body is served but not stored", func(t *testing.T) {
		big := strings.Repeat("x", 2<<20)
		o := &fakeOrigin{etag: `"a"`, body: big}
		rt := newETagTransport(o)

		if _, body := get(t, rt, http.MethodGet, u); body != big {
			t.Fatalf("body length %d", len(body))
		}

		get(t, rt, http.MethodGet, u)

		if o.lastINM != "" {
			t.Errorf("oversized body was stored: If-None-Match %q", o.lastINM)
		}
	})

	t.Run("the least recently used of 257 entries is evicted", func(t *testing.T) {
		o := &fakeOrigin{etag: `"a"`, body: `1`}
		rt := newETagTransport(o)

		for i := range 256 {
			get(t, rt, http.MethodGet, fmt.Sprintf("%s/%d", u, i))
		}

		get(t, rt, http.MethodGet, u+"/0") // a hit makes it the most recent
		get(t, rt, http.MethodGet, u+"/256")

		get(t, rt, http.MethodGet, u+"/0")

		if o.lastINM != `"a"` {
			t.Errorf("the recently used entry was evicted")
		}

		get(t, rt, http.MethodGet, u+"/1")

		if o.lastINM != "" {
			t.Errorf("the least recently used entry survived: If-None-Match %q", o.lastINM)
		}
	})

	t.Run("an error response keeps the cached entry", func(t *testing.T) {
		o := &fakeOrigin{etag: `"a"`, body: `1`}
		rt := newETagTransport(o)

		get(t, rt, http.MethodGet, u)

		o.status = http.StatusBadGateway

		get(t, rt, http.MethodGet, u)

		o.status = 0

		get(t, rt, http.MethodGet, u)

		if o.lastINM != `"a"` {
			t.Errorf("If-None-Match = %q, the error evicted the entry", o.lastINM)
		}
	})

	t.Run("a cache hit hands out independent headers", func(t *testing.T) {
		o := &fakeOrigin{etag: `"a"`, body: `1`}
		rt := newETagTransport(o)

		get(t, rt, http.MethodGet, u)

		r1, _ := get(t, rt, http.MethodGet, u)
		r1.Header.Set("ETag", "mutated")

		r2, _ := get(t, rt, http.MethodGet, u)
		if r2.Header.Get("ETag") != `"a"` {
			t.Error("a hit shares headers with an earlier response")
		}
	})
}

package github

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
)

const (
	etagMaxBody    = 1 << 20
	etagMaxEntries = 256
)

// etagTransport answers conditional GETs from an LRU cache. It is safe for
// concurrent use.
type etagTransport struct {
	next http.RoundTripper

	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // front = most recently used
}

type etagEntry struct {
	key    string
	etag   string
	header http.Header
	body   []byte
}

func newETagTransport(next http.RoundTripper) *etagTransport {
	return &etagTransport{next: next, entries: map[string]*list.Element{}, order: list.New()}
}

func (t *etagTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.next.RoundTrip(req)
	}

	key := req.URL.String()
	cached := t.lookup(key)

	out := req
	if cached != nil {
		out = req.Clone(req.Context())
		out.Header.Set("If-None-Match", cached.etag)
	}

	resp, err := t.next.RoundTrip(out)
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode == http.StatusNotModified:
		_ = resp.Body.Close()

		if cached == nil {
			return nil, errors.New("github: 304 without a cached response")
		}

		return cached.response(req), nil
	case resp.StatusCode == http.StatusOK && resp.Header.Get("ETag") != "":
		return t.store(key, resp)
	case resp.StatusCode == http.StatusOK:
		t.remove(key)

		return resp, nil
	default:
		return resp, nil
	}
}

func (e *etagEntry) response(req *http.Request) *http.Response {
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        e.header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(e.body)),
		ContentLength: int64(len(e.body)),
		Request:       req,
	}
}

// store caches a 200 response when its body is small enough; either way the
// caller still gets the full body.
func (t *etagTransport) store(key string, resp *http.Response) (*http.Response, error) {
	buf, err := io.ReadAll(io.LimitReader(resp.Body, etagMaxBody+1))
	if err != nil {
		_ = resp.Body.Close()

		return nil, fmt.Errorf("github: read response: %w", err)
	}

	if len(buf) > etagMaxBody {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(buf), resp.Body), resp.Body}

		t.remove(key)

		return resp, nil
	}

	_ = resp.Body.Close()

	e := &etagEntry{key: key, etag: resp.Header.Get("ETag"), header: resp.Header.Clone(), body: buf}
	t.put(e)

	resp.Body = io.NopCloser(bytes.NewReader(buf))
	resp.ContentLength = int64(len(buf))
	resp.Header.Set("Content-Length", strconv.Itoa(len(buf)))

	return resp, nil
}

func (t *etagTransport) lookup(key string) *etagEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	el, ok := t.entries[key]
	if !ok {
		return nil
	}

	t.order.MoveToFront(el)

	return el.Value.(*etagEntry)
}

func (t *etagTransport) put(e *etagEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if el, ok := t.entries[e.key]; ok {
		el.Value = e
		t.order.MoveToFront(el)

		return
	}

	t.entries[e.key] = t.order.PushFront(e)

	for t.order.Len() > etagMaxEntries {
		oldest := t.order.Back()
		t.order.Remove(oldest)
		delete(t.entries, oldest.Value.(*etagEntry).key)
	}
}

func (t *etagTransport) remove(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if el, ok := t.entries[key]; ok {
		t.order.Remove(el)
		delete(t.entries, key)
	}
}

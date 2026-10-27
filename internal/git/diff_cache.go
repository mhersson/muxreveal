package git

import (
	"container/list"
	"sync"
)

const (
	cacheEntries = 2048
	cacheBytes   = 64 << 20 // line text held by the cache
)

type hunkKey struct{ old, new string }

type cacheItem struct {
	key   hunkKey
	hunks []Hunk
	size  int
}

// hunkCache is an LRU of hunks by (old blob id, new blob id), bounded by entry
// count and by the line text it holds.
type hunkCache struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int
	bytes      int
	order      *list.List // front = most recently used
	items      map[hunkKey]*list.Element
}

func newHunkCache(maxEntries, maxBytes int) *hunkCache {
	return &hunkCache{
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
		order:      list.New(),
		items:      map[hunkKey]*list.Element{},
	}
}

func (c *hunkCache) get(k hunkKey) ([]Hunk, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[k]
	if !ok {
		return nil, false
	}

	c.order.MoveToFront(el)

	return el.Value.(*cacheItem).hunks, true
}

func (c *hunkCache) put(k hunkKey, hunks []Hunk) {
	size := hunksSize(hunks)

	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[k]; ok {
		c.drop(el)
	}

	if size > c.maxBytes {
		return
	}

	c.items[k] = c.order.PushFront(&cacheItem{key: k, hunks: hunks, size: size})
	c.bytes += size

	for c.order.Len() > c.maxEntries || c.bytes > c.maxBytes {
		c.drop(c.order.Back())
	}
}

func (c *hunkCache) drop(el *list.Element) {
	it := c.order.Remove(el).(*cacheItem)
	delete(c.items, it.key)
	c.bytes -= it.size
}

func hunksSize(hunks []Hunk) int {
	n := 0

	for _, h := range hunks {
		n += len(h.Section)

		for _, l := range h.Lines {
			n += len(l.Text) + 32
		}
	}

	return n
}

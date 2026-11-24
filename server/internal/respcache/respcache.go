// Package respcache keeps upstream responses in memory so repeated identical
// requests do not spend quota. It is a byte-bounded LRU with per-entry expiry.
package respcache

import (
	"container/list"
	"net/http"
	"sync"
	"time"
)

// Entry is a stored response.
type Entry struct {
	Status  int
	Header  http.Header
	Body    []byte
	KeyID   int64
	Expires time.Time
	// Fingerprint identifies the request that made an idempotent entry (a
	// hash of its body), so a reused key with another request is caught.
	Fingerprint string
}

func (e *Entry) size() int64 {
	n := int64(len(e.Body)) + 64
	for k, vs := range e.Header {
		n += int64(len(k))
		for _, v := range vs {
			n += int64(len(v))
		}
	}
	return n
}

type item struct {
	key   string
	entry *Entry
	size  int64
}

type Stats struct {
	Entries  int   `json:"entries"`
	Bytes    int64 `json:"bytes"`
	MaxBytes int64 `json:"max_bytes"`
	Hits     int64 `json:"hits"`
	Misses   int64 `json:"misses"`
}

type Cache struct {
	mu       sync.Mutex
	ll       *list.List
	m        map[string]*list.Element
	bytes    int64
	maxBytes int64
	hits     int64
	misses   int64
	now      func() time.Time
	// claims are keys a request is filling right now (Claim).
	claims map[string]struct{}
}

// New makes a cache holding at most maxBytes. maxBytes <= 0 disables it.
func New(maxBytes int64) *Cache {
	return &Cache{ll: list.New(), m: map[string]*list.Element{}, maxBytes: maxBytes, now: time.Now}
}

func (c *Cache) Enabled() bool { return c != nil && c.maxBytes > 0 }

// Get returns a live entry. The caller must not modify it.
func (c *Cache) Get(key string) (*Entry, bool) {
	if !c.Enabled() {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[key]
	if !ok {
		c.misses++
		return nil, false
	}
	it := el.Value.(*item)
	if !it.entry.Expires.After(c.now()) {
		c.removeLocked(el)
		c.misses++
		return nil, false
	}
	c.ll.MoveToFront(el)
	c.hits++
	return it.entry, true
}

// Set stores an entry for ttl. Entries larger than a quarter of the cache are skipped.
func (c *Cache) Set(key string, e *Entry, ttl time.Duration) {
	if !c.Enabled() || ttl <= 0 {
		return
	}
	e.Expires = c.now().Add(ttl)
	sz := e.size() + int64(len(key))
	if sz > c.maxBytes/4 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[key]; ok {
		c.removeLocked(el)
	}
	el := c.ll.PushFront(&item{key: key, entry: e, size: sz})
	c.m[key] = el
	c.bytes += sz
	for c.bytes > c.maxBytes {
		back := c.ll.Back()
		if back == nil {
			break
		}
		c.removeLocked(back)
	}
}

// Claim marks key as being filled by one request. It returns false when
// another request holds the key; otherwise release must be called when the
// request ends, whether or not it stored anything. Claims work even when
// the cache is disabled, and they live only in this process.
func (c *Cache) Claim(key string) (release func(), ok bool) {
	if c == nil {
		return func() {}, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, held := c.claims[key]; held {
		return nil, false
	}
	if c.claims == nil {
		c.claims = map[string]struct{}{}
	}
	c.claims[key] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.claims, key)
			c.mu.Unlock()
		})
	}, true
}

// PurgePrefix drops every entry whose key starts with prefix, for example
// all entries of one service after its settings change.
func (c *Cache) PurgePrefix(prefix string) int {
	if !c.Enabled() {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for k, el := range c.m {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			c.removeLocked(el)
			n++
		}
	}
	return n
}

func (c *Cache) removeLocked(el *list.Element) {
	it := el.Value.(*item)
	c.ll.Remove(el)
	delete(c.m, it.key)
	c.bytes -= it.size
}

func (c *Cache) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{Entries: c.ll.Len(), Bytes: c.bytes, MaxBytes: c.maxBytes, Hits: c.hits, Misses: c.misses}
}

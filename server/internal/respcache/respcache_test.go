package respcache

import (
	"strings"
	"testing"
	"time"
)

func TestHitMissAndExpiry(t *testing.T) {
	c := New(1 << 20)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	if _, ok := c.Get("a"); ok {
		t.Fatal("empty cache should miss")
	}
	c.Set("a", &Entry{Status: 200, Body: []byte("hello")}, time.Minute)
	e, ok := c.Get("a")
	if !ok || string(e.Body) != "hello" {
		t.Fatal("want a hit")
	}
	now = now.Add(61 * time.Second)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expired entries must miss")
	}
	st := c.Stats()
	if st.Hits != 1 || st.Misses != 2 || st.Entries != 0 {
		t.Fatalf("stats wrong: %+v", st)
	}
}

func TestEvictsLeastRecentlyUsed(t *testing.T) {
	c := New(1000)
	body := []byte(strings.Repeat("x", 180))
	c.Set("a", &Entry{Body: body}, time.Hour)
	c.Set("b", &Entry{Body: body}, time.Hour)
	c.Set("c", &Entry{Body: body}, time.Hour)
	c.Get("a") // a is now most recent, b is the oldest
	c.Set("d", &Entry{Body: body}, time.Hour)
	c.Set("e", &Entry{Body: body}, time.Hour)
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted first")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a was used recently and should survive")
	}
	if c.Stats().Bytes > 1000 {
		t.Fatal("cache exceeded its byte cap")
	}
}

func TestSkipsHugeEntriesAndDisabled(t *testing.T) {
	c := New(1000)
	c.Set("big", &Entry{Body: make([]byte, 600)}, time.Hour)
	if _, ok := c.Get("big"); ok {
		t.Fatal("entries over a quarter of the cache are not stored")
	}
	off := New(0)
	off.Set("a", &Entry{Body: []byte("x")}, time.Hour)
	if _, ok := off.Get("a"); ok {
		t.Fatal("a zero-size cache stores nothing")
	}
}

func TestPurgePrefix(t *testing.T) {
	c := New(1 << 20)
	c.Set("svc1|a", &Entry{}, time.Hour)
	c.Set("svc1|b", &Entry{}, time.Hour)
	c.Set("svc2|a", &Entry{}, time.Hour)
	if n := c.PurgePrefix("svc1|"); n != 2 {
		t.Fatalf("want 2 purged, got %d", n)
	}
	if _, ok := c.Get("svc2|a"); !ok {
		t.Fatal("other services keep their entries")
	}
}

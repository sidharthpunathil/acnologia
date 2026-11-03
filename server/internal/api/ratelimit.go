package api

import (
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// clientLimit is a windowLimit for each client plus one for everyone.
//
// The client's own budget is what stops a single source: it runs out and
// only that source waits. The global one is a ceiling on many sources at
// once (rotated addresses, or a spoofed X-Forwarded-For when the proxy
// passes it through), and is kept short, a minute or a capped day of misses,
// so a flood can make others wait but never lock the owner out for a day.
//
// Clients are keyed by clientKey. Behind a reverse proxy without
// ACNOLOGIA_TRUST_PROXY_HEADERS every client shares the proxy's address and
// so one bucket, which is the old global behaviour.
type clientLimit struct {
	mu      sync.Mutex
	each    windowLimit // the limits a new client starts with
	all     windowLimit
	clients map[string]*clientWindow
}

type clientWindow struct {
	windowLimit
	last time.Time
}

// clientLimitCap bounds memory: at most this many clients are tracked. When
// it is full of live entries the least recently seen client is forgotten,
// which only helps that client; the global ceiling still holds.
const clientLimitCap = 10_000

func newClientLimit(perMin, perDay, allMin, allDay int) *clientLimit {
	return &clientLimit{
		each:    windowLimit{perMin: perMin, perDay: perDay},
		all:     windowLimit{perMin: allMin, perDay: allDay},
		clients: map[string]*clientWindow{},
	}
}

// allow reports whether key still has budget of its own, without counting.
// Use it before a lookup, so a source that spent its budget is refused
// unseen whatever it sends.
func (c *clientLimit) allow(key string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	cw := c.clients[key]
	return cw == nil || cw.ok(now)
}

// ok reports whether key and everyone together still have budget, without
// counting. wait is how long until the tightest exhausted limit resets.
func (c *clientLimit) ok(key string, now time.Time) (bool, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var wait time.Duration
	if cw := c.clients[key]; cw != nil && !cw.ok(now) {
		wait = cw.wait(now)
	}
	if !c.all.ok(now) {
		wait = max(wait, c.all.wait(now))
	}
	return wait == 0, wait
}

// take counts one hit for key and for everyone, and reports whether both
// were within their limits. A refused hit is not counted.
func (c *clientLimit) take(key string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	cw := c.clients[key]
	if cw != nil && !cw.ok(now) {
		return false
	}
	if !c.all.ok(now) {
		return false
	}
	if cw == nil {
		c.makeRoom(now)
		cw = &clientWindow{windowLimit: windowLimit{perMin: c.each.perMin, perDay: c.each.perDay}}
		cw.ok(now) // start its windows at now
		c.clients[key] = cw
	}
	cw.last = now
	cw.take(now)
	c.all.take(now)
	return true
}

// makeRoom drops clients whose windows have all passed, and when the map is
// still full, the least recently seen one. Caller holds mu.
func (c *clientLimit) makeRoom(now time.Time) {
	if len(c.clients) < clientLimitCap {
		return
	}
	var oldest string
	var oldestAt time.Time
	for k, cw := range c.clients {
		if cw.idle(now) {
			delete(c.clients, k)
			continue
		}
		if oldest == "" || cw.last.Before(oldestAt) {
			oldest, oldestAt = k, cw.last
		}
	}
	if len(c.clients) >= clientLimitCap && oldest != "" {
		delete(c.clients, oldest)
	}
}

// clientKey is the client address a limiter counts against: clientIP, with
// an IPv6 address cut to its /64, since one host usually holds the whole
// /64 and could otherwise mint a fresh key per request.
func clientKey(r *http.Request) string {
	ip := clientIP(r)
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.WithZone("").Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}

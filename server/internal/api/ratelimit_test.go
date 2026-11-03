package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLoginLimitHoldsUnderBurst: the limit is checked and counted in one
// step before bcrypt, so a parallel burst of wrong passwords for one account
// gets at most loginMaxFails password checks.
func TestLoginLimitHoldsUnderBurst(t *testing.T) {
	e := newEnv(t)
	const n = 50
	codes := make([]int, n)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := 0; i < n; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			body := strings.NewReader(fmt.Sprintf(`{"email":"root@x.io","password":"wrong-%d"}`, i))
			req, _ := http.NewRequest("POST", e.srv.URL+"/api/auth/login", body)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
			codes[i] = resp.StatusCode
		}(i)
	}
	start.Done()
	done.Wait()
	checked := 0
	for _, c := range codes {
		switch c {
		case 401:
			checked++
		case 429:
		default:
			t.Fatalf("unexpected status %d", c)
		}
	}
	if checked > loginMaxFails {
		t.Fatalf("%d passwords were checked in a burst, want at most %d", checked, loginMaxFails)
	}
	e.do(client(), "POST", "/api/auth/login", map[string]any{"email": "root@x.io", "password": "wrong-again"}, 429, nil)
}

// TestLoginLimiterNeverFailsOpen: with the address table full of live
// entries, an account's counter still counts, and so does a known address
// and a new one.
func TestLoginLimiterNeverFailsOpen(t *testing.T) {
	l := newLoginLimiter()
	now := time.Unix(1_800_000_000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < limiterCap; i++ {
		if !l.reserve(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255)) {
			t.Fatalf("fill %d refused", i)
		}
	}
	now = now.Add(time.Second)
	for i := 0; i < loginMaxFails; i++ {
		if !l.reserve(fmt.Sprintf("192.0.2.%d", i), "email:victim@x.io") {
			t.Fatalf("attempt %d refused early", i)
		}
	}
	if l.allow("email:victim@x.io") || l.reserve("192.0.2.200", "email:victim@x.io") {
		t.Fatal("the account counter stopped counting with the address table full")
	}
	// A known address keeps counting while the table is full.
	for i := 1; i < loginMaxFails; i++ {
		l.reserve("192.0.2.0")
	}
	if l.reserve("192.0.2.0") {
		t.Fatal("a known address stopped counting with the address table full")
	}
	if len(l.hits) > limiterCap {
		t.Fatalf("address table grew past its cap: %d", len(l.hits))
	}
}

func TestClientKeyGroupsIPv6By64(t *testing.T) {
	for addr, want := range map[string]string{
		"203.0.113.9:4000":               "203.0.113.9",
		"[2001:db8:1:2:aaaa::1]:4000":    "2001:db8:1:2::/64",
		"[2001:db8:1:2:bbbb::9]:4000":    "2001:db8:1:2::/64",
		"[::ffff:203.0.113.9]:4000":      "203.0.113.9",
		"[fe80::1%en0]:4000":             "fe80::/64",
		"not an address":                 "not an address",
		"[2001:db8:1:3::1]:4000":         "2001:db8:1:3::/64",
		"[2001:db8:ffff:ffff::1]:65535":  "2001:db8:ffff:ffff::/64",
		"[2001:db8:ffff:ffff:1::1]:1234": "2001:db8:ffff:ffff::/64",
	} {
		r := &http.Request{RemoteAddr: addr, Header: http.Header{}}
		if got := clientKey(r); got != want {
			t.Errorf("%s: got %q want %q", addr, got, want)
		}
	}
}

// TestClientLimit: a client that spends its budget waits alone, the global
// ceiling holds across clients, and the table stays bounded.
func TestClientLimit(t *testing.T) {
	c := newClientLimit(2, 3, 5, 0)
	now := time.Unix(1_800_000_000, 0)
	if !c.take("a", now) || !c.take("a", now) || c.take("a", now) {
		t.Fatal("per client minute limit")
	}
	if c.allow("a", now) || !c.allow("b", now) {
		t.Fatal("allow should refuse only the spent client")
	}
	now = now.Add(time.Minute)
	if !c.take("a", now) || c.take("a", now) {
		t.Fatal("per client daily limit")
	}
	if ok, wait := c.ok("a", now); ok || wait <= time.Minute {
		t.Fatalf("daily wait = %v", wait)
	}
	// Global: 5 a minute across clients ("a" took one), then refused for
	// everyone.
	for i := 0; i < 4; i++ {
		if !c.take(fmt.Sprint("g", i), now) {
			t.Fatalf("global take %d refused", i)
		}
	}
	if c.take("fresh", now) {
		t.Fatal("global minute ceiling did not hold")
	}
	now = now.Add(time.Minute)
	if !c.take("fresh", now) {
		t.Fatal("global ceiling outlived its minute")
	}
	// Bounded memory.
	big := newClientLimit(1, 1, clientLimitCap*2, 0)
	for i := 0; i < clientLimitCap+10; i++ {
		big.take(fmt.Sprint("k", i), now)
	}
	if len(big.clients) > clientLimitCap {
		t.Fatalf("client table grew past its cap: %d", len(big.clients))
	}
}

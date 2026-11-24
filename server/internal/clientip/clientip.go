// Package clientip finds the client IP behind a reverse proxy without
// trusting headers a client controls.
package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// The client IP behind a reverse proxy. The per address limiters (sign in,
// lock screen, CLI sign in, invites, claim links) key on it, so a client
// must not be able to choose it.
//
// chi's RealIP middleware trusts True-Client-IP, then X-Real-IP, then the
// leftmost X-Forwarded-For entry. A client can send all three, and most
// proxies pass them through or append to them, so it could pick a fresh
// address for every attempt. Here, with ACNOLOGIA_TRUST_PROXY_HEADERS on:
//
//   - X-Forwarded-For is read from the right. Each proxy appends the address
//     it saw, so the entry ACNOLOGIA_TRUSTED_PROXY_HOPS from the right (1,
//     the last one, by default) was written by a proxy the operator runs.
//     Entries to its left came from the client and are ignored.
//   - True-Client-IP and X-Real-IP are ignored, unless
//     ACNOLOGIA_CLIENT_IP_HEADER names one header the edge sets itself (for
//     example Fly-Client-IP or CF-Connecting-IP). Then that header alone is
//     read, and X-Forwarded-For is not.
//
// A value that is no IP address, or a chain shorter than the hop count,
// leaves the socket's address in place.

// Forwarded returns the client IP the proxy headers name, or "" when they
// name none that can be trusted.
func Forwarded(h http.Header, hops int, header string) string {
	if header != "" {
		return parseIP(h.Get(header))
	}
	if hops < 1 {
		hops = 1
	}
	var chain []string
	// Several X-Forwarded-For lines read as one list, in order.
	for _, line := range h.Values("X-Forwarded-For") {
		for _, part := range strings.Split(line, ",") {
			chain = append(chain, strings.TrimSpace(part))
		}
	}
	if len(chain) < hops {
		return ""
	}
	return parseIP(chain[len(chain)-hops])
}

// parseIP accepts an address with or without a port (and IPv6 in brackets)
// and returns it in canonical form, or "" when it is no IP address.
func parseIP(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if a, err := netip.ParseAddr(v); err == nil {
		return a.Unmap().String()
	}
	if host, _, err := net.SplitHostPort(v); err == nil {
		if a, err := netip.ParseAddr(host); err == nil {
			return a.Unmap().String()
		}
	}
	if a, err := netip.ParseAddr(strings.Trim(v, "[]")); err == nil {
		return a.Unmap().String()
	}
	return ""
}

// Middleware replaces RemoteAddr with the client IP the trusted proxy
// headers name, as chi's RealIP did, so the server reads it the same way.
func Middleware(hops int, header string) func(http.Handler) http.Handler {
	header = http.CanonicalHeaderKey(strings.TrimSpace(header))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip := Forwarded(r.Header, hops, header); ip != "" {
				r.RemoteAddr = ip
			}
			next.ServeHTTP(w, r)
		})
	}
}

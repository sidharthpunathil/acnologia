package ctl

// Guards for what the CLI hands to other parties: the site password goes only
// to the server it was configured for, text from a server is made safe for a
// terminal, and a sign in link from a server is opened only when it points
// back at that server.

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sidharthpunathil/acnologia/server/internal/config"
)

// accessHeader is the header that carries the site password.
const accessHeader = "X-Acnologia-Access"

// origin is the scheme and host of a URL, lower cased, with the default port
// dropped, or "" when raw is not an http(s) URL.
func origin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}

// accessPasswordFor is ACNOLOGIA_ACCESS_PASSWORD when target is on the server
// the password belongs to: ACNOLOGIA_ACCESS_URL, or else ACNOLOGIA_URL. With
// neither set, or for any other server, it is empty, so a password exported
// for one instance never reaches another one the CLI talks to.
func accessPasswordFor(target string) string {
	pw := config.Getenv("ACNOLOGIA_ACCESS_PASSWORD")
	if pw == "" {
		return ""
	}
	bound := config.Getenv("ACNOLOGIA_ACCESS_URL")
	if bound == "" {
		bound = config.Getenv("ACNOLOGIA_URL")
	}
	want := origin(bound)
	if want == "" || origin(target) != want {
		return ""
	}
	return pw
}

// httpClient is c.http (or a new client with timeout) whose redirects never
// carry the site password to another origin. Go already drops Authorization
// and cookies on a hop to another host, but not custom headers.
func (c *cli) httpClient(timeout time.Duration) *http.Client {
	hc := &http.Client{Timeout: timeout}
	if c.http != nil {
		cp := *c.http
		hc = &cp
	}
	next := hc.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && origin(req.URL.String()) != origin(via[0].URL.String()) {
			req.Header.Del(accessHeader)
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after %d redirects", len(via))
		}
		return nil
	}
	return hc
}

// clean makes text that came from a server or a peer safe to print to a
// terminal: control characters (C0, DEL and C1, which start escape
// sequences that can rewrite the screen, set the clipboard or forge links),
// bidi overrides and invalid UTF-8 are shown as visible escapes instead.
func clean(s string) string {
	ok := true
	for i, r := range s {
		if unsafeRune(r) || (r == utf8.RuneError && !validAt(s, i)) {
			ok = false
			break
		}
	}
	if ok {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == utf8.RuneError && !validAt(s, i):
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x100 && unsafeRune(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		case unsafeRune(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}

// validAt reports whether the U+FFFD at s[i] was really written as one,
// rather than standing for an invalid byte.
func validAt(s string, i int) bool {
	_, size := utf8.DecodeRuneInString(s[i:])
	return size == 3
}

// clip is clean(s) cut to n runes, with "..." when it was longer.
func clip(s string, n int) string {
	s = clean(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-3]) + "..."
}

// cleanJSON escapes, inside indented JSON, the characters a terminal would
// act on that JSON allows raw (DEL, C1, bidi overrides) and invalid UTF-8.
// They can only sit inside strings, where \uXXXX means the same thing.
func cleanJSON(b []byte) []byte {
	var out bytes.Buffer
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			out.WriteString(`�`)
		case unsafeRune(r) && r != '\n' && r != '\t' && r != '\r' && r != ' ':
			fmt.Fprintf(&out, `\u%04x`, r)
		default:
			out.Write(b[i : i+size])
		}
		i += size
	}
	return out.Bytes()
}

// verifyLink is the browser sign in link to open: the server's own link when
// it is http(s) on the host the person signed in to (never a downgrade from
// https), with no user info. ok is false otherwise, and link is then the
// page the real server serves, built from the URL the person typed, to print
// instead of opening anything.
func verifyLink(server string, st deviceStart) (link string, ok bool) {
	local := server + "/cli/authorize?code=" + url.QueryEscape(st.UserCode)
	raw := st.VerificationURIComplete
	if raw == "" {
		if st.VerificationURI == "" {
			return local, false
		}
		raw = st.VerificationURI + "?code=" + url.QueryEscape(st.UserCode)
	}
	s, err := url.Parse(server)
	if err != nil {
		return local, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.Host == "" {
		return local, false
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme == "https":
	case scheme == "http" && strings.ToLower(s.Scheme) == "http":
	default:
		return local, false
	}
	if !strings.EqualFold(u.Hostname(), s.Hostname()) {
		return local, false
	}
	return u.String(), true
}

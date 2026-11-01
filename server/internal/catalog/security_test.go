package catalog

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
)

func TestBlockedIP(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "172.16.0.1": true, "192.168.1.1": true,
		"169.254.169.254": true, "0.0.0.0": true, "0.1.2.3": true,
		"100.64.0.1": true, "100.100.100.200": true, "198.18.0.1": true, "192.0.0.192": true,
		"224.0.0.1": true, "::1": true, "::": true, "fe80::1": true, "fd00::1": true,
		"::ffff:127.0.0.1": true, "64:ff9b::a9fe:a9fe": true, "64:ff9b::7f00:1": true,
		"2002:7f00:1::1": true, "2002:a9fe:a9fe::": true,
		"8.8.8.8": false, "1.1.1.1": false, "100.63.255.255": false, "100.128.0.1": false,
		"2606:4700:4700::1111": false, "64:ff9b::808:808": false, "2002:808:808::1": false,
	}
	for s, want := range cases {
		if got := BlockedIP(netip.MustParseAddr(s)); got != want {
			t.Errorf("BlockedIP(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestCheckRejectsNonHTTPLinks(t *testing.T) {
	spec, _ := json.Marshal(map[string]any{})
	for _, bad := range []string{"javascript:alert(1)", "JavaScript:fetch('/x')", "data:text/html,x", "//evil.com", "https://"} {
		for _, field := range []string{"docs_url", "website"} {
			in := Input{Slug: "x", Name: "X", Spec: spec}
			if field == "docs_url" {
				in.DocsURL = bad
			} else {
				in.Website = bad
			}
			_, errs, _ := Check(&in)
			if !strings.Contains(strings.Join(errs, ";"), field+" must be an http(s) URL") {
				t.Errorf("%s=%q accepted: %v", field, bad, errs)
			}
		}
	}
	in := Input{Slug: "x", Name: "X", Spec: spec, DocsURL: " https://docs.example.com/api ", Website: "http://example.com"}
	_, errs, _ := Check(&in)
	if j := strings.Join(errs, ";"); strings.Contains(j, "docs_url must") || strings.Contains(j, "website must") {
		t.Errorf("valid links rejected: %v", errs)
	}
}

func TestLiveSafeMethod(t *testing.T) {
	for m, want := range map[string]bool{"": true, "get": true, "HEAD": true, "POST": false, "DELETE": false, "PUT": false, "PATCH": false} {
		if LiveSafeMethod(m) != want {
			t.Errorf("LiveSafeMethod(%q) != %v", m, want)
		}
	}
}

func TestScrubBeforeTruncate(t *testing.T) {
	// A key straddling the 600 byte preview cut must not leak in part.
	secret := "sk_live_ABCDEFGHIJKLMNOP"
	body := strings.Repeat("a", 590) + secret
	preview := scrub(body, secret)
	if strings.Contains(preview, "sk_live_AB") {
		t.Fatal("secret survived scrub")
	}
}

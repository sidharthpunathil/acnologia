package catalog

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestMetadataIP(t *testing.T) {
	cases := map[string]bool{
		"169.254.169.254": true, "169.254.1.1": true, "fe80::1": true, "fd00:ec2::254": true,
		"100.100.100.200": true, "0.0.0.0": true, "::": true, "::ffff:169.254.169.254": true,
		"64:ff9b::a9fe:a9fe": true, "2002:a9fe:a9fe::": true,
		// Loopback and private stay reachable for local model servers.
		"127.0.0.1": false, "::1": false, "10.0.0.5": false, "192.168.1.10": false, "172.16.0.1": false,
		"fd00::1": false, "100.64.0.1": false, "8.8.8.8": false,
	}
	for s, want := range cases {
		if got := MetadataIP(netip.MustParseAddr(s)); got != want {
			t.Errorf("MetadataIP(%s) = %v, want %v", s, got, want)
		}
	}
	if err := NoMetadata("tcp", "169.254.169.254:80", nil); err == nil || !strings.Contains(err.Error(), ErrMetadataAddr) {
		t.Errorf("NoMetadata: %v", err)
	}
	if err := NoMetadata("tcp", "127.0.0.1:11434", nil); err != nil {
		t.Errorf("NoMetadata refused loopback: %v", err)
	}
}

func TestGuardProxyRefusesLiteralMetadata(t *testing.T) {
	g := GuardProxy(nil)
	for _, u := range []string{"http://169.254.169.254/latest", "http://[fe80::1]:80/", "http://[fd00:ec2::254]/"} {
		req, _ := http.NewRequest("GET", u, nil)
		if _, err := g(req); err == nil {
			t.Errorf("%s: not refused", u)
		}
	}
	req, _ := http.NewRequest("GET", "http://api.example.com/v1", nil)
	if p, err := g(req); err != nil || p != nil {
		t.Errorf("public host: %v %v", p, err)
	}
}

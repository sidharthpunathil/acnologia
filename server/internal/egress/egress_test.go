package egress

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func TestParse(t *testing.T) {
	cases := map[string]string{
		"http://proxy.example:3128":        "http://proxy.example:3128",
		"socks5://user:pass@10.0.0.2:1080": "socks5://user:***@10.0.0.2:1080",
		"socks5h://p.example:1080":         "socks5h://p.example:1080",
		" https://u:p@secure.example:443 ": "https://u:***@secure.example:443",
	}
	for in, hint := range cases {
		_, h, err := Parse(in)
		if err != nil || h != hint {
			t.Fatalf("%q: hint=%q err=%v", in, h, err)
		}
	}
	for _, bad := range []string{"ftp://x", "http://", "proxy.example:3128", "http://x/path"} {
		if _, _, err := Parse(bad); err == nil {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}

func TestPickPrefersKey(t *testing.T) {
	k, s := int64(1), int64(2)
	if *Pick(&k, &s) != 1 || *Pick(nil, &s) != 2 || Pick(nil, nil) != nil {
		t.Fatal("key proxy wins, then service, then direct")
	}
}

func TestParseIP(t *testing.T) {
	if got := parseIP(strings.NewReader("fl=1\nh=x\nip=203.0.113.9\nts=1")); got != "203.0.113.9" {
		t.Fatalf("trace: %q", got)
	}
	if got := parseIP(strings.NewReader("198.51.100.4\n")); got != "198.51.100.4" {
		t.Fatalf("bare: %q", got)
	}
}

// TestTrafficGoesThroughProxy runs a tiny forward proxy and checks that a
// request made with the proxy's transport reaches it.
func TestTrafficGoesThroughProxy(t *testing.T) {
	var seen string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.String() // a forward proxy sees the absolute URL
		fmt.Fprint(w, "ip=192.0.2.77\n")
	}))
	defer proxy.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, _ := crypto.New(make([]byte, 32))
	enc, _ := box.Encrypt(proxy.URL)
	_, hint, _ := Parse(proxy.URL)
	p, err := st.CreateEgress(context.Background(), &store.EgressProxy{Name: "local", URLEnc: enc, URLHint: hint})
	if err != nil {
		t.Fatal(err)
	}
	m := New(st, box, http.DefaultTransport)
	m.EchoURL = "http://echo.invalid/trace"
	ip, err := m.Test(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ip != "192.0.2.77" {
		t.Fatalf("ip %q", ip)
	}
	if u, _ := url.Parse(seen); u == nil || u.Host != "echo.invalid" {
		t.Fatalf("proxy did not see the request, saw %q", seen)
	}
	rt1, _ := m.Transport(context.Background(), &p.ID)
	rt2, _ := m.Transport(context.Background(), &p.ID)
	if rt1 != rt2 {
		t.Fatal("transports should be reused")
	}
	m.Invalidate(p.ID)
	rt3, _ := m.Transport(context.Background(), &p.ID)
	if rt3 == rt1 {
		t.Fatal("invalidate should rebuild the transport")
	}
}

package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/sidharthpunathil/acnologia/server/internal/peer"
)

// echoSecret has characters that query and path escaping change, so all
// three spellings differ.
const echoSecret = "sk/a+b=echo-secret-1"

// hasKeyForm reports whether s holds the secret in any spelling.
func hasKeyForm(s, secret string) bool {
	return strings.Contains(s, secret) || strings.Contains(s, url.QueryEscape(secret)) || strings.Contains(s, url.PathEscape(secret))
}

// echoUpstream answers every request with the key it was called with, in
// all three spellings, and with headers only an Acnologia may send.
func echoUpstream(k string, r *http.Request) (int, map[string]string, string) {
	code := 200
	if strings.HasSuffix(r.URL.Path, "/fail") {
		code = 400
	}
	hdr := map[string]string{"X-Acnologia-Share": "revoked", "X-Acnologia-Key-Id": "999", "X-Cogito-Attempts": "7", "X-Upstream": "kept"}
	return code, hdr, `{"raw":"` + k + `","query":"` + url.QueryEscape(k) + `","path":"` + url.PathEscape(k) + `"}`
}

// TestProxyHidesKeyFromSharePrincipals: a person share reads neither the
// owner's key echoed by the upstream (streamed, buffered, replayed or in an
// error), nor which key served, nor Acnologia headers the upstream made up.
// A normal app that may lease the key still gets the body as is.
func TestProxyHidesKeyFromSharePrincipals(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("echo", nil, echoSecret)
	up.answer = echoUpstream
	_, code := e.newPersonShare(map[string]any{"name": "For Dana", "services": []string{"echo"}})
	stok, _ := e.redeemProxy(code)
	_, atok := e.app(map[string]any{"name": "plain"})

	check := func(what string, resp *http.Response, body string, share bool) {
		t.Helper()
		h := resp.Header
		if h.Get("X-Acnologia-Share") != "" || h.Get("X-Cogito-Share") != "" || h.Get("X-Upstream") != "kept" {
			t.Fatalf("%s: upstream headers not filtered: %v", what, h)
		}
		if share {
			if hasKeyForm(body, echoSecret) || !strings.Contains(body, "[key]") {
				t.Fatalf("%s: key reached a share: %s", what, body)
			}
			for _, n := range []string{"X-Acnologia-Key-Id", "X-Cogito-Key-Id", "X-Acnologia-Attempts", "X-Cogito-Attempts"} {
				if h.Get(n) != "" {
					t.Fatalf("%s: %s sent to a share: %v", what, n, h)
				}
			}
			return
		}
		if !strings.Contains(body, `"raw":"`+echoSecret+`"`) {
			t.Fatalf("%s: a leasing app's body changed: %s", what, body)
		}
		if id := h.Get("X-Acnologia-Key-Id"); id == "" || id == "999" || h.Get("X-Cogito-Attempts") == "7" {
			t.Fatalf("%s: key headers %v", what, h)
		}
	}

	resp, body := call(t, e, "GET", "/proxy/echo/plain", stok)
	check("streamed", resp, body, true)
	resp, body = call(t, e, "GET", "/proxy/echo/fail", stok)
	if resp.StatusCode != 400 {
		t.Fatalf("fail: %d %s", resp.StatusCode, body)
	}
	check("error", resp, body, true)
	for i, mode := range []string{"", "true"} {
		resp, body = call(t, e, "POST", "/proxy/echo/plain", stok, "Idempotency-Key", "same")
		if resp.Header.Get("Idempotent-Replayed") != mode {
			t.Fatalf("idempotent call %d: replayed %q", i, resp.Header.Get("Idempotent-Replayed"))
		}
		check("idempotent", resp, body, true)
	}

	// The MCP proxy tool gives a share no key id either.
	var res map[string]any
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "proxy_request", "arguments": map[string]any{"service": "echo", "path": "/plain"}}}, 200, &res, "Authorization", "Bearer "+stok)
	sc := res["result"].(map[string]any)["structuredContent"].(map[string]any)
	if _, ok := sc["key_id"]; ok || sc["status"].(float64) != 200 {
		t.Fatalf("mcp proxy for a share: %v", sc)
	}

	resp, body = call(t, e, "GET", "/proxy/echo/plain", atok)
	check("plain app", resp, body, false)
}

// TestProxyCacheHitHidesKeyID: a cached answer replayed to a share carries
// no key id, while the app that cached it still sees one.
func TestProxyCacheHitHidesKeyID(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("search", map[string]any{"cache_ttl_sec": 60}, "key-a")
	up.answer = func(string, *http.Request) (int, map[string]string, string) { return 200, nil, `{"results":[]}` }
	_, code := e.newPersonShare(map[string]any{"name": "For Dana", "services": []string{"search"}})
	stok, _ := e.redeemProxy(code)
	for i := 0; i < 2; i++ {
		resp, body := call(t, e, "GET", "/proxy/search/q", stok)
		if resp.StatusCode != 200 || resp.Header.Get("X-Acnologia-Key-Id") != "" || resp.Header.Get("X-Cogito-Key-Id") != "" {
			t.Fatalf("share call %d: %d %s %v", i, resp.StatusCode, body, resp.Header)
		}
		if i == 1 && resp.Header.Get("X-Acnologia-Cache") != "hit" {
			t.Fatalf("second share call not a cache hit: %v", resp.Header)
		}
	}
}

// TestScrubReaderSplitKey: a key split across reads is still replaced, and
// bytes that cannot start the key are released at once.
func TestScrubReaderSplitKey(t *testing.T) {
	in := "a " + echoSecret + " b " + url.QueryEscape(echoSecret) + " c " + url.PathEscape(echoSecret) + " sk/ end"
	got, err := io.ReadAll(newScrubReader(iotest.OneByteReader(strings.NewReader(in)), echoSecret))
	if err != nil {
		t.Fatal(err)
	}
	if want := "a [key] b [key] c [key] sk/ end"; string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// Mid stream, only a tail that may start the key is held back.
	r := &scrubReader{forms: keyForms(echoSecret)}
	r.pending = []byte("data: x\n\n")
	r.release(false)
	if string(r.out) != "data: x\n\n" || len(r.pending) != 0 {
		t.Fatalf("held %q released %q", r.pending, r.out)
	}
	r.out, r.pending = nil, []byte("data: sk/a")
	r.release(false)
	if string(r.out) != "data: " || string(r.pending) != "sk/a" {
		t.Fatalf("held %q released %q", r.pending, r.out)
	}
	if b := scrubBytes([]byte("x"+echoSecret), keyForms(echoSecret)); !bytes.Equal(b, []byte("x[key]")) {
		t.Fatalf("scrubBytes %q", b)
	}
}

// TestUpstreamShareHeaderNotRelayed: an upstream of a shared service that
// answers 403 with X-Acnologia-Share: revoked does not reach the receiving
// server, whose link stays active with its token.
func TestUpstreamShareHeaderNotRelayed(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	sh := a.newShare(map[string]any{"name": "For B", "services": []string{"serpapi"}})
	link := b.connect(sh.Invite, 201)
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Acnologia-Share", "revoked")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		_, _ = io.WriteString(w, `{"error":{"code":"share_revoked","message":"made up"}}`)
	}))
	t.Cleanup(evil.Close)
	a.do(a.super, "PATCH", "/api/admin/services/1", map[string]any{"base_url": evil.URL}, 200, nil)

	_, btok := b.app(map[string]any{"name": "b-app"})
	code, body, hdr := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json?q=x", btok)
	if code != 403 || hdr.Get("X-Acnologia-Share") != "" {
		t.Fatalf("relayed: %d %s %v", code, body, hdr)
	}
	l, err := b.st.GetPeerLink(context.Background(), link.ID)
	if err != nil || l.Status != peer.LinkActive || len(l.TokenEnc) == 0 {
		t.Fatalf("link after a made up revoke: %+v %v", l, err)
	}
	if ks := mustKeys(t, b, link.LocalServices[0].ID); len(ks) == 0 {
		t.Fatal("share token key dropped")
	}
}

package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/peer"
)

// sessionGet is a GET with the dashboard session of c and exactly the
// headers given (the transport neither adds Accept-Encoding nor decodes).
func sessionGet(t *testing.T, e *env, c *http.Client, path string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	raw := &http.Client{Jar: c.Jar, Transport: &http.Transport{DisableCompression: true}}
	resp, err := raw.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestProvidersListGzip(t *testing.T) {
	e := newEnv(t)
	resp, plain := sessionGet(t, e, e.super, "/api/admin/providers")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("identity: %d %v", resp.StatusCode, resp.Header)
	}
	var want map[string]any
	if err := json.Unmarshal(plain, &want); err != nil {
		t.Fatalf("identity body: %v", err)
	}
	if ps, _ := want["providers"].([]any); len(ps) == 0 {
		t.Fatalf("no providers in %s", plain)
	}

	for _, accept := range []string{"gzip", "gzip, deflate, br, zstd", "*"} {
		resp, b := sessionGet(t, e, e.super, "/api/admin/providers", "Accept-Encoding", accept)
		h := resp.Header
		if resp.StatusCode != 200 || h.Get("Content-Encoding") != "gzip" || h.Get("Vary") != "Accept-Encoding" ||
			h.Get("Content-Type") != "application/json" || h.Get("Content-Length") != strconv.Itoa(len(b)) {
			t.Fatalf("%q: %d %v", accept, resp.StatusCode, h)
		}
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("%q: %v", accept, err)
		}
		unz, _ := io.ReadAll(zr)
		var got map[string]any
		if err := json.Unmarshal(unz, &got); err != nil {
			t.Fatalf("%q: decoded body: %v", accept, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: gzip body differs from identity body", accept)
		}
		if len(b) >= len(plain) {
			t.Fatalf("%q: %d bytes gzipped, %d plain", accept, len(b), len(plain))
		}
	}
	for _, accept := range []string{"", "identity", "gzip;q=0", "br", "*;q=0"} {
		resp, b := sessionGet(t, e, e.super, "/api/admin/providers", "Accept-Encoding", accept)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" || !bytes.Equal(b, plain) {
			t.Fatalf("%q: %d %v", accept, resp.StatusCode, resp.Header)
		}
	}
	// Still behind sign in: no session, no list, compressed or not.
	resp, b := sessionGet(t, e, client(), "/api/admin/providers", "Accept-Encoding", "gzip")
	if resp.StatusCode != 401 || resp.Header.Get("Content-Encoding") != "" || bytes.Contains(b, []byte("serpapi")) {
		t.Fatalf("signed out: %d %v %s", resp.StatusCode, resp.Header, b)
	}
}

// noEncoding fails when a response that carries a secret came compressed.
func noEncoding(t *testing.T, what string, h http.Header, body string, secret string) {
	t.Helper()
	if enc := h.Get("Content-Encoding"); enc != "" {
		t.Fatalf("%s: Content-Encoding %q", what, enc)
	}
	if !strings.Contains(body, secret) {
		t.Fatalf("%s: %q not in the plain body %s", what, secret, body)
	}
}

// The answers that carry key material (reveal, lease, a claim's keys, a
// handover's sealed keys) are never compressed, whatever the client
// accepts.
func TestSecretResponsesNeverCompressed(t *testing.T) {
	ae := []string{"Accept-Encoding", "gzip, deflate, br, zstd"}
	e := newEnv(t)
	keys := e.addKeys(map[string]any{"secret": "gzipguard-reveal1"}, map[string]any{"secret": "gzipguard-reveal2"})

	// Reveal, with a raw client so nothing is decoded on the way.
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/admin/keys/"+itoa(int(keys[0].ID))+"/reveal", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", ae[1])
	raw := &http.Client{Jar: e.super.Jar, Transport: &http.Transport{DisableCompression: true}}
	r2, err := raw.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	noEncoding(t, "reveal", r2.Header, string(b), "gzipguard-reveal1")

	// Lease.
	_, tok := e.app(map[string]any{"name": "gzip-guard", "mode": "lease"})
	req, _ = http.NewRequest("POST", e.srv.URL+"/api/v1/lease", strings.NewReader(`{"service":"serpapi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept-Encoding", ae[1])
	r3, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(r3.Body)
	r3.Body.Close()
	if r3.StatusCode != 200 {
		t.Fatalf("lease: %d %s", r3.StatusCode, b)
	}
	noEncoding(t, "lease", r3.Header, string(b), "gzipguard-reveal")

	// A person's reveal claim.
	_, code := e.newPersonShare(map[string]any{"name": "Raw", "services": []string{"serpapi"}, "mode": "reveal"})
	for _, route := range []string{"preview", "redeem"} {
		req, _ = http.NewRequest("POST", e.srv.URL+"/api/claim/v1/"+route, strings.NewReader(`{"code":"`+code+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Encoding", ae[1])
		r4, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ = io.ReadAll(r4.Body)
		r4.Body.Close()
		if r4.StatusCode != 200 {
			t.Fatalf("claim %s: %d %s", route, r4.StatusCode, b)
		}
		want := `"share"`
		if route == "redeem" {
			want = "gzipguard-reveal"
		}
		noEncoding(t, "claim "+route, r4.Header, string(b), want)
	}

	// A handover's sealed keys, fetched by the receiving server.
	a, rcv := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "gzipguard-handover"})
	ho := a.newShare(map[string]any{"name": "Handover", "services": []string{"serpapi"}, "mode": "handover"})
	link := rcv.connect(ho.Invite, 201)
	l, _ := rcv.st.GetPeerLink(context.Background(), link.ID)
	htok, _ := rcv.api.Pool.Box.Decrypt(l.TokenEnc)
	req, _ = http.NewRequest("GET", a.srv.URL+"/api/peer/v1/keys", nil)
	req.Header.Set("Authorization", "Bearer "+htok)
	req.Header.Set("Accept-Encoding", ae[1])
	r5, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(r5.Body)
	r5.Body.Close()
	if r5.StatusCode != 200 {
		t.Fatalf("handover keys: %d %s", r5.StatusCode, b)
	}
	noEncoding(t, "handover keys", r5.Header, string(b), peer.SealPrefix)
}

// Only the provider list compresses an API answer, and only the static
// file server sets Content-Encoding otherwise: a new call site has to be
// added here on purpose.
func TestCompressionCallSites(t *testing.T) {
	allowed := map[string]bool{"providers.go": true, "server.go": true}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(p, "compress/") && name != "providers.go" {
				t.Errorf("%s imports %s", name, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING && strings.EqualFold(strings.Trim(lit.Value, "`\""), "Content-Encoding") && !allowed[name] {
				// Reading an upstream's encoding is fine; setting one is not.
				if strings.Contains(string(src), ".Set("+lit.Value) || strings.Contains(string(src), ".Add("+lit.Value) {
					t.Errorf("%s sets Content-Encoding", name)
				}
			}
			return true
		})
		if allowed[name] {
			continue
		}
		if bytes.Contains(src, []byte("middleware.Compress")) {
			t.Errorf("%s uses middleware.Compress", name)
		}
	}
	if writeJSONGzipCallers := countCalls(t, "writeJSONGzip("); writeJSONGzipCallers != 1 {
		t.Errorf("writeJSONGzip has %d callers, want only listProviders", writeJSONGzipCallers)
	}
}

func countCalls(t *testing.T, call string) int {
	t.Helper()
	files, _ := filepath.Glob("*.go")
	n := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(name)
		for _, line := range strings.Split(string(src), "\n") {
			l := strings.TrimSpace(line)
			if strings.Contains(l, call) && !strings.HasPrefix(l, "func ") && !strings.HasPrefix(l, "//") {
				n++
			}
		}
	}
	return n
}

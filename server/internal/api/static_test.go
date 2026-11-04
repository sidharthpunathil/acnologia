package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// staticIndex is a shell with an inline script, so the CSP hash it needs
// can be checked against the bytes served.
const staticInline = `document.documentElement.dataset.theme="dark"`
const staticIndex = `<!doctype html><html><head><script>` + staticInline + `</script>` +
	`<script type="module" src="/assets/index-abc123.js"></script></head><body></body></html>`

var (
	staticJS   = []byte(strings.Repeat("console.log('acnologia');\n", 200))
	staticBr   = []byte("pretend brotli bytes")
	staticGz   = gzipBytes(staticJS)
	staticMP4  = []byte("0123456789 mp4 bytes that are long enough to seek in")
	staticMark = []byte("\x89PNG mark bytes")
)

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

func staticFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                {Data: []byte(staticIndex)},
		"assets/index-abc123.js":    {Data: staticJS},
		"assets/index-abc123.js.br": {Data: staticBr},
		"assets/index-abc123.js.gz": {Data: staticGz},
		"assets/index-abc123.css":   {Data: []byte("body{}")},
		"mark.png":                  {Data: staticMark},
		// A sibling outside assets/ is never served in its place.
		"site.webmanifest":    {Data: []byte(`{"name":"Acnologia"}`)},
		"site.webmanifest.gz": {Data: gzipBytes([]byte(`{"name":"Acnologia"}`))},
		"video/acnologia.mp4": {Data: staticMP4},
	}
}

// staticEnv serves staticFS, with the access gate on when locked is true.
func staticEnv(t *testing.T, locked bool) *env {
	t.Helper()
	e := newEnv(t)
	e.api.WebFS = staticFS()
	if locked {
		e.api.setAccessPassword(testAccessPassword)
	}
	srv := httptest.NewServer(e.api.Router())
	t.Cleanup(srv.Close)
	e.srv = srv
	return e
}

// rawGet sends a request with exactly the headers given: the transport adds
// no Accept-Encoding of its own and decompresses nothing.
func rawGet(t *testing.T, method, url string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	c := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestStaticPrecompressedNegotiation(t *testing.T) {
	e := staticEnv(t, false)
	url := e.srv.URL + "/assets/index-abc123.js"
	for _, c := range []struct {
		accept, enc string
		body        []byte
	}{
		{"br, gzip, deflate", "br", staticBr},
		{"gzip, deflate, br, zstd", "br", staticBr},
		{"gzip", "gzip", staticGz},
		{"br;q=0, gzip", "gzip", staticGz},
		{"BR;Q=0.5", "br", staticBr},
		{"*", "br", staticBr},
		{"*, br;q=0", "gzip", staticGz},
		{"gzip;q=0, br;q=0", "", staticJS},
		{"identity", "", staticJS},
		{"deflate", "", staticJS},
		{"", "", staticJS},
	} {
		resp, b := rawGet(t, "GET", url, "Accept-Encoding", c.accept)
		h := resp.Header
		if resp.StatusCode != 200 || h.Get("Content-Encoding") != c.enc || !bytes.Equal(b, c.body) {
			t.Fatalf("Accept-Encoding %q: %d enc %q body %q", c.accept, resp.StatusCode, h.Get("Content-Encoding"), b)
		}
		// The type comes from the uncompressed name, whatever is sent.
		if ct := h.Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
			t.Fatalf("Accept-Encoding %q: Content-Type %q", c.accept, ct)
		}
		if h.Get("Vary") != "Accept-Encoding" || h.Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("Accept-Encoding %q: headers %v", c.accept, h)
		}
		if h.Get("Content-Length") != strconv.Itoa(len(c.body)) {
			t.Fatalf("Accept-Encoding %q: Content-Length %q, want %d", c.accept, h.Get("Content-Length"), len(c.body))
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") == "" {
			t.Fatalf("Accept-Encoding %q: privateSite headers missing: %v", c.accept, h)
		}
	}
	// The gzip sibling decodes to the file.
	resp, b := rawGet(t, "GET", url, "Accept-Encoding", "gzip")
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if !bytes.Equal(plain, staticJS) || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("gzip answer does not decode to the file")
	}
	// HEAD carries the encoded length and no body.
	resp, b = rawGet(t, "HEAD", url, "Accept-Encoding", "br")
	if resp.StatusCode != 200 || len(b) != 0 || resp.Header.Get("Content-Encoding") != "br" || resp.Header.Get("Content-Length") != strconv.Itoa(len(staticBr)) {
		t.Fatalf("HEAD: %d %v", resp.StatusCode, resp.Header)
	}
}

// A file with no siblings is sent as is, with no Vary, and a sibling
// outside assets/ is never used.
func TestStaticUncompressedFiles(t *testing.T) {
	e := staticEnv(t, false)
	resp, b := rawGet(t, "GET", e.srv.URL+"/assets/index-abc123.css", "Accept-Encoding", "br, gzip")
	if resp.StatusCode != 200 || string(b) != "body{}" || resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("Vary") != "" {
		t.Fatalf("css: %d %v %q", resp.StatusCode, resp.Header, b)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("css type %q", ct)
	}
	resp, b = rawGet(t, "GET", e.srv.URL+"/site.webmanifest", "Accept-Encoding", "gzip")
	if resp.StatusCode != 200 || string(b) != `{"name":"Acnologia"}` || resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("manifest: %d %v %q", resp.StatusCode, resp.Header, b)
	}
}

// A Range request gets identity bytes even when the client accepts br, so
// the offsets are offsets into the file.
func TestStaticRangeIsIdentity(t *testing.T) {
	e := staticEnv(t, false)
	for _, c := range []struct {
		path string
		file []byte
	}{
		{"/video/acnologia.mp4", staticMP4},
		{"/assets/index-abc123.js", staticJS},
	} {
		resp, b := rawGet(t, "GET", e.srv.URL+c.path, "Accept-Encoding", "br, gzip", "Range", "bytes=4-15")
		if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(b, c.file[4:16]) {
			t.Fatalf("%s range: %d %q", c.path, resp.StatusCode, b)
		}
		if enc := resp.Header.Get("Content-Encoding"); enc != "" {
			t.Fatalf("%s range: Content-Encoding %q", c.path, enc)
		}
		if cr := resp.Header.Get("Content-Range"); cr != fmt.Sprintf("bytes 4-15/%d", len(c.file)) {
			t.Fatalf("%s range: Content-Range %q", c.path, cr)
		}
	}
	resp, _ := rawGet(t, "GET", e.srv.URL+"/video/acnologia.mp4", "Range", "bytes=0-3")
	if resp.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("mp4 type %q", resp.Header.Get("Content-Type"))
	}
}

// Files outside assets/ get an ETag from their content and an hour of
// caching; a matching If-None-Match is a 304. Each encoding has its own tag.
func TestStaticETagRevalidation(t *testing.T) {
	e := staticEnv(t, false)
	resp, b := rawGet(t, "GET", e.srv.URL+"/mark.png")
	etag := resp.Header.Get("ETag")
	if resp.StatusCode != 200 || !bytes.Equal(b, staticMark) || !strings.HasPrefix(etag, `"`) || strings.HasPrefix(etag, "W/") {
		t.Fatalf("mark: %d etag %q", resp.StatusCode, etag)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Fatalf("mark Cache-Control %q", cc)
	}
	resp, b = rawGet(t, "GET", e.srv.URL+"/mark.png", "If-None-Match", etag)
	if resp.StatusCode != http.StatusNotModified || len(b) != 0 {
		t.Fatalf("mark revalidation: %d", resp.StatusCode)
	}
	resp, _ = rawGet(t, "GET", e.srv.URL+"/mark.png", "If-None-Match", `"stale"`)
	if resp.StatusCode != 200 {
		t.Fatalf("stale tag: %d", resp.StatusCode)
	}

	js := e.srv.URL + "/assets/index-abc123.js"
	plain, _ := rawGet(t, "GET", js)
	br, _ := rawGet(t, "GET", js, "Accept-Encoding", "br")
	gz, _ := rawGet(t, "GET", js, "Accept-Encoding", "gzip")
	tags := map[string]bool{plain.Header.Get("ETag"): true, br.Header.Get("ETag"): true, gz.Header.Get("ETag"): true}
	if len(tags) != 3 || tags[""] {
		t.Fatalf("tags per encoding: %v", tags)
	}
	resp, _ = rawGet(t, "GET", js, "Accept-Encoding", "br", "If-None-Match", br.Header.Get("ETag"))
	if resp.StatusCode != http.StatusNotModified || resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("br revalidation: %d %v", resp.StatusCode, resp.Header)
	}
	// The identity tag does not validate the br bytes.
	resp, b = rawGet(t, "GET", js, "Accept-Encoding", "br", "If-None-Match", plain.Header.Get("ETag"))
	if resp.StatusCode != 200 || !bytes.Equal(b, staticBr) {
		t.Fatalf("identity tag on br: %d", resp.StatusCode)
	}
}

// The shell stays no-cache and byte for byte the file, so the CSP hash of
// its inline script still matches; an ETag only lets a reload be a 304.
func TestStaticShellUnchanged(t *testing.T) {
	e := staticEnv(t, false)
	sum := sha256.Sum256([]byte(staticInline))
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	var etag string
	for _, p := range []string{"/", "/landing", "/keys", "/services/1", "/index.html", "/assets/missing.js"} {
		resp, b := rawGet(t, "GET", e.srv.URL+p, "Accept-Encoding", "br, gzip")
		h := resp.Header
		if resp.StatusCode != 200 || string(b) != staticIndex {
			t.Fatalf("%s: %d %q", p, resp.StatusCode, b)
		}
		if h.Get("Cache-Control") != "no-cache" || h.Get("Content-Encoding") != "" || h.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("%s: headers %v", p, h)
		}
		if !strings.Contains(h.Get("Content-Security-Policy"), hash) {
			t.Fatalf("%s: CSP %q lacks %s", p, h.Get("Content-Security-Policy"), hash)
		}
		if etag == "" {
			etag = h.Get("ETag")
		}
		if h.Get("ETag") == "" || h.Get("ETag") != etag {
			t.Fatalf("%s: ETag %q, want %q", p, h.Get("ETag"), etag)
		}
	}
	resp, b := rawGet(t, "GET", e.srv.URL+"/keys", "If-None-Match", etag)
	if resp.StatusCode != http.StatusNotModified || len(b) != 0 {
		t.Fatalf("shell revalidation: %d", resp.StatusCode)
	}
	resp, _ = rawGet(t, "GET", e.srv.URL+"/keys", "If-None-Match", `W/`+etag)
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("weak shell revalidation: %d", resp.StatusCode)
	}
	resp, _ = rawGet(t, "GET", e.srv.URL+"/api/nope")
	if resp.StatusCode != 404 {
		t.Fatalf("/api/nope: %d", resp.StatusCode)
	}
}

// A locked deploy still answers the shell and the landing assets, but
// every one of them is no-store, with the privateSite headers.
func TestStaticLockedIsNoStore(t *testing.T) {
	e := staticEnv(t, true)
	for _, c := range []struct {
		path, accept, enc string
	}{
		{"/", "br, gzip", ""},
		{"/keys", "", ""},
		{"/assets/index-abc123.js", "br", "br"},
		{"/assets/index-abc123.js", "", ""},
		{"/mark.png", "", ""},
		{"/video/acnologia.mp4", "", ""},
	} {
		resp, _ := rawGet(t, "GET", e.srv.URL+c.path, "Accept-Encoding", c.accept)
		h := resp.Header
		if resp.StatusCode != 200 || h.Get("Content-Encoding") != c.enc {
			t.Fatalf("%s: %d enc %q", c.path, resp.StatusCode, h.Get("Content-Encoding"))
		}
		if h.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s locked: Cache-Control %q", c.path, h.Get("Cache-Control"))
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") == "" ||
			!strings.Contains(h.Get("X-Robots-Tag"), "noindex") || h.Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s locked: privateSite headers missing: %v", c.path, h)
		}
	}
	// Data stays locked, compressed or not.
	resp, b := rawGet(t, "GET", e.srv.URL+"/api/admin/providers", "Accept-Encoding", "gzip")
	if resp.StatusCode != 401 || resp.Header.Get("Content-Encoding") != "" || !strings.Contains(string(b), "locked") {
		t.Fatalf("locked providers: %d %v %s", resp.StatusCode, resp.Header, b)
	}
}

// The file table holds only files that exist, so clients asking for any
// number of made up paths cannot grow it.
func TestStaticTableOnlyHoldsFiles(t *testing.T) {
	e := staticEnv(t, false)
	for i := 0; i < 200; i++ {
		resp, _ := rawGet(t, "GET", fmt.Sprintf("%s/claim/made-up-%d", e.srv.URL, i))
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
	}
	rawGet(t, "GET", e.srv.URL+"/mark.png")
	n := 0
	e.api.static.Load().files.Range(func(k, _ any) bool {
		if _, ok := staticFS()[k.(string)]; !ok {
			t.Errorf("table holds %q", k)
		}
		n++
		return true
	})
	if n == 0 || n > len(staticFS()) {
		t.Fatalf("table size %d", n)
	}
}

func TestAcceptsEncoding(t *testing.T) {
	for _, c := range []struct {
		header, enc string
		want        bool
	}{
		{"", "gzip", false},
		{"gzip", "gzip", true},
		{"gzip, br", "br", true},
		{" GZIP ", "gzip", true},
		{"gzip;q=0", "gzip", false},
		{"gzip; q=0.0", "gzip", false},
		{"gzip;q=0.001", "gzip", true},
		{"gzip;q=nonsense", "gzip", false},
		{"*", "gzip", true},
		{"*;q=0", "gzip", false},
		{"*;q=0, gzip", "gzip", true},
		{"gzip;q=0, *", "gzip", false},
		{"x-gzip", "gzip", false},
		{"brotli", "br", false},
		{"identity", "br", false},
	} {
		if got := acceptsEncoding(c.header, c.enc); got != c.want {
			t.Errorf("acceptsEncoding(%q, %q) = %v, want %v", c.header, c.enc, got, c.want)
		}
	}
}

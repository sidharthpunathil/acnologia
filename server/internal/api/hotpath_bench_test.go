package api

// Hot path benchmarks: the time and memory Acnologia adds to a proxied
// request, a lease and an MCP proxy_request, measured against a fake upstream
// that answers at once.
//
//	go test ./internal/api -run XXX -bench 'Proxy|MCP|Upstream' -benchmem -benchtime=3s
//	HOTPATH=1 go test ./internal/api -run 'TestHotPathLatency|TestUpstreamConnChurn' -v
//
// BENCH_LOG=off silences logging; the default installs the redacting handler
// main uses, writing to a discard sink.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/config"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/redact"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

var hpSmallBody = []byte(`{"id":"cmpl-1","object":"text_completion","choices":[{"text":"hello","index":0}],"usage":{"total_tokens":7}}`)

// hpUpstream answers /big with 1 MiB in 32 KiB writes and anything else
// with a small JSON body.
type hpUpstream struct{}

func (hpUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	if strings.HasSuffix(r.URL.Path, "/big") {
		chunk := bytes.Repeat([]byte("a"), 32<<10)
		for range 32 {
			_, _ = w.Write(chunk)
		}
		return
	}
	_, _ = w.Write(hpSmallBody)
}

type hpEnv struct {
	srv      *httptest.Server
	up       *httptest.Server
	newConns atomic.Int64 // connections the upstream accepted
	token    string
	api      *Server
	client   *http.Client
}

func hpLogging() {
	if os.Getenv("BENCH_LOG") == "off" {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 10})))
		return
	}
	slog.SetDefault(slog.New(redact.NewHandler(slog.NewTextHandler(io.Discard, nil))))
}

func newHPEnv(tb testing.TB) *hpEnv {
	tb.Helper()
	prev := slog.Default()
	hpLogging()
	tb.Cleanup(func() { slog.SetDefault(prev) })
	e := &hpEnv{}
	st, err := store.Open(tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { st.Close() })
	box, _ := crypto.New(bytes.Repeat([]byte{7}, 32))
	e.up = httptest.NewUnstartedServer(hpUpstream{})
	e.up.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			e.newConns.Add(1)
		}
	}
	e.up.Start()
	tb.Cleanup(e.up.Close)
	cat := &catalog.Catalog{Store: st}
	if err := cat.Boot(context.Background()); err != nil {
		tb.Fatal(err)
	}
	ag := agent.New(agent.Config{}, st, cat, box)
	e.api = New(config.Config{Version: "test", MaxBodyBytes: 1 << 20}, st, testPool(st, box), cat, ag,
		fstest.MapFS{"index.html": {Data: []byte("ui")}}, []byte("# Acnologia {{BASE_URL}}"))
	e.srv = httptest.NewServer(e.api.Router())
	tb.Cleanup(e.srv.Close)
	super := client()
	e.call(tb, super, "POST", "/api/auth/setup", map[string]any{"email": "root@x.io", "password": "supersecret1"}, nil)
	var sv store.Service
	e.call(tb, super, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "gen", "base_url": e.up.URL}, &sv)
	keys := []map[string]any{}
	for i := range 4 {
		keys = append(keys, map[string]any{"secret": fmt.Sprintf("sk-bench-key-%04d-abcdefgh", i)})
	}
	e.call(tb, super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID), map[string]any{"keys": keys}, nil)
	var app struct {
		Token string `json:"token"`
	}
	e.call(tb, super, "POST", "/api/admin/apps", map[string]any{"name": "bench"}, &app)
	e.token = app.Token
	e.client = &http.Client{Transport: &http.Transport{MaxIdleConns: 512, MaxIdleConnsPerHost: 512}}
	tb.Cleanup(e.client.CloseIdleConnections)
	return e
}

func (e *hpEnv) call(tb testing.TB, c *http.Client, method, path string, body, out any) {
	tb.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "test")
	resp, err := c.Do(req)
	if err != nil {
		tb.Fatal(err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		tb.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, rb)
	}
	if out != nil {
		_ = json.Unmarshal(rb, out)
	}
}

// proxyOnlyApp creates a proxy only app, whose responses are scrubbed.
func (e *hpEnv) proxyOnlyApp(tb testing.TB) string {
	tb.Helper()
	super := client()
	e.call(tb, super, "POST", "/api/auth/login", map[string]any{"email": "root@x.io", "password": "supersecret1"}, nil)
	var out struct {
		Token string `json:"token"`
	}
	e.call(tb, super, "POST", "/api/admin/apps", map[string]any{"name": "ponly", "mode": "proxy"}, &out)
	if out.Token == "" {
		tb.Fatal("no proxy only token")
	}
	return out.Token
}

func (e *hpEnv) proxyReq(path string) *http.Request {
	req, _ := http.NewRequest("GET", e.srv.URL+"/proxy/gen/"+path, nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	return req
}

func (e *hpEnv) directReq(path string) *http.Request {
	req, _ := http.NewRequest("GET", e.up.URL+"/"+path, nil)
	req.Header.Set("Authorization", "sk-direct")
	return req
}

func hpDo(tb testing.TB, c *http.Client, req *http.Request) {
	resp, err := c.Do(req)
	if err != nil {
		tb.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		tb.Fatalf("status %d", resp.StatusCode)
	}
}

func BenchmarkDirectSmall(b *testing.B) {
	e := newHPEnv(b)
	b.ReportAllocs()
	for b.Loop() {
		hpDo(b, e.client, e.directReq("v1/small"))
	}
}

func BenchmarkProxySmall(b *testing.B) {
	e := newHPEnv(b)
	hpDo(b, e.client, e.proxyReq("v1/small"))
	b.ReportAllocs()
	for b.Loop() {
		hpDo(b, e.client, e.proxyReq("v1/small"))
	}
}

func BenchmarkProxyBig(b *testing.B) {
	e := newHPEnv(b)
	b.SetBytes(1 << 20)
	b.ReportAllocs()
	for b.Loop() {
		hpDo(b, e.client, e.proxyReq("v1/big"))
	}
}

// BenchmarkProxyBigScrubbed streams 1 MiB to a proxy only app, through the
// scrubbing reader.
func BenchmarkProxyBigScrubbed(b *testing.B) {
	e := newHPEnv(b)
	e.token = e.proxyOnlyApp(b)
	b.SetBytes(1 << 20)
	b.ReportAllocs()
	for b.Loop() {
		hpDo(b, e.client, e.proxyReq("v1/big"))
	}
}

func BenchmarkDirectBig(b *testing.B) {
	e := newHPEnv(b)
	b.SetBytes(1 << 20)
	b.ReportAllocs()
	for b.Loop() {
		hpDo(b, e.client, e.directReq("v1/big"))
	}
}

// BenchmarkProxyHandlerInProc calls the router in process (no client
// socket), so allocations are the server's own plus the upstream round trip.
func BenchmarkProxyHandlerInProc(b *testing.B) {
	e := newHPEnv(b)
	h := e.api.Router()
	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequest("GET", "/proxy/gen/v1/small", nil)
		req.Header.Set("Authorization", "Bearer "+e.token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			b.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
	}
}

// BenchmarkUpstreamOnlyInProc measures the upstream client alone.
func BenchmarkUpstreamOnlyInProc(b *testing.B) {
	e := newHPEnv(b)
	c := e.api.UpClient
	b.ReportAllocs()
	for b.Loop() {
		hpDo(b, c, e.directReq("v1/small"))
	}
}

func BenchmarkMCPProxyRequest(b *testing.B) {
	e := newHPEnv(b)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"proxy_request","arguments":{"service":"gen","method":"GET","path":"/v1/small"}}}`
	b.ReportAllocs()
	first := true
	for b.Loop() {
		req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+e.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := e.client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		rb, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if first && !bytes.Contains(rb, []byte(`hello`)) {
			b.Fatalf("mcp: %s", rb)
		}
		first = false
	}
}

func hpPct(d []time.Duration, p float64) time.Duration {
	return d[min(int(float64(len(d))*p), len(d)-1)]
}

func hpSeq(tb testing.TB, n int, mk func() *http.Request, c *http.Client) []time.Duration {
	out := make([]time.Duration, 0, n)
	for range n {
		t := time.Now()
		hpDo(tb, c, mk())
		out = append(out, time.Since(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func hpConc(tb testing.TB, workers int, dur time.Duration, mk func() *http.Request, c *http.Client) (float64, []time.Duration) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var all []time.Duration
	start := time.Now()
	stop := start.Add(dur)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local []time.Duration
			for time.Now().Before(stop) {
				t := time.Now()
				hpDo(tb, c, mk())
				local = append(local, time.Since(t))
			}
			mu.Lock()
			all = append(all, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	el := time.Since(start)
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	return float64(len(all)) / el.Seconds(), all
}

// TestHotPathLatency prints sequential percentiles and concurrent
// throughput, direct and through the proxy. Run with HOTPATH=1.
func TestHotPathLatency(t *testing.T) {
	if os.Getenv("HOTPATH") == "" {
		t.Skip("set HOTPATH=1")
	}
	e := newHPEnv(t)
	for range 200 {
		hpDo(t, e.client, e.proxyReq("v1/small"))
		hpDo(t, e.client, e.directReq("v1/small"))
	}
	n := 3000
	d := hpSeq(t, n, func() *http.Request { return e.directReq("v1/small") }, e.client)
	p := hpSeq(t, n, func() *http.Request { return e.proxyReq("v1/small") }, e.client)
	t.Logf("SEQ small n=%d direct p50=%v p99=%v | proxy p50=%v p99=%v | added p50=%v p99=%v",
		n, hpPct(d, .5), hpPct(d, .99), hpPct(p, .5), hpPct(p, .99), hpPct(p, .5)-hpPct(d, .5), hpPct(p, .99)-hpPct(d, .99))
	db := hpSeq(t, 300, func() *http.Request { return e.directReq("v1/big") }, e.client)
	pb := hpSeq(t, 300, func() *http.Request { return e.proxyReq("v1/big") }, e.client)
	t.Logf("SEQ 1MiB n=300 direct p50=%v p99=%v | proxy p50=%v p99=%v", hpPct(db, .5), hpPct(db, .99), hpPct(pb, .5), hpPct(pb, .99))
	for _, w := range []int{1, 8, 64} {
		rd, ld := hpConc(t, w, 3*time.Second, func() *http.Request { return e.directReq("v1/small") }, e.client)
		rp, lp := hpConc(t, w, 3*time.Second, func() *http.Request { return e.proxyReq("v1/small") }, e.client)
		t.Logf("CONC c=%d direct %.0f rps p50=%v p99=%v | proxy %.0f rps p50=%v p99=%v", w, rd, hpPct(ld, .5), hpPct(ld, .99), rp, hpPct(lp, .5), hpPct(lp, .99))
	}
}

// TestUpstreamConnChurn counts the upstream connections the proxy opens at
// 8, 32 and 64 parallel clients. Run with HOTPATH=1.
func TestUpstreamConnChurn(t *testing.T) {
	if os.Getenv("HOTPATH") == "" {
		t.Skip("set HOTPATH=1")
	}
	e := newHPEnv(t)
	for _, w := range []int{8, 32, 64} {
		e.newConns.Store(0)
		rps, _ := hpConc(t, w, 3*time.Second, func() *http.Request { return e.proxyReq("v1/small") }, e.client)
		t.Logf("CHURN c=%d proxy %.0f rps, new upstream conns=%d", w, rps, e.newConns.Load())
	}
}

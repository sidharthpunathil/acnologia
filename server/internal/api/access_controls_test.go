package api

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// newToken adds a token to an app with the given extra fields.
func (e *env) newToken(appID int64, body map[string]any) (string, store.AppToken) {
	e.t.Helper()
	var out struct {
		Token    string         `json:"token"`
		AppToken store.AppToken `json:"app_token"`
	}
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/tokens", appID), body, 201, &out)
	return out.Token, out.AppToken
}

func bodyCode(body string) string {
	var e struct {
		Error apiError `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &e)
	return e.Error.Code
}

// admissionsHeld reports whether the app (and the server) hold no
// reservation: every admitted request released its place.
func admissionsHeld(s *Server, appID int64) string {
	if v, ok := s.admitted.Load(appID); ok {
		ad := v.(*admission)
		ad.mu.Lock()
		defer ad.mu.Unlock()
		if ad.reqs != 0 || len(ad.tok) != 0 || len(ad.grant) != 0 || len(ad.tag) != 0 {
			return fmt.Sprintf("app holds %d requests, %d tokens, %d grants, %d tags", ad.reqs, len(ad.tok), len(ad.grant), len(ad.tag))
		}
	}
	if v, ok := s.admitted.Load(serverAdmitKey{}); ok {
		ad := v.(*admission)
		ad.mu.Lock()
		defer ad.mu.Unlock()
		if ad.reqs != 0 {
			return fmt.Sprintf("server holds %d requests", ad.reqs)
		}
	}
	return ""
}

// A token's own cap holds next to the app's: the capped token stops, the
// app's other token goes on.
func TestTokenLimitsApartFromApp(t *testing.T) {
	e, s := newFeatureEnv(t)
	e.upstream("svc", nil, "k1")
	appID, other := e.app(map[string]any{"name": "a", "limits": map[string]any{"daily_requests": 10}})
	capped, at := e.newToken(appID, map[string]any{"name": "agent-1", "limits": map[string]any{"daily_requests": 2}})
	if at.Limits.DailyRequests == nil || *at.Limits.DailyRequests != 2 {
		t.Fatalf("token limits not stored: %+v", at.Limits)
	}
	for i := 0; i < 2; i++ {
		if resp, b := call(t, e, "GET", "/proxy/svc/x", capped); resp.StatusCode != 200 {
			t.Fatalf("call %d: %d %s", i, resp.StatusCode, b)
		}
	}
	resp, b := call(t, e, "GET", "/proxy/svc/x", capped)
	if resp.StatusCode != 429 || bodyCode(b) != "token_limit_reached" || !strings.Contains(b, `"limit":"token_daily_requests"`) {
		t.Fatalf("over the token cap: %d %s", resp.StatusCode, b)
	}
	if resp, b := call(t, e, "GET", "/proxy/svc/x", other); resp.StatusCode != 200 {
		t.Fatalf("the app's other token: %d %s", resp.StatusCode, b)
	}
	// The app's own limit still counts every token: 3 used, 7 left.
	var u struct {
		Usage store.AppUsage `json:"usage"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/apps/%d/usage", appID), nil, 200, &u)
	if u.Usage.TodayRequests != 3 {
		t.Fatalf("app usage %d, want 3", u.Usage.TodayRequests)
	}
	// Lifting the token's cap with PATCH lets it go on.
	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/apps/%d/tokens/%d", appID, at.ID), map[string]any{"limits": nil}, 200, nil)
	if resp, b := call(t, e, "GET", "/proxy/svc/x", capped); resp.StatusCode != 200 {
		t.Fatalf("after clearing: %d %s", resp.StatusCode, b)
	}
	if msg := admissionsHeld(s, appID); msg != "" {
		t.Fatal(msg)
	}
}

// The app's per-token default gives every token, such as each claimant of
// a share link, a cap of its own; a token's own field wins over it.
func TestTokenDefaultLimits(t *testing.T) {
	e, _ := newFeatureEnv(t)
	e.upstream("svc", nil, "k1")
	appID, first := e.app(map[string]any{"name": "a"})
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/token-limits", appID), map[string]any{"limits": map[string]any{"daily_requests": 1}}, 200, nil)
	second, _ := e.newToken(appID, map[string]any{"name": "b"})
	roomy, _ := e.newToken(appID, map[string]any{"name": "c", "limits": map[string]any{"daily_requests": 3}})
	for _, tok := range []string{first, second} {
		if resp, _ := call(t, e, "GET", "/proxy/svc/x", tok); resp.StatusCode != 200 {
			t.Fatalf("first call: %d", resp.StatusCode)
		}
		if resp, b := call(t, e, "GET", "/proxy/svc/x", tok); resp.StatusCode != 429 || bodyCode(b) != "token_limit_reached" {
			t.Fatalf("second call: %d %s", resp.StatusCode, b)
		}
	}
	for i := 0; i < 3; i++ {
		if resp, b := call(t, e, "GET", "/proxy/svc/x", roomy); resp.StatusCode != 200 {
			t.Fatalf("own limit over the default, call %d: %d %s", i, resp.StatusCode, b)
		}
	}
	var c struct {
		TokenLimits store.AppLimits `json:"token_limits"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/apps/%d/controls", appID), nil, 200, &c)
	if c.TokenLimits.DailyRequests == nil || *c.TokenLimits.DailyRequests != 1 {
		t.Fatalf("controls: %+v", c.TokenLimits)
	}
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/token-limits", appID), map[string]any{"limits": map[string]any{"daily_requests": -1}}, 400, nil)
}

// N concurrent requests on a token capped at M: exactly M reach the
// upstream, and every reservation is released afterwards.
func TestTokenLimitConcurrency(t *testing.T) {
	e, s := newFeatureEnv(t)
	up, _ := e.upstream("slow", nil, "k1")
	block := make(chan struct{})
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		<-block
		return 200, nil, `{"ok":true}`
	}
	appID, _ := e.app(map[string]any{"name": "a"})
	const n, m = 24, 5
	tok, _ := e.newToken(appID, map[string]any{"name": "t", "limits": map[string]any{"daily_requests": m}})
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _ := call(t, e, "GET", "/proxy/slow/q", tok, metaHeader, "customer=c1")
			codes <- resp.StatusCode
		}()
	}
	got := map[int]int{}
	for i := 0; i < n-m; i++ {
		select {
		case c := <-codes:
			got[c]++
		case <-time.After(10 * time.Second):
			close(block)
			t.Fatalf("refused requests are waiting: %v", got)
		}
	}
	close(block)
	wg.Wait()
	close(codes)
	for c := range codes {
		got[c]++
	}
	if got[200] != m || got[429] != n-m || int(up.total.Load()) != m {
		t.Fatalf("codes %v, upstream calls %d, want exactly %d admitted", got, up.total.Load(), m)
	}
	if msg := admissionsHeld(s, appID); msg != "" {
		t.Fatal(msg)
	}
}

// The same with a tag budget and a spend ceiling in play: the tag value's
// cap decides, and nothing stays reserved.
func TestTagBudgetConcurrency(t *testing.T) {
	e, s := newFeatureEnv(t)
	up, _ := e.upstream("slow", map[string]any{"cost_per_request": 0.01}, "k1")
	block := make(chan struct{})
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		<-block
		return 200, nil, `{"ok":true}`
	}
	appID, tok := e.app(map[string]any{"name": "a"})
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/tag-budgets", appID),
		map[string]any{"budgets": []map[string]any{{"key": "customer", "daily_requests": 3}}}, 200, nil)
	e.do(e.super, "PUT", "/api/admin/settings/spend-ceiling", map[string]any{"usd": 100}, 200, nil)
	const n, m = 16, 3
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _ := call(t, e, "GET", "/proxy/slow/q", tok, metaHeader, "customer=c1")
			codes <- resp.StatusCode
		}()
	}
	for i := 0; i < n-m; i++ {
		select {
		case c := <-codes:
			if c != 429 {
				t.Fatalf("a refused request got %d", c)
			}
		case <-time.After(10 * time.Second):
			close(block)
			t.Fatal("refused requests are waiting")
		}
	}
	close(block)
	wg.Wait()
	if int(up.total.Load()) != m {
		t.Fatalf("upstream calls %d, want %d", up.total.Load(), m)
	}
	if msg := admissionsHeld(s, appID); msg != "" {
		t.Fatal(msg)
	}
}

// Tag budgets hold per tag value; usage is readable per tag; the header is
// validated and never reaches the provider.
func TestCallerTags(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("svc", map[string]any{"cost_per_request": 0.5}, "k1")
	sawMeta := false
	var mu sync.Mutex
	up.answer = func(_ string, r *http.Request) (int, map[string]string, string) {
		mu.Lock()
		if r.Header.Get(metaHeader) != "" {
			sawMeta = true
		}
		mu.Unlock()
		return 200, nil, `{"ok":true}`
	}
	appID, tok := e.app(map[string]any{"name": "a"})
	// Each customer at most $1 a day: two calls at $0.50.
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/tag-budgets", appID),
		map[string]any{"budgets": []map[string]any{{"key": "customer", "daily_budget": 1}}}, 200, nil)
	for i := 0; i < 2; i++ {
		if resp, b := call(t, e, "GET", "/proxy/svc/x", tok, metaHeader, "customer=cust_A, workspace=w1"); resp.StatusCode != 200 {
			t.Fatalf("call %d: %d %s", i, resp.StatusCode, b)
		}
	}
	resp, b := call(t, e, "GET", "/proxy/svc/x", tok, metaHeader, "customer=cust_A")
	if resp.StatusCode != 429 || bodyCode(b) != "tag_limit_reached" || !strings.Contains(b, `"tag":"customer=cust_A"`) {
		t.Fatalf("over the tag budget: %d %s", resp.StatusCode, b)
	}
	if resp, b := call(t, e, "GET", "/proxy/svc/x", tok, metaHeader, "customer=cust_B"); resp.StatusCode != 200 {
		t.Fatalf("another customer: %d %s", resp.StatusCode, b)
	}
	if resp, b := call(t, e, "GET", "/proxy/svc/x", tok); resp.StatusCode != 200 {
		t.Fatalf("untagged: %d %s", resp.StatusCode, b)
	}
	if sawMeta {
		t.Fatal("X-Acnologia-Meta reached the provider")
	}
	for _, bad := range []string{"customer", "a=1,b=2,c=3,d=4,e=5,f=6", "customer=a b", "Bad Key=x", "customer=a,customer=b", "k=" + strings.Repeat("v", 65)} {
		if resp, b := call(t, e, "GET", "/proxy/svc/x", tok, metaHeader, bad); resp.StatusCode != 400 || bodyCode(b) != "bad_meta" {
			t.Fatalf("meta %q: %d %s", bad, resp.StatusCode, b)
		}
	}
	var out struct {
		Tags []store.TagUsageRow `json:"tags"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/apps/%d/usage/tags", appID), nil, 200, &out)
	byTag := map[string]store.TagUsageRow{}
	for _, r := range out.Tags {
		byTag[r.Key+"="+r.Value] = r
	}
	if a := byTag["customer=cust_A"]; a.TodayRequests != 2 || a.TodayCost < 0.99 || a.TodayCost > 1.01 {
		t.Fatalf("cust_A usage %+v", a)
	}
	if w := byTag["workspace=w1"]; w.TodayRequests != 2 {
		t.Fatalf("w1 usage %+v", w)
	}
	if bb := byTag["customer=cust_B"]; bb.TodayRequests != 1 {
		t.Fatalf("cust_B usage %+v (%v)", bb, out.Tags)
	}
	// The app reads its own.
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/usage/tags", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 200 || !strings.Contains(string(body), `"cust_A"`) {
		t.Fatalf("v1 usage by tag: %d %s", r2.StatusCode, body)
	}
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/tag-budgets", appID),
		map[string]any{"budgets": []map[string]any{{"key": "customer"}}}, 400, nil)
}

// A token pinned to tag values always carries them and cannot retag.
func TestPinnedTags(t *testing.T) {
	e, _ := newFeatureEnv(t)
	e.upstream("svc", nil, "k1")
	appID, _ := e.app(map[string]any{"name": "a"})
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/tag-budgets", appID),
		map[string]any{"budgets": []map[string]any{{"key": "customer", "daily_requests": 2}}}, 200, nil)
	tok, at := e.newToken(appID, map[string]any{"name": "cust-a", "pinned_tags": map[string]string{"customer": "cust_A"}})
	if at.PinnedTags["customer"] != "cust_A" {
		t.Fatalf("pinned tags not stored: %+v", at)
	}
	resp, b := call(t, e, "GET", "/proxy/svc/x", tok, metaHeader, "customer=cust_B")
	if resp.StatusCode != 400 || bodyCode(b) != "tag_pinned" {
		t.Fatalf("retag: %d %s", resp.StatusCode, b)
	}
	if resp, b := call(t, e, "GET", "/proxy/svc/x", tok, metaHeader, "customer=cust_A,workspace=w"); resp.StatusCode != 200 {
		t.Fatalf("same value: %d %s", resp.StatusCode, b)
	}
	if resp, b := call(t, e, "GET", "/proxy/svc/x", tok); resp.StatusCode != 200 {
		t.Fatalf("no header: %d %s", resp.StatusCode, b)
	}
	// Both counted under cust_A, so its budget is spent.
	if resp, b := call(t, e, "GET", "/proxy/svc/x", tok); resp.StatusCode != 429 || bodyCode(b) != "tag_limit_reached" {
		t.Fatalf("pinned budget: %d %s", resp.StatusCode, b)
	}
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/tokens", appID), map[string]any{"name": "x", "pinned_tags": map[string]string{"Bad Key": "v"}}, 400, nil)
}

// Deny rules hold against every spelling of a path.
func TestDenyRulesBypassAttempts(t *testing.T) {
	e, s := newFeatureEnv(t)
	up, _ := e.upstream("svc", nil, "k1")
	appID, tok := e.app(map[string]any{"name": "a"})
	var rule store.DenyRule
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/deny-rules", appID),
		map[string]any{"method": "post", "service": "svc", "path_prefix": "/v1/files/", "note": "no uploads"}, 201, &rule)
	if rule.PathPrefix != "/v1/files" || rule.Method != "POST" {
		t.Fatalf("rule not normalized: %+v", rule)
	}
	denied := []string{
		"/proxy/svc/v1/files", "/proxy/svc/v1/files/abc", "/proxy/svc/v1//files", "/proxy/svc//v1/files",
		"/proxy/svc/v1/%66iles", "/proxy/svc/v1%2Ffiles", "/proxy/svc/v1%252Ffiles", "/proxy/svc/V1/FILES",
		"/proxy/svc/v1/files;x=1", "/proxy/svc/v1/files%3Bx", "/proxy/svc/v1%5Cfiles", "/proxy/svc/v1/files%00.json",
		"/proxy/svc/x/%2E%2E/v1/files", "/proxy/svc/v1/./files",
	}
	for _, p := range denied {
		req, _ := http.NewRequest("POST", e.srv.URL+p, strings.NewReader("{}"))
		req.URL.Opaque = "" // keep the raw path as written
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Fatalf("%s got through: %d %s", p, resp.StatusCode, b)
		}
		if resp.StatusCode == 403 && bodyCode(string(b)) != "denied_by_rule" {
			t.Fatalf("%s: %d %s", p, resp.StatusCode, b)
		}
	}
	if n := up.total.Load(); n != 0 {
		t.Fatalf("the upstream saw %d denied requests", n)
	}
	resp, b := call(t, e, "POST", "/proxy/svc/v1/files", tok)
	if resp.StatusCode != 403 || !strings.Contains(b, "no uploads") || !strings.Contains(b, fmt.Sprintf(`"id":%d`, rule.ID)) {
		t.Fatalf("the refusal names the rule: %d %s", resp.StatusCode, b)
	}
	// Other methods, other paths and a longer segment pass.
	for _, c := range [][2]string{{"GET", "/proxy/svc/v1/files"}, {"POST", "/proxy/svc/v1/filesystem"}, {"POST", "/proxy/svc/v1/chat"}} {
		if resp, b := call(t, e, c[0], c[1], tok); resp.StatusCode != 200 {
			t.Fatalf("%s %s: %d %s", c[0], c[1], resp.StatusCode, b)
		}
	}
	// A rule on the whole service with *: every method.
	var all store.DenyRule
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/deny-rules", appID), map[string]any{"service": "svc"}, 201, &all)
	if resp, _ := call(t, e, "GET", "/proxy/svc/anything", tok); resp.StatusCode != 403 {
		t.Fatalf("whole service: %d", resp.StatusCode)
	}
	e.do(e.super, "DELETE", fmt.Sprintf("/api/admin/apps/%d/deny-rules/%d", appID, all.ID), nil, 204, nil)
	if resp, _ := call(t, e, "GET", "/proxy/svc/anything", tok); resp.StatusCode != 200 {
		t.Fatalf("after delete: %d", resp.StatusCode)
	}
	// Bad rules are refused.
	for _, bad := range []map[string]any{
		{"method": "TRACE"}, {"service": "svc", "host": "x.io"}, {"host": "http://x.io/"}, {"path_prefix": "/a*/b"},
	} {
		e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/deny-rules", appID), bad, 400, nil)
	}
	// An upstream MCP tool is matched as POST /tools/<name>.
	sv, err := s.Store.GetServiceBySlug(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/deny-rules", appID), map[string]any{"service": "svc", "path_prefix": "/tools/delete_*"}, 201, nil)
	a := &store.App{ID: appID}
	if err := s.denyMCPTool(context.Background(), a, sv, "delete_file"); err == nil || !strings.Contains(err.Error(), "denied_by_rule") {
		t.Fatalf("tool delete_file: %v", err)
	}
	if err := s.denyMCPTool(context.Background(), a, sv, "read_file"); err != nil {
		t.Fatalf("tool read_file: %v", err)
	}
}

// A host rule covers the forward proxy: plain HTTP and CONNECT.
func TestDenyRuleForwardProxy(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "poolkey1"})
	appID, tok := e.app(map[string]any{"name": "fwd", "mode": "proxy"})
	host := strings.Split(strings.TrimPrefix(e.upURL, "http://"), ":")[0]
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/deny-rules", appID), map[string]any{"host": host, "path_prefix": "/search.json"}, 201, nil)
	resp, err := e.proxyClient(tok, nil).Get(e.upURL + "/search.json?q=coffee")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 || bodyCode(string(b)) != "denied_by_rule" {
		t.Fatalf("forward proxy: %d %s", resp.StatusCode, b)
	}
	resp, err = e.proxyClient(tok, nil).Get(e.upURL + "//SEARCH.json?q=coffee")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("forward proxy, other spelling: %d", resp.StatusCode)
	}
	// A whole-host rule blocks a CONNECT tunnel too.
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/deny-rules", appID), map[string]any{"host": "blocked.example"}, 201, nil)
	e.api.Cfg.ForwardPassthrough = true
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	_, _ = io.WriteString(conn, "CONNECT blocked.example:443 HTTP/1.1\r\nHost: blocked.example:443\r\nProxy-Authorization: Basic "+basicAuth("app", tok)+"\r\n\r\n")
	r2, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 403 || bodyCode(string(b)) != "denied_by_rule" {
		t.Fatalf("CONNECT: %d %s", r2.StatusCode, b)
	}
}

// The server-wide ceiling refuses every app once the day's spend reaches it.
func TestServerSpendCeiling(t *testing.T) {
	e, s := newFeatureEnv(t)
	e.upstream("svc", map[string]any{"cost_per_request": 0.4}, "k1")
	_, a := e.app(map[string]any{"name": "a"})
	_, b := e.app(map[string]any{"name": "b"})
	// From the environment first.
	s.Cfg.DailySpendCeilingUSD = 1
	for _, tok := range []string{a, b} {
		if resp, body := call(t, e, "GET", "/proxy/svc/x", tok); resp.StatusCode != 200 {
			t.Fatalf("under the ceiling: %d %s", resp.StatusCode, body)
		}
	}
	// $0.80 spent: another $0.40 would pass $1.
	for _, tok := range []string{a, b} {
		resp, body := call(t, e, "GET", "/proxy/svc/x", tok)
		if resp.StatusCode != 503 || bodyCode(body) != "server_spend_ceiling" || !strings.Contains(body, "reset_at") || resp.Header.Get("Retry-After") == "" {
			t.Fatalf("over the ceiling: %d %s", resp.StatusCode, body)
		}
	}
	// A saved value wins over the environment: off.
	e.do(e.super, "PUT", "/api/admin/settings/spend-ceiling", map[string]any{"usd": 0}, 200, nil)
	if resp, body := call(t, e, "GET", "/proxy/svc/x", a); resp.StatusCode != 200 {
		t.Fatalf("ceiling off: %d %s", resp.StatusCode, body)
	}
	var v map[string]any
	e.do(e.super, "PUT", "/api/admin/settings/spend-ceiling", map[string]any{"usd": 5}, 200, &v)
	if v["ceiling_usd"].(float64) != 5 || v["source"] != "saved" {
		t.Fatalf("view: %v", v)
	}
	e.do(e.super, "PUT", "/api/admin/settings/spend-ceiling", map[string]any{"usd": nil}, 200, &v)
	if v["ceiling_usd"].(float64) != 1 || v["source"] != "env" {
		t.Fatalf("back to env: %v", v)
	}
	e.do(e.super, "PUT", "/api/admin/settings/spend-ceiling", map[string]any{"usd": -1}, 400, nil)
}

// Capabilities go through the proxy: a rule on a route's service makes the
// capability fall to the next route, and a rule on the capability itself
// refuses it.
func TestDenyRuleCapabilities(t *testing.T) {
	e, _ := newFeatureEnv(t)
	omkar, _ := e.upstream("omkar", nil, "o1")
	serp, _ := e.upstream("serp", nil, "s1")
	e.do(e.super, "POST", "/api/admin/capabilities", mapsCapability("priority"), 201, nil)
	appID, tok := e.app(map[string]any{"name": "a"})
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/deny-rules", appID), map[string]any{"service": "omkar", "path_prefix": "/search"}, 201, nil)
	r, body := call(t, e, "GET", "/proxy/maps-search?query=coffee", tok)
	if omkar.total.Load() != 0 || serp.total.Load() != 1 {
		t.Fatalf("denied route was called: omkar %d serp %d (%d %s)", omkar.total.Load(), serp.total.Load(), r.StatusCode, body)
	}
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/deny-rules", appID), map[string]any{"service": "maps-search"}, 201, nil)
	if r, body := call(t, e, "GET", "/proxy/maps-search?query=coffee", tok); r.StatusCode != 403 || bodyCode(body) != "denied_by_rule" {
		t.Fatalf("capability rule: %d %s", r.StatusCode, body)
	}
	if serp.total.Load() != 1 {
		t.Fatal("a denied capability reached a route")
	}
}

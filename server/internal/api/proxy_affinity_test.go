package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// jobsProvider registers a provider whose jobs live on the key that
// created them, and starts an upstream that answers 404 to any other key.
func jobsProvider(t *testing.T, e *env) (*scripted, store.Service) {
	t.Helper()
	spec := providers.Spec{BaseURL: "https://jobs.example", Auth: providers.Auth{Type: "header", Name: "X-Key"}, UseStandardRules: true,
		Affinity: []providers.AffinityRule{{
			Create: providers.AffinityCreate{Method: "POST", Path: "/v1/jobs$"}, ID: "id", Related: []string{"result_file"},
			Follow: []providers.AffinityFollow{{Path: "/v1/jobs/{id}"}, {Path: "/v1/files/{id}"}},
		}}}
	raw, _ := json.Marshal(spec)
	if _, err := e.st.SaveProvider(context.Background(), &store.Provider{Slug: "jobs-test", Name: "Jobs", Status: "active", Source: "custom", Spec: raw}, "test", "test"); err != nil {
		t.Fatal(err)
	}
	providers.Set("jobs-test", &spec)
	t.Cleanup(func() { providers.Remove("jobs-test") })
	up, sv := e.upstream("jobs", map[string]any{"provider": "jobs-test", "strategy": "round_robin"}, "ka", "kb", "kc")
	var n atomic.Int32
	up.answer = func(key string, r *http.Request) (int, map[string]string, string) {
		if r.Header.Get(affinityHeader) != "" {
			return 500, nil, `{"error":"the affinity header reached the provider"}`
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/jobs":
			id := fmt.Sprintf("job-%s-%d", key, n.Add(1))
			return 201, nil, `{"id":"` + id + `","result_file":"file-` + id + `"}`
		case strings.HasPrefix(r.URL.Path, "/v1/jobs/"), strings.HasPrefix(r.URL.Path, "/v1/files/"):
			if !strings.Contains(r.URL.Path, "-"+key+"-") {
				return 404, nil, `{"error":"not found"}`
			}
			return 200, nil, `{"status":"done"}`
		}
		return 200, nil, `{"key":"` + key + `"}`
	}
	return up, sv
}

func TestJobAffinityKeepsAJobOnItsKey(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := jobsProvider(t, e)
	_, tok := e.app(map[string]any{"name": "x"})
	var ids, owners []string
	for range 3 {
		r, body := call(t, e, "POST", "/proxy/jobs/v1/jobs", tok)
		var out struct{ ID string }
		if r.StatusCode != 201 || json.Unmarshal([]byte(body), &out) != nil {
			t.Fatalf("create: %d %s", r.StatusCode, body)
		}
		ids, owners = append(ids, out.ID), append(owners, r.Header.Get("X-Acnologia-Key-Id"))
	}
	// Round robin spreads polls over the keys; every poll must still land
	// on the key that made the job, and so must its result file.
	for range 4 {
		for _, id := range ids {
			for _, p := range []string{"/v1/jobs/" + id, "/v1/jobs/" + id + "/cancel", "/v1/files/file-" + id} {
				if r, body := call(t, e, "GET", "/proxy/jobs"+p, tok); r.StatusCode != 200 {
					t.Fatalf("%s: %d %s", p, r.StatusCode, body)
				}
			}
		}
	}

	// The job's key is disabled: a clear error, and no other key is tried.
	e.do(e.super, "PATCH", "/api/admin/keys/"+owners[0], map[string]any{"status": "disabled"}, 200, nil)
	before := up.total.Load()
	r, body := call(t, e, "GET", "/proxy/jobs/v1/jobs/"+ids[0], tok)
	if r.StatusCode != 503 || !strings.Contains(body, "key_unavailable_for_resource") || up.total.Load() != before {
		t.Fatalf("a job whose key is off: %d %s (upstream %d -> %d)", r.StatusCode, body, before, up.total.Load())
	}
	// A job nobody recorded is not bound.
	if r, _ := call(t, e, "GET", "/proxy/jobs/v1/other", tok); r.StatusCode != 200 {
		t.Fatalf("unbound request: %d", r.StatusCode)
	}
}

// A refusal on the bound key is relayed rather than moved to another key.
func TestJobAffinityRelaysTheBoundKeysRefusal(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := jobsProvider(t, e)
	_, tok := e.app(map[string]any{"name": "x"})
	_, body := call(t, e, "POST", "/proxy/jobs/v1/jobs", tok)
	var out struct{ ID string }
	_ = json.Unmarshal([]byte(body), &out)
	base := up.answer
	up.answer = func(key string, r *http.Request) (int, map[string]string, string) {
		if r.Method == "GET" {
			return 429, map[string]string{"Retry-After": "30"}, `{"error":"slow down"}`
		}
		return base(key, r)
	}
	before := up.total.Load()
	r, _ := call(t, e, "GET", "/proxy/jobs/v1/jobs/"+out.ID, tok)
	if r.StatusCode != 429 || up.total.Load() != before+1 {
		t.Fatalf("bound key refused: %d, upstream calls %d", r.StatusCode, up.total.Load()-before)
	}
	// Now cooling down for 30 seconds: no other key, a clear error.
	if r, body := call(t, e, "GET", "/proxy/jobs/v1/jobs/"+out.ID, tok); r.StatusCode != 503 || !strings.Contains(body, "key_unavailable_for_resource") || r.Header.Get("Retry-After") == "" {
		t.Fatalf("bound key cooling down: %d %s", r.StatusCode, body)
	}
}

// Every request with one X-Acnologia-Key-Affinity value uses one key; the
// header never reaches the provider.
func TestHeaderAffinityPinsAKey(t *testing.T) {
	e, _ := newFeatureEnv(t)
	jobsProvider(t, e)
	_, tok := e.app(map[string]any{"name": "x"})
	seen := map[string]map[string]bool{}
	for _, sess := range []string{"s1", "s2", "s3", "s4"} {
		seen[sess] = map[string]bool{}
		for range 5 {
			r, body := call(t, e, "GET", "/proxy/jobs/v1/chat", tok, affinityHeader, sess)
			if r.StatusCode != 200 {
				t.Fatalf("%s: %d %s", sess, r.StatusCode, body)
			}
			seen[sess][r.Header.Get("X-Acnologia-Key-Id")] = true
		}
		if len(seen[sess]) != 1 {
			t.Fatalf("value %s used keys %v", sess, seen[sess])
		}
	}
	// Without the header, round robin spreads.
	all := map[string]bool{}
	for range 6 {
		r, _ := call(t, e, "GET", "/proxy/jobs/v1/chat", tok)
		all[r.Header.Get("X-Acnologia-Key-Id")] = true
	}
	if len(all) < 2 {
		t.Fatalf("no header, no pin: %v", all)
	}
}

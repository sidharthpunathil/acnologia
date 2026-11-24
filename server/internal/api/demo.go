package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Demo data: a fake provider this server answers itself, two keys for it, an
// app and a token, so a new user can make a first successful call through
// the proxy before they own a real key.
//
//	GET    /api/admin/demo   whether demo data is loaded
//	POST   /api/admin/demo   load it: {"force": true} also on a server with services
//	DELETE /api/admin/demo   remove everything it made
//
// The fake upstream lives at /api/v1/demo/upstream: on the consumer side of
// the access gate, since the proxy calls it like any provider. It answers
// only requests that carry one of the demo keys, and nothing at all while no
// demo is loaded.

const (
	demoMeta = "demo_state"
	// demoKeyHeader is where the demo service puts its key.
	demoKeyHeader = "X-Demo-Key"
	demoUpstream  = "/api/v1/demo/upstream"
)

// demoState is what POST made, so DELETE removes exactly that.
type demoState struct {
	ServiceID   int64    `json:"service_id"`
	ServiceSlug string   `json:"service_slug"`
	AppID       int64    `json:"app_id"`
	KeyHashes   []string `json:"key_hashes"`
}

func (s *Server) loadDemo(r *http.Request) (*demoState, bool) {
	raw := s.Store.GetMeta(r.Context(), demoMeta)
	if raw == "" {
		return nil, false
	}
	var st demoState
	if json.Unmarshal([]byte(raw), &st) != nil || st.ServiceID == 0 {
		return nil, false
	}
	return &st, true
}

// demo serves /api/admin/demo for a superadmin.
func (s *Server) demo(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st, ok := s.loadDemo(r)
		out := map[string]any{"loaded": ok}
		if ok {
			out["service_id"], out["service_slug"], out["app_id"] = st.ServiceID, st.ServiceSlug, st.AppID
		}
		writeJSON(w, 200, out)
	case http.MethodPost:
		s.loadDemoData(w, r)
	case http.MethodDelete:
		s.removeDemoData(w, r)
	default:
		writeErr(w, 405, "method_not_allowed", "use GET, POST or DELETE")
	}
}

// demoBaseURL is where the proxy reaches this server's fake upstream: the
// listen port on loopback, so it works behind any reverse proxy. Without a
// configured address (tests, embedding) the request's own host is used.
func (s *Server) demoBaseURL(r *http.Request) string {
	host := r.Host
	if addr := s.Cfg.Addr; addr != "" {
		if h, port, err := net.SplitHostPort(addr); err == nil && port != "" {
			if h == "" || h == "0.0.0.0" || h == "::" {
				h = "127.0.0.1"
			}
			host = net.JoinHostPort(h, port)
		}
	}
	return "http://" + host + demoUpstream
}

func (s *Server) loadDemoData(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Force bool `json:"force"`
	}
	if b, _ := io.ReadAll(io.LimitReader(r.Body, 4<<10)); len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &in); err != nil {
			writeErr(w, 400, "bad_json", err.Error())
			return
		}
	}
	ctx := r.Context()
	if _, ok := s.loadDemo(r); ok {
		writeErr(w, 409, "demo_loaded", "demo data is already loaded; remove it first")
		return
	}
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if len(svcs) > 0 && !in.Force {
		writeErr(w, 409, "has_services", "this server already has services; send {\"force\": true} to add the demo anyway")
		return
	}
	p, err := s.Store.GetProvider(ctx, "generic")
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	sv, err := catalog.ServiceDefaults(p)
	if err != nil {
		writeErr(w, 500, "bad_provider", err.Error())
		return
	}
	sv.Slug = catalog.UniqueServiceSlug(ctx, s.Store, "demo")
	sv.Name = "Demo provider (sample data)"
	sv.Category = "Demo"
	sv.Description = "Sample data: a fake API this server answers itself, so you can try a call without a real key. Remove it from Getting started."
	sv.BaseURL = s.demoBaseURL(r)
	sv.AuthType, sv.AuthName = "header", demoKeyHeader
	sv.TargetTrusted = true
	created, err := s.Store.CreateService(ctx, sv)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	st := demoState{ServiceID: created.ID, ServiceSlug: created.Slug}
	undo := func() { _ = s.Store.DeleteService(ctx, created.ID) }
	var nks []store.NewKey
	for _, label := range []string{"Demo key 1", "Demo key 2"} {
		secret := crypto.RandomToken("demo_", 16)
		lbl := label
		nk, err := s.newKey(created, keyInput{Secret: &secret, Label: &lbl}, userID(r))
		if err != nil {
			undo()
			writeStoreErr(w, err)
			return
		}
		nks = append(nks, nk)
		st.KeyHashes = append(st.KeyHashes, crypto.SHA256(secret))
	}
	if _, _, err := s.Store.CreateKeys(ctx, nks); err != nil {
		undo()
		writeStoreErr(w, err)
		return
	}
	tok, prefix, hash := newAppToken()
	a := &store.App{Name: "Demo app", Description: "Sample data: calls the demo provider. Remove it from Getting started.",
		Mode: "proxy", Active: true, AllowedServices: []string{created.Slug}, TokenPrefix: prefix}
	app, _, err := s.Store.CreateAppWithToken(ctx, a, "Demo", store.TokenPrefixHint(prefix), hash, userID(r))
	if err != nil {
		undo()
		writeStoreErr(w, err)
		return
	}
	st.AppID = app.ID
	b, _ := json.Marshal(st)
	if err := s.Store.SetMeta(ctx, demoMeta, string(b)); err != nil {
		undo()
		_ = s.Store.DeleteApp(ctx, app.ID)
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(ctx, userFrom(r), "demo.load", created.Slug, "demo service, 2 keys and an app")
	s.forgetRotating()
	base := s.publicURL(r)
	writeJSON(w, 201, map[string]any{
		"service": map[string]any{"id": created.ID, "slug": created.Slug, "name": created.Name},
		"app":     map[string]any{"id": app.ID, "name": app.Name},
		"token":   tok,
		"try": map[string]any{
			"method": "GET", "path": "/v1/hello",
			"curl": "curl -H 'X-Acnologia-Token: " + tok + "' " + base + "/proxy/" + created.Slug + "/v1/hello",
		},
	})
}

func (s *Server) removeDemoData(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadDemo(r)
	if !ok {
		writeErr(w, 404, "no_demo", "no demo data is loaded")
		return
	}
	ctx := r.Context()
	if err := s.Store.DeleteApp(ctx, st.AppID); err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreErr(w, err)
		return
	}
	if err := s.Store.DeleteService(ctx, st.ServiceID); err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreErr(w, err)
		return
	}
	s.Cache.PurgePrefix(servicePrefix(st.ServiceID))
	s.mcpSessions.deleteService(st.ServiceID)
	_ = s.Store.ClearKeySourcesOf(ctx, st.ServiceID)
	if err := s.Store.SetMeta(ctx, demoMeta, ""); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.forgetRotating()
	s.Store.AddAudit(ctx, userFrom(r), "demo.remove", st.ServiceSlug, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// demoUpstreamHandler is the fake provider behind the demo service. It knows
// only the demo keys' hashes and answers a few endpoints.
func (s *Server) demoUpstreamHandler(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadDemo(r)
	if !ok {
		writeErr(w, 404, "not_found", "no demo data is loaded")
		return
	}
	got := crypto.SHA256(r.Header.Get(demoKeyHeader))
	which := 0
	for i, h := range st.KeyHashes {
		if subtle.ConstantTimeCompare([]byte(got), []byte(h)) == 1 {
			which = i + 1
		}
	}
	if which == 0 {
		writeErr(w, 401, "invalid_key", "the demo API needs a demo key in "+demoKeyHeader)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, demoUpstream)
	keyName := "Demo key " + string(rune('0'+which))
	switch {
	case path == "/v1/hello" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{
			"message": "Hello from the demo provider. Acnologia added the key; your app only sent its token.",
			"key":     keyName,
		})
	case path == "/v1/models" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"data": []map[string]any{
			{"id": "demo-small", "context": 8192}, {"id": "demo-large", "context": 128000},
		}})
	case path == "/v1/echo" && r.Method == http.MethodPost:
		var body any
		_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
		writeJSON(w, 200, map[string]any{"you_sent": body, "key": keyName})
	default:
		writeErr(w, 404, "not_found", "the demo API has GET /v1/hello, GET /v1/models and POST /v1/echo")
	}
}

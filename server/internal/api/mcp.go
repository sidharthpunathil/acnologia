package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/cogito-keys/cogito/internal/pool"
	"github.com/cogito-keys/cogito/internal/store"
)

// MCP over streamable HTTP. Each POST carries one JSON-RPC message and gets a
// JSON response. Auth is the app token, same as the consumer API.

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const mcpProtocol = "2025-06-18"

var mcpTools = []map[string]any{
	{
		"name":        "list_services",
		"description": "List the API services this app can use and whether each has a key available right now.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		"name":        "pool_status",
		"description": "Check whether a service currently has an available key.",
		"inputSchema": obj(map[string]any{"service": str("Service slug, for example serpapi")}, "service"),
	},
	{
		"name":        "get_key",
		"description": "Lease an API key for a service. Returns the secret, where to put it (query param or header), and a lease_id. Always call report_key when done: on services with a concurrency limit the lease reserves the key until reported.",
		"inputSchema": obj(map[string]any{
			"service":         str("Service slug"),
			"exclude_key_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Key ids to skip"},
			"ttl_sec":         map[string]any{"type": "integer", "description": "How long to hold the key. On concurrency limited services the key is reserved for you until you report or this expires."},
			"wait_sec":        map[string]any{"type": "integer", "description": "Wait up to this many seconds (max 60) for a key when all are busy"},
		}, "service"),
	},
	{
		"name":        "report_key",
		"description": "Report how a leased key performed. Use outcome rate_limited, quota_exhausted or invalid with rotate=true to get the next key.",
		"inputSchema": obj(map[string]any{
			"lease_id":        str("Lease id from get_key"),
			"outcome":         map[string]any{"type": "string", "enum": []string{"ok", "rate_limited", "quota_exhausted", "invalid", "error"}},
			"retry_after_sec": map[string]any{"type": "integer"},
			"message":         str("Error text from the provider"),
			"rotate":          map[string]any{"type": "boolean", "description": "Return a new lease when the outcome is a failure"},
		}, "lease_id", "outcome"),
	},
	{
		"name":        "proxy_request",
		"description": "Call a service through Cogito. Cogito picks a key, injects it, and retries with the next key on rate limits. Example: service=serpapi, path=/search.json, query={\"q\":\"coffee\",\"engine\":\"google\"}.",
		"inputSchema": obj(map[string]any{
			"service": str("Service slug"),
			"method":  map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, "default": "GET"},
			"path":    str("Path on the provider, for example /search.json or /v1/text-to-speech/{voice_id}"),
			"query":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"body":    map[string]any{"description": "JSON body for POST/PUT/PATCH"},
		}, "service", "path"),
	},
}

func obj(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		w.Header().Set("Allow", "POST, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	a, code, msg := s.authApp(r)
	if a == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cogito"`)
		writeErr(w, code, "unauthenticated", msg)
		return
	}
	ctx := context.WithValue(r.Context(), appKey, a)
	var req rpcReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": nil, "error": rpcErr{-32700, "parse error"}})
		return
	}
	if len(req.ID) == 0 { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rerr := s.mcpDispatch(ctx, r, req)
	resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if rerr != nil {
		resp["error"] = rerr
	} else {
		resp["result"] = result
	}
	writeJSON(w, 200, resp)
}

func (s *Server) mcpDispatch(ctx context.Context, r *http.Request, req rpcReq) (any, *rpcErr) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := p.ProtocolVersion
		if v == "" {
			v = mcpProtocol
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "cogito", "version": s.Cfg.Version},
			"instructions":    "Cogito hands out API keys from a pool and rotates them on rate limits. Prefer proxy_request; use get_key and report_key when you must call the provider yourself.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcErr{-32602, "invalid params"}
		}
		out, err := s.mcpCall(ctx, r, p.Name, p.Arguments)
		if err != nil {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "structuredContent": out}, nil
	}
	return nil, &rpcErr{-32601, "method not found: " + req.Method}
}

func (s *Server) mcpCall(ctx context.Context, r *http.Request, name string, raw json.RawMessage) (map[string]any, error) {
	a := ctx.Value(appKey).(*store.App)
	var args struct {
		Service       string            `json:"service"`
		ExcludeKeyIDs []int64           `json:"exclude_key_ids"`
		TTLSec        int               `json:"ttl_sec"`
		WaitSec       int               `json:"wait_sec"`
		LeaseID       string            `json:"lease_id"`
		Outcome       string            `json:"outcome"`
		RetryAfterSec int               `json:"retry_after_sec"`
		Message       string            `json:"message"`
		Rotate        bool              `json:"rotate"`
		Method        string            `json:"method"`
		Path          string            `json:"path"`
		Query         map[string]string `json:"query"`
		Body          json.RawMessage   `json:"body"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %v", err)
		}
	}
	svcFor := func() (*store.Service, error) {
		if !a.CanUse(args.Service) {
			return nil, fmt.Errorf("this app cannot use service %q", args.Service)
		}
		sv, err := s.Store.GetServiceBySlug(ctx, args.Service)
		if err != nil {
			return nil, fmt.Errorf("no service named %q", args.Service)
		}
		return sv, nil
	}
	switch name {
	case "list_services", "pool_status":
		svcs, err := s.Store.ListServices(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.Store.FillStats(ctx, svcs, true, s.Pool.InFlight()); err != nil {
			return nil, err
		}
		list := []map[string]any{}
		for _, sv := range svcs {
			if !a.CanUse(sv.Slug) || (name == "pool_status" && sv.Slug != args.Service) {
				continue
			}
			list = append(list, map[string]any{"slug": sv.Slug, "name": sv.Name, "category": sv.Category,
				"available": sv.KeyCounts.Available > sv.KeyCounts.Busy, "base_url": sv.BaseURL})
		}
		if name == "pool_status" {
			if len(list) == 0 {
				return nil, fmt.Errorf("no service named %q for this app", args.Service)
			}
			return list[0], nil
		}
		return map[string]any{"services": list}, nil
	case "get_key":
		if !a.AllowsLease() {
			return nil, errors.New("this app is proxy only, use proxy_request")
		}
		sv, err := svcFor()
		if err != nil {
			return nil, err
		}
		l, err := s.issueLease(ctx, a, sv, args.ExcludeKeyIDs, time.Duration(args.TTLSec)*time.Second, time.Duration(min(max(args.WaitSec, 0), 60))*time.Second)
		if err != nil {
			var nk *pool.NoKeyError
			if errors.As(err, &nk) {
				return nil, fmt.Errorf("%s, retry in %.0f seconds", nk.Error(), math.Ceil(nk.RetryAfter.Seconds()))
			}
			return nil, err
		}
		return toMap(l), nil
	case "report_key":
		next, _, err := s.report(ctx, a, args.LeaseID, reportInput{Outcome: args.Outcome, RetryAfterSec: args.RetryAfterSec, Message: args.Message, Rotate: args.Rotate})
		if err != nil {
			return nil, err
		}
		res := map[string]any{"ok": true, "next": nil}
		if next != nil {
			res["next"] = toMap(next)
		}
		return res, nil
	case "proxy_request":
		if _, err := svcFor(); err != nil {
			return nil, err
		}
		return s.mcpProxy(ctx, r, args.Service, args.Method, args.Path, args.Query, args.Body)
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

// mcpProxy runs the regular proxy handler in-process and captures the result.
func (s *Server) mcpProxy(ctx context.Context, orig *http.Request, service, method, path string, query map[string]string, body json.RawMessage) (map[string]any, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case "":
		method = http.MethodGet
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, fmt.Errorf("method must be GET, POST, PUT, PATCH or DELETE")
	}
	q := url.Values{}
	for k, v := range query {
		q.Set(k, v)
	}
	target := "/proxy/" + service + "/" + strings.TrimPrefix(path, "/")
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	var rdr io.Reader = http.NoBody
	if len(body) > 0 && string(body) != "null" {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://cogito.local"+target, rdr)
	if err != nil {
		return nil, fmt.Errorf("bad path or query: %v", err)
	}
	req.RemoteAddr = orig.RemoteAddr
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("service", service)
	rctx.URLParams.Add("*", strings.TrimPrefix(path, "/"))
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	const limit = 200 << 10
	// Keep at most limit+1 bytes: the HTTP proxy streams, but MCP buffers.
	rec := &cappedRecorder{ResponseRecorder: httptest.NewRecorder(), max: limit + 1}
	s.proxy(rec, req)

	out := map[string]any{"status": rec.Code, "content_type": rec.Header().Get("Content-Type"), "key_id": rec.Header().Get("X-Cogito-Key-Id")}
	b := rec.Body.Bytes()
	ct := rec.Header().Get("Content-Type")
	switch {
	case strings.Contains(ct, "json"):
		var v any
		if json.Unmarshal(b, &v) == nil && len(b) <= limit {
			out["body"] = v
		} else {
			out["body"] = string(truncate(b, limit))
		}
	case utf8.Valid(b) && !strings.HasPrefix(ct, "audio/") && !strings.HasPrefix(ct, "image/"):
		out["body"] = string(truncate(b, limit))
	default:
		out["bytes"] = len(b)
		if len(b) <= limit {
			out["body_base64"] = base64.StdEncoding.EncodeToString(b)
		} else {
			out["note"] = "binary response too large for MCP, use the HTTP proxy directly"
		}
	}
	return out, nil
}

// cappedRecorder records a response but stops accepting body bytes past max,
// which makes the proxy stop reading the upstream body.
type cappedRecorder struct {
	*httptest.ResponseRecorder
	max int
}

func (c *cappedRecorder) Write(p []byte) (int, error) {
	room := c.max - c.Body.Len()
	if room <= 0 {
		return 0, io.ErrShortWrite
	}
	if len(p) > room {
		n, _ := c.ResponseRecorder.Write(p[:room])
		return n, io.ErrShortWrite
	}
	return c.ResponseRecorder.Write(p)
}

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

func toMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}

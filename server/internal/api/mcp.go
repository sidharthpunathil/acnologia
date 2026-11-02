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

	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
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

type mcpCtxKey int

// mcpLimitKey carries the app's max result size (KB) into tool handlers.
const mcpLimitKey mcpCtxKey = 1

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
		w.Header().Set("WWW-Authenticate", `Bearer realm="acnologia"`)
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
	a := ctx.Value(appKey).(*store.App)
	st, err := s.Store.GetMCPSettings(ctx, a.ID)
	if err != nil {
		return nil, &rpcErr{-32603, "could not load MCP settings"}
	}
	ctx = context.WithValue(ctx, mcpLimitKey, st.MaxResultKB)
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
		caps := map[string]any{"tools": map[string]any{"listChanged": false}}
		if st.Resources {
			caps["resources"] = map[string]any{"listChanged": false, "subscribe": false}
		}
		if st.Prompts {
			caps["prompts"] = map[string]any{"listChanged": false}
		}
		instr := "Acnologia hands out API keys from a pool and rotates them on rate limits. Prefer the generated service tools or proxy_request: the key never reaches you. Use get_key and report_key only when you must call the provider yourself; pass exclusive=true to keep a key to yourself."
		if strings.TrimSpace(st.Instructions) != "" {
			instr += "\n\n" + strings.TrimSpace(st.Instructions)
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    caps,
			"serverInfo":      map[string]any{"name": "acnologia", "title": "Acnologia: " + a.Name, "version": s.Cfg.Version},
			"instructions":    instr,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.mcpTools(ctx, a, st)}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcErr{-32602, "invalid params"}
		}
		t := findTool(s.mcpTools(ctx, a, st), p.Name)
		if t == nil {
			return nil, &rpcErr{-32602, "unknown or disabled tool: " + p.Name}
		}
		var out map[string]any
		var err error
		switch t.Set {
		case setService:
			out, err = s.callServiceTool(ctx, r, t, p.Arguments)
		case setInsights, setManage:
			out, err = s.mcpAdminCall(ctx, a, p.Name, p.Arguments)
		default:
			out, err = s.mcpCall(ctx, r, p.Name, p.Arguments)
		}
		if err != nil {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "structuredContent": out}, nil
	case "resources/list":
		if !st.Resources {
			return nil, &rpcErr{-32601, "resources are off for this app"}
		}
		return map[string]any{"resources": s.mcpResources(ctx, a)}, nil
	case "resources/templates/list":
		if !st.Resources {
			return nil, &rpcErr{-32601, "resources are off for this app"}
		}
		return map[string]any{"resourceTemplates": []map[string]any{{"uriTemplate": "acnologia://services/{slug}", "name": "service", "title": "A service", "mimeType": "application/json"}}}, nil
	case "resources/read":
		if !st.Resources {
			return nil, &rpcErr{-32601, "resources are off for this app"}
		}
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &p)
		res, err := s.mcpReadResource(ctx, a, p.URI)
		if err != nil {
			return nil, &rpcErr{-32002, err.Error()}
		}
		return res, nil
	case "prompts/list":
		if !st.Prompts {
			return nil, &rpcErr{-32601, "prompts are off for this app"}
		}
		return map[string]any{"prompts": mcpPrompts}, nil
	case "prompts/get":
		if !st.Prompts {
			return nil, &rpcErr{-32601, "prompts are off for this app"}
		}
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		res, err := s.mcpGetPrompt(ctx, a, st, p.Name, p.Arguments)
		if err != nil {
			return nil, &rpcErr{-32602, err.Error()}
		}
		return res, nil
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
		Exclusive     *bool             `json:"exclusive"`
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
		if hit := s.appLimitHit(ctx, a); hit != nil {
			return nil, fmt.Errorf("this app reached its %s limit, it resets %s", strings.ReplaceAll(hit.Limit, "_", " "), hit.ResetAt.Format(time.RFC3339))
		}
		st, _ := s.Store.GetMCPSettings(ctx, a.ID)
		exclusive := st.ExclusiveDefault
		if args.Exclusive != nil {
			exclusive = *args.Exclusive
		}
		l, err := s.issueLeaseOpts(ctx, a, sv, args.ExcludeKeyIDs, time.Duration(args.TTLSec)*time.Second, time.Duration(min(max(args.WaitSec, 0), 60))*time.Second, exclusive)
		if err != nil {
			var nk *pool.NoKeyError
			if errors.As(err, &nk) {
				return nil, fmt.Errorf("%s, retry in %.0f seconds", nk.Error(), math.Ceil(nk.RetryAfter.Seconds()))
			}
			return nil, err
		}
		return toMap(l), nil
	case "renew_key":
		exp, err := s.renewLease(ctx, a, args.LeaseID, time.Duration(args.TTLSec)*time.Second)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "expires_at": exp}, nil
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
	req, err := http.NewRequestWithContext(ctx, method, "http://acnologia.local"+target, rdr)
	if err != nil {
		return nil, fmt.Errorf("bad path or query: %v", err)
	}
	if orig != nil {
		req.RemoteAddr = orig.RemoteAddr
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("service", service)
	rctx.URLParams.Add("*", strings.TrimPrefix(path, "/"))
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	limit := 200 << 10
	if kb, ok := ctx.Value(mcpLimitKey).(int); ok && kb > 0 {
		limit = min(kb, 2048) << 10
	}
	// Keep at most limit+1 bytes: the HTTP proxy streams, but MCP buffers.
	rec := &cappedRecorder{ResponseRecorder: httptest.NewRecorder(), max: limit + 1}
	s.proxy(rec, req)

	out := map[string]any{"status": rec.Code, "content_type": rec.Header().Get("Content-Type"), "key_id": rec.Header().Get("X-Acnologia-Key-Id")}
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

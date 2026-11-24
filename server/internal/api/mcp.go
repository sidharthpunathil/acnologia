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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
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

// mcpVersions are the protocol revisions both MCP servers speak. The tools
// use only what every one of them has, plus title and structuredContent,
// which older clients ignore.
var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// negotiateVersion answers initialize: the client's version when this server
// speaks it, else this server's own (the client then decides whether to go on).
func negotiateVersion(params json.RawMessage) string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	for _, v := range mcpVersions {
		if v == p.ProtocolVersion {
			return v
		}
	}
	return mcpProtocol
}

type mcpCtxKey int

// mcpLimitKey carries the app's max result size (KB) into tool handlers.
const mcpLimitKey mcpCtxKey = 1

// mcpBaseKey carries this server's public URL, for proxy URLs in guides.
const mcpBaseKey mcpCtxKey = 2

// mcpReadOnlyKey carries effective read-only mode into tool handlers.
const mcpReadOnlyKey mcpCtxKey = 3

// mcpBase is the public URL stored by mcpDispatch, else the configured one.
func mcpBase(ctx context.Context, s *Server) string {
	if b, ok := ctx.Value(mcpBaseKey).(string); ok && b != "" {
		return b
	}
	return strings.TrimRight(s.Cfg.PublicURL, "/")
}

func obj(props map[string]any, required ...string) map[string]any {
	// A nil slice encodes as null, which MCP clients reject: required must be an array.
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodGet:
		s.mcpStream(w, r)
		return
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	a, f := s.authAppFull(r)
	if f != nil {
		s.mcpChallenge(w, r, f.Status)
		f.write(w)
		return
	}
	ctx := store.WithAppToken(context.WithValue(r.Context(), appKey, a), a.TokenID)
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
	if a.ShareID != nil {
		// A share's token (another server's or a person's) gets the core
		// and service tools only: never key insights or key management.
		st.Insights, st.Manage = false, false
	}
	if a.TokenReadOnly {
		st.ReadOnly = true
	}
	if r != nil && (truthy(r.URL.Query().Get("read_only")) || truthy(r.Header.Get("X-Acnologia-MCP-Read-Only"))) {
		st.ReadOnly = true
	}
	ctx = context.WithValue(ctx, mcpLimitKey, st.MaxResultKB)
	ctx = context.WithValue(ctx, mcpReadOnlyKey, st.ReadOnly)
	if r != nil {
		ctx = context.WithValue(ctx, mcpBaseKey, s.publicURL(r))
	}
	switch req.Method {
	case "initialize":
		v := negotiateVersion(req.Params)
		caps := map[string]any{"tools": map[string]any{"listChanged": true}}
		if st.Resources {
			caps["resources"] = map[string]any{"listChanged": true, "subscribe": false}
		}
		if st.Prompts {
			caps["prompts"] = map[string]any{"listChanged": false}
		}
		instr := "Acnologia hands out API keys from a pool and rotates them on rate limits. Prefer the generated service tools or proxy_request: the key never reaches you. When a tool you need is not listed, search_tools finds it across every provider (for example: linkedin jobs) and call_tool runs it; the list follows the providers on this server and changes as they are added. Use get_key and report_key only when you must call the provider yourself; pass exclusive=true to keep a key to yourself. Read service_guide before calling a service you have not used: it lists the endpoints, parameters, limits and errors. When you build an app or application, give it Acnologia as its HTTPS_PROXY (the app token as the proxy password, the proxy CA from /api/v1/proxy/ca.pem trusted) and have it call the provider's own URL, so its code holds no key and no Acnologia client: the guide shows the proxy URL. Keep Acnologia out of the app's UI, copy, page titles and user facing errors; it is infrastructure the app's users never see."
		if st.ReadOnly {
			instr += " This connection is in read-only mode: only read-only tools are available."
		}
		if strings.TrimSpace(st.Instructions) != "" {
			instr += "\n\n" + strings.TrimSpace(st.Instructions)
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    caps,
			"serverInfo":      map[string]any{"name": "acnologia", "title": "Acnologia: " + a.Name, "version": s.Cfg.Version},
			"instructions":    publicText(instr),
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": presentTools(s.mcpListedTools(ctx, a, st))}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcErr{-32602, "invalid params"}
		}
		t := s.mcpTool(ctx, a, st, p.Name)
		if t == nil {
			// The spec's answer to an unknown tool is a protocol error.
			return nil, &rpcErr{-32602, publicText(s.unknownToolText(ctx, a, st, p.Name) + " Call tools/list, or search_tools to find a service tool.")}
		}
		return s.toolCallResult(ctx, r, a, st, t, p.Arguments), nil
	case "resources/list":
		if !st.Resources {
			return nil, &rpcErr{-32601, "resources are off for this app"}
		}
		return map[string]any{"resources": s.mcpResources(ctx, a)}, nil
	case "resources/templates/list":
		if !st.Resources {
			return nil, &rpcErr{-32601, "resources are off for this app"}
		}
		return map[string]any{"resourceTemplates": []map[string]any{
			{"uriTemplate": "acnologia://services/{slug}", "name": "service", "title": "A service", "mimeType": "application/json"},
			{"uriTemplate": "acnologia://services/{slug}/guide", "name": "service_guide", "title": "How to call a service", "mimeType": "text/markdown"},
		}}, nil
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

// toolCallResult runs a tool and gives the tools/call result. call_tool is
// unwrapped first, so a tool reached through it answers in the same shape
// as when called by name: an upstream MCP tool's result as the upstream
// gave it (isError included), any other tool's as text and
// structuredContent.
func (s *Server) toolCallResult(ctx context.Context, r *http.Request, a *store.App, st store.MCPSettings, t *mcpTool, args json.RawMessage) map[string]any {
	// Errors are shaped for the tool the client called: call_tool's, not
	// the target's, since the client checks against the called tool.
	called := *t
	if t.Name == "call_tool" {
		target, inner, err := s.resolveCallTool(ctx, a, st, args)
		if err != nil {
			return toolErrorResult(err, called)
		}
		t, args = target, inner
	}
	if t.upstreamMCP {
		res, err := s.callUpstreamMCPTool(ctx, r, a, st, t, args)
		if err != nil {
			return toolErrorResult(err, called)
		}
		return res
	}
	out, err := s.runTool(ctx, r, a, st, t, args)
	if err != nil {
		return toolErrorResult(err, called)
	}
	b, _ := json.Marshal(out)
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "structuredContent": out}
}

// toolErrorResult is an isError result: the message and the registry's
// hint as text, for clients that read only text, and {code, message, hint,
// fix, doc_url, retry_after_sec} under _meta["acnologia/error"]. The same
// object is the structuredContent when the called tool declares no output
// schema with required fields: clients check structuredContent against the
// schema even on an error, and an error object would fail it.
func toolErrorResult(err error, called mcpTool) map[string]any {
	info := toolErrorInfo(err)
	text := info["message"].(string)
	if h, ok := info["hint"].(string); ok && h != "" {
		text += "\n\nHint (" + info["code"].(string) + "): " + h
	}
	res := map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": true,
		"_meta":   map[string]any{"acnologia/error": info},
	}
	if schemaAcceptsError(presentTools([]mcpTool{called})[0].OutputSchema) {
		res["structuredContent"] = info
	}
	return res
}

// schemaAcceptsError reports whether an error object passes a tool's output
// schema: there is none, or it requires no field.
func schemaAcceptsError(schema map[string]any) bool {
	if schema == nil {
		return true
	}
	switch req := schema["required"].(type) {
	case []string:
		return len(req) == 0
	case []any:
		return len(req) == 0
	case nil:
		return true
	}
	return false
}

func (s *Server) mcpCall(ctx context.Context, r *http.Request, name string, raw json.RawMessage) (map[string]any, error) {
	a := ctx.Value(appKey).(*store.App)
	ro, _ := ctx.Value(mcpReadOnlyKey).(bool)
	if a.TokenReadOnly {
		ro = true
	}
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
		Limit         int               `json:"limit"`
		Offset        int               `json:"offset"`
		Exclusive     *bool             `json:"exclusive"`
		Method        string            `json:"method"`
		Path          string            `json:"path"`
		Query         map[string]string `json:"query"`
		Body          json.RawMessage   `json:"body"`
		IdemKey       string            `json:"idempotency_key"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, toolErrf("invalid_arguments", "invalid arguments: %v", err)
		}
	}
	svcFor := func() (*store.Service, error) {
		if !a.CanUse(args.Service) {
			return nil, toolErrf("service_not_allowed", "%s; list_services shows them all", s.notAllowedMsg(ctx, a, args.Service))
		}
		sv, err := s.Store.GetServiceBySlug(ctx, args.Service)
		if err != nil {
			return nil, toolErrf("unknown_service", "%s; list_services shows the services this app can use", s.unknownServiceMsg(ctx, a, args.Service))
		}
		if !s.sharedAs(ctx, a, sv) {
			return nil, toolErrf("service_not_allowed", "%s; list_services shows them all", s.notAllowedMsg(ctx, a, args.Service))
		}
		return sv, nil
	}
	switch name {
	case "whoami":
		return s.meFacts(ctx, r, a, meOpts{usable: true, readOnly: ro})
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
				return nil, toolErrf("unknown_service", "%s for this app; list_services shows the services it can use", s.unknownServiceMsg(ctx, a, args.Service))
			}
			return list[0], nil
		}
		page, pg := paginate(list, args.Offset, args.Limit, 100, 500)
		return pg.into(map[string]any{"services": page}), nil
	case "get_key":
		if ro {
			return nil, toolErrf("read_only_token", "read-only mode cannot lease raw API keys")
		}
		if !a.AllowsLease() {
			return nil, toolErrf("lease_not_allowed", "this app is proxy only, use proxy_request")
		}
		sv, err := svcFor()
		if err != nil {
			return nil, err
		}
		hit, release := s.admitApp(ctx, a, costPerRequest(sv, providers.Get(sv.Provider), "", ""))
		if hit != nil {
			return nil, &toolErr{code: "app_limit_reached", msg: fmt.Sprintf("this app reached its %s limit, it resets %s", strings.ReplaceAll(hit.Limit, "_", " "), hit.ResetAt.Format(time.RFC3339)),
				retryAfter: max(int(math.Ceil(time.Until(hit.ResetAt).Seconds())), 0)}
		}
		defer release()
		st, _ := s.Store.GetMCPSettings(ctx, a.ID)
		exclusive := st.ExclusiveDefault
		if args.Exclusive != nil {
			exclusive = *args.Exclusive
		}
		l, err := s.issueLeaseOpts(ctx, a, sv, args.ExcludeKeyIDs, time.Duration(args.TTLSec)*time.Second, time.Duration(min(max(args.WaitSec, 0), 60))*time.Second, exclusive)
		if err != nil {
			var nk *pool.NoKeyError
			if errors.As(err, &nk) {
				code := "no_key_available"
				if nk.Busy {
					code = "all_keys_busy"
				}
				retry := int(math.Ceil(nk.RetryAfter.Seconds()))
				return nil, &toolErr{code: code, msg: fmt.Sprintf("%s, retry in %d seconds", nk.Error(), retry), retryAfter: retry}
			}
			return nil, err
		}
		return toMap(l), nil
	case "renew_key":
		if ro {
			return nil, toolErrf("read_only_token", "read-only mode cannot renew key leases")
		}
		exp, err := s.renewLease(ctx, a, args.LeaseID, time.Duration(args.TTLSec)*time.Second)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "expires_at": exp}, nil
	case "report_key":
		if ro {
			return nil, toolErrf("read_only_token", "read-only mode cannot report key leases")
		}
		next, status, err := s.report(ctx, a, args.LeaseID, reportInput{Outcome: args.Outcome, RetryAfterSec: args.RetryAfterSec, Message: args.Message, Rotate: args.Rotate})
		if err != nil {
			if errors.Is(err, errReadOnlyLease) || status >= 500 {
				return nil, err
			}
			code := "report_failed"
			if status == 400 {
				code = "invalid_arguments"
			}
			return nil, toolErrf(code, "%s", err.Error())
		}
		res := map[string]any{"ok": true, "next": nil}
		if next != nil {
			res["next"] = toMap(next)
		}
		return res, nil
	case "service_guide":
		d, err := s.mcpServiceGuide(ctx, a, args.Service)
		if err != nil {
			return nil, err
		}
		return map[string]any{"service": args.Service, "generated": d.Generated, "markdown": publicText(d.Render(false))}, nil
	case "proxy_request":
		if ro {
			m := strings.ToUpper(strings.TrimSpace(args.Method))
			if m != "" && m != http.MethodGet && m != http.MethodHead {
				return nil, toolErrf("read_only_token", "this connection is read-only; only GET and HEAD requests are allowed")
			}
		}
		sv, err := svcFor()
		if err != nil {
			return nil, err
		}
		var hdr map[string]string
		if k := strings.TrimSpace(args.IdemKey); k != "" {
			hdr = map[string]string{"Idempotency-Key": k}
		}
		return s.mcpProxy(withResolvedService(ctx, sv), r, args.Service, args.Method, args.Path, args.Query, args.Body, hdr)
	}
	return nil, toolErrf("unknown_tool", "unknown tool %q", name)
}

// mcpDrainMax is how far past the result limit an MCP proxy call keeps
// counting (and dropping) the body, to say how long the whole answer was.
const mcpDrainMax = 8 << 20

// mcpProxy runs the regular proxy handler in-process and captures the result.
// hdr holds request headers the tool sets, such as Idempotency-Key.
func (s *Server) mcpProxy(ctx context.Context, orig *http.Request, service, method, path string, query map[string]string, body json.RawMessage, hdr map[string]string) (map[string]any, error) {
	limit := 200 << 10
	if kb, ok := ctx.Value(mcpLimitKey).(int); ok && kb > 0 {
		limit = min(kb, 2048) << 10
	}
	rec := &cappedRecorder{ResponseRecorder: httptest.NewRecorder(), max: limit + 1, drain: mcpDrainMax}
	if err := s.proxyCaptureInto(ctx, orig, service, method, path, query, body, hdr, rec); err != nil {
		return nil, err
	}
	out := map[string]any{"status": rec.Code, "content_type": rec.Header().Get("Content-Type")}
	liftProxyFacts(out, rec.Header())
	full := rec.Body.Bytes()
	b := full
	cut := len(full) > limit
	if cut {
		b = full[:limit]
		out["truncated"] = true
		out["bytes_total"] = rec.seen
		if rec.seen-rec.max > rec.drain {
			// The proxy stopped reading: the body is longer still.
			out["bytes_total_at_least"] = true
		}
	}
	ct := rec.Header().Get("Content-Type")
	text := !strings.HasPrefix(ct, "audio/") && !strings.HasPrefix(ct, "image/")
	switch {
	case strings.Contains(ct, "json") && !cut && json.Valid(b) && utf8.Valid(b):
		// Embedded as is, not decoded into maps only to be encoded again.
		// Invalid UTF-8 takes the decoding path below, which replaces it.
		out["body"] = json.RawMessage(b)
	case strings.Contains(ct, "json") && !cut:
		var v any
		if json.Unmarshal(b, &v) == nil {
			out["body"] = v
		} else {
			out["body"] = string(b)
		}
	case strings.Contains(ct, "json") || (text && utf8.Valid(wholeRunes(b))):
		// Cut JSON is no longer JSON: it comes back as text, marked cut.
		out["body"] = string(wholeRunes(b))
	case !cut:
		out["bytes"] = len(b)
		out["body_base64"] = base64.StdEncoding.EncodeToString(b)
	default:
		out["bytes"] = rec.seen
		out["note"] = "binary response too large for MCP, use the HTTP proxy directly"
	}
	if cut && out["note"] == nil {
		out["note"] = fmt.Sprintf("the body was cut at %d bytes; ask for less (a smaller page or fewer fields) or raise the app's max result size", limit)
	}
	return out, nil
}

// liftProxyFacts copies what the proxy says about a call from its headers
// into an MCP result: which key and service answered, how many attempts it
// took, which services a capability tried, whether the cache answered, and
// how long the provider asked to wait.
func liftProxyFacts(out map[string]any, h http.Header) {
	// Share and person principals get no key id header, so no key_id either.
	if id := h.Get(headerPrefix + "Key-Id"); id != "" {
		out["key_id"] = id
	}
	if n, err := strconv.Atoi(h.Get(headerPrefix + "Attempts")); err == nil && n > 0 {
		out["attempts"] = n
	}
	if v := h.Get(headerPrefix + "Service"); v != "" {
		out["served_by"] = v
	}
	if v := h.Get(headerPrefix + "Tried"); v != "" {
		out["tried"] = v
	}
	if v := h.Get(headerPrefix + "Cache"); v != "" {
		out["cache"] = v
	}
	if sec, ok := retryAfterSec(h.Get("Retry-After")); ok {
		out["retry_after_sec"] = sec
	}
}

// retryAfterSec reads a Retry-After header: seconds, or an HTTP date.
func retryAfterSec(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return n, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(int(math.Ceil(time.Until(t).Seconds())), 0), true
	}
	return 0, false
}

// wholeRunes drops a UTF-8 sequence cut off at the end of b.
func wholeRunes(b []byte) []byte {
	i := len(b) - 1
	for i >= 0 && i > len(b)-utf8.UTFMax && !utf8.RuneStart(b[i]) {
		i--
	}
	if i >= 0 && !utf8.FullRune(b[i:]) {
		return b[:i]
	}
	return b
}

// proxyCapture runs the proxy handler in process for the app in ctx and
// keeps at most limit+1 bytes of the response: the HTTP proxy streams, but
// MCP buffers.
func (s *Server) proxyCapture(ctx context.Context, orig *http.Request, service, method, path string, query map[string]string, body json.RawMessage, hdr map[string]string, limit int) (*cappedRecorder, error) {
	rec := &cappedRecorder{ResponseRecorder: httptest.NewRecorder(), max: limit + 1}
	if err := s.proxyCaptureInto(ctx, orig, service, method, path, query, body, hdr, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// proxyCaptureInto is proxyCapture with the caller's recorder.
func (s *Server) proxyCaptureInto(ctx context.Context, orig *http.Request, service, method, path string, query map[string]string, body json.RawMessage, hdr map[string]string, rec *cappedRecorder) error {
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case "":
		method = http.MethodGet
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return fmt.Errorf("method must be GET, POST, PUT, PATCH or DELETE")
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
		return fmt.Errorf("bad path or query: %v", err)
	}
	if orig != nil {
		req.RemoteAddr = orig.RemoteAddr
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("service", service)
	rctx.URLParams.Add("*", strings.TrimPrefix(path, "/"))
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	s.proxy(rec, req)
	return nil
}

// cappedRecorder records a response but stops accepting body bytes past max,
// which makes the proxy stop reading the upstream body. With drain set it
// first keeps counting (and dropping) up to drain more bytes, so seen tells
// how long the whole body was.
type cappedRecorder struct {
	*httptest.ResponseRecorder
	max   int
	drain int
	// seen counts the body bytes the proxy wrote, kept or not.
	seen int
}

func (c *cappedRecorder) Write(p []byte) (int, error) {
	c.seen += len(p)
	room := c.max - c.Body.Len()
	if room <= 0 {
		if c.seen-c.max <= c.drain {
			return len(p), nil
		}
		return 0, io.ErrShortWrite
	}
	if len(p) > room {
		n, _ := c.ResponseRecorder.Write(p[:room])
		if c.seen-c.max <= c.drain && c.drain > 0 {
			return len(p), nil
		}
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

// pageInfo is the paging block a list tool returns next to its items.
type pageInfo struct{ total, count, offset int }

func (p pageInfo) into(m map[string]any) map[string]any {
	m["total"], m["count"], m["offset"] = p.total, p.count, p.offset
	m["has_more"] = p.offset+p.count < p.total
	if p.offset+p.count < p.total {
		m["next_offset"] = p.offset + p.count
	}
	return m
}

// paginate cuts one page out of items: limit 0 means def, and limit is
// capped at most.
func paginate[T any](items []T, offset, limit, def, most int) ([]T, pageInfo) {
	if limit <= 0 {
		limit = def
	}
	limit = min(limit, most)
	offset = min(max(offset, 0), len(items))
	end := min(offset+limit, len(items))
	return items[offset:end], pageInfo{total: len(items), count: end - offset, offset: offset}
}

func toMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/mcpclient"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const (
	mcpSessionTTL     = 15 * time.Minute
	mcpSessionMaxSize = 512
)

type mcpSessKey struct {
	serviceID int64
	keyID     int64
	endpoint  string
}

type mcpSessEntry struct {
	sess     mcpclient.Session
	lastUsed time.Time
}

type mcpSessionCache struct {
	mu      sync.Mutex
	entries map[mcpSessKey]mcpSessEntry
}

func (c *mcpSessionCache) get(k mcpSessKey) (mcpclient.Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		return mcpclient.Session{}, false
	}
	e, ok := c.entries[k]
	if !ok || time.Since(e.lastUsed) > mcpSessionTTL {
		delete(c.entries, k)
		return mcpclient.Session{}, false
	}
	e.lastUsed = time.Now()
	c.entries[k] = e
	return e.sess, true
}

func (c *mcpSessionCache) put(k mcpSessKey, sess mcpclient.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[mcpSessKey]mcpSessEntry{}
	}
	if len(c.entries) >= mcpSessionMaxSize {
		now := time.Now()
		for ek, ev := range c.entries {
			if now.Sub(ev.lastUsed) > mcpSessionTTL {
				delete(c.entries, ek)
			}
		}
		if len(c.entries) >= mcpSessionMaxSize {
			c.entries = map[mcpSessKey]mcpSessEntry{}
		}
	}
	c.entries[k] = mcpSessEntry{sess: sess, lastUsed: time.Now()}
}

func (c *mcpSessionCache) delete(k mcpSessKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, k)
}

func (c *mcpSessionCache) deleteService(serviceID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if k.serviceID == serviceID {
			delete(c.entries, k)
		}
	}
}

// serviceMCPInfo resolves the upstream MCP endpoint and config for a service.
func serviceMCPInfo(sv *store.Service) (endpoint string, fromProvider bool, spec *providers.Spec) {
	spec = providers.Get(sv.Provider)
	if strings.TrimSpace(sv.MCPURL) != "" {
		return providers.ResolveMCPURL(sv.BaseURL, sv.MCPURL, nil), false, spec
	}
	if spec.MCP != nil && strings.TrimSpace(spec.MCP.URL) != "" {
		return providers.ResolveMCPURL(sv.BaseURL, "", spec.MCP), true, spec
	}
	return "", false, spec
}

func mcpAuthFunc(sv *store.Service, secret string) mcpclient.AuthFunc {
	if secret == "" {
		return nil
	}
	auth := serviceAuth(sv)
	return func(req *http.Request) {
		q := req.URL.Query()
		auth.Apply(req, q, secret)
		req.URL.RawQuery = q.Encode()
	}
}

func mcpExtraHeaders(spec *providers.Spec) map[string]string {
	if spec == nil || spec.MCP == nil {
		return nil
	}
	return spec.MCP.Headers
}

// upstreamToolName produces the Acnologia MCP tool name for one upstream tool
// of a service without double-prefixing when the tool already starts with the
// service slug.
func upstreamToolName(sv *store.Service, upstreamName string) string {
	slug := toolName(sv.Slug)
	raw := toolName(upstreamName)
	if raw == "" {
		return ""
	}
	if slug != "" && (raw == slug || strings.HasPrefix(raw, slug+"_")) {
		return raw
	}
	return toolName(sv.Slug, upstreamName)
}


func scrubMCPText(s, secret string) string {
	if secret == "" || s == "" {
		return s
	}
	for _, v := range providers.SecretVariants(secret) {
		if v != "" {
			s = scrub(s, v)
		}
	}
	return s
}

func scrubMCPValue(v any, secret string) any {
	if secret == "" || v == nil {
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	for _, variant := range providers.SecretVariants(secret) {
		b = scrubBytes(b, keyForms(variant))
	}
	var out any
	if json.Unmarshal(b, &out) == nil {
		return out
	}
	return v
}

// ---- Admin API for upstream MCP per service ----

func (s *Server) serviceMCPView(ctx context.Context, sv *store.Service) map[string]any {
	endpoint, fromProvider, spec := serviceMCPInfo(sv)
	st, err := s.Store.GetServiceMCP(ctx, sv.ID)
	if err != nil {
		st = &store.ServiceMCP{ServiceID: sv.ID, Tools: []store.ServiceMCPTool{}}
	}
	// Resolve the exact tool names this server exposes in /mcp for this service.
	cat := s.toolCatalog(ctx)
	publicByUpstream := map[string]string{}
	for _, t := range cat.tools {
		if t.upstreamMCP && t.service == sv.Slug {
			publicByUpstream[t.upstreamTool] = t.Name
		}
	}
	tools := make([]map[string]any, 0, len(st.Tools))
	for _, ut := range st.Tools {
		pub := publicByUpstream[ut.Name]
		if pub == "" {
			pub = upstreamToolName(sv, ut.Name)
		}
		tools = append(tools, map[string]any{
			"name":         ut.Name,
			"public_name":  pub,
			"title":        ut.Title,
			"description":  ut.Description,
			"inputSchema":  ut.InputSchema,
			"outputSchema": ut.OutputSchema,
			"annotations":  ut.Annotations,
			// read_only is what /mcp enforces: access when the admin set
			// it, else upstream_read_only, the upstream's own hint.
			"read_only":          ut.ReadOnly(),
			"upstream_read_only": ut.UpstreamReadOnly(),
			"access":             ut.Access,
		})
	}
	transport := "streamable_http"
	if spec != nil && spec.MCP != nil {
		transport = spec.MCP.EffectiveTransport()
	}
	return map[string]any{
		"service_id":       sv.ID,
		"service_slug":     sv.Slug,
		"configured":       endpoint != "",
		"endpoint":         endpoint,
		"mcp_url":          sv.MCPURL,
		"from_provider":    fromProvider,
		"transport":        transport,
		"server_name":      st.ServerName,
		"server_version":   st.ServerVersion,
		"protocol_version": st.ProtocolVersion,
		"instructions":     st.Instructions,
		"tool_count":       len(st.Tools),
		"tools":            tools,
		"synced_at":        st.SyncedAt,
		"last_error":       st.LastError,
	}
}

func (s *Server) getServiceMCP(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, s.serviceMCPView(r.Context(), sv))
}

func (s *Server) syncServiceMCP(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	allowHidden := isSuper(r) && sv.TargetTrusted
	err := s.doSyncServiceMCP(r.Context(), sv, allowHidden)
	view := s.serviceMCPView(r.Context(), sv)
	if err != nil {
		var code int
		errCode := "mcp_sync_failed"
		switch {
		case errors.Is(err, errNoMCPConfigured):
			code, errCode = 400, "no_mcp_url"
		case errors.Is(err, errUpstreamBlocked):
			code, errCode = 502, "upstream_blocked"
		default:
			code = 502
		}
		writeJSON(w, code, map[string]any{
			"error": apiError{Code: errCode, Message: err.Error()},
			"mcp":   view,
		})
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "service.mcp_sync", sv.Slug,
		fmt.Sprintf("%d tools synced", view["tool_count"]))
	writeJSON(w, 200, view)
}

// setServiceMCPToolAccess sets whether one synced upstream tool counts as
// read-only: access "" follows the upstream's readOnlyHint, "read" and
// "write" override it. Read-only apps and tokens follow the result.
func (s *Server) setServiceMCPToolAccess(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	var in struct {
		Name   string `json:"name"`
		Access string `json:"access"`
	}
	if !decode(w, r, &in) {
		return
	}
	switch in.Access {
	case store.MCPToolAccessUpstream, store.MCPToolAccessRead, store.MCPToolAccessWrite:
	default:
		writeErr(w, 400, "invalid", `access must be "" (follow the server), "read" or "write"`)
		return
	}
	if err := s.Store.SetServiceMCPToolAccess(r.Context(), sv.ID, in.Name, in.Access); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "not_found", "this service has no synced MCP tool named "+strconv.Quote(in.Name))
			return
		}
		writeStoreErr(w, err)
		return
	}
	s.catalogChanged()
	label := map[string]string{"": "follow the server's hint", "read": "always read-only", "write": "always write"}[in.Access]
	s.Store.AddAudit(r.Context(), userFrom(r), "service.mcp_tool_access", sv.Slug+"/"+in.Name, label)
	writeJSON(w, 200, s.serviceMCPView(r.Context(), sv))
}

var errNoMCPConfigured = errors.New("this service has no upstream MCP URL configured")

func (s *Server) doSyncServiceMCP(ctx context.Context, sv *store.Service, allowHidden bool) error {
	endpoint, _, spec := serviceMCPInfo(sv)
	if endpoint == "" {
		return errNoMCPConfigured
	}
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	// Try picking a pooled key; if none exist, fall back to unauthenticated sync
	// so open MCP servers work and auth-required servers give a clear hint.
	var secret string
	var picked *pool.Picked
	var keyForClient *store.Key
	p, err := s.Pool.PickFor(cctx, sv, 0, allowHidden, nil, 0, false, pool.Request{Method: http.MethodPost, Path: "/"})
	if err == nil {
		picked = p
		secret = p.Secret
		keyForClient = p.Key
		defer picked.Release()
	}

	client, err := s.upstreamClient(cctx, sv, keyForClient)
	if err != nil {
		return fmt.Errorf("egress proxy error: %w", err)
	}
	res, syncErr := mcpclient.Sync(cctx, client, endpoint, mcpAuthFunc(sv, secret), mcpExtraHeaders(spec), s.Cfg.Version)
	prev, _ := s.Store.GetServiceMCP(ctx, sv.ID)
	if syncErr != nil {
		msg := scrubMCPText(syncErr.Error(), secret)
		if picked == nil && res != nil && (res.HTTPStatus == 401 || res.HTTPStatus == 403) {
			msg = "This MCP server requires authentication. Add a token in Keys, then sync tools."
		}
		if strings.Contains(msg, catalog.ErrMetadataAddr) {
			msg = catalog.ErrMetadataAddr
			syncErr = errUpstreamBlocked
		} else {
			syncErr = errors.New(msg)
		}
		save := &store.ServiceMCP{ServiceID: sv.ID, Tools: []store.ServiceMCPTool{}, LastError: msg}
		if prev != nil {
			*save = *prev
			save.LastError = msg
		}
		_ = s.Store.SaveServiceMCP(ctx, save)
		return syncErr
	}

	// An admin's access override outlives the sync: the upstream's hints
	// are replaced, the admin's word on a tool is not.
	access := map[string]string{}
	if prev != nil {
		for _, t := range prev.Tools {
			if t.Access != "" {
				access[t.Name] = t.Access
			}
		}
	}
	tools := make([]store.ServiceMCPTool, 0, len(res.Tools))
	for _, t := range res.Tools {
		tools = append(tools, store.ServiceMCPTool{
			Name:         t.Name,
			Title:        scrubMCPText(t.Title, secret),
			Description:  scrubMCPText(t.Description, secret),
			InputSchema:  t.InputSchema,
			OutputSchema: t.OutputSchema,
			Annotations:  t.Annotations,
			Access:       access[t.Name],
		})
	}
	now := time.Now().UTC()
	state := &store.ServiceMCP{
		ServiceID:       sv.ID,
		ServerName:      scrubMCPText(orDefault(res.Session.ServerTitle, res.Session.ServerName), secret),
		ServerVersion:   scrubMCPText(res.Session.ServerVersion, secret),
		ProtocolVersion: res.Session.ProtocolVersion,
		Instructions:    scrubMCPText(res.Session.Instructions, secret),
		Tools:           tools,
		ToolCount:       len(tools),
		SyncedAt:        &now,
		LastError:       "",
	}
	if err := s.Store.SaveServiceMCP(ctx, state); err != nil {
		return err
	}
	if picked != nil {
		s.Pool.ApplyProxied(ctx, sv, picked.Key, 0, providers.Result{Outcome: providers.OK})
	}
	s.mcpSessions.deleteService(sv.ID)
	s.catalogChanged()
	return nil
}

// syncServiceMCPBackground triggers an initial MCP tool sync when a service
// with an upstream MCP endpoint gets its first key and has not synced yet.
func (s *Server) syncServiceMCPBackground(sv *store.Service) {
	endpoint, _, _ := serviceMCPInfo(sv)
	if endpoint == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if prev, err := s.Store.GetServiceMCP(ctx, sv.ID); err == nil && prev.SyncedAt != nil {
		return
	}
	_ = s.doSyncServiceMCP(ctx, sv, false)
}

// ---- Runtime execution of an upstream MCP tool through the key pool ----

// callUpstreamMCPTool executes an upstream MCP tool through Acnologia's key
// pool with rotation, limits, budgets, circuit breaker, egress proxy, event
// logging and secret scrubbing. Returns a standard MCP tools/call result map.
func (s *Server) callUpstreamMCPTool(ctx context.Context, r *http.Request, a *store.App, st store.MCPSettings, t *mcpTool, args json.RawMessage) (map[string]any, error) {
	if !a.AllowsProxy() {
		return nil, errors.New("this app is lease only")
	}
	if st.ReadOnly && !toolIsReadOnly(*t) {
		return nil, fmt.Errorf("tool %q is not read-only; this token or app is in read-only mode", t.Name)
	}
	if !a.CanUse(t.service) {
		return nil, fmt.Errorf("this app cannot use service %q", t.service)
	}
	sv, err := s.Store.GetServiceBySlug(ctx, t.service)
	if err != nil {
		return nil, fmt.Errorf("no service named %q", t.service)
	}
	if !s.sharedAs(ctx, a, sv) {
		return nil, fmt.Errorf("this app cannot use service %q", t.service)
	}
	endpoint, _, prov := serviceMCPInfo(sv)
	if endpoint == "" {
		return nil, fmt.Errorf("service %q no longer has an upstream MCP URL configured", sv.Slug)
	}
	cost := costPerRequest(sv, prov, http.MethodPost, "/mcp")
	if hit, release := s.admitApp(ctx, a, cost); hit != nil {
		return nil, fmt.Errorf("this app reached its %s limit, it resets %s", strings.ReplaceAll(hit.Limit, "_", " "), hit.ResetAt.Format(time.RFC3339))
	} else {
		defer release()
	}

	bcfg := breakerSettings(prov)
	if ok, ra := s.Breakers.Allow(sv.ID, bcfg); !ok {
		return nil, fmt.Errorf("%s keeps failing, retry in %.0f seconds", sv.Name, math.Ceil(ra.Seconds()))
	}

	limit := 200 << 10
	if kb, ok := ctx.Value(mcpLimitKey).(int); ok && kb > 0 {
		limit = min(kb, 2048) << 10
	}

	verdict := false
	defer func() {
		if !verdict {
			s.Breakers.Abort(sv.ID)
		}
	}()

	tried := map[int64]bool{}
	queue := time.Duration(sv.QueueTimeoutSec) * time.Second
	allowHidden := a.LeaseHidden || sv.Provider == providers.PeerSlug || isHandoverService(sv)
	pr := pool.Request{Method: http.MethodPost, Path: "/mcp/" + t.upstreamTool}

	var lastErr error
	for attempt := 1; attempt <= sv.MaxAttempts; attempt++ {
		waitStart := time.Now()
		picked, err := s.Pool.PickFor(ctx, sv, a.ID, allowHidden, tried, queue, false, pr)
		waited := time.Since(waitStart)
		if err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			var nk *pool.NoKeyError
			if errors.As(err, &nk) {
				if !nk.Busy {
					s.Pool.EmitPoolEmpty(sv, err)
				}
				return nil, fmt.Errorf("%s, retry in %.0f seconds", nk.Error(), math.Ceil(nk.RetryAfter.Seconds()))
			}
			return nil, err
		}
		k := picked.Key
		tried[k.ID] = true

		client, err := s.upstreamClient(ctx, sv, k)
		if err != nil {
			picked.Release()
			s.Store.AddEvent(ctx, store.NewEvent{Kind: "error", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, Detail: "egress: " + err.Error()})
			return nil, fmt.Errorf("could not reach %s via egress proxy", sv.Name)
		}

		apply := mcpAuthFunc(sv, picked.Secret)
		extraHdrs := mcpExtraHeaders(prov)
		skey := mcpSessKey{serviceID: sv.ID, keyID: k.ID, endpoint: endpoint}

		start := time.Now()
		callRes, callErr := s.execUpstreamCall(ctx, client, skey, endpoint, apply, extraHdrs, t.upstreamTool, args, limit)
		latency := time.Since(start).Milliseconds()
		picked.Release()

		detail := "MCP " + t.upstreamTool
		if waited >= 100*time.Millisecond {
			detail += " (queued " + waited.Round(10*time.Millisecond).String() + ")"
		}

		if callErr != nil && (callRes == nil || callRes.HTTPStatus == 0) {
			msg := scrubMCPText(callErr.Error(), picked.Secret)
			s.Store.AddEvent(ctx, store.NewEvent{Kind: "error", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, LatencyMS: latency, Detail: detail + ": " + msg})
			blocked := strings.Contains(msg, catalog.ErrMetadataAddr)
			if ctx.Err() == nil && !blocked {
				s.Breakers.Failure(sv.ID, bcfg, "could not connect: "+msg)
				verdict = true
			}
			if blocked {
				return nil, fmt.Errorf("could not reach %s: %s", sv.Name, catalog.ErrMetadataAddr)
			}
			return nil, fmt.Errorf("could not reach %s: %s", sv.Name, msg)
		}

		status := callRes.HTTPStatus
		if status >= 400 {
			cls := prov.Classify(status, callRes.Headers, callRes.RawBody)
			cls.Message = scrubMCPText(cls.Message, picked.Secret)
			s.Store.AddEvent(ctx, store.NewEvent{Kind: "proxy", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: status, LatencyMS: latency, Detail: detail})
			_ = s.Pool.ApplyProxied(ctx, sv, k, a.ID, cls)
			if cls.Outcome.ProviderFault() {
				s.Breakers.Failure(sv.ID, bcfg, cls.Message)
				verdict = true
			} else if cls.Outcome == providers.OK {
				s.Breakers.Success(sv.ID)
				verdict = true
			}
			lastErr = errors.New(scrubMCPText(callErr.Error(), picked.Secret))
			if cls.Outcome.Rotates() {
				s.mcpSessions.delete(skey)
				continue
			}
			_ = s.Store.RecordAppUsage(ctx, a.ID, 0, false)
			return nil, lastErr
		}

		// HTTP 2xx: check JSON-RPC error vs tool result.
		s.Breakers.Success(sv.ID)
		verdict = true
		_ = s.Pool.ApplyProxied(ctx, sv, k, a.ID, providers.Result{Outcome: providers.OK, Quota: prov.ReadQuota(callRes.Headers)})
		if errors.Is(callErr, mcpclient.ErrTooLarge) {
			// The key and the upstream are fine, but the app never got an
			// answer: logged as an error, not billed to the app.
			s.Store.AddEvent(ctx, store.NewEvent{Kind: "error", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: status, LatencyMS: latency,
				Detail: detail + ": response over the app's max result size"})
			_ = s.Store.RecordAppUsage(ctx, a.ID, 0, false)
			return nil, fmt.Errorf("%s answered with more than this app's max result size (%d KB), so the answer was dropped; ask for less, or raise max result size in the app's MCP settings", sv.Name, limit>>10)
		}
		s.Store.AddEvent(ctx, store.NewEvent{Kind: "proxy", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: status, LatencyMS: latency, Cost: cost, Detail: detail})
		_ = s.Store.RecordAppUsage(ctx, a.ID, cost, false)

		if callErr != nil {
			return nil, errors.New(scrubMCPText(callErr.Error(), picked.Secret))
		}

		out := map[string]any{}
		if callRes.Content != nil {
			out["content"] = scrubMCPValue(callRes.Content, picked.Secret)
		} else {
			out["content"] = []any{}
		}
		if callRes.StructuredContent != nil {
			out["structuredContent"] = scrubMCPValue(callRes.StructuredContent, picked.Secret)
		}
		if callRes.IsError {
			out["isError"] = true
		}
		return out, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("no key could complete the upstream MCP call")
}

func (s *Server) execUpstreamCall(ctx context.Context, client *http.Client, skey mcpSessKey, endpoint string, apply mcpclient.AuthFunc, extraHdrs map[string]string, toolName string, args json.RawMessage, maxBytes int) (*mcpclient.CallResult, error) {
	sess, cached := s.mcpSessions.get(skey)
	if !cached {
		inited, hres, err := mcpclient.Initialize(ctx, client, endpoint, apply, extraHdrs, s.Cfg.Version)
		if err != nil {
			return &mcpclient.CallResult{HTTPResult: hres}, err
		}
		sess = inited
		s.mcpSessions.put(skey, sess)
	}
	res, err := mcpclient.CallTool(ctx, client, &sess, apply, extraHdrs, toolName, args, maxBytes)
	if err != nil && cached && mcpclient.SessionExpired(&sess, res.HTTPResult, err) {
		s.mcpSessions.delete(skey)
		inited, hres, initErr := mcpclient.Initialize(ctx, client, endpoint, apply, extraHdrs, s.Cfg.Version)
		if initErr != nil {
			return &mcpclient.CallResult{HTTPResult: hres}, initErr
		}
		sess = inited
		s.mcpSessions.put(skey, sess)
		res, err = mcpclient.CallTool(ctx, client, &sess, apply, extraHdrs, toolName, args, maxBytes)
	}
	return res, err
}

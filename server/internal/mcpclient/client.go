// Package mcpclient speaks MCP over Streamable HTTP as a client, so Acnologia
// can connect to upstream MCP servers (Scrapebox, Firecrawl, Linear, custom
// tools), discover their tools, health-check pooled tokens, and call tools
// through the key pool.
package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	// ProtocolVersion is the MCP protocol version requested on initialize.
	ProtocolVersion = "2025-06-18"
	maxListPages    = 50
	maxListTools    = 2000
	defaultMaxBytes = 4 << 20
)

var nextRPCID atomic.Int64

// ErrTooLarge is wrapped by the error a call returns when the upstream's
// answer was longer than the caller allowed. The upstream did the work; the
// caller never saw the result.
var ErrTooLarge = errors.New("upstream MCP response too large")

// AuthFunc applies credentials (header, bearer, query param, basic, fields)
// to an outbound HTTP request. May be nil for unauthenticated requests.
type AuthFunc func(req *http.Request)

// Tool is one tool advertised by an upstream MCP server.
type Tool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

// Session holds the negotiated Streamable HTTP MCP session state for one
// upstream endpoint and credential.
type Session struct {
	Endpoint        string
	SessionID       string
	ProtocolVersion string
	ServerName      string
	ServerTitle     string
	ServerVersion   string
	Instructions    string
}

// RPCError is a JSON-RPC 2.0 error returned by an upstream MCP server.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

// HTTPResult holds raw HTTP metadata returned alongside an MCP response so
// Acnologia's provider classifier can inspect status codes, headers and bodies.
type HTTPResult struct {
	HTTPStatus int
	Headers    http.Header
	RawBody    []byte
}

// SyncResult is the outcome of Initialize + ListTools.
type SyncResult struct {
	HTTPResult
	Session Session
	Tools   []Tool
}

// CallResult is the outcome of tools/call.
type CallResult struct {
	HTTPResult
	Content           []any          `json:"content,omitempty"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
	RPCError          *RPCError      `json:"-"`
	Truncated         bool           `json:"-"`
	RawResult         map[string]any `json:"-"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Initialize performs the Streamable HTTP MCP initialize handshake and sends
// notifications/initialized. If the upstream returns a non-2xx HTTP status,
// HTTPResult is populated and an error is returned.
func Initialize(ctx context.Context, client *http.Client, endpoint string, auth AuthFunc, headers map[string]string, clientVersion string) (Session, HTTPResult, error) {
	if clientVersion == "" {
		clientVersion = "dev"
	}
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "acnologia",
			"version": clientVersion,
		},
	}
	sess := Session{Endpoint: endpoint}
	env, hres, resolvedURL, err := postRPC(ctx, client, &sess, auth, headers, "initialize", params, defaultMaxBytes)
	if resolvedURL != "" {
		sess.Endpoint = resolvedURL
	}
	if err != nil {
		return sess, hres, err
	}
	if env.Error != nil {
		return sess, hres, env.Error
	}
	var initRes struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(env.Result, &initRes); err != nil {
		return sess, hres, fmt.Errorf("invalid initialize response: %w", err)
	}
	if initRes.ProtocolVersion != "" {
		sess.ProtocolVersion = initRes.ProtocolVersion
	} else {
		sess.ProtocolVersion = ProtocolVersion
	}
	sess.ServerName = initRes.ServerInfo.Name
	sess.ServerTitle = initRes.ServerInfo.Title
	sess.ServerVersion = initRes.ServerInfo.Version
	sess.Instructions = initRes.Instructions

	// Best-effort notifications/initialized per Streamable HTTP MCP spec.
	_ = postNotification(ctx, client, &sess, auth, headers, "notifications/initialized", nil)
	return sess, hres, nil
}

// ListTools fetches all tools from an initialized MCP session, following
// nextCursor pagination up to a safe limit.
func ListTools(ctx context.Context, client *http.Client, sess *Session, auth AuthFunc, headers map[string]string) ([]Tool, HTTPResult, error) {
	var all []Tool
	var lastHTTP HTTPResult
	cursor := ""
	seenCursors := map[string]bool{}
	for page := 0; page < maxListPages; page++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		} else {
			params = map[string]any{}
		}
		env, hres, _, err := postRPC(ctx, client, sess, auth, headers, "tools/list", params, 8<<20)
		lastHTTP = hres
		if err != nil {
			return all, lastHTTP, err
		}
		if env.Error != nil {
			return all, lastHTTP, env.Error
		}
		var res struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(env.Result, &res); err != nil {
			return all, lastHTTP, fmt.Errorf("invalid tools/list response: %w", err)
		}
		for _, t := range res.Tools {
			if strings.TrimSpace(t.Name) == "" {
				continue
			}
			if t.InputSchema == nil {
				t.InputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			all = append(all, t)
			if len(all) >= maxListTools {
				return all, lastHTTP, nil
			}
		}
		if res.NextCursor == "" || seenCursors[res.NextCursor] {
			break
		}
		seenCursors[res.NextCursor] = true
		cursor = res.NextCursor
	}
	return all, lastHTTP, nil
}

// Sync runs Initialize followed by ListTools against an upstream MCP server.
func Sync(ctx context.Context, client *http.Client, endpoint string, auth AuthFunc, headers map[string]string, clientVersion string) (*SyncResult, error) {
	sess, hres, err := Initialize(ctx, client, endpoint, auth, headers, clientVersion)
	if err != nil {
		return &SyncResult{HTTPResult: hres, Session: sess}, err
	}
	tools, listHTTP, err := ListTools(ctx, client, &sess, auth, headers)
	if listHTTP.HTTPStatus != 0 {
		hres = listHTTP
	}
	if err != nil {
		return &SyncResult{HTTPResult: hres, Session: sess}, err
	}
	return &SyncResult{HTTPResult: hres, Session: sess, Tools: tools}, nil
}

// CallTool invokes one tool on an initialized MCP session.
func CallTool(ctx context.Context, client *http.Client, sess *Session, auth AuthFunc, headers map[string]string, name string, args json.RawMessage, maxBytes int) (*CallResult, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	var argVal any = map[string]any{}
	if trimmed := bytes.TrimSpace(args); len(trimmed) > 0 && string(trimmed) != "null" {
		argVal = json.RawMessage(trimmed)
	}
	params := map[string]any{
		"name":      name,
		"arguments": argVal,
	}
	env, hres, _, err := postRPC(ctx, client, sess, auth, headers, "tools/call", params, maxBytes)
	out := &CallResult{HTTPResult: hres}
	if err != nil {
		return out, err
	}
	if env.Error != nil {
		out.RPCError = env.Error
		return out, env.Error
	}
	var raw map[string]any
	if err := json.Unmarshal(env.Result, &raw); err != nil {
		return out, fmt.Errorf("invalid tools/call response: %w", err)
	}
	out.RawResult = raw
	if c, ok := raw["content"].([]any); ok {
		out.Content = c
	}
	if sc, ok := raw["structuredContent"]; ok {
		out.StructuredContent = sc
	}
	if ie, ok := raw["isError"].(bool); ok {
		out.IsError = ie
	}
	return out, nil
}

// SessionExpired reports whether an HTTP or RPC failure indicates that a
// previously negotiated Mcp-Session-Id is no longer valid on the upstream server.
func SessionExpired(sess *Session, hres HTTPResult, err error) bool {
	if sess == nil || sess.SessionID == "" {
		return false
	}
	if hres.HTTPStatus == http.StatusNotFound {
		return true
	}
	if hres.HTTPStatus == http.StatusBadRequest {
		low := strings.ToLower(string(hres.RawBody))
		if strings.Contains(low, "session") {
			return true
		}
	}
	if err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "session") && (strings.Contains(low, "expired") || strings.Contains(low, "invalid") || strings.Contains(low, "not found") || strings.Contains(low, "unknown")) {
			return true
		}
	}
	return false
}

func postNotification(ctx context.Context, client *http.Client, sess *Session, auth AuthFunc, headers map[string]string, method string, params any) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		payload["params"] = params
	}
	body, _ := json.Marshal(payload)
	resp, _, err := doMCPPost(ctx, client, sess, auth, headers, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return nil
}

func postRPC(ctx context.Context, client *http.Client, sess *Session, auth AuthFunc, headers map[string]string, method string, params any, maxBytes int) (*rpcEnvelope, HTTPResult, string, error) {
	id := nextRPCID.Add(1)
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, HTTPResult{}, "", err
	}
	resp, resolvedURL, err := doMCPPost(ctx, client, sess, auth, headers, body)
	if err != nil {
		return nil, HTTPResult{}, resolvedURL, err
	}
	defer resp.Body.Close()

	if sid := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sid != "" {
		sess.SessionID = sid
	}

	readLimit := int64(maxBytes)
	if readLimit < 64<<10 {
		readLimit = 64 << 10
	}
	// Read slightly over maxBytes so we can tell if the response was capped.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, readLimit+1))
	if err != nil {
		return nil, HTTPResult{HTTPStatus: resp.StatusCode, Headers: resp.Header.Clone()}, resolvedURL, err
	}
	hres := HTTPResult{
		HTTPStatus: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		RawBody:    raw,
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, hres, resolvedURL, fmt.Errorf("upstream MCP returned HTTP %d: %s", resp.StatusCode, shortSnippet(raw))
	}
	if int64(len(raw)) > readLimit {
		return nil, hres, resolvedURL, fmt.Errorf("%w: it exceeded %d bytes", ErrTooLarge, readLimit)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	var env *rpcEnvelope
	if strings.Contains(ct, "text/event-stream") {
		env, err = parseSSEResponse(raw, id)
	} else {
		env, err = parseJSONResponse(raw, id)
	}
	if err != nil {
		return nil, hres, resolvedURL, err
	}
	return env, hres, resolvedURL, nil
}

// doMCPPost sends a POST to sess.Endpoint, following at most one same-origin
// trailing-slash 307/308 redirect (e.g. /mcp -> /mcp/) so FastAPI/Starlette
// MCP servers work even if the trailing slash was omitted, without ever
// sending credentials to another host.
func doMCPPost(ctx context.Context, client *http.Client, sess *Session, auth AuthFunc, headers map[string]string, body []byte) (*http.Response, string, error) {
	endpoint := sess.Endpoint
	for hop := 0; hop < 2; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, endpoint, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sess.ProtocolVersion != "" {
			req.Header.Set("MCP-Protocol-Version", sess.ProtocolVersion)
		}
		if sess.SessionID != "" {
			req.Header.Set("Mcp-Session-Id", sess.SessionID)
		}
		if auth != nil {
			auth(req)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, endpoint, err
		}
		if hop == 0 && (resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect) {
			loc := resp.Header.Get("Location")
			if next, ok := sameOriginTrailingSlash(endpoint, loc); ok {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
				resp.Body.Close()
				endpoint = next
				continue
			}
		}
		return resp, endpoint, nil
	}
	return nil, endpoint, errors.New("too many redirects")
}

// sameOriginTrailingSlash returns the resolved URL if loc points to the exact
// same scheme+host+query as base and only adds or removes a trailing slash on
// the path. Credentials are never redirected anywhere else.
func sameOriginTrailingSlash(base, loc string) (string, bool) {
	if strings.TrimSpace(loc) == "" {
		return "", false
	}
	bu, err := url.Parse(base)
	if err != nil {
		return "", false
	}
	lu, err := bu.Parse(loc)
	if err != nil {
		return "", false
	}
	if !strings.EqualFold(bu.Scheme, lu.Scheme) || !strings.EqualFold(bu.Host, lu.Host) {
		return "", false
	}
	if lu.User != nil {
		return "", false
	}
	if bu.RawQuery != lu.RawQuery {
		return "", false
	}
	if strings.TrimRight(bu.Path, "/") != strings.TrimRight(lu.Path, "/") || bu.Path == lu.Path {
		return "", false
	}
	return lu.String(), true
}

func parseJSONResponse(raw []byte, wantID int64) (*rpcEnvelope, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("empty response from upstream MCP server")
	}
	if trimmed[0] == '[' {
		var batch []rpcEnvelope
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return nil, fmt.Errorf("invalid JSON-RPC batch from upstream MCP: %w", err)
		}
		for i := range batch {
			if idMatches(batch[i].ID, wantID) {
				return &batch[i], nil
			}
		}
		return nil, errors.New("upstream MCP batch did not contain a response for request")
	}
	var env rpcEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return nil, fmt.Errorf("invalid JSON-RPC from upstream MCP: %w", err)
	}
	return &env, nil
}

// parseSSEResponse scans a text/event-stream body and returns the JSON-RPC
// response matching wantID (skipping server notifications and progress events).
func parseSSEResponse(raw []byte, wantID int64) (*rpcEnvelope, error) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64<<10), max(len(raw)+1024, 1<<20))
	var dataLines [][]byte
	var fallback *rpcEnvelope

	flush := func() *rpcEnvelope {
		if len(dataLines) == 0 {
			return nil
		}
		joined := bytes.TrimSpace(bytes.Join(dataLines, []byte("\n")))
		dataLines = dataLines[:0]
		if len(joined) == 0 {
			return nil
		}
		env, err := parseJSONResponse(joined, wantID)
		if err != nil {
			return nil
		}
		if idMatches(env.ID, wantID) {
			return env
		}
		if len(env.ID) > 0 && string(env.ID) != "null" && fallback == nil {
			fallback = env
		}
		return nil
	}

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			if env := flush(); env != nil {
				return env, nil
			}
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			val := line[5:]
			if len(val) > 0 && val[0] == ' ' {
				val = val[1:]
			}
			cp := append([]byte(nil), val...)
			dataLines = append(dataLines, cp)
		}
	}
	if env := flush(); env != nil {
		return env, nil
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, errors.New("upstream MCP SSE stream ended without a JSON-RPC response")
}

func idMatches(raw json.RawMessage, want int64) bool {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n == want
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str == strconv.FormatInt(want, 10)
	}
	return false
}

func shortSnippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 240 {
		s = s[:240] + "..."
	}
	return s
}

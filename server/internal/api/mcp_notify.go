package api

// Keeping MCP clients current. A client that opens GET /mcp (the streamable
// HTTP server stream) gets notifications/tools/list_changed, and
// notifications/resources/list_changed, whenever what its app sees changes:
// a provider or service added, edited or removed, a capability or group
// saved, the app's access or MCP settings changed. The client then calls
// tools/list again, so new providers show up without a reconnect.
//
// Any write under /api/admin (the dashboard, the CLI, the control MCP and
// the Assistant all go through it) wakes the open streams at once; a slow
// check also catches changes made elsewhere, such as the provider refresh
// or another server updating what it shares.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

var (
	mcpStreamKeepalive = 25 * time.Second
	mcpStreamRecheck   = 60 * time.Second
	mcpStreamMaxAge    = time.Hour
)

// catalogWait returns a channel closed at the next catalog change.
func (s *Server) catalogWait() <-chan struct{} {
	s.catMu.Lock()
	defer s.catMu.Unlock()
	if s.catCh == nil {
		s.catCh = make(chan struct{})
	}
	return s.catCh
}

// catalogChanged wakes every open MCP stream to check its app's tools.
func (s *Server) catalogChanged() {
	s.catGen.Add(1)
	s.catMu.Lock()
	defer s.catMu.Unlock()
	if s.catCh != nil {
		close(s.catCh)
	}
	s.catCh = make(chan struct{})
}

// catalogNotify calls catalogChanged after every admin write.
func (s *Server) catalogNotify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			s.catalogChanged()
		}
	})
}

// mcpFingerprint hashes what tools/list and resources/list would answer.
func (s *Server) mcpFingerprint(ctx context.Context, a *store.App) (tools, resources string) {
	st, err := s.Store.GetMCPSettings(ctx, a.ID)
	if err != nil {
		return "", ""
	}
	if a.ShareID != nil {
		st.Insights, st.Manage = false, false
	}
	hash := func(v any) string {
		b, _ := json.Marshal(v)
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:8])
	}
	tools = hash(s.mcpListedTools(ctx, a, st))
	if st.Resources {
		resources = hash(s.mcpResources(ctx, a))
	}
	return tools, resources
}

// mcpStream serves GET /mcp: a server sent events stream of list changed
// notifications for the app's token.
func (s *Server) mcpStream(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeErr(w, 405, "method_not_allowed", "GET /mcp opens the notification stream: send Accept: text/event-stream")
		return
	}
	a, f := s.authAppFull(r)
	if f != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="acnologia"`)
		f.write(w)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "no_stream", "streaming is not supported here")
		return
	}
	ctx := store.WithAppToken(context.WithValue(r.Context(), appKey, a), a.TokenID)
	ctx = context.WithValue(ctx, mcpBaseKey, s.publicURL(r))
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": acnologia mcp notifications\n\n")
	fl.Flush()

	tools, resources := s.mcpFingerprint(ctx, a)
	send := func(method string) bool {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
		if _, err := fmt.Fprintf(w, "event: message\ndata: %s\n\n", b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	keep := time.NewTicker(mcpStreamKeepalive)
	defer keep.Stop()
	recheck := time.NewTicker(mcpStreamRecheck)
	defer recheck.Stop()
	end := time.NewTimer(mcpStreamMaxAge)
	defer end.Stop()
	for {
		wake := s.catalogWait()
		select {
		case <-ctx.Done():
			return
		case <-end.C:
			return
		case <-keep.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
			continue
		case <-wake:
			// Let a burst of writes settle before checking.
			select {
			case <-time.After(300 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		case <-recheck.C:
		}
		// The token may have been revoked or the app disabled meanwhile.
		if cur, f := s.authAppFull(r); f != nil || cur.ID != a.ID {
			return
		} else {
			a = cur
		}
		t2, r2 := s.mcpFingerprint(ctx, a)
		if t2 != tools && t2 != "" {
			tools = t2
			if !send("notifications/tools/list_changed") {
				return
			}
		}
		if r2 != resources {
			resources = r2
			if r2 != "" && !send("notifications/resources/list_changed") {
				return
			}
		}
	}
}

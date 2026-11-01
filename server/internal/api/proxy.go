package api

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/cogito-keys/cogito/internal/providers"
	"github.com/cogito-keys/cogito/internal/store"
)

func newUpstreamClient() *http.Client {
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 180 * time.Second,
	}
	return &http.Client{
		Transport: t,
		// Pass redirects through to the caller instead of following them with our key attached.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Host", "Cookie", "X-Cogito-Token",
	"X-Forwarded-For", "X-Real-Ip", "Content-Length",
	// Let the Go transport negotiate gzip so error bodies can be classified.
	"Accept-Encoding",
}

// buildUpstream creates the upstream request for one attempt.
func buildUpstream(ctx context.Context, r *http.Request, sv *store.Service, path string, body []byte, secret string) (*http.Request, error) {
	q := r.URL.Query()
	q.Del("cogito_token")
	if sv.AuthType == "query" {
		q.Set(sv.AuthName, secret)
	}
	target := sv.BaseURL + "/" + strings.TrimPrefix(path, "/")
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vs := range r.Header {
		req.Header[k] = append([]string(nil), vs...)
	}
	for _, h := range hopHeaders {
		req.Header.Del(h)
	}
	if strings.HasPrefix(req.Header.Get("Authorization"), "Bearer cog_") {
		req.Header.Del("Authorization")
	}
	switch sv.AuthType {
	case "header":
		req.Header.Set(sv.AuthName, secret)
	case "bearer":
		name := sv.AuthName
		if name == "" {
			name = "Authorization"
		}
		req.Header.Set(name, "Bearer "+secret)
	}
	req.ContentLength = int64(len(body))
	return req, nil
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request) {
	a := appFrom(r)
	if !a.AllowsProxy() {
		writeErr(w, 403, "proxy_not_allowed", "this app is lease only, use POST /api/v1/lease")
		return
	}
	sv, ok := s.serviceForApp(w, r, chi.URLParam(r, "service"))
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.Cfg.MaxBodyBytes))
	if err != nil {
		writeErr(w, 413, "body_too_large", "request body is too large")
		return
	}
	path := chi.URLParam(r, "*")
	prov := providers.Get(sv.Provider)
	tried := map[int64]bool{}
	var last *http.Response
	var lastBody []byte
	var lastKey int64

	for attempt := 1; attempt <= sv.MaxAttempts; attempt++ {
		picked, err := s.Pool.Pick(r.Context(), sv, a.ID, tried)
		if err != nil {
			if last != nil {
				break // out of keys, relay the last upstream answer
			}
			s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "error", ServiceID: sv.ID, AppID: a.ID, StatusCode: 503, Detail: "proxy: " + err.Error()})
			if !writeNoKey(w, err) {
				writeStoreErr(w, err)
			}
			return
		}
		k := picked.Key
		tried[k.ID] = true
		req, err := buildUpstream(r.Context(), r, sv, path, body, picked.Secret)
		if err != nil {
			writeErr(w, 400, "bad_request", err.Error())
			return
		}
		start := time.Now()
		resp, err := s.UpClient.Do(req)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "error", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, LatencyMS: latency, Detail: "upstream: " + scrub(err.Error(), picked.Secret)})
			writeErr(w, 502, "upstream_unreachable", "could not reach "+sv.Name)
			return
		}
		s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "proxy", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: resp.StatusCode, LatencyMS: latency,
			Detail: r.Method + " /" + path})

		if resp.StatusCode < 400 {
			_ = s.Pool.Apply(r.Context(), sv, k, providers.Result{Outcome: providers.OK})
			relay(w, resp, k.ID, attempt, nil)
			return
		}
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		res := prov.Classify(resp.StatusCode, resp.Header, rb)
		res.Message = scrub(res.Message, picked.Secret)
		_ = s.Pool.Apply(r.Context(), sv, k, res)
		if res.Outcome != providers.OK {
			s.Store.AddEvent(r.Context(), store.NewEvent{Kind: string(res.Outcome), ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: resp.StatusCode, Detail: res.Message})
		}
		last, lastBody, lastKey = resp, rb, k.ID
		if !res.Outcome.Rotates() {
			break
		}
	}
	relay(w, last, lastKey, len(tried), lastBody)
}

// relay copies the upstream response to the client, streaming when body is nil.
func relay(w http.ResponseWriter, resp *http.Response, keyID int64, attempts int, body []byte) {
	for k, vs := range resp.Header {
		switch http.CanonicalHeaderKey(k) {
		case "Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade", "Set-Cookie", "Content-Length":
			continue
		}
		w.Header()[k] = vs
	}
	w.Header().Set("X-Cogito-Key-Id", itoa(int(keyID)))
	w.Header().Set("X-Cogito-Attempts", itoa(attempts))
	if body != nil {
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(resp.StatusCode)
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

// scrub removes the secret from upstream messages before they are stored.
func scrub(msg, secret string) string {
	if secret == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, secret, "[key]")
	return strings.ReplaceAll(msg, url.QueryEscape(secret), "[key]")
}

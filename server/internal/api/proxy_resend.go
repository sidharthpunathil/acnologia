package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// When a request may go to another key, service or capability route.
//
// Moving on re-sends the request, so it depends on whether the provider may
// already have done the work:
//
//   - Refused before any work: a 429, 401, 402 or 403 (any outcome that
//     rotates keys), no free key, an open breaker, or a connection that never
//     carried the request (the dial, the proxy CONNECT or the TLS handshake
//     failed). These may always move on.
//   - The work may have happened: a 5xx, a 408 or 425, a response header
//     timeout, or a transport error after the request was written. These move
//     on only for GET, HEAD and OPTIONS, or when the request carries an
//     Idempotency-Key. Otherwise the answer (or a 502) goes back to the
//     caller, who knows whether repeating it is safe.
//
// Inside one service, keys rotate only on outcomes that rotate (refusals),
// never on a 5xx, so the same rule holds there. A transport error through a
// key's own egress proxy moves on to the next key under this rule too.

// resendable reports whether a request may be sent again after the work
// may have happened: a safe method, or an Idempotency-Key.
func resendable(method string, h http.Header) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return strings.TrimSpace(h.Get("Idempotency-Key")) != ""
}

// sentTrace learns whether a request reached the wire. Until the headers
// are written the upstream has seen nothing, so a failure before that point
// cannot have done any work.
type sentTrace struct{ wrote atomic.Bool }

func (t *sentTrace) with(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteHeaders: func() { t.wrote.Store(true) }})
}

func (t *sentTrace) sent() bool { return t.wrote.Load() }

// proxyReport tells an in-process caller of the proxy (a capability) how
// its request went: whether an upstream may have done the work.
type proxyReport struct{ mayHaveRun bool }

type proxyReportKey struct{}

func withProxyReport(ctx context.Context, rep *proxyReport) context.Context {
	return context.WithValue(ctx, proxyReportKey{}, rep)
}

// idemStorable reports whether a final status may be stored for replay. A
// timeout, too early, rate limit or provider error says nothing about the
// request itself, so a retry must reach a provider again.
func idemStorable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return false
	}
	return status < 500
}

// bodyFingerprint identifies a request body for an Idempotency-Key.
func bodyFingerprint(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// isEventStream reports whether an answer is a server-sent event stream,
// which is always relayed as it arrives and never buffered or stored.
func isEventStream(resp *http.Response) bool {
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}

// idleReader resets an idle timer on every read that returns data.
type idleReader struct {
	r io.Reader
	t *time.Timer
}

func (ir idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.t.Reset(proxyIdleTimeout)
	}
	return n, err
}

// tokenCost is the USD cost of the tokens a metered answer used, from the
// provider's token priced offers, for the model the request named (or the
// answer reported). 0 when no offer prices them.
func tokenCost(prov *providers.Spec, method, path string, body []byte, meter *tokenMeter, in, out int64) float64 {
	if (in == 0 && out == 0) || !prov.HasTokenPrices() {
		return 0
	}
	model := requestModel(body)
	if model == "" {
		model = meter.model()
	}
	c, _ := prov.TokenCost(method, path, model, in, out)
	return c
}

// requestModel reads the top level "model" of a JSON request body.
func requestModel(body []byte) string {
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		return ""
	}
	var v struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	return v.Model
}

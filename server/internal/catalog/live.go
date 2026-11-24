package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// LiveReport is the result of trying a provider against the real API.
type LiveReport struct {
	OK     bool `json:"ok"`
	Health struct {
		OK             bool   `json:"ok"`
		Status         int    `json:"status"`
		Detail         string `json:"detail"`
		QuotaLimit     *int64 `json:"quota_limit"`
		QuotaRemaining *int64 `json:"quota_remaining"`
	} `json:"health"`
	Example struct {
		Ran     bool   `json:"ran"`
		OK      bool   `json:"ok"`
		Status  int    `json:"status"`
		Outcome string `json:"outcome"`
		Detail  string `json:"detail"`
		// Name, Method and Path say which example ran.
		Name   string `json:"name,omitempty"`
		Method string `json:"method,omitempty"`
		Path   string `json:"path,omitempty"`
		// Sample is the answer's body (up to 64KB, the key already
		// replaced) and its content type, for SaveSample. Never sent.
		Sample      []byte `json:"-"`
		ContentType string `json:"-"`
	} `json:"example"`
}

// SaveSample stores the example's answer as the provider's sample response
// for it, when the example ran and succeeded. The store scrubs every form
// of secret, and every pool key, before and after cutting it to 4KB.
func (r *LiveReport) SaveSample(ctx context.Context, st *store.Store, provider, secret string) {
	ex := r.Example
	if st == nil || !ex.Ran || !ex.OK || provider == "" || ex.Name == "" {
		return
	}
	_ = st.SaveExampleResponse(ctx, store.ExampleResponse{Provider: provider, Example: ex.Name, Method: ex.Method, Path: ex.Path,
		Status: ex.Status, ContentType: ex.ContentType}, ex.Sample, secret)
}

// LiveCheck runs the health check and the first safe example with a real key.
func LiveCheck(ctx context.Context, spec *providers.Spec, secret string) LiveReport {
	return liveCheck(ctx, SafeClient(20*time.Second), spec, secret)
}

func liveCheck(ctx context.Context, c *http.Client, spec *providers.Spec, secret string) LiveReport {
	var rep LiveReport
	base, err := url.Parse(spec.BaseURL)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil {
		rep.Health.Detail = "base_url must be an absolute http(s) URL with no user info"
		return rep
	}
	// The request carries a real key, so never follow a redirect, and never
	// send anything to a host other than base_url's, whatever the spec's
	// paths say.
	next := c.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	c.Transport = pinnedTransport{next: next, scheme: base.Scheme, host: base.Host}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if h := spec.Health; h != nil && !liveHealthOK(h) {
		rep.Health.Detail = "health check refused: its path must start with / and its method must be GET, HEAD or POST"
	} else if h != nil {
		res, err := spec.Check(ctx, c, spec.BaseURL, secret)
		switch {
		case err != nil:
			rep.Health.Detail = scrub(err.Error(), secret)
		case !res.Valid:
			rep.Health.Status = res.HTTPStatus
			rep.Health.Detail = "key rejected: " + scrub(res.Message, secret)
		default:
			rep.Health.OK = true
			rep.Health.Status = res.HTTPStatus
			rep.Health.Detail = strings.TrimSpace("ok " + res.Message)
			rep.Health.QuotaLimit, rep.Health.QuotaRemaining = res.QuotaLimit, res.QuotaRemaining
		}
	} else {
		rep.Health.OK = true
		rep.Health.Detail = "no health check defined"
	}
	for _, ex := range spec.Examples {
		// Only read-only requests run with a real key, whatever the spec claims.
		if !ex.Safe || !LiveSafeMethod(ex.Method) || !strings.HasPrefix(ex.Path, "/") {
			continue
		}
		// The key stays pinned to base_url's host, so an example on another
		// of the provider's hosts is not run here.
		if _, _, onHost, _ := spec.ResolveHost(ex.Path); onHost || strings.HasPrefix(ex.Path, providers.HostPath("")) {
			continue
		}
		rep.Example.Ran = true
		status, outcome, detail, sample, ct := runExample(ctx, c, spec, ex, secret)
		rep.Example.Status, rep.Example.Outcome, rep.Example.Detail = status, outcome, detail
		rep.Example.OK = status > 0 && status < 400
		rep.Example.Name, rep.Example.Method, rep.Example.Path = ex.Name, strings.ToUpper(orGet(ex.Method)), ex.Path
		if rep.Example.OK {
			rep.Example.Sample, rep.Example.ContentType = sample, ct
		}
		break
	}
	rep.OK = rep.Health.OK && (!rep.Example.Ran || rep.Example.OK)
	return rep
}

func orGet(m string) string {
	if strings.TrimSpace(m) == "" {
		return http.MethodGet
	}
	return m
}

// runExample sends one example request with a real key and classifies the
// answer. It also returns the body, the key replaced, and its content type.
func runExample(ctx context.Context, c *http.Client, spec *providers.Spec, ex providers.Example, secret string) (int, string, string, []byte, string) {
	q := url.Values{}
	for k, v := range ex.Query {
		q.Set(k, v)
	}
	var body io.Reader
	if ex.Body != nil {
		b, _ := json.Marshal(ex.Body)
		body = bytes.NewReader(b)
	}
	method := strings.ToUpper(ex.Method)
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, spec.BaseURL+ex.Path, body)
	if err != nil {
		return 0, "", err.Error(), nil, ""
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	spec.Auth.Apply(req, q, secret)
	req.URL.RawQuery = q.Encode()
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", scrub(err.Error(), secret), nil, ""
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	res, rule := spec.ClassifyExplain(resp.StatusCode, resp.Header, rb)
	detail := resp.Header.Get("Content-Type")
	if rule != "" {
		detail += ", matched rule: " + rule
	}
	// Scrub before truncating so a key that straddles the cut cannot survive in part.
	preview := scrub(string(rb), secret)
	if len(preview) > 600 {
		preview = store.TruncateUTF8(preview, 600) + "..."
	}
	if strings.Contains(detail, "json") || strings.HasPrefix(strings.TrimSpace(preview), "{") {
		detail += "\n" + preview
	}
	return resp.StatusCode, string(res.Outcome), scrub(detail, secret), []byte(scrub(string(rb), secret)), resp.Header.Get("Content-Type")
}

// liveHealthOK reports whether a health check may run with a real key: the
// rules Spec.Validate enforces, checked again here because a live check can
// run on a draft that has not passed validation.
func liveHealthOK(h *providers.Health) bool {
	switch strings.ToUpper(strings.TrimSpace(h.Method)) {
	case "", http.MethodGet, http.MethodHead, http.MethodPost:
	default:
		return false
	}
	return strings.HasPrefix(h.Path, "/")
}

// pinnedTransport refuses any request that would leave one scheme and host,
// so a key cannot be carried anywhere else.
type pinnedTransport struct {
	next         http.RoundTripper
	scheme, host string
}

func (t pinnedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.User != nil || req.URL.Scheme != t.scheme || !strings.EqualFold(req.URL.Host, t.host) {
		return nil, fmt.Errorf("refusing to send the key outside %s", t.host)
	}
	return t.next.RoundTrip(req)
}

// LiveSafeMethod reports whether an example may run against the real API
// with an operator key. Only GET and HEAD are read-only by convention.
func LiveSafeMethod(m string) bool {
	m = strings.ToUpper(strings.TrimSpace(m))
	return m == "" || m == http.MethodGet || m == http.MethodHead
}

// scrub removes a secret from text before it is shown or stored.
func scrub(msg, secret string) string {
	if secret == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, secret, "[key]")
	return strings.ReplaceAll(msg, url.QueryEscape(secret), "[key]")
}

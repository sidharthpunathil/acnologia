package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cogito-keys/cogito/internal/providers"
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
	} `json:"example"`
}

// LiveCheck runs the health check and the first safe example with a real key.
func LiveCheck(ctx context.Context, spec *providers.Spec, secret string) LiveReport {
	var rep LiveReport
	c := SafeClient(20 * time.Second)
	if spec.Health != nil {
		res, err := spec.Check(ctx, c, spec.BaseURL, secret)
		switch {
		case err != nil:
			rep.Health.Detail = scrub(err.Error(), secret)
		case !res.Valid:
			rep.Health.Detail = "key rejected: " + scrub(res.Message, secret)
		default:
			rep.Health.OK = true
			rep.Health.Detail = strings.TrimSpace("ok " + res.Message)
			rep.Health.QuotaLimit, rep.Health.QuotaRemaining = res.QuotaLimit, res.QuotaRemaining
		}
	} else {
		rep.Health.OK = true
		rep.Health.Detail = "no health check defined"
	}
	for _, ex := range spec.Examples {
		if !ex.Safe {
			continue
		}
		rep.Example.Ran = true
		status, outcome, detail := runExample(ctx, c, spec, ex, secret)
		rep.Example.Status, rep.Example.Outcome, rep.Example.Detail = status, outcome, detail
		rep.Example.OK = status > 0 && status < 400
		break
	}
	rep.OK = rep.Health.OK && (!rep.Example.Ran || rep.Example.OK)
	return rep
}

// runExample sends one example request with a real key and classifies the answer.
func runExample(ctx context.Context, c *http.Client, spec *providers.Spec, ex providers.Example, secret string) (int, string, string) {
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
		return 0, "", err.Error()
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	spec.Auth.Apply(req, q, secret)
	req.URL.RawQuery = q.Encode()
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", scrub(err.Error(), secret)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	res, rule := spec.ClassifyExplain(resp.StatusCode, resp.Header, rb)
	detail := resp.Header.Get("Content-Type")
	if rule != "" {
		detail += ", matched rule: " + rule
	}
	preview := string(rb)
	if len(preview) > 600 {
		preview = preview[:600] + "..."
	}
	if strings.Contains(detail, "json") || strings.HasPrefix(strings.TrimSpace(preview), "{") {
		detail += "\n" + preview
	}
	return resp.StatusCode, string(res.Outcome), scrub(detail, secret)
}

// scrub removes a secret from text before it is shown or stored.
func scrub(msg, secret string) string {
	if secret == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, secret, "[key]")
	return strings.ReplaceAll(msg, url.QueryEscape(secret), "[key]")
}

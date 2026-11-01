package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// serpAPI: https://serpapi.com. Key goes in the api_key query parameter.
// Searches are counted per month and capped per hour.
type serpAPI struct{}

func (serpAPI) Template() Template {
	return Template{
		Provider: "serpapi", Name: "SerpAPI", Category: "Search",
		Description: "Google, Bing, YouTube and 50+ search engines as JSON. Monthly search quota with an hourly cap.",
		BaseURL:     "https://serpapi.com", AuthType: "query", AuthName: "api_key", Icon: "search",
		DefaultCooldownSec: 3600, DocsURL: "https://serpapi.com/search-api",
	}
}

func (serpAPI) CountsPerRequest() bool { return true }

func (serpAPI) Classify(status int, h http.Header, body []byte) Result {
	msg := snippet(body)
	var parsed struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)
	low := strings.ToLower(parsed.Error + " " + msg)
	switch {
	case strings.Contains(low, "run out of searches") || strings.Contains(low, "plan searches"):
		return Result{Outcome: QuotaExhausted, Message: parsed.Error}
	case strings.Contains(low, "past hour") || strings.Contains(low, "hourly"):
		next := time.Now().Truncate(time.Hour).Add(time.Hour)
		return Result{Outcome: RateLimited, RetryAfter: time.Until(next), Message: parsed.Error}
	case status == http.StatusUnauthorized || strings.Contains(low, "invalid api key"):
		return Result{Outcome: Invalid, Message: firstNonEmpty(parsed.Error, msg)}
	case status == http.StatusTooManyRequests:
		return Result{Outcome: RateLimited, RetryAfter: ParseRetryAfter(h), Message: firstNonEmpty(parsed.Error, msg)}
	case status >= 500:
		return Result{Outcome: Error, Message: msg}
	}
	return Result{Outcome: OK}
}

func (serpAPI) Check(ctx context.Context, c *http.Client, baseURL, secret string) (*CheckResult, error) {
	if baseURL == "" {
		baseURL = "https://serpapi.com"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/account.json?api_key="+url.QueryEscape(secret), nil)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var acc struct {
		Error                   string `json:"error"`
		SearchesPerMonth        *int64 `json:"searches_per_month"`
		PlanSearchesLeft        *int64 `json:"plan_searches_left"`
		ExtraCredits            *int64 `json:"extra_credits"`
		TotalSearchesLeft       *int64 `json:"total_searches_left"`
		AccountRateLimitPerHour *int64 `json:"account_rate_limit_per_hour"`
		LastHourSearches        *int64 `json:"last_hour_searches"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&acc); err != nil {
		return nil, fmt.Errorf("serpapi account: status %d: %w", resp.StatusCode, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || strings.Contains(strings.ToLower(acc.Error), "invalid api key") {
		return &CheckResult{Valid: false, Message: firstNonEmpty(acc.Error, resp.Status)}, nil
	}
	if acc.Error != "" || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("serpapi account: %s", firstNonEmpty(acc.Error, resp.Status))
	}
	r := &CheckResult{Valid: true}
	if acc.SearchesPerMonth != nil {
		limit := *acc.SearchesPerMonth
		if acc.ExtraCredits != nil {
			limit += *acc.ExtraCredits
		}
		r.QuotaLimit = &limit
	}
	r.QuotaRemaining = acc.TotalSearchesLeft
	if r.QuotaRemaining == nil {
		r.QuotaRemaining = acc.PlanSearchesLeft
	}
	if acc.AccountRateLimitPerHour != nil && acc.LastHourSearches != nil && *acc.AccountRateLimitPerHour > 0 &&
		*acc.LastHourSearches >= *acc.AccountRateLimitPerHour {
		r.CooldownUntil = ptr(time.Now().Truncate(time.Hour).Add(time.Hour))
		r.Message = "hourly search cap reached"
	}
	return r, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

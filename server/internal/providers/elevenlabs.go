package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// elevenLabs: https://elevenlabs.io. Key goes in the xi-api-key header.
// Characters are counted per billing period, concurrency is capped per plan.
type elevenLabs struct{}

func (elevenLabs) Template() Template {
	return Template{
		Provider: "elevenlabs", Name: "ElevenLabs", Category: "Voice",
		Description: "Text to speech, speech to text, voice cloning and sound effects. Character quota per billing cycle.",
		BaseURL:     "https://api.elevenlabs.io", AuthType: "header", AuthName: "xi-api-key", Icon: "audio-lines",
		DefaultCooldownSec: 5, DocsURL: "https://elevenlabs.io/docs/api-reference",
	}
}

func (elevenLabs) CountsPerRequest() bool { return false }

func (elevenLabs) Classify(status int, h http.Header, body []byte) Result {
	if status < 400 {
		return Result{Outcome: OK}
	}
	low := strings.ToLower(string(body))
	msg := snippet(body)
	var parsed struct {
		Detail struct {
			Status  string `json:"status"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"detail"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Detail.Message != "" {
		msg = parsed.Detail.Message
	}
	switch {
	case strings.Contains(low, "quota_exceeded") || strings.Contains(low, "exceeds your quota"):
		return Result{Outcome: QuotaExhausted, Message: msg}
	case strings.Contains(low, "too_many_concurrent_requests"):
		ra := ParseRetryAfter(h)
		if ra == 0 {
			ra = 5 * time.Second
		}
		return Result{Outcome: RateLimited, RetryAfter: ra, Message: msg}
	case strings.Contains(low, "system_busy"):
		// Provider side congestion, not the key's fault.
		return Result{Outcome: Error, Message: msg}
	case strings.Contains(low, "invalid_api_key") || strings.Contains(low, "detected_unusual_activity") ||
		strings.Contains(low, "account_suspended"):
		return Result{Outcome: Invalid, Message: msg}
	case status == http.StatusTooManyRequests:
		return Result{Outcome: RateLimited, RetryAfter: ParseRetryAfter(h), Message: msg}
	case status == http.StatusUnauthorized && !strings.Contains(low, "missing_permissions"):
		return Result{Outcome: Invalid, Message: msg}
	case status == http.StatusPaymentRequired:
		return Result{Outcome: QuotaExhausted, Message: msg}
	case status >= 500:
		return Result{Outcome: Error, Message: msg}
	}
	return Result{Outcome: OK}
}

func (elevenLabs) Check(ctx context.Context, c *http.Client, baseURL, secret string) (*CheckResult, error) {
	if baseURL == "" {
		baseURL = "https://api.elevenlabs.io"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/user/subscription", nil)
	req.Header.Set("xi-api-key", secret)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("elevenlabs subscription: status %d: %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 400 {
		if strings.Contains(strings.ToLower(string(raw)), "missing_permissions") {
			return &CheckResult{Valid: true, Message: "key works but lacks user_read permission, quota unknown"}, nil
		}
		return &CheckResult{Valid: false, Message: snippet(raw)}, nil
	}
	var sub struct {
		CharacterCount int64  `json:"character_count"`
		CharacterLimit int64  `json:"character_limit"`
		NextReset      int64  `json:"next_character_count_reset_unix"`
		Status         string `json:"status"`
		Tier           string `json:"tier"`
	}
	_ = json.Unmarshal(raw, &sub)
	r := &CheckResult{Valid: true, Message: strings.TrimSpace(sub.Tier + " " + sub.Status)}
	limit := sub.CharacterLimit
	remaining := sub.CharacterLimit - sub.CharacterCount
	if remaining < 0 {
		remaining = 0
	}
	r.QuotaLimit, r.QuotaRemaining = &limit, &remaining
	if sub.NextReset > 0 {
		t := time.Unix(sub.NextReset, 0).UTC()
		r.ResetAt = &t
	}
	return r, nil
}

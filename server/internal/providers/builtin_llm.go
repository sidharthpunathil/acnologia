package providers

// gemini is a model's offer: free on free tier keys, paid per token otherwise.
func gemini(name, model, paid string) Offer {
	return Offer{Name: name, Match: &OfferMatch{Method: "POST", Path: "/v1beta/models/" + model + ":"}, Free: true,
		Notes:     "Free of charge on free tier keys, within per model limits shown in AI Studio. Paid tier: " + paid + ".",
		SourceURL: "https://ai.google.dev/gemini-api/docs/pricing", CheckedAt: "2026-11-02"}
}

// llmBuiltins are model APIs whose keys people pool across accounts.
func llmBuiltins() []Builtin {
	t := func(name string, status int, body string, expect Outcome) Fixture {
		return Fixture{Name: name, Status: status, Body: body, Expect: expect}
	}
	return []Builtin{
		{
			Slug: "gemini", Name: "Google Gemini", Category: "LLM", Color: "#1a73e8",
			Description: "Gemini models through the Gemini API (generativelanguage.googleapis.com): text, images, embeddings and more. Per minute and per day limits per key.",
			Website:     "https://ai.google.dev", DocsURL: "https://ai.google.dev/api",
			Spec: Spec{
				BaseURL: "https://generativelanguage.googleapis.com",
				// Google accepts the key as ?key= or this header; the header keeps it out of URLs and logs.
				Auth:     Auth{Type: "header", Name: "x-goog-api-key"},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 5},
				Quota:    QuotaInfo{Unit: "requests", FreeTier: "Free tier on most models, with per minute and per day limits"},
				// The free tier's per model limits are shown only in AI Studio, so none are listed here.
				Offers: []Offer{
					gemini("Gemini 2.5 Flash-Lite", "gemini-2.5-flash-lite", "$0.10 input and $0.40 output per million tokens"),
					gemini("Gemini 2.5 Flash", "gemini-2.5-flash", "$0.30 input and $2.50 output per million tokens"),
					gemini("Gemini 2.5 Pro", "gemini-2.5-pro", "$1.25 input and $10 output per million tokens (prompts up to 200k tokens)"),
				},
				Rules: []Rule{
					// Free tier daily caps say "per day" in the quota id; they reset at midnight Pacific.
					{Name: "Daily quota used up", Match: Match{Status: []int{429}, BodyContains: []string{"PerDay", "per day"}}, Outcome: QuotaExhausted},
					{Name: "Rate limited", Match: Match{Status: []int{429}, BodyContains: []string{"RESOURCE_EXHAUSTED"}}, Outcome: RateLimited, RetryAfter: &RetryAfter{Source: "fixed", Value: "60"}},
					{Name: "Invalid API key", Match: Match{BodyContains: []string{"API_KEY_INVALID", "API key not valid"}}, Outcome: Invalid},
					{Name: "Key not allowed or API not enabled", Match: Match{Status: []int{403}, BodyContains: []string{"PERMISSION_DENIED"}}, Outcome: Invalid},
					{Name: "Model overloaded (not the key's fault)", Match: Match{Status: []int{503}, BodyContains: []string{"UNAVAILABLE", "overloaded"}}, Outcome: Error},
				},
				UseStandardRules: true,
				Health: &Health{
					Method: "GET", Path: "/v1beta/models", Query: map[string]string{"pageSize": "1"},
					Invalid: &Match{BodyContains: []string{"API_KEY_INVALID", "API key not valid"}},
				},
				Examples: []Example{
					{Name: "List models", Method: "GET", Path: "/v1beta/models", Safe: true},
					{Name: "Generate text", Method: "POST", Path: "/v1beta/models/gemini-2.5-flash:generateContent",
						Body: map[string]any{"contents": []map[string]any{{"parts": []map[string]any{{"text": "Say hello in five words"}}}}}},
				},
				Tests: []Fixture{
					t("success", 200, `{"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}`, OK),
					t("invalid key", 400, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`, Invalid),
					t("per minute limit", 429, `{"error":{"code":429,"message":"You exceeded your current quota, please check your plan and billing details.","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerMinutePerProjectPerModel-FreeTier"}]}]}}`, RateLimited),
					t("daily limit", 429, `{"error":{"code":429,"message":"You exceeded your current quota.","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier"}]}]}}`, QuotaExhausted),
					t("permission denied", 403, `{"error":{"code":403,"message":"Method doesn't allow unregistered callers.","status":"PERMISSION_DENIED"}}`, Invalid),
					t("overloaded", 503, `{"error":{"code":503,"message":"The model is overloaded. Please try again later.","status":"UNAVAILABLE"}}`, Error),
					t("bad request is the caller's fault", 400, `{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT"}}`, OK),
				},
			},
		},
	}
}

package providers

import _ "embed"

//go:embed seedlogos/serpapi.png
var serpLogo []byte

//go:embed seedlogos/elevenlabs.png
var elevenLogo []byte

// Builtin is a provider shipped with Cogito. It is seeded into the database on
// first boot; after that the database copy is the source of truth and can be
// edited like any other provider.
type Builtin struct {
	Slug, Name, Category, Description, Website, DocsURL, Color string
	Logo                                                       []byte
	LogoMime                                                   string
	Spec                                                       Spec
}

func Builtins() []Builtin {
	return append([]Builtin{
		{
			Slug: "serpapi", Name: "SerpAPI", Category: "Search", Color: "#1a73e8",
			Description: "Google, Bing, YouTube and 50+ search engines as JSON. Monthly search quota with an hourly cap.",
			Website:     "https://serpapi.com", DocsURL: "https://serpapi.com/search-api",
			Logo: serpLogo, LogoMime: "image/png",
			Spec: Spec{
				BaseURL:  "https://serpapi.com",
				Auth:     Auth{Type: "query", Name: "api_key"},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 3600, MaxAttempts: 5},
				Quota:    QuotaInfo{Unit: "searches", CountsPerRequest: true},
				Rules: []Rule{
					{Name: "Out of searches", Match: Match{BodyContains: []string{"run out of searches", "plan searches"}}, Outcome: QuotaExhausted},
					{Name: "Hourly cap", Match: Match{BodyContains: []string{"past hour", "hourly"}}, Outcome: RateLimited, RetryAfter: &RetryAfter{Source: "next_hour"}},
					{Name: "Invalid API key", Match: Match{BodyContains: []string{"invalid api key"}}, Outcome: Invalid},
				},
				UseStandardRules: true,
				Health: &Health{
					Method: "GET", Path: "/account.json",
					Invalid: &Match{BodyContains: []string{"invalid api key"}},
					Quota: &QuotaPaths{
						Limit:     []string{"searches_per_month", "extra_credits"},
						Remaining: []string{"total_searches_left"},
						Window:    &QuotaWindow{Limit: "account_rate_limit_per_hour", Used: "last_hour_searches", Period: "hour"},
					},
				},
				Examples: []Example{{Name: "Google search", Method: "GET", Path: "/search.json", Query: map[string]string{"engine": "google", "q": "coffee"}, Safe: false}},
				Tests: []Fixture{
					{Name: "success", Status: 200, Body: `{"search_metadata":{"status":"Success"}}`, Expect: OK},
					{Name: "out of searches", Status: 429, Body: `{"error":"Your account has run out of searches."}`, Expect: QuotaExhausted},
					{Name: "hourly cap", Status: 429, Body: `{"error":"Your searches for the past hour have exceeded the limit of your plan."}`, Expect: RateLimited},
					{Name: "invalid key", Status: 401, Body: `{"error":"Invalid API key. Your API key should be here: https://serpapi.com/manage-api-key"}`, Expect: Invalid},
					{Name: "server error", Status: 503, Body: `oops`, Expect: Error},
				},
			},
		},
		{
			Slug: "elevenlabs", Name: "ElevenLabs", Category: "Voice", Color: "#111111",
			Description: "Text to speech, speech to text, voice cloning and sound effects. Character quota per billing cycle.",
			Website:     "https://elevenlabs.io", DocsURL: "https://elevenlabs.io/docs/api-reference",
			Logo: elevenLogo, LogoMime: "image/png",
			Spec: Spec{
				BaseURL: "https://api.elevenlabs.io",
				Auth:    Auth{Type: "header", Name: "xi-api-key"},
				// The free plan allows 2 concurrent requests; raise it per key for paid plans.
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 5, MaxAttempts: 5, MaxConcurrency: 2, QueueTimeoutSec: 30},
				Quota:    QuotaInfo{Unit: "characters"},
				Rules: []Rule{
					{Name: "Character quota exceeded", Match: Match{BodyContains: []string{"quota_exceeded", "exceeds your quota"}}, Outcome: QuotaExhausted},
					{Name: "Too many concurrent requests", Match: Match{BodyContains: []string{"too_many_concurrent_requests"}}, Outcome: Busy, RetryAfter: &RetryAfter{Source: "fixed", Value: "1"}},
					{Name: "System busy (not the key's fault)", Match: Match{BodyContains: []string{"system_busy"}}, Outcome: Error},
					{Name: "Suspended account", Match: Match{BodyContains: []string{"detected_unusual_activity", "account_suspended"}}, Outcome: Banned},
					{Name: "Invalid key", Match: Match{BodyContains: []string{"invalid_api_key"}}, Outcome: Invalid},
					{Name: "Key lacks a permission", Match: Match{Status: []int{401, 403}, BodyContains: []string{"missing_permissions"}}, Outcome: OK},
				},
				UseStandardRules: true,
				Health: &Health{
					Method: "GET", Path: "/v1/user/subscription",
					OKAnyway: &Match{BodyContains: []string{"missing_permissions"}},
					Quota:    &QuotaPaths{Limit: []string{"character_limit"}, Used: []string{"character_count"}, ResetAt: "next_character_count_reset_unix", ResetFormat: "unix"},
				},
				Examples: []Example{{Name: "List voices", Method: "GET", Path: "/v1/voices", Safe: true}},
				Tests: []Fixture{
					{Name: "success", Status: 200, Body: `ID3`, Expect: OK},
					{Name: "quota exceeded", Status: 401, Body: `{"detail":{"status":"quota_exceeded","message":"This request exceeds your quota."}}`, Expect: QuotaExhausted},
					{Name: "concurrency limit", Status: 429, Body: `{"detail":{"status":"too_many_concurrent_requests","message":"Too many"}}`, Expect: Busy},
					{Name: "suspended account", Status: 401, Body: `{"detail":{"status":"detected_unusual_activity","message":"Unusual activity detected"}}`, Expect: Banned},
					{Name: "system busy", Status: 429, Body: `{"detail":{"status":"system_busy","message":"busy"}}`, Expect: Error},
					{Name: "invalid key", Status: 401, Body: `{"detail":{"status":"invalid_api_key","message":"Invalid API key"}}`, Expect: Invalid},
					{Name: "validation error is not the key's fault", Status: 422, Body: `{"detail":[{"msg":"field required"}]}`, Expect: OK},
				},
			},
		},
		{
			Slug: "generic", Name: "Custom API", Category: "Other", Color: "#6b6b66",
			Description: "Any HTTP API that takes a key in a header, a bearer token, or a query parameter. Uses standard status codes.",
			Spec: Spec{
				BaseURL:          "https://api.example.com",
				Auth:             Auth{Type: "header", Name: "Authorization"},
				Defaults:         Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 5},
				UseStandardRules: true,
				Tests: []Fixture{
					{Name: "429", Status: 429, Expect: RateLimited},
					{Name: "402", Status: 402, Expect: QuotaExhausted},
					{Name: "403", Status: 403, Expect: Invalid},
					{Name: "400 is the caller's fault", Status: 400, Expect: OK},
				},
			},
		},
	}, searchBuiltins()...)
}

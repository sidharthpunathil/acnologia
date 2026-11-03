package providers

import "fmt"

// llmChecked is the date the LLM provider pages behind these offers were read.
const llmChecked = "2026-11-03"

// llmDefaults suit model APIs: rotate on a limit, cool a minute, try up to five keys.
var llmDefaults = Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 5}

// tokenOffer is one model's token prices. The model is named in the request
// body, not the path, so a provider's model offers share their endpoint's
// match and only the first one ever selects a request; they describe prices
// for people and the AI tools. Token prices never decide a request's cost,
// so cost tracking stays on quota.cost_per_request.
func tokenOffer(name, method, path, model, in, out, extra, src string) Offer {
	notes := fmt.Sprintf("Model %s: $%s input and $%s output per million tokens.", model, in, out)
	if extra != "" {
		notes += " " + extra
	}
	return Offer{Name: name, Match: &OfferMatch{Method: method, Path: path}, Notes: notes, SourceURL: src, CheckedAt: llmChecked}
}

// retryHeader is a rate limit rule that honors the Retry-After header.
func retryHeader(name string, m Match) Rule {
	return Rule{Name: name, Match: m, Outcome: RateLimited, RetryAfter: &RetryAfter{Source: "header", Value: "Retry-After"}}
}

func fx(name string, status int, body string, expect Outcome) Fixture {
	return Fixture{Name: name, Status: status, Body: body, Expect: expect}
}

func fxh(name string, status int, headers map[string]string, body string, expect Outcome) Fixture {
	return Fixture{Name: name, Status: status, Headers: headers, Body: body, Expect: expect}
}

// chatExample is the OpenAI style chat completion most LLM APIs accept.
func chatExample(path, model string) Example {
	return Example{Name: "Chat completion", Method: "POST", Path: path, Body: map[string]any{
		"model": model, "max_tokens": 64,
		"messages": []map[string]any{{"role": "user", "content": "Say hello in five words"}},
	}}
}

// labBuiltins are the first party APIs of model makers. Facts come from each
// provider's own docs, error code, rate limit and pricing pages, read
// 2026-11-03, and the invalid key bodies from live calls made that day
// without a real key.
func labBuiltins() []Builtin {
	return []Builtin{openAIBuiltin(), anthropicBuiltin(), xaiBuiltin(), mistralBuiltin(), deepseekBuiltin(), cohereBuiltin(), perplexityBuiltin()}
}

func openAIBuiltin() Builtin {
	const pricing = "https://developers.openai.com/api/docs/pricing"
	const tiers = "Rate limits grow with usage tier (Free, Build, Launch, Grow): Build allows 5,000 requests a minute on the GPT-6 family. " +
		"See https://developers.openai.com/api/docs/guides/rate-limits."
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/v1/responses", id, in, out, extra, pricing)
	}
	return Builtin{
		Slug: "openai", Name: "OpenAI", Category: "LLM", Color: "#0080F7",
		Description: "GPT models through the OpenAI API: Responses, Chat Completions, embeddings, images and audio. Request and token limits per minute grow with the usage tier.",
		Website:     "https://openai.com", DocsURL: "https://developers.openai.com/api/reference/overview",
		Spec: Spec{
			BaseURL:  "https://api.openai.com",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			Quota:    QuotaInfo{Unit: "requests"},
			Offers: []Offer{
				model("GPT-6 Astra", "gpt-6-astra", "10", "50", "Standard tier, prompts up to 272K tokens. Same rates on Chat Completions."),
				model("GPT-6.1 Sol", "gpt-6.1-sol", "2", "10", "Standard tier, prompts up to 272K tokens."),
				model("GPT-6.1 Terra", "gpt-6.1-terra", "1", "5", "Standard tier, prompts up to 272K tokens."),
				model("GPT-6 Luna", "gpt-6-luna", "0.10", "0.50", tiers),
			},
			Rules: []Rule{
				// Billing problems answer 429 too; error.code tells them apart from a rate limit.
				{Name: "Out of credits or over a spend limit", Match: Match{Status: []int{429}, BodyContains: []string{
					"insufficient_quota", "credit_balance_exhausted", "spend_limit_exceeded", "usage_limit_exceeded", "exceeded your current quota"}}, Outcome: QuotaExhausted},
				retryHeader("Rate limited", Match{Status: []int{429}}),
				{Name: "Region not supported (not the key's fault)", Match: Match{Status: []int{403}, BodyContains: []string{
					"unsupported_country_region_territory", "Country, region, or territory not supported"}}, Outcome: Error},
				{Name: "Invalid, revoked or not in an organization", Match: Match{Status: []int{401}}, Outcome: Invalid},
			},
			UseStandardRules: true,
			Health:           &Health{Method: "GET", Path: "/v1/models"},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				chatExample("/v1/chat/completions", "gpt-6-luna"),
				{Name: "Create a response", Method: "POST", Path: "/v1/responses", Body: map[string]any{"model": "gpt-6-luna", "input": "Say hello in five words"}},
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`, OK),
				fx("invalid key (observed live)", 401, `{"error":{"message":"Incorrect API key provided: sk-inval***test. You can find your API key at https://platform.openai.com/account/api-keys.","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`, Invalid),
				fx("missing key (observed live)", 401, `{"error":{"message":"Missing bearer authentication in header","type":"invalid_request_error","param":null,"code":null}}`, Invalid),
				fx("out of quota", 429, `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}`, QuotaExhausted),
				fx("credit balance exhausted", 429, `{"error":{"message":"Credit balance exhausted.","type":"insufficient_quota","param":null,"code":"credit_balance_exhausted"}}`, QuotaExhausted),
				fx("project spend limit", 429, `{"error":{"message":"Project spend limit exceeded.","type":"insufficient_quota","param":null,"code":"project_spend_limit_exceeded"}}`, QuotaExhausted),
				fxh("rate limited", 429, map[string]string{"Retry-After": "20", "x-ratelimit-reset-requests": "20s"}, `{"error":{"message":"Rate limit reached for gpt-6-luna on requests per min (RPM): Limit 500, Used 500, Requested 1.","type":"requests","param":null,"code":"rate_limit_exceeded"}}`, RateLimited),
				fx("overloaded", 503, `{"error":{"message":"The server is overloaded, please try again later.","type":"service_unavailable_error","param":null,"code":"server_is_overloaded"}}`, Error),
				fx("server error", 500, `{"error":{"message":"The server had an error while processing your request.","type":"server_error"}}`, Error),
				fx("region not supported", 403, `{"error":{"code":"unsupported_country_region_territory","message":"Country, region, or territory not supported","param":null,"type":"request_forbidden"}}`, Error),
				fx("bad request is the caller's fault", 400, `{"error":{"message":"Invalid value for 'model'.","type":"invalid_request_error","param":"model","code":null}}`, OK),
			},
		},
	}
}

func anthropicBuiltin() Builtin {
	const pricing = "https://platform.claude.com/docs/en/about-claude/pricing"
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/v1/messages", id, in, out, extra, pricing)
	}
	return Builtin{
		Slug: "anthropic", Name: "Anthropic", Category: "LLM", Color: "#D97757",
		Description: "Claude models through the Claude API (Messages). Pool your team's Claude keys; callers send the anthropic-version header, as the Anthropic SDKs do.",
		Website:     "https://www.anthropic.com", DocsURL: "https://platform.claude.com/docs/en/api/overview",
		Spec: Spec{
			BaseURL: "https://api.anthropic.com",
			// The proxy passes the caller's headers through, so anthropic-version
			// comes from the client; specs have no static headers.
			Auth:     Auth{Type: "header", Name: "x-api-key"},
			Defaults: llmDefaults,
			Quota:    QuotaInfo{Unit: "requests", FreeTier: "A small amount of free credit for new accounts"},
			Offers: []Offer{
				model("Claude Opus 5.5", "claude-opus-5-5", "4", "20", ""),
				model("Claude Sonnet 5.5", "claude-sonnet-5-5", "2", "10", ""),
				model("Claude Haiku 5.5", "claude-haiku-5-5", "0.10", "0.50", "Prompts over 100K tokens: $0.50 input and $2.50 output."),
				model("Claude Fable 5.1", "claude-fable-5-1", "10", "50",
					"Start tier limits per model: 4,000 requests a minute (Opus, Sonnet, Haiku 5.5 and Fable 5.x). New organizations may begin on a lower Evaluation tier. "+
						"See https://platform.claude.com/docs/en/api/rate-limits."),
			},
			Rules: []Rule{
				// The monthly cap set by the organization's tier is a 429 with its own error code.
				{Name: "Monthly spend cap reached", Match: Match{Status: []int{429}, BodyContains: []string{"enforced_spend_limit_reached"}}, Outcome: QuotaExhausted},
				{Name: "Usage limit set in the Console or no credit", Match: Match{Status: []int{400}, BodyContains: []string{
					"reached your specified", "credit balance is too low"}}, Outcome: QuotaExhausted},
				{Name: "Billing problem", Match: Match{BodyContains: []string{"billing_error"}}, Outcome: QuotaExhausted},
				retryHeader("Rate limited", Match{Status: []int{429}}),
				{Name: "Overloaded (not the key's fault)", Match: Match{BodyContains: []string{"overloaded_error"}}, Outcome: Error},
				{Name: "Invalid or revoked key", Match: Match{Status: []int{401}}, Outcome: Invalid},
				// The key works but may not use this resource; the health check would revive it at once.
				{Name: "Not allowed for this key (not a bad key)", Match: Match{Status: []int{403}, BodyContains: []string{"permission_error"}}, Outcome: OK},
			},
			UseStandardRules: true,
			Health: &Health{
				Method: "GET", Path: "/v1/models", Query: map[string]string{"limit": "1"},
				// The check cannot send anthropic-version; a key that gets past
				// authentication and is asked for it is a working key.
				OKAnyway: &Match{Status: []int{400}, BodyContains: []string{"anthropic-version"}},
			},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				{Name: "Create a message", Method: "POST", Path: "/v1/messages", Body: map[string]any{
					"model": "claude-haiku-5-5", "max_tokens": 64,
					"messages": []map[string]any{{"role": "user", "content": "Say hello in five words"}},
				}},
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"Hello"}]}`, OK),
				fx("invalid key (observed live)", 401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"},"request_id":"req_011CgfdhA6joGjHDcmBwLrby"}`, Invalid),
				fx("missing key (observed live)", 401, `{"type":"error","error":{"type":"authentication_error","message":"x-api-key header is required"},"request_id":"req_011CgfdgRtUWuzNsZEMeGEST"}`, Invalid),
				fxh("rate limited", 429, map[string]string{"retry-after": "12", "anthropic-ratelimit-requests-reset": "2026-11-03T10:00:12Z"}, `{"type":"error","error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`, RateLimited),
				fx("monthly spend cap", 429, `{"type":"error","error":{"type":"rate_limit_error","message":"You have reached your API usage limits: your organization has crossed its monthly API usage threshold. You will regain access on 2026-12-01 at 00:00 UTC.","details":{"error_code":"enforced_spend_limit_reached"}}}`, QuotaExhausted),
				fx("console usage limit", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"You have reached your specified workspace API usage limits. You will regain access on 2026-12-01 at 00:00 UTC."}}`, QuotaExhausted),
				fx("credit balance too low", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."}}`, QuotaExhausted),
				fx("billing error", 402, `{"type":"error","error":{"type":"billing_error","message":"There is an issue with your billing or payment information."}}`, QuotaExhausted),
				fx("overloaded", 529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, Error),
				fx("api error", 500, `{"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, Error),
				fx("permission error", 403, `{"type":"error","error":{"type":"permission_error","message":"Your API key does not have permission to use the specified resource."}}`, OK),
				fx("bad request is the caller's fault", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: Field required"}}`, OK),
			},
		},
	}
}

func xaiBuiltin() Builtin {
	const pricing = "https://docs.x.ai/developers/pricing"
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/v1/chat/completions", id, in, out, extra, pricing)
	}
	return Builtin{
		Slug: "xai", Name: "xAI", Category: "LLM", Color: "#000000",
		Description: "Grok models through the xAI API, compatible with OpenAI's Chat Completions. Prepaid credits; limits grow with spend tier.",
		Website:     "https://x.ai", DocsURL: "https://docs.x.ai/developers/rest-api-reference",
		Spec: Spec{
			BaseURL:  "https://api.x.ai",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			Quota:    QuotaInfo{Unit: "requests"},
			Offers: []Offer{
				model("Grok 4.7", "grok-4.7", "2", "6", "Prompts of 200K tokens or more: $4 input and $12 output."),
				model("Grok 4.3", "grok-4.3", "1.25", "2.50", ""),
				model("Grok Build 0.1", "grok-build-0.1", "1", "2",
					"Web search tool: $5 per 1,000 calls. Limits grow with spend tier (T0 to T4); grok-4.7 allows 150 to 500 requests a second. See https://docs.x.ai/developers/rate-limits."),
			},
			Rules: []Rule{
				// xAI answers a wrong key with 400 invalid-argument, not 401.
				{Name: "Incorrect API key", Match: Match{Status: []int{400, 401}, BodyContains: []string{"Incorrect API key", "unauthenticated"}}, Outcome: Invalid},
				// Not documented word for word: requests are rejected once prepaid credits run out.
				{Name: "Out of credits or over the spending limit", Match: Match{Status: []int{402, 403}, BodyContains: []string{"credit", "spending limit"}}, Outcome: QuotaExhausted},
				{Name: "Key lacks a permission (not a bad key)", Match: Match{Status: []int{403}, BodyContains: []string{"permission"}}, Outcome: OK},
				retryHeader("Rate limited", Match{Status: []int{429}}),
			},
			UseStandardRules: true,
			// GET /v1/api-key describes the calling key, including whether it is blocked.
			Health: &Health{
				Method: "GET", Path: "/v1/api-key",
				Invalid: &Match{BodyRegex: `"(api_key_blocked|api_key_disabled|team_blocked)"\s*:\s*true|incorrect api key`},
			},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				chatExample("/v1/chat/completions", "grok-4.3"),
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`, OK),
				fx("incorrect key (observed live)", 400, `{"code":"invalid-argument","error":"Incorrect API key provided. You can obtain an API key from https://console.x.ai."}`, Invalid),
				fx("missing key (observed live)", 401, `{"code":"unauthenticated:no-credentials","error":"No credentials presented."}`, Invalid),
				fx("out of credits", 403, `{"code":"permission-denied","error":"Your team has either used all available credits or reached its monthly spending limit."}`, QuotaExhausted),
				fx("missing permission", 403, `{"code":"permission-denied","error":"Ask your team admin for permission."}`, OK),
				fxh("rate limited", 429, map[string]string{"Retry-After": "2"}, `{"code":"resource-exhausted","error":"You are sending requests too frequently and have reached the rate limit."}`, RateLimited),
				fx("server error", 503, `{"code":"unavailable","error":"Service temporarily unavailable."}`, Error),
				fx("unknown model is the caller's fault", 404, `{"code":"not-found","error":"The model grok-x does not exist or your team does not have access to it."}`, OK),
				fx("bad request is the caller's fault", 400, `{"code":"invalid-argument","error":"messages must not be empty"}`, OK),
			},
		},
	}
}

func mistralBuiltin() Builtin {
	const pricing = "https://docs.mistral.ai/inference/pricing"
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/v1/chat/completions", id, in, out, extra, pricing)
	}
	free := Offer{Name: "Free mode", Match: &OfferMatch{Method: "POST", Path: "/v1/chat/completions"}, Free: true,
		Notes: "New accounts start in Free mode: no card, limited usage and rate limits, shown on the console's Limits page. " +
			"Limits are per organization, requests a second and tokens a minute.",
		SourceURL: "https://docs.mistral.ai/resources/error-glossary", CheckedAt: llmChecked}
	return Builtin{
		Slug: "mistral", Name: "Mistral AI", Category: "LLM", Color: "#FA500F",
		Description: "Mistral and Codestral models through La Plateforme: chat, embeddings, OCR and agents. Free mode for new accounts, then pay as you go.",
		Website:     "https://mistral.ai", DocsURL: "https://docs.mistral.ai/api",
		Spec: Spec{
			BaseURL:  "https://api.mistral.ai",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			Quota:    QuotaInfo{Unit: "requests", FreeTier: "Free mode for new accounts with limited usage and rate limits"},
			// The free offer comes last: it shares the chat match, so paid model
			// offers select the request and the free one only informs.
			Offers: []Offer{
				model("Mistral Large 4", "mistral-large-4", "0.7", "2.1", "Public preview, 1M token context. The pricing FAQ still quotes older Large rates."),
				model("Mistral Small", "mistral-small-2603", "0.15", "0.6", ""),
				model("Codestral", "codestral-2508", "0.3", "0.9", ""),
				free,
			},
			Rules: []Rule{
				{Name: "No balance, spending limit or unpaid invoice", Match: Match{Status: []int{402}}, Outcome: QuotaExhausted},
				retryHeader("Rate limited", Match{Status: []int{429}}),
				{Name: "Invalid, missing or expired key", Match: Match{Status: []int{401}}, Outcome: Invalid},
				{Name: "Overloaded or in maintenance (not the key's fault)", Match: Match{Status: []int{500, 502, 503, 504}}, Outcome: Error},
			},
			UseStandardRules: true,
			Health:           &Health{Method: "GET", Path: "/v1/models"},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				chatExample("/v1/chat/completions", "mistral-small-2603"),
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`, OK),
				fx("invalid key (observed live)", 401, `{"detail":"Invalid API Key"}`, Invalid),
				fx("invalid key, documented shape", 401, `{"object":"error","message":"Unauthorized","type":"authentication_error","param":null,"code":null}`, Invalid),
				fx("payment required", 402, `{"object":"error","message":"Insufficient balance","type":"invalid_request_error","param":null,"code":null}`, QuotaExhausted),
				fxh("rate limited", 429, map[string]string{"Retry-After": "1"}, `{"object":"error","message":"Requests rate limit exceeded","type":"rate_limit_error","param":null,"code":null}`, RateLimited),
				fx("overloaded", 503, `{"object":"error","message":"Service unavailable","type":"server_error","param":null,"code":null}`, Error),
				fx("unknown model is the caller's fault", 400, `{"object":"error","message":"Invalid model: x","type":"invalid_request_error","param":"model","code":"unknown_model"}`, OK),
				fx("validation error is the caller's fault", 422, `{"object":"error","message":"messages: Field required","type":"invalid_request_error","param":null,"code":null}`, OK),
			},
		},
	}
}

func deepseekBuiltin() Builtin {
	const pricing = "https://api-docs.deepseek.com/quick_start/pricing"
	const offPeak = "Off peak (outside 01:00 to 04:00 and 06:00 to 10:00 UTC on weekdays) costs half."
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/chat/completions", id, in, out, extra, pricing)
	}
	return Builtin{
		Slug: "deepseek", Name: "DeepSeek", Category: "LLM", Color: "#4D6BFE",
		Description: "DeepSeek models through an OpenAI compatible API. Prepaid balance, read live from the balance endpoint; concurrency limits per account.",
		Website:     "https://www.deepseek.com", DocsURL: "https://api-docs.deepseek.com",
		Spec: Spec{
			BaseURL:  "https://api.deepseek.com",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			// The balance endpoint reports money in the account's currency
			// (USD or CNY), rounded to whole units.
			Quota: QuotaInfo{Unit: "balance (USD or CNY)"},
			Offers: []Offer{
				model("DeepSeek Flash", "deepseek-flash", "0.30", "1.20", "Peak rates; cache hits $0.006 input. "+offPeak),
				model("DeepSeek V4 Pro", "deepseek-v4-pro", "1.32", "3.96",
					"Peak rates; cache hits $0.044 input. "+offPeak+" Concurrency per account: 2,500 for deepseek-flash, 500 for deepseek-v4-pro; over it answers 429."),
			},
			Rules: []Rule{
				{Name: "Insufficient balance", Match: Match{Status: []int{402}}, Outcome: QuotaExhausted},
				// 429 means the account is at its concurrency limit: rotate, cool briefly.
				{Name: "Concurrency limit reached", Match: Match{Status: []int{429}}, Outcome: Busy, RetryAfter: &RetryAfter{Source: "fixed", Value: "2"}},
				{Name: "Server overloaded (not the key's fault)", Match: Match{Status: []int{500, 503}}, Outcome: Error},
				{Name: "Authentication fails", Match: Match{Status: []int{401}}, Outcome: Invalid},
			},
			UseStandardRules: true,
			Health: &Health{
				Method: "GET", Path: "/user/balance",
				Quota: &QuotaPaths{Remaining: []string{"balance_infos.0.total_balance"}},
			},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/models", Safe: true},
				{Name: "Balance", Method: "GET", Path: "/user/balance", Safe: true},
				chatExample("/chat/completions", "deepseek-flash"),
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`, OK),
				fx("invalid key (observed live)", 401, `{"error":{"message":"Authentication Fails, Your api key: ****test is invalid (request_id: df79f1b0-5ad6-481b-90b1-24f63d88bc40)","type":"authentication_error","param":null,"code":"invalid_request_error"}}`, Invalid),
				fx("missing key (observed live)", 401, `Authentication Fails (governor)`, Invalid),
				fx("insufficient balance", 402, `{"error":{"message":"Insufficient Balance","type":"unknown_error","param":null,"code":"invalid_request_error"}}`, QuotaExhausted),
				fx("concurrency limit", 429, `{"error":{"message":"Rate Limit Reached","type":"rate_limit_error","param":null,"code":"rate_limit_reached"}}`, Busy),
				fx("server overloaded", 503, `{"error":{"message":"Server Overloaded","type":"server_error","param":null,"code":null}}`, Error),
				fx("invalid format is the caller's fault", 400, `{"error":{"message":"Invalid Format","type":"invalid_request_error","param":null,"code":null}}`, OK),
				fx("invalid parameters are the caller's fault", 422, `{"error":{"message":"Invalid Parameters","type":"invalid_request_error","param":null,"code":null}}`, OK),
			},
		},
	}
}

func cohereBuiltin() Builtin {
	const pricing = "https://cohere.com/pricing"
	const limits = "https://docs.cohere.com/v2/docs/rate-limits"
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/v2/chat", id, in, out, extra, pricing)
	}
	trial := Offer{Name: "Trial keys", Match: &OfferMatch{Method: "POST", Path: "/v2/chat"}, Free: true,
		Notes: "Trial keys are free and capped at 1,000 API calls a month, with low per minute limits (chat 20 a minute per the docs). " +
			"Production keys allow 500 chat requests a minute on Command A, R+, R and R7B.",
		SourceURL: limits, CheckedAt: llmChecked}
	return Builtin{
		Slug: "cohere", Name: "Cohere", Category: "LLM", Color: "#FF7759",
		Description: "Command models, embeddings and rerank through the Cohere API. Free trial keys with a monthly call cap; production keys pay per token.",
		Website:     "https://cohere.com", DocsURL: "https://docs.cohere.com/reference/about",
		Spec: Spec{
			BaseURL:  "https://api.cohere.com",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			Quota:    QuotaInfo{Unit: "requests", FreeTier: "Trial keys: free, 1,000 API calls a month"},
			// The trial offer comes last so it only informs; see tokenOffer.
			Offers: []Offer{
				model("Command R", "command-r-08-2024", "0.15", "0.60", ""),
				model("Command R7B", "command-r7b-12-2024", "0.0375", "0.15", "Command A no longer shows a price on the pricing page."),
				trial,
			},
			Rules: []Rule{
				// The monthly trial cap's text is not documented; it mentions the month.
				{Name: "Trial key monthly cap", Match: Match{Status: []int{429}, BodyContains: []string{"per month", "a month", "/ month", "monthly"}}, Outcome: QuotaExhausted},
				{Name: "Billing maximum or payment method", Match: Match{Status: []int{402}}, Outcome: QuotaExhausted},
				retryHeader("Per minute limit", Match{Status: []int{429}}),
				{Name: "Deny listed key", Match: Match{Status: []int{498}}, Outcome: Banned},
				{Name: "Invalid, missing or expired key", Match: Match{Status: []int{401}}, Outcome: Invalid},
			},
			UseStandardRules: true,
			// check-api-key answers 200 {"valid":false} for a bad key.
			Health: &Health{Method: "POST", Path: "/v1/check-api-key", Invalid: &Match{JSONPath: "valid", JSONEquals: "false"}},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				{Name: "Chat", Method: "POST", Path: "/v2/chat", Body: map[string]any{
					"model":    "command-r-08-2024",
					"messages": []map[string]any{{"role": "user", "content": "Say hello in five words"}},
				}},
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"1","finish_reason":"COMPLETE","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}]}}`, OK),
				fx("invalid key (observed live)", 401, `{"id":"0a08e848-a4e3-4660-8c74-dbc3af285cec","message":"Incorrect API key provided: ***********test. You can find your API key at https://dashboard.cohere.com/api-keys."}`, Invalid),
				fx("missing key (observed live)", 401, `{"id":"a585a18a-f3b7-4fb4-b658-00dc4c3efe5b","message":"no api key supplied"}`, Invalid),
				fx("expired trial key", 401, `{"id":"1","message":"Your API key has expired. Please create a production key at dashboard.cohere.com or reach out to your contact at Cohere to continue usage."}`, Invalid),
				fx("billing maximum", 402, `{"id":"1","message":"Maximum billing reached for this API key as set in your dashboard, please go to https://dashboard.cohere.com/billing?tab=payment to increase your maximum amount to continue using this API key. Your billing capacity will reset at the beginning of next month."}`, QuotaExhausted),
				fx("trial monthly cap", 429, `{"id":"1","message":"You are using a Trial key, which is limited to 1000 API calls / month."}`, QuotaExhausted),
				fxh("trial per minute limit", 429, map[string]string{"Retry-After": "30"}, `{"id":"1","message":"You are using a Trial key, which is limited to 40 API calls / minute. You can continue to use the Trial key for free or upgrade to a Production key with higher rate limits."}`, RateLimited),
				fx("per minute limit", 429, `{"id":"1","message":"You are past the per minute request limit, please wait and try again later."}`, RateLimited),
				fx("deny listed key", 498, `{"id":"1","message":"blocked token"}`, Banned),
				fx("server error", 503, `{"id":"1","message":"service unavailable"}`, Error),
				fx("bad request is the caller's fault", 400, `{"id":"1","message":"invalid request: message must be at least 1 token long."}`, OK),
				fx("unknown model is the caller's fault", 404, `{"id":"1","message":"model 'xyz' not found, make sure the correct model ID was used and that you have access to the model."}`, OK),
			},
		},
	}
}

func perplexityBuiltin() Builtin {
	const pricing = "https://docs.perplexity.ai/docs/getting-started/pricing"
	return Builtin{
		Slug: "perplexity", Name: "Perplexity", Category: "LLM", Color: "#22808C",
		Description: "Web grounded answers and search: the Agent API, Sonar and the Search API. Limits grow with usage tier.",
		Website:     "https://www.perplexity.ai", DocsURL: "https://docs.perplexity.ai",
		Spec: Spec{
			BaseURL:  "https://api.perplexity.ai",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			Quota:    QuotaInfo{Unit: "requests"},
			Offers: []Offer{
				{Name: "Search API", Match: &OfferMatch{Method: "POST", Path: "/search$"},
					Price:  &Price{Amount: 0.005, Currency: "USD", Unit: "request"},
					Limits: []Limit{{Count: 50, Per: "second", Unit: "requests"}},
					Notes: "$5 per 1,000 successful requests ($1 with search_type fast). Up to five queries in one request bill once. " +
						"Invalid, rate limited and failed requests are not billed. 50 query units a second on every tier.",
					SourceURL: pricing, CheckedAt: llmChecked},
				tokenOffer("Sonar", "POST", "/v1/sonar", "sonar", "0.25", "2.50", "Sonar Chat Completions are now served by the Agent API.", "https://docs.perplexity.ai/docs/agent-api/models"),
				{Name: "Agent API", Match: &OfferMatch{Method: "POST", Path: "/v1/agent"},
					Notes: "Per token at each model's rate (perplexity/sonar $0.25 input and $2.50 output per million tokens) plus tool fees: " +
						"web_search $0.0025 a call ($0.001 fast), fetch_url $0.0005, sandbox $0.03 a session. Requests a second grow by tier: 1 at Tier 0 up to 33.",
					SourceURL: pricing, CheckedAt: llmChecked},
			},
			Rules: []Rule{
				// Overload is a 429 whose error.type is overloaded; it is not this key's limit.
				{Name: "Overloaded (not the key's fault)", Match: Match{Status: []int{429, 529}, BodyRegex: `"type"\s*:\s*"overloaded(_error)?"`}, Outcome: Error},
				{Name: "Credits exhausted", Match: Match{Status: []int{402}}, Outcome: QuotaExhausted},
				retryHeader("Rate limited", Match{Status: []int{429}}),
				{Name: "Blocked safety identifier (not a bad key)", Match: Match{Status: []int{403}, BodyContains: []string{"safety_identifier_blocked"}}, Outcome: OK},
				{Name: "Invalid API key", Match: Match{Status: []int{401}}, Outcome: Invalid},
			},
			UseStandardRules: true,
			Health:           &Health{Method: "GET", Path: "/v1/models"},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				{Name: "Sonar", Method: "POST", Path: "/v1/sonar", Body: map[string]any{
					"model":    "sonar",
					"messages": []map[string]any{{"role": "user", "content": "What changed in AI agents this week?"}},
				}},
				{Name: "Search", Method: "POST", Path: "/search", Body: map[string]any{"query": "coffee roasters", "max_results": 5}},
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"1","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`, OK),
				fx("invalid key (observed live)", 401, `{"error":{"message":"Invalid API key provided. Ensure your API key is correct and active.","type":"invalid_api_key","code":401}}`, Invalid),
				fx("credits exhausted", 402, `{"error":{"message":"Your API credits are exhausted.","type":"insufficient_credits","code":402}}`, QuotaExhausted),
				fxh("token limit", 429, map[string]string{"Retry-After": "14"}, `{"error":{"message":"Token rate limit exceeded (TPM). Retry after the Retry-After interval.","type":"token_rate_limit_exceeded","code":429}}`, RateLimited),
				fxh("request limit", 429, map[string]string{"Retry-After": "1"}, `{"error":{"message":"Request rate limit exceeded, please try again later.","type":"request_rate_limit_exceeded","code":429}}`, RateLimited),
				fx("overloaded", 429, `{"error":{"message":"The service is overloaded, please try again later.","type":"overloaded","code":429}}`, Error),
				fx("blocked safety identifier", 403, `{"error":{"message":"This safety identifier is blocked for your API organization.","type":"safety_identifier_blocked","code":403}}`, OK),
				fx("server error", 500, `{"error":{"message":"Internal server error","type":"server_error","code":500}}`, Error),
				fx("bad request is the caller's fault", 400, `{"error":{"message":"messages is required","type":"invalid_request","code":400}}`, OK),
			},
		},
	}
}

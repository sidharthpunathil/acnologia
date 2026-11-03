package providers

// hostBuiltins are inference hosts that serve open and third party models
// behind OpenAI compatible APIs. Facts come from each provider's own docs,
// error code, rate limit and pricing pages, read 2026-11-03, and the invalid
// key bodies from live calls made that day without a real key.
func hostBuiltins() []Builtin {
	return []Builtin{togetherBuiltin(), groqBuiltin(), fireworksBuiltin(), cerebrasBuiltin(), openrouterBuiltin()}
}

func togetherBuiltin() Builtin {
	const pricing = "https://www.together.ai/pricing"
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/v1/chat/completions", id, in, out, extra, pricing)
	}
	return Builtin{
		Slug: "together", Name: "Together AI", Category: "LLM", Color: "#FC4C02",
		Description: "Open models (Kimi, GLM, DeepSeek, gpt-oss, Llama and more) through an OpenAI compatible API, plus images, embeddings and fine tuning. Prepaid credits.",
		Website:     "https://www.together.ai", DocsURL: "https://docs.together.ai/reference",
		Spec: Spec{
			BaseURL:  "https://api.together.xyz",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			Quota:    QuotaInfo{Unit: "requests"},
			Offers: []Offer{
				model("Kimi K3", "moonshotai/Kimi-K3", "3.00", "15.00", ""),
				model("GLM 5.3", "zai-org/GLM-5.3", "1.40", "4.40", ""),
				model("DeepSeek V4.1 Flash", "deepseek-ai/DeepSeek-V4.1-Flash", "0.30", "1.20", ""),
				model("gpt-oss-120b", "openai/gpt-oss-120b", "0.15", "0.60",
					"No free trial: the first credit purchase is at least $15. Rate limits are not published per tier."),
			},
			Rules: []Rule{
				// 402: the balance reached the spend cut off, or the model needs a positive balance.
				{Name: "Balance at the spend cut off", Match: Match{Status: []int{402}}, Outcome: QuotaExhausted},
				retryHeader("Rate limited", Match{Status: []int{429}}),
				{Name: "Model at capacity (not the key's fault)", Match: Match{Status: []int{503, 524, 529}}, Outcome: Error},
				{Name: "Invalid or missing key", Match: Match{Status: []int{401}}, Outcome: Invalid},
			},
			UseStandardRules: true,
			Health:           &Health{Method: "GET", Path: "/v1/models"},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				chatExample("/v1/chat/completions", "openai/gpt-oss-120b"),
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`, OK),
				fx("invalid key on models (observed live)", 401, `{"error":{"message":"Unauthorized"}}`, Invalid),
				fx("invalid key on chat (observed live)", 401, `{"id":"p7oTSwA-2kFHot-a549ce326f223d2c","error":{"message":"Invalid API key provided. You can find your API key at https://api.together.ai/settings/api-keys.","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`, Invalid),
				fx("missing key (observed live)", 401, `Missing API key`, Invalid),
				fx("balance at cut off", 402, `{"error":{"message":"Credit limit exceeded","type":"invalid_request_error","param":null,"code":null}}`, QuotaExhausted),
				fxh("rate limited", 429, map[string]string{"Retry-After": "5"}, `{"error":{"message":"rate limit exceeded","type":"rate_limit","param":null,"code":null}}`, RateLimited),
				fx("model at max replicas", 529, `{"error":{"message":"The model is overloaded","type":"server_error"}}`, Error),
				fx("service unavailable", 503, `{"error":{"message":"Service unavailable","type":"server_error"}}`, Error),
				fx("bad request is the caller's fault", 400, `{"error":{"message":"Input validation error","type":"invalid_request_error","param":"messages","code":null}}`, OK),
				fx("unknown model is the caller's fault", 404, `{"error":{"message":"Unable to access model x.","type":"invalid_request_error","param":null,"code":"model_not_available"}}`, OK),
			},
		},
	}
}

func groqBuiltin() Builtin {
	const docs = "https://console.groq.com/docs/models"
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/v1/chat/completions", id, in, out, extra, docs)
	}
	free := Offer{Name: "Free plan", Match: &OfferMatch{Method: "POST", Path: "/v1/chat/completions"}, Free: true,
		Notes: "The Free plan needs no card and has per model limits in requests and tokens a minute and a day (gpt-oss-120b: 30 a minute, 1,000 a day). " +
			"The Developer plan raises them. Request headers count requests per day and tokens per minute.",
		SourceURL: "https://console.groq.com/docs/rate-limits", CheckedAt: llmChecked}
	return Builtin{
		Slug: "groq", Name: "Groq", Category: "LLM", Color: "#F43E01",
		Description: "Very fast inference on open models (gpt-oss, Llama, Qwen and more) through an OpenAI compatible API. Free plan with per model daily limits.",
		Website:     "https://groq.com", DocsURL: "https://console.groq.com/docs/api-reference",
		Spec: Spec{
			BaseURL:  "https://api.groq.com/openai",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			Quota:    QuotaInfo{Unit: "requests", FreeTier: "Free plan with per model limits a minute and a day"},
			// The free offer comes last so it only informs; see tokenOffer.
			Offers: []Offer{
				model("gpt-oss-120b", "openai/gpt-oss-120b", "0.15", "0.60", ""),
				model("gpt-oss-20b", "openai/gpt-oss-20b", "0.075", "0.30", "Groq does not bill requests that fail with 5xx."),
				free,
			},
			Rules: []Rule{
				// A spend limit set in the console blocks the key with 400 blocked_api_access.
				{Name: "Spend limit reached", Match: Match{Status: []int{400, 403}, BodyContains: []string{"blocked_api_access"}}, Outcome: QuotaExhausted},
				// Daily caps name the window in the message; Retry-After says when they refill.
				{Name: "Daily limit reached", Match: Match{Status: []int{429}, BodyContains: []string{"per day", "(RPD)", "(TPD)"}}, Outcome: QuotaExhausted,
					RetryAfter: &RetryAfter{Source: "header", Value: "Retry-After"}},
				retryHeader("Rate limited", Match{Status: []int{429}}),
				{Name: "Flex tier capacity exceeded (not the key's fault)", Match: Match{Status: []int{498}}, Outcome: Error},
				{Name: "Invalid API key", Match: Match{Status: []int{401}}, Outcome: Invalid},
			},
			UseStandardRules: true,
			Health:           &Health{Method: "GET", Path: "/v1/models"},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				chatExample("/v1/chat/completions", "openai/gpt-oss-20b"),
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`, OK),
				fx("invalid key (observed live)", 401, `{"error":{"message":"Invalid API Key","type":"invalid_request_error","code":"invalid_api_key"}}`, Invalid),
				fx("spend limit", 400, `{"error":{"message":"Organization has been restricted. Spend limit reached.","type":"invalid_request_error","code":"blocked_api_access"}}`, QuotaExhausted),
				fxh("per minute limit", 429, map[string]string{"retry-after": "8", "x-ratelimit-reset-tokens": "7.66s"}, `{"error":{"message":"Rate limit reached for model openai/gpt-oss-20b on tokens per minute (TPM): Limit 8000, Used 7800, Requested 900. Please try again in 7.66s.","type":"tokens","code":"rate_limit_exceeded"}}`, RateLimited),
				fxh("daily limit", 429, map[string]string{"retry-after": "3600"}, `{"error":{"message":"Rate limit reached for model openai/gpt-oss-20b on requests per day (RPD): Limit 1000, Used 1000, Requested 1.","type":"requests","code":"rate_limit_exceeded"}}`, QuotaExhausted),
				fx("flex capacity", 498, `{"error":{"message":"Flex tier capacity exceeded","type":"capacity_exceeded"}}`, Error),
				fx("service unavailable", 503, `{"error":{"message":"Service Unavailable","type":"internal_server_error"}}`, Error),
				fx("bad request is the caller's fault", 400, `{"error":{"message":"'messages' : property 'messages' is missing","type":"invalid_request_error"}}`, OK),
				fx("semantic error is the caller's fault", 422, `{"error":{"message":"Unprocessable Entity","type":"invalid_request_error"}}`, OK),
			},
		},
	}
}

func fireworksBuiltin() Builtin {
	const pricing = "https://docs.fireworks.ai/serverless/pricing"
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/v1/chat/completions", "accounts/fireworks/models/"+id, in, out, extra, pricing)
	}
	return Builtin{
		Slug: "fireworks", Name: "Fireworks AI", Category: "LLM", Color: "#280016",
		Description: "Fast serverless inference on open models through an OpenAI compatible API, plus fine tuning and dedicated deployments. Spend tiers raise the limits.",
		Website:     "https://fireworks.ai", DocsURL: "https://docs.fireworks.ai/api-reference/introduction",
		Spec: Spec{
			BaseURL:  "https://api.fireworks.ai/inference",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			Quota:    QuotaInfo{Unit: "requests", FreeTier: "A small starter credit; 10 requests a minute until a payment method is added"},
			Offers: []Offer{
				model("Kimi K3", "kimi-k3", "3.00", "15.00", "Cached input $0.30."),
				model("GLM 5.3", "glm-5p3", "1.40", "4.40", ""),
				model("DeepSeek V4.1 Flash", "deepseek-v4p1-flash", "0.30", "1.20", ""),
				model("gpt-oss-120b", "gpt-oss-120b", "0.15", "0.60",
					"Batch is 50% off. Accounts without a payment method or credits get 10 requests a minute; with both, 6,000. "+
						"Token limits per model adapt to traffic. See https://docs.fireworks.ai/serverless/rate-limits."),
			},
			Rules: []Rule{
				{Name: "Not on a paid plan or over usage limits", Match: Match{Status: []int{402}}, Outcome: QuotaExhausted},
				{Name: "Account suspended", Match: Match{Status: []int{412}, BodyContains: []string{"suspended"}}, Outcome: Banned},
				retryHeader("Rate limited", Match{Status: []int{429}}),
				{Name: "Deployment scaling up or overloaded (not the key's fault)", Match: Match{Status: []int{503}}, Outcome: Error},
				{Name: "Invalid API key", Match: Match{Status: []int{401}}, Outcome: Invalid},
			},
			UseStandardRules: true,
			Health:           &Health{Method: "GET", Path: "/v1/models"},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				chatExample("/v1/chat/completions", "accounts/fireworks/models/gpt-oss-120b"),
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`, OK),
				fx("invalid key (observed live)", 401, `{"error":{"message":"The API key you provided is invalid.","param":null,"code":"UNAUTHORIZED","type":"error"},"request_id":"767a70e9-ecf5-41f1-9292-2f1839dd0d24"}`, Invalid),
				fx("missing key (observed live)", 401, `{"error":{"message":"You must provide an API key. See https://docs.fireworks.ai/api-reference/introduction#authentication for more information.","param":null,"code":"UNAUTHORIZED","type":"error"}}`, Invalid),
				fx("payment required", 402, `{"error":{"message":"Account is not on a paid plan or has exceeded usage limits","code":"PAYMENT_REQUIRED","type":"error"}}`, QuotaExhausted),
				fx("account suspended", 412, `{"error":{"message":"Account is suspended","code":"FAILED_PRECONDITION","type":"error"}}`, Banned),
				fxh("rate limited", 429, map[string]string{"Retry-After": "3"}, `{"error":{"message":"Too many requests","code":"RESOURCE_EXHAUSTED","type":"error"}}`, RateLimited),
				fx("scaling up", 503, `{"error":{"message":"Deployment is currently scaled to zero and is scaling up. Please retry your request in a few minutes.","code":"DEPLOYMENT_SCALING_UP","type":"error"}}`, Error),
				fx("unknown model is the caller's fault (observed live)", 404, `{"error":{"message":"Model not found, inaccessible, and/or not deployed","param":"model","code":"NOT_FOUND","type":"error"}}`, OK),
				fx("bad request is the caller's fault", 400, `{"error":{"message":"messages must not be empty","code":"INVALID_ARGUMENT","type":"error"}}`, OK),
			},
		},
	}
}

func cerebrasBuiltin() Builtin {
	const pricing = "https://www.cerebras.ai/pricing"
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/v1/chat/completions", id, in, out, extra, pricing)
	}
	return Builtin{
		Slug: "cerebras", Name: "Cerebras", Category: "LLM", Color: "#F15A29",
		Description: "Wafer scale inference at thousands of tokens a second through an OpenAI compatible API. Trial credit, then pay as you go.",
		Website:     "https://www.cerebras.ai", DocsURL: "https://inference-docs.cerebras.ai/api-reference/chat-completions",
		Spec: Spec{
			BaseURL:  "https://api.cerebras.ai",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			Quota:    QuotaInfo{Unit: "requests", FreeTier: "$5 of credit for 30 days once a payment method is verified"},
			Offers: []Offer{
				model("gpt-oss-120b", "gpt-oss-120b", "0.35", "0.75",
					"Pay as you go: 1,000 requests a minute. Trial credit: 5 requests a minute and 1M tokens a day per model."),
				model("Qwen 3.8 27B", "qwen-3.8-27b", "0.99", "1.49",
					"Pay as you go: 300 requests a minute. Trial credit: 5 requests a minute. See https://inference-docs.cerebras.ai/support/rate-limits."),
			},
			Rules: []Rule{
				{Name: "Payment required", Match: Match{Status: []int{402}}, Outcome: QuotaExhausted},
				// Daily caps (trial credit) name the day window. Older docs list
				// x-ratelimit-reset-*-day headers in fractional seconds, which
				// Retry-After parsing does not read, so the pool's default applies.
				{Name: "Daily limit reached", Match: Match{Status: []int{429}, BodyContains: []string{"per day", "tokens_per_day", "requests_per_day"}}, Outcome: QuotaExhausted},
				// Retry-After is not documented; it is honored when sent, else the cooldown applies.
				retryHeader("Rate limited", Match{Status: []int{429}}),
				{Name: "Wrong or missing API key", Match: Match{Status: []int{401, 403}, BodyContains: []string{"wrong_api_key", "Wrong API Key", "Not authenticated"}}, Outcome: Invalid},
			},
			UseStandardRules: true,
			Health:           &Health{Method: "GET", Path: "/v1/models"},
			Examples: []Example{
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				chatExample("/v1/chat/completions", "gpt-oss-120b"),
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`, OK),
				fx("wrong key (observed live)", 401, `{"message":"Wrong API Key","type":"invalid_request_error","param":"api_key","code":"wrong_api_key"}`, Invalid),
				fx("missing key on models (observed live)", 403, `{"detail":"Not authenticated"}`, Invalid),
				fx("payment required", 402, `{"message":"Payment required","type":"invalid_request_error","param":null,"code":"payment_required"}`, QuotaExhausted),
				fxh("per minute limit", 429, map[string]string{"Retry-After": "12"}, `{"message":"Tokens per minute limit exceeded (uncached). Too many tokens processed.","type":"too_many_tokens_error","param":"quota","code":"token_quota_exceeded"}`, RateLimited),
				fx("daily limit", 429, `{"message":"Requests per day limit exceeded. Too many requests processed.","type":"too_many_requests_error","param":"quota","code":"request_quota_exceeded"}`, QuotaExhausted),
				fx("service unavailable", 503, `{"message":"Service unavailable","type":"server_error"}`, Error),
				fx("bad request is the caller's fault", 400, `{"message":"Model gpt-oss-120b does not support image inputs","type":"invalid_request_error","param":"messages","code":"wrong_api_format"}`, OK),
			},
		},
	}
}

func openrouterBuiltin() Builtin {
	const models = "https://openrouter.ai/models"
	model := func(name, id, in, out, extra string) Offer {
		return tokenOffer(name, "POST", "/v1/chat/completions", id, in, out, extra, models)
	}
	free := Offer{Name: "Free models", Match: &OfferMatch{Method: "POST", Path: "/v1/chat/completions"}, Free: true,
		Limits: []Limit{{Count: 20, Per: "minute", Unit: "requests"}},
		Notes: "Models whose id ends in :free cost nothing: 20 requests a minute, and 50 a day, or 1,000 a day once 10 credits have been bought. " +
			"Listed after the paid models, so these limits never pace paid traffic.",
		SourceURL: "https://openrouter.ai/docs/api_reference/limits", CheckedAt: llmChecked}
	return Builtin{
		Slug: "openrouter", Name: "OpenRouter", Category: "LLM", Color: "#7624F4",
		Description: "One OpenAI compatible API for hundreds of models across providers, with free models. Credits per key are read live from the key endpoint.",
		Website:     "https://openrouter.ai", DocsURL: "https://openrouter.ai/docs/api_reference/overview",
		Spec: Spec{
			BaseURL:  "https://openrouter.ai/api",
			Auth:     Auth{Type: "bearer"},
			Defaults: llmDefaults,
			// GET /v1/key reports USD credits for keys with a credit limit,
			// rounded to whole dollars; unlimited keys report none.
			Quota: QuotaInfo{Unit: "credits (USD)", FreeTier: "Free models: 20 requests a minute, 50 a day (1,000 a day after buying 10 credits)"},
			// The free offer comes last so it only informs; see tokenOffer.
			Offers: []Offer{
				model("Claude Sonnet 5.5", "anthropic/claude-sonnet-5.5", "2", "10", ""),
				model("GPT-6.1 Terra", "openai/gpt-6.1-terra", "1", "5", ""),
				model("DeepSeek V4.1 Flash", "deepseek/deepseek-v4.1-flash", "0.045", "0.5",
					"Prices follow the upstream provider; the live list is GET /api/v1/models. Paid models have no platform request cap."),
				free,
			},
			Rules: []Rule{
				// In flight credit holds clear once running requests settle.
				{Name: "Credit held by requests in flight", Match: Match{Status: []int{402}, BodyContains: []string{"in_flight_budget", "in-flight requests"}}, Outcome: Busy,
					RetryAfter: &RetryAfter{Source: "header", Value: "Retry-After"}},
				{Name: "Insufficient credits", Match: Match{Status: []int{402}}, Outcome: QuotaExhausted},
				{Name: "Key or budget limit reached", Match: Match{Status: []int{403}, BodyContains: []string{"openrouter_key_limit", "Budget limit exceeded", "openrouter_workspace_budget", "openrouter_guardrail_budget"}}, Outcome: QuotaExhausted},
				{Name: "Moderation or guardrail block (not the key's fault)", Match: Match{Status: []int{403}, BodyContains: []string{
					"content_policy_violation", "refusal", "flagged", "Request blocked", "moderation"}}, Outcome: OK},
				{Name: "Free model daily cap", Match: Match{Status: []int{429}, BodyContains: []string{"free-models-per-day"}}, Outcome: QuotaExhausted},
				retryHeader("Rate limited", Match{Status: []int{429}}),
				{Name: "No provider available (not the key's fault)", Match: Match{Status: []int{502, 503}}, Outcome: Error},
				{Name: "Invalid, disabled or unknown key", Match: Match{Status: []int{401}}, Outcome: Invalid},
				// A non streaming 200 can carry only an error object from the upstream.
				{Name: "Upstream error inside a 200", Match: Match{Status: []int{200}, JSONPath: "error.code"}, Outcome: Error},
			},
			UseStandardRules: true,
			Health: &Health{
				Method: "GET", Path: "/v1/key",
				Quota: &QuotaPaths{Limit: []string{"data.limit"}, Remaining: []string{"data.limit_remaining"}},
			},
			Examples: []Example{
				{Name: "Key info", Method: "GET", Path: "/v1/key", Safe: true},
				{Name: "List models", Method: "GET", Path: "/v1/models", Safe: true},
				chatExample("/v1/chat/completions", "deepseek/deepseek-v4.1-flash"),
			},
			Tests: []Fixture{
				fx("success", 200, `{"id":"gen-1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`, OK),
				fx("unknown key (observed live)", 401, `{"error":{"message":"User not found.","code":401}}`, Invalid),
				fx("malformed key (observed live)", 401, `{"error":{"message":"Missing Authentication header","code":401}}`, Invalid),
				fx("insufficient credits", 402, `{"error":{"code":402,"message":"Your account or API key has insufficient credits. Add more credits and retry the request.","metadata":{"error_type":"payment_required"}}}`, QuotaExhausted),
				fxh("credit held in flight", 402, map[string]string{"Retry-After": "5"}, `{"error":{"code":402,"message":"This request would exceed your available credits given your current in-flight requests. Retry after in-flight requests settle, or add credits.","metadata":{"reason":"in_flight_budget_exhausted","limit_source":"openrouter_in_flight_budget"}}}`, Busy),
				fx("key limit", 403, `{"error":{"code":403,"message":"Key limit exceeded.","metadata":{"limit_source":"openrouter_key_limit"}}}`, QuotaExhausted),
				fx("budget limit", 403, `{"error":{"code":403,"message":"Budget limit exceeded (weekly limit). Contact your org admin. $52.00 of $50.00 used.","metadata":{"limit_source":"openrouter_workspace_budget"}}}`, QuotaExhausted),
				fx("moderation", 403, `{"error":{"code":403,"message":"Input was flagged by moderation.","metadata":{"error_type":"content_policy_violation","reasons":["violence"],"flagged_input":"..."}}}`, OK),
				fx("free model daily cap", 429, `{"error":{"code":429,"message":"Rate limit exceeded: free-models-per-day. Add 10 credits to unlock 1000 free model requests per day"}}`, QuotaExhausted),
				fxh("rate limited", 429, map[string]string{"Retry-After": "10"}, `{"error":{"code":429,"message":"Rate limit exceeded","metadata":{"error_type":"rate_limit_exceeded"}}}`, RateLimited),
				fx("model down", 502, `{"error":{"code":502,"message":"Your chosen model is down or we received an invalid response from it","metadata":{"error_type":"provider_unavailable"}}}`, Error),
				fx("no provider", 503, `{"error":{"code":503,"message":"There is no available model provider that meets your routing requirements","metadata":{"error_type":"provider_overloaded"}}}`, Error),
				fx("error inside a 200", 200, `{"error":{"code":502,"message":"Provider returned error"}}`, Error),
				fx("bad request is the caller's fault", 400, `{"error":{"code":400,"message":"context length exceeded","metadata":{"error_type":"context_length_exceeded"}}}`, OK),
			},
		},
	}
}

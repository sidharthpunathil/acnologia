package providers

// searchBuiltins are web search APIs that agents pool keys for. Endpoints,
// auth placement, prices and free allowances follow the providers' public
// docs and OmniRoute's search registry (MIT). Free tiers change often, so
// the wording stays approximate and points at the provider's pricing page.
// Health checks are included only where a free, documented endpoint exists;
// for the rest Cogito learns validity from real traffic.
func searchBuiltins() []Builtin {
	t := func(name string, status int, body string, expect Outcome) Fixture {
		return Fixture{Name: name, Status: status, Body: body, Expect: expect}
	}
	return []Builtin{
		{
			Slug: "serper", Name: "Serper", Category: "Search", Color: "#4a6cf7",
			Description: "Google search results as JSON: web, news, images, places and more. Fast and cheap per query.",
			Website:     "https://serper.dev", DocsURL: "https://serper.dev/playground",
			Spec: Spec{
				BaseURL:  "https://google.serper.dev",
				Auth:     Auth{Type: "header", Name: "X-API-KEY"},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 5, CacheTTLSec: 3600},
				Quota:    QuotaInfo{Unit: "queries", CountsPerRequest: true, CostPerRequest: 0.001, FreeTier: "Free queries on sign up (about 2,500), then paid credits"},
				Rules: []Rule{
					{Name: "Out of credits", Match: Match{BodyContains: []string{"not enough credits", "insufficient credits", "out of credits"}}, Outcome: QuotaExhausted},
					{Name: "Bad API key", Match: Match{Status: []int{401, 403}, BodyContains: []string{"unauthorized", "invalid api key"}}, Outcome: Invalid},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Google search", Method: "POST", Path: "/search", Body: map[string]any{"q": "coffee"}}},
				Tests: []Fixture{
					t("success", 200, `{"organic":[]}`, OK),
					t("no credits", 400, `{"message":"Not enough credits","statusCode":400}`, QuotaExhausted),
					t("bad key", 403, `{"message":"Unauthorized.","statusCode":403}`, Invalid),
					t("rate limit", 429, `{"message":"Too many requests"}`, RateLimited),
				},
			},
		},
		{
			Slug: "brave-search", Name: "Brave Search", Category: "Search", Color: "#fb542b",
			Description: "Independent web index with web, news, image and video search. Quota comes back in every response's headers.",
			Website:     "https://brave.com/search/api/", DocsURL: "https://api-dashboard.search.brave.com/app/documentation",
			Spec: Spec{
				BaseURL:  "https://api.search.brave.com",
				Auth:     Auth{Type: "header", Name: "X-Subscription-Token"},
				Defaults: Defaults{Strategy: "most_quota", CooldownSec: 2, MaxAttempts: 5, CacheTTLSec: 3600},
				// Brave sends "per second, per month" pairs; index 1 is the monthly window.
				Quota: QuotaInfo{Unit: "queries", CostPerRequest: 0.005, FreeTier: "Free plan with a small monthly query allowance (check current pricing)",
					Headers: &QuotaHeaders{Limit: "X-RateLimit-Limit", Remaining: "X-RateLimit-Remaining", Reset: "X-RateLimit-Reset", ResetFormat: "seconds", Index: 1}},
				Rules: []Rule{
					{Name: "Invalid token", Match: Match{BodyContains: []string{"SUBSCRIPTION_TOKEN_INVALID", "invalid subscription token"}}, Outcome: Invalid},
					{Name: "Monthly quota used up", Match: Match{BodyContains: []string{"QUOTA_LIMITED", "quota exceeded"}}, Outcome: QuotaExhausted},
					{Name: "Per second limit", Match: Match{Status: []int{429}}, Outcome: RateLimited, RetryAfter: &RetryAfter{Source: "fixed", Value: "1"}},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Web search", Method: "GET", Path: "/res/v1/web/search", Query: map[string]string{"q": "coffee"}}},
				Tests: []Fixture{
					t("success", 200, `{"type":"search"}`, OK),
					t("bad token", 422, `{"error":{"code":"SUBSCRIPTION_TOKEN_INVALID","detail":"The provided subscription token is invalid."}}`, Invalid),
					t("quota", 429, `{"error":{"code":"QUOTA_LIMITED","detail":"Quota exceeded"}}`, QuotaExhausted),
					t("per second", 429, `{"error":{"code":"RATE_LIMITED"}}`, RateLimited),
				},
			},
		},
		{
			Slug: "tavily", Name: "Tavily", Category: "Search", Color: "#0f62fe",
			Description: "Search, extract and crawl built for AI agents, with answers and cleaned page content in one call.",
			Website:     "https://tavily.com", DocsURL: "https://docs.tavily.com",
			Spec: Spec{
				BaseURL:  "https://api.tavily.com",
				Auth:     Auth{Type: "bearer"},
				Defaults: Defaults{Strategy: "most_quota", CooldownSec: 60, MaxAttempts: 5, CacheTTLSec: 3600},
				Quota:    QuotaInfo{Unit: "credits", CountsPerRequest: true, CostPerRequest: 0.008, FreeTier: "About 1,000 free credits a month"},
				Rules: []Rule{
					{Name: "Plan limit reached", Match: Match{Status: []int{432, 433}}, Outcome: QuotaExhausted},
					{Name: "Usage limit text", Match: Match{BodyContains: []string{"exceeds your plan", "usage limit"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Health: &Health{
					Method: "GET", Path: "/usage",
					Quota: &QuotaPaths{Limit: []string{"account.plan_limit"}, Used: []string{"account.plan_usage"}},
				},
				Examples: []Example{{Name: "Search", Method: "POST", Path: "/search", Body: map[string]any{"query": "coffee"}}},
				Tests: []Fixture{
					t("success", 200, `{"results":[]}`, OK),
					t("plan limit", 432, `{"detail":{"error":"This request exceeds your plan's set usage limit."}}`, QuotaExhausted),
					t("pay as you go limit", 433, `{"detail":{"error":"limit reached"}}`, QuotaExhausted),
					t("bad key", 401, `{"detail":{"error":"Unauthorized: missing or invalid API key."}}`, Invalid),
					t("rate limit", 429, `{"detail":{"error":"Rate limit"}}`, RateLimited),
				},
			},
		},
		{
			Slug: "exa", Name: "Exa", Category: "Search", Color: "#1f40ed",
			Description: "Neural web search with full page contents, similar pages and research answers.",
			Website:     "https://exa.ai", DocsURL: "https://docs.exa.ai",
			Spec: Spec{
				BaseURL:  "https://api.exa.ai",
				Auth:     Auth{Type: "header", Name: "x-api-key"},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 5, CacheTTLSec: 3600},
				Quota:    QuotaInfo{Unit: "requests", CountsPerRequest: true, CostPerRequest: 0.007, FreeTier: "Free credits on sign up"},
				Rules: []Rule{
					{Name: "Out of credits", Match: Match{BodyContains: []string{"insufficient credits", "out of credits", "credit limit"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Search", Method: "POST", Path: "/search", Body: map[string]any{"query": "coffee roasters", "numResults": 5}}},
				Tests: []Fixture{
					t("success", 200, `{"results":[]}`, OK),
					t("credits", 402, `{"error":"Insufficient credits"}`, QuotaExhausted),
					t("bad key", 401, `{"error":"Invalid API key"}`, Invalid),
					t("rate limit", 429, `{"error":"rate limited"}`, RateLimited),
				},
			},
		},
		{
			Slug: "firecrawl", Name: "Firecrawl", Category: "Search", Color: "#ff6a00",
			Description: "Scrape, crawl and search the web into clean markdown or structured data for LLMs.",
			Website:     "https://firecrawl.dev", DocsURL: "https://docs.firecrawl.dev",
			Spec: Spec{
				BaseURL:  "https://api.firecrawl.dev",
				Auth:     Auth{Type: "bearer"},
				Defaults: Defaults{Strategy: "most_quota", CooldownSec: 60, MaxAttempts: 5},
				Quota:    QuotaInfo{Unit: "credits", CountsPerRequest: true, CostPerRequest: 0.002, FreeTier: "Free credits on sign up, then monthly plans"},
				Rules: []Rule{
					{Name: "Out of credits", Match: Match{BodyContains: []string{"insufficient credits", "payment required"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Scrape a page", Method: "POST", Path: "/v2/scrape", Body: map[string]any{"url": "https://example.com", "formats": []string{"markdown"}}}},
				Tests: []Fixture{
					t("success", 200, `{"success":true}`, OK),
					t("credits", 402, `{"success":false,"error":"Insufficient credits to perform this request."}`, QuotaExhausted),
					t("bad key", 401, `{"success":false,"error":"Unauthorized: Invalid token"}`, Invalid),
					t("rate limit", 429, `{"success":false,"error":"Rate limit exceeded"}`, RateLimited),
				},
			},
		},
		{
			Slug: "jina", Name: "Jina Search", Category: "Search", Color: "#009191",
			Description: "Web search that returns pages as LLM-ready text (s.jina.ai). Token based billing.",
			Website:     "https://jina.ai", DocsURL: "https://jina.ai/reader",
			Spec: Spec{
				BaseURL:  "https://s.jina.ai",
				Auth:     Auth{Type: "bearer"},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 5, CacheTTLSec: 3600},
				Quota:    QuotaInfo{Unit: "tokens", CostPerRequest: 0.002, FreeTier: "Free token allowance for new API keys"},
				Rules: []Rule{
					{Name: "Balance used up", Match: Match{BodyContains: []string{"insufficient balance", "insufficientbalance"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Search", Method: "GET", Path: "/", Query: map[string]string{"q": "coffee"}}},
				Tests: []Fixture{
					t("success", 200, `{"code":200,"data":[]}`, OK),
					t("balance", 402, `{"code":402,"name":"InsufficientBalanceError","message":"Account balance not enough"}`, QuotaExhausted),
					t("bad key", 401, `{"code":401,"message":"Invalid API key"}`, Invalid),
				},
			},
		},
		{
			Slug: "searchapi", Name: "SearchApi", Category: "Search", Color: "#2563eb",
			Description: "Google, Bing, YouTube, Amazon and more as JSON from one endpoint, picked with the engine parameter.",
			Website:     "https://www.searchapi.io", DocsURL: "https://www.searchapi.io/docs/google",
			Spec: Spec{
				BaseURL:  "https://www.searchapi.io",
				Auth:     Auth{Type: "query", Name: "api_key"},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 5, CacheTTLSec: 3600},
				Quota:    QuotaInfo{Unit: "searches", CountsPerRequest: true, CostPerRequest: 0.004, FreeTier: "About 100 free searches on sign up"},
				Rules: []Rule{
					{Name: "Out of searches", Match: Match{BodyContains: []string{"run out of searches", "monthly allowance", "upgrade your plan"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Google search", Method: "GET", Path: "/api/v1/search", Query: map[string]string{"engine": "google", "q": "coffee"}}},
				Tests: []Fixture{
					t("success", 200, `{"organic_results":[]}`, OK),
					t("no searches", 429, `{"error":"You have run out of searches. Please upgrade your plan."}`, QuotaExhausted),
					t("bad key", 401, `{"error":"Invalid API key."}`, Invalid),
				},
			},
		},
		{
			Slug: "linkup", Name: "Linkup", Category: "Search", Color: "#111827",
			Description: "Search API for AI apps with sourced answers, structured output and deep search.",
			Website:     "https://www.linkup.so", DocsURL: "https://docs.linkup.so",
			Spec: Spec{
				BaseURL:  "https://api.linkup.so",
				Auth:     Auth{Type: "bearer"},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 5, CacheTTLSec: 3600},
				Quota:    QuotaInfo{Unit: "requests", CountsPerRequest: true, CostPerRequest: 0.005, FreeTier: "Free monthly credit"},
				Rules: []Rule{
					{Name: "Out of credits", Match: Match{BodyContains: []string{"insufficient credit", "insufficient_credit", "no credits"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Search", Method: "POST", Path: "/v1/search", Body: map[string]any{"q": "coffee", "depth": "standard", "outputType": "searchResults"}}},
				Tests: []Fixture{
					t("success", 200, `{"results":[]}`, OK),
					t("credits", 402, `{"statusCode":402,"error":{"code":"INSUFFICIENT_CREDIT","message":"You do not have enough credits"}}`, QuotaExhausted),
					t("bad key", 401, `{"statusCode":401,"error":{"message":"Unauthorized"}}`, Invalid),
				},
			},
		},
		{
			Slug: "youcom", Name: "You.com Search", Category: "Search", Color: "#6b4eff",
			Description: "Web and news results with LLM-ready snippets from You.com's index.",
			Website:     "https://you.com/api", DocsURL: "https://documentation.you.com",
			Spec: Spec{
				BaseURL:          "https://ydc-index.io",
				Auth:             Auth{Type: "header", Name: "X-API-Key"},
				Defaults:         Defaults{Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 5, CacheTTLSec: 3600},
				Quota:            QuotaInfo{Unit: "requests", CountsPerRequest: true, CostPerRequest: 0.005},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Search", Method: "GET", Path: "/v1/search", Query: map[string]string{"query": "coffee"}}},
				Tests: []Fixture{
					t("success", 200, `{"results":{}}`, OK),
					t("bad key", 403, `{"detail":"Invalid API key"}`, Invalid),
					t("rate limit", 429, `{"detail":"Too many requests"}`, RateLimited),
				},
			},
		},
	}
}

package providers

// searchBuiltins are web search APIs that agents pool keys for. Endpoints,
// auth placement, prices and free allowances follow the providers' public
// docs and OmniRoute's search registry (MIT). Free tiers change often, so
// the wording stays approximate and points at the provider's pricing page.
// Health checks are included only where a free, documented endpoint exists;
// for the rest Acnologia learns validity from real traffic.
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
				Offers: []Offer{{Name: "Google search", Price: &Price{Amount: 0.001, Currency: "USD", Unit: "query"},
					Notes:     "2,500 free queries on sign up. Starter: $50 for 50,000 credits at 50 queries a second; Standard $0.75 per 1,000 at 100 a second; Ultimate $0.30 per 1,000 at 300 a second.",
					SourceURL: "https://serper.dev", CheckedAt: "2026-11-02"}},
				Rules: []Rule{
					{Name: "Out of credits", Match: Match{BodyContains: []string{"not enough credits", "insufficient credits", "out of credits"}}, Outcome: QuotaExhausted},
					{Name: "Bad API key", Match: Match{Status: []int{401, 403}, BodyContains: []string{"unauthorized", "invalid api key"}}, Outcome: Invalid},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Google search", Method: "POST", Path: "/search", Body: map[string]any{"q": "coffee"}}},
				Guide:            serperGuide(),
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
				Offers: []Offer{{Name: "Search", Match: &OfferMatch{Method: "GET", Path: "/res/v1/"},
					Price:     &Price{Amount: 0.005, Currency: "USD", Unit: "request"},
					Notes:     "$5 per 1,000 requests at up to 50 a second. Every plan gets $5 of free credit each month.",
					SourceURL: "https://brave.com/search/api/", CheckedAt: "2026-11-02"}},
				Rules: []Rule{
					{Name: "Invalid token", Match: Match{BodyContains: []string{"SUBSCRIPTION_TOKEN_INVALID", "invalid subscription token"}}, Outcome: Invalid},
					{Name: "Monthly quota used up", Match: Match{BodyContains: []string{"QUOTA_LIMITED", "quota exceeded"}}, Outcome: QuotaExhausted},
					{Name: "Per second limit", Match: Match{Status: []int{429}}, Outcome: RateLimited, RetryAfter: &RetryAfter{Source: "fixed", Value: "1"}},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Web search", Method: "GET", Path: "/res/v1/web/search", Query: map[string]string{"q": "coffee"}}},
				Guide:            braveGuide(),
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
				Offers: []Offer{
					{Name: "Search", Match: &OfferMatch{Method: "POST", Path: "/search"}, Price: &Price{Amount: 0.008, Currency: "USD", Unit: "credit", PerRequest: 1},
						Notes:     "1 credit for basic search, 2 for advanced. 1,000 free credits a month, then $0.008 a credit. Development keys allow 100 requests a minute, production keys 1,000.",
						SourceURL: "https://docs.tavily.com/documentation/api-credits", CheckedAt: "2026-11-02"},
					{Name: "Extract", Match: &OfferMatch{Method: "POST", Path: "/extract"}, Price: &Price{Amount: 0.008, Currency: "USD", Unit: "credit"},
						Notes:     "1 credit per 5 successful URLs (basic), 2 per 5 (advanced). Failed URLs are free.",
						SourceURL: "https://docs.tavily.com/documentation/api-credits", CheckedAt: "2026-11-02"},
					{Name: "Map", Match: &OfferMatch{Method: "POST", Path: "/map"}, Price: &Price{Amount: 0.008, Currency: "USD", Unit: "credit"},
						Notes: "1 credit per 10 pages, 2 per 10 with instructions.", SourceURL: "https://docs.tavily.com/documentation/api-credits", CheckedAt: "2026-11-02"},
					{Name: "Crawl", Match: &OfferMatch{Method: "POST", Path: "/crawl"}, Price: &Price{Amount: 0.008, Currency: "USD", Unit: "credit"},
						Limits:    []Limit{{Count: 100, Per: "minute", Unit: "requests"}},
						Notes:     "Costs the map credits plus the extract credits. 100 requests a minute on every key.",
						SourceURL: "https://docs.tavily.com/documentation/rate-limits", CheckedAt: "2026-11-02"},
				},
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
				Guide:    tavilyGuide(),
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
				// Exa's rate limits apply per team, across all its keys.
				Offers: []Offer{
					{Name: "Search", Match: &OfferMatch{Method: "POST", Path: "/search"}, Price: &Price{Amount: 0.007, Currency: "USD", Unit: "request"},
						Limits:    []Limit{{Count: 10, Per: "second", Unit: "requests"}},
						Notes:     "$7 per 1,000 (auto, fast), $4 per 1,000 (lite, instant); each result past 10 adds $1 per 1,000. $10 free credit, reset to $10 on the 1st of each month.",
						SourceURL: "https://exa.ai/pricing", CheckedAt: "2026-11-02"},
					{Name: "Contents", Match: &OfferMatch{Method: "POST", Path: "/contents"}, Price: &Price{Amount: 0.001, Currency: "USD", Unit: "page"},
						Limits: []Limit{{Count: 100, Per: "second", Unit: "requests"}},
						Notes:  "$1 per 1,000 pages, per content type.", SourceURL: "https://exa.ai/pricing", CheckedAt: "2026-11-02"},
					{Name: "Answer", Match: &OfferMatch{Method: "POST", Path: "/answer"}, Price: &Price{Amount: 0.005, Currency: "USD", Unit: "request"},
						Limits: []Limit{{Count: 10, Per: "second", Unit: "requests"}},
						Notes:  "$5 per 1,000.", SourceURL: "https://exa.ai/pricing", CheckedAt: "2026-11-02"},
				},
				Rules: []Rule{
					{Name: "Out of credits", Match: Match{BodyContains: []string{"insufficient credits", "out of credits", "credit limit"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Search", Method: "POST", Path: "/search", Body: map[string]any{"query": "coffee roasters", "numResults": 5}}},
				Guide:            exaGuide(),
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
				Offers: []Offer{
					{Name: "Scrape", Match: &OfferMatch{Method: "POST", Path: "/v2/scrape"}, Price: &Price{Amount: 0.0038, Currency: "USD", Unit: "credit", PerRequest: 1},
						Notes:     "1 credit a page. Free: 1,000 credits a month; Hobby: $19 a month for 5,000. Scrapes a minute: Free 10, Hobby 100, Standard 500. Concurrent browsers: Free 2, Hobby 5, Standard 25.",
						SourceURL: "https://www.firecrawl.dev/pricing", CheckedAt: "2026-11-02"},
					{Name: "Crawl", Match: &OfferMatch{Method: "POST", Path: "/v2/crawl"}, Price: &Price{Amount: 0.0038, Currency: "USD", Unit: "credit"},
						Notes: "1 credit a page. Crawls a minute: Free 2, Hobby 20, Standard 100.", SourceURL: "https://docs.firecrawl.dev/rate-limits", CheckedAt: "2026-11-02"},
					{Name: "Search", Match: &OfferMatch{Method: "POST", Path: "/v2/search"}, Price: &Price{Amount: 0.0038, Currency: "USD", Unit: "credit"},
						Notes: "2 credits per 10 results. Searches a minute: Free 10, Hobby 100, Standard 500.", SourceURL: "https://www.firecrawl.dev/pricing", CheckedAt: "2026-11-02"},
				},
				Rules: []Rule{
					{Name: "Out of credits", Match: Match{BodyContains: []string{"insufficient credits", "payment required"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Scrape a page", Method: "POST", Path: "/v2/scrape", Body: map[string]any{"url": "https://example.com", "formats": []string{"markdown"}}}},
				Guide:            firecrawlGuide(),
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
				Offers: []Offer{{Name: "Search", Limits: []Limit{{Count: 100, Per: "minute", Unit: "requests"}},
					Notes:     "Billed in tokens, from 10,000 tokens a request. New keys get 10 million free tokens. 100 requests a minute with a free or paid key, 1,000 with premium.",
					SourceURL: "https://jina.ai/reader/", CheckedAt: "2026-11-02"}},
				Rules: []Rule{
					{Name: "Balance used up", Match: Match{BodyContains: []string{"insufficient balance", "insufficientbalance"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Search", Method: "GET", Path: "/", Query: map[string]string{"q": "coffee"}}},
				Guide:            jinaGuide(),
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
				Offers: []Offer{{Name: "Search", Match: &OfferMatch{Method: "GET", Path: "/api/v1/search"}, Price: &Price{Amount: 0.004, Currency: "USD", Unit: "search"},
					Notes:     "Developer: $40 a month for 10,000 searches. 100 free requests on sign up. Up to 20% of the plan's searches in any hour.",
					SourceURL: "https://www.searchapi.io/pricing", CheckedAt: "2026-11-02"}},
				Rules: []Rule{
					{Name: "Out of searches", Match: Match{BodyContains: []string{"run out of searches", "monthly allowance", "upgrade your plan"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Google search", Method: "GET", Path: "/api/v1/search", Query: map[string]string{"engine": "google", "q": "coffee"}}},
				Guide:            searchapiGuide(),
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
				Offers: []Offer{{Name: "Search", Match: &OfferMatch{Method: "POST", Path: "/v1/search"}, Price: &Price{Amount: 0.005, Currency: "USD", Unit: "request"},
					Notes:     "Standard search $0.005, deep $0.05; with sourcedAnswer or structured output $0.006 and $0.055. $20 credit on sign up, topped back up to $20 each month for eligible accounts.",
					SourceURL: "https://docs.linkup.so/pages/documentation/development/pricing", CheckedAt: "2026-11-02"}},
				Rules: []Rule{
					{Name: "Out of credits", Match: Match{BodyContains: []string{"insufficient credit", "insufficient_credit", "no credits"}}, Outcome: QuotaExhausted},
				},
				UseStandardRules: true,
				Examples:         []Example{{Name: "Search", Method: "POST", Path: "/v1/search", Body: map[string]any{"q": "coffee", "depth": "standard", "outputType": "searchResults"}}},
				Guide:            linkupGuide(),
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
				Guide:            youcomGuide(),
				Tests: []Fixture{
					t("success", 200, `{"results":{}}`, OK),
					t("bad key", 403, `{"detail":"Invalid API key"}`, Invalid),
					t("rate limit", 429, `{"detail":"Too many requests"}`, RateLimited),
				},
			},
		},
	}
}

// Guide helpers shared by the search, voice, TinyFish, peer and generic
// guides. The sg prefix keeps them apart from the LLM guides' helpers.

// sgNoKey is the auth note every guide carries: callers never send a key.
const sgNoKey = "Send only your Acnologia app token. Never send a provider API key: Acnologia adds the key for you and rotates keys on rate limits and quota errors."

// sgP builds a GuideParam.
func sgP(name, in, typ string, required bool, desc string) GuideParam {
	return GuideParam{Name: name, In: in, Type: typ, Required: required, Description: desc}
}

// sgCacheGET says GET responses are cached for an hour, with an extra note.
func sgCacheGET(extra string) string {
	n := "Acnologia caches successful GET responses for 1 hour (cache_ttl_sec 3600, unless the admin changed it on the service). " +
		"A cached answer carries X-Acnologia-Cache: hit; send Cache-Control: no-cache to force a fresh call."
	if extra != "" {
		n += " " + extra
	}
	return n
}

func serperGuide() *Guide {
	q := sgP("q", "body", "string", true, "Search query. Google operators such as site: work.")
	gl := sgP("gl", "body", "string", false, "Country code, for example us, gb, in.")
	hl := sgP("hl", "body", "string", false, "Interface language, for example en.")
	page := sgP("page", "body", "integer", false, "Results page, starting at 1.")
	return &Guide{
		Summary: "Google results as JSON: web, news, images, places, videos and autocomplete, one cheap credit per query. " +
			"Pick it for plain Google results at the lowest price; pick Tavily, Exa or Linkup when you want page content or an answer instead of links.",
		Endpoints: []GuideEndpoint{
			{Name: "Web search", Method: "POST", Path: "/search", Description: "Google web results.",
				Params: []GuideParam{q, gl, hl, sgP("location", "body", "string", false, "Free text location, for example \"Austin, Texas, United States\"."),
					sgP("num", "body", "integer", false, "Results per page (default 10)."), page,
					sgP("tbs", "body", "string", false, "Time filter: qdr:h, qdr:d, qdr:w, qdr:m, qdr:y.")},
				Body:     map[string]any{"q": "best coffee grinder", "gl": "us", "hl": "en"},
				Response: "organic[] with title, link, snippet, position; also knowledgeGraph, answerBox, peopleAlsoAsk[] and relatedSearches[] when Google shows them."},
			{Name: "News", Method: "POST", Path: "/news", Description: "Google News results.",
				Params:   []GuideParam{q, gl, hl, page, sgP("tbs", "body", "string", false, "Time filter, for example qdr:d for the past day.")},
				Body:     map[string]any{"q": "coffee prices", "tbs": "qdr:d"},
				Response: "news[] with title, link, snippet, date, source, imageUrl."},
			{Name: "Images", Method: "POST", Path: "/images", Description: "Google Images results.",
				Params:   []GuideParam{q, gl, hl, page},
				Body:     map[string]any{"q": "latte art"},
				Response: "images[] with title, imageUrl, imageWidth, imageHeight, thumbnailUrl, link (the page the image is on)."},
			{Name: "Places", Method: "POST", Path: "/places", Description: "Google local results (businesses).",
				Params:   []GuideParam{q, gl, hl, sgP("location", "body", "string", false, "Where to search, for example \"Seattle, Washington, United States\".")},
				Body:     map[string]any{"q": "coffee roasters in Seattle"},
				Response: "places[] with title, address, latitude, longitude, rating, ratingCount, category, phoneNumber, website, cid."},
			{Name: "Videos", Method: "POST", Path: "/videos", Description: "Google video results.",
				Params:   []GuideParam{q, gl, hl, page},
				Body:     map[string]any{"q": "pour over technique"},
				Response: "videos[] with title, link, snippet, imageUrl, duration, source, channel, date."},
			{Name: "Autocomplete", Method: "POST", Path: "/autocomplete", Description: "Google query suggestions.",
				Params:   []GuideParam{q, gl, hl},
				Body:     map[string]any{"q": "coffee gr"},
				Response: "suggestions[] with value."},
		},
		Notes: []string{
			sgNoKey,
			"Every endpoint is a POST with a JSON body; parameters go in the body, not the query string.",
			"A body may be a JSON array of query objects to run several searches in one call; each query is billed.",
			"Acnologia only caches GET responses, so Serper calls (all POST) are never served from the cache even though the provider has a cache TTL.",
			"Serper's page scraper lives on another host (scrape.serper.dev) and is not reachable through this service.",
			"401 or 403 means a bad key and 400 \"Not enough credits\" means the key is out of credits; Acnologia rotates to another key on both.",
		},
	}
}

func braveGuide() *Guide {
	q := sgP("q", "query", "string", true, "Search query, at most 400 characters and 50 words.")
	country := sgP("country", "query", "string", false, "2 letter country code, for example US.")
	lang := sgP("search_lang", "query", "string", false, "Result language, for example en.")
	fresh := sgP("freshness", "query", "string", false, "pd (24 hours), pw (7 days), pm (31 days), py (365 days), or a range YYYY-MM-DDtoYYYY-MM-DD.")
	return &Guide{
		Summary: "Brave's own independent web index (not Google or Bing) for web, news, images, videos and suggestions, all plain GETs. " +
			"Pick it when you want a non Google index, cacheable GET calls, or quota read from response headers.",
		Endpoints: []GuideEndpoint{
			{Name: "Web search", Method: "GET", Path: "/res/v1/web/search", Description: "Web results, plus news, videos and discussions mixed in when relevant.",
				Params: []GuideParam{q, sgP("count", "query", "integer", false, "Results per page, at most 20 (default 20)."),
					sgP("offset", "query", "integer", false, "Page offset in pages of count, 0 to 9."), country, lang, fresh,
					sgP("safesearch", "query", "string", false, "off, moderate (default) or strict."),
					sgP("result_filter", "query", "string", false, "Comma separated result types to keep, for example web,news."),
					sgP("extra_snippets", "query", "boolean", false, "true adds up to 5 extra excerpts per result.")},
				Response: "web.results[] with title, url, description, age, extra_snippets; news.results[], videos.results[] and query.more_results_available when present."},
			{Name: "News search", Method: "GET", Path: "/res/v1/news/search", Description: "News articles.",
				Params:   []GuideParam{q, sgP("count", "query", "integer", false, "Results per page, at most 50 (default 20)."), sgP("offset", "query", "integer", false, "Page offset, 0 to 9."), country, lang, fresh},
				Response: "results[] with title, url, description, age, page_age, meta_url.hostname."},
			{Name: "Image search", Method: "GET", Path: "/res/v1/images/search", Description: "Images.",
				Params:   []GuideParam{q, sgP("count", "query", "integer", false, "Results, at most 200 (default 50)."), country, lang},
				Response: "results[] with title, url (the page), source, thumbnail.src, properties.url (the full image)."},
			{Name: "Video search", Method: "GET", Path: "/res/v1/videos/search", Description: "Videos.",
				Params:   []GuideParam{q, sgP("count", "query", "integer", false, "Results per page, at most 50 (default 20)."), sgP("offset", "query", "integer", false, "Page offset, 0 to 9."), country, lang, fresh},
				Response: "results[] with title, url, description, age, video.duration, thumbnail.src."},
			{Name: "Suggest", Method: "GET", Path: "/res/v1/suggest/search", Description: "Query autocompletion.",
				Params:   []GuideParam{q, sgP("count", "query", "integer", false, "Suggestions, at most 20 (default 5)."), country},
				Response: "results[] with query."},
		},
		Notes: []string{
			sgNoKey,
			"All endpoints are GETs with query parameters; there is no request body.",
			sgCacheGET("The Accept header is part of the cache key."),
			"offset counts pages, not results: offset=1 with count=20 returns results 21 to 40. Check query.more_results_available before asking for the next page.",
			"Each request costs one query whatever the count. Quota is read from X-RateLimit-Remaining on every answer; a 429 with QUOTA_LIMITED means the month is used up and Acnologia moves to another key.",
		},
	}
}

func tavilyGuide() *Guide {
	return &Guide{
		Summary: "Search, extract, crawl and map built for agents: results come with cleaned page content and an optional answer. " +
			"Pick it when you want ranked snippets or page text in one call; use Serper or Brave for raw Google style links, Firecrawl for heavy scraping.",
		Endpoints: []GuideEndpoint{
			{Name: "Search", Method: "POST", Path: "/search", Description: "Web search with relevance ranked content chunks.",
				Params: []GuideParam{
					sgP("query", "body", "string", true, "The search query."),
					sgP("search_depth", "body", "string", false, "basic (default, 1 credit), fast or ultra-fast (1 credit), advanced (2 credits, better chunks)."),
					sgP("topic", "body", "string", false, "general (default), news or finance."),
					sgP("max_results", "body", "integer", false, "0 to 20 (default 10)."),
					sgP("include_answer", "body", "boolean or string", false, "true or basic for a short LLM answer, advanced for a longer one."),
					sgP("include_raw_content", "body", "boolean or string", false, "true, markdown or text to add the full page per result."),
					sgP("time_range", "body", "string", false, "day, week, month or year."),
					sgP("include_domains", "body", "string[]", false, "Restrict to these domains (up to 300)."),
					sgP("exclude_domains", "body", "string[]", false, "Drop these domains (up to 150).")},
				Body:     map[string]any{"query": "who won the 2026 world cup", "search_depth": "basic", "max_results": 5, "include_answer": true},
				Response: "results[] with title, url, content (snippet), score, raw_content (when asked), published_date; answer when include_answer is set; response_time."},
			{Name: "Extract", Method: "POST", Path: "/extract", Description: "Page content for known URLs.",
				Params: []GuideParam{
					sgP("urls", "body", "string or string[]", true, "Up to 20 URLs."),
					sgP("extract_depth", "body", "string", false, "basic (default) or advanced (tables and embedded content, costs double)."),
					sgP("format", "body", "string", false, "markdown (default) or text.")},
				Body:     map[string]any{"urls": []string{"https://example.com/article"}},
				Response: "results[] with url, raw_content; failed_results[] with url, error (failed URLs are not billed)."},
			{Name: "Crawl", Method: "POST", Path: "/crawl", Description: "Follow links from a start URL and return the content of each page.",
				Params: []GuideParam{
					sgP("url", "body", "string", true, "Start URL."),
					sgP("max_depth", "body", "integer", false, "Link hops from the start (default 1)."),
					sgP("max_breadth", "body", "integer", false, "Links followed per page (default 20)."),
					sgP("limit", "body", "integer", false, "Total pages before stopping (default 50)."),
					sgP("instructions", "body", "string", false, "Natural language focus, for example \"only the API reference\". Doubles the mapping cost.")},
				Body:     map[string]any{"url": "https://docs.example.com", "max_depth": 1, "limit": 20},
				Response: "base_url, results[] with url, raw_content; response_time."},
			{Name: "Map", Method: "POST", Path: "/map", Description: "Discover a site's URLs without fetching their content.",
				Params:   []GuideParam{sgP("url", "body", "string", true, "Start URL."), sgP("max_depth", "body", "integer", false, "Default 1."), sgP("limit", "body", "integer", false, "Default 50."), sgP("instructions", "body", "string", false, "Natural language focus.")},
				Body:     map[string]any{"url": "https://docs.example.com"},
				Response: "base_url, results[] (a list of URL strings)."},
			{Name: "Usage", Method: "GET", Path: "/usage", Description: "Credits used and the plan limit of the key Acnologia picked.",
				Response: "key.usage, key.limit, account.plan_usage, account.plan_limit. It reflects one pooled key, not the whole pool."},
		},
		Notes: []string{
			sgNoKey,
			"Search, extract, crawl and map are POSTs with JSON bodies, so Acnologia does not cache them; only GET /usage is cached for 1 hour.",
			"Credits: search 1 (basic, fast, ultra-fast) or 2 (advanced); extract 1 per 5 successful URLs (2 when advanced); map 1 per 10 pages; crawl costs the map credits plus the extract credits.",
			"Crawl is synchronous and can take a minute or more on large sites; keep limit small and set a long client timeout.",
			"Status 432 or 433 means the key's plan or pay as you go limit is reached; Acnologia rotates to another key.",
		},
	}
}

func exaGuide() *Guide {
	return &Guide{
		Summary: "Neural (embedding based) web search that can return full page text, highlights or summaries in the same call, plus a cited answer endpoint. " +
			"Pick it for semantic queries (\"companies building X\"), category filters such as news, company or people, and content in one round trip.",
		Endpoints: []GuideEndpoint{
			{Name: "Search", Method: "POST", Path: "/search", Description: "Search, optionally with page contents.",
				Params: []GuideParam{
					sgP("query", "body", "string", true, "Natural language query."),
					sgP("type", "body", "string", false, "auto (default), lite (cheaper, index only), deep-lite or deep (multi step research, slower)."),
					sgP("numResults", "body", "integer", false, "1 to 100 (default 10). Results past 10 cost extra."),
					sgP("category", "body", "string", false, "company, publication, news, personal site, financial report or people."),
					sgP("includeDomains", "body", "string[]", false, "Only these domains."),
					sgP("excludeDomains", "body", "string[]", false, "Skip these domains."),
					sgP("startPublishedDate", "body", "string", false, "ISO 8601; endPublishedDate is the upper bound. Not for company or people."),
					sgP("contents", "body", "object", false, "{\"text\": true} or {\"text\": {\"maxCharacters\": 2000}}, {\"highlights\": true}, {\"summary\": {\"query\": \"...\"}}. Each content type is billed per page.")},
				Body:     map[string]any{"query": "startups building battery recycling", "numResults": 5, "contents": map[string]any{"highlights": true}},
				Response: "results[] with title, url, id, publishedDate, author, and text, highlights, summary when asked; costDollars.total; requestId."},
			{Name: "Contents", Method: "POST", Path: "/contents", Description: "Page text, highlights or summaries for URLs or for ids from an earlier search.",
				Params: []GuideParam{
					sgP("urls", "body", "string[]", false, "1 to 100 URLs. Send urls or ids, not both."),
					sgP("ids", "body", "string[]", false, "Result ids from /search."),
					sgP("text", "body", "boolean or object", false, "true, or {\"maxCharacters\": N}."),
					sgP("highlights", "body", "boolean or object", false, "true, or {\"query\": \"...\"}."),
					sgP("maxAgeHours", "body", "integer", false, "0 always fetches live, -1 always uses Exa's cache, up to 720.")},
				Body:     map[string]any{"urls": []string{"https://example.com"}, "text": map[string]any{"maxCharacters": 5000}},
				Response: "results[] with url, title, text, highlights; statuses[] with id, status (success or error) and error.tag for each URL."},
			{Name: "Answer", Method: "POST", Path: "/answer", Description: "A short answer to a question, grounded in search results with citations.",
				Params: []GuideParam{
					sgP("query", "body", "string", true, "The question."),
					sgP("text", "body", "boolean", false, "true includes the full text of each citation."),
					sgP("stream", "body", "boolean", false, "true streams server sent events."),
					sgP("outputSchema", "body", "object", false, "JSON Schema to get the answer as an object instead of a string.")},
				Body:     map[string]any{"query": "What is the capital of Australia?"},
				Response: "answer (string, or object with outputSchema), citations[] with title, url, publishedDate; costDollars.total."},
		},
		Notes: []string{
			sgNoKey,
			"All three endpoints are POSTs, so Acnologia does not cache them even though the provider has a cache TTL.",
			"Price: search about $7 per 1,000 (auto), $4 per 1,000 (lite); each result past 10 adds $1 per 1,000; contents $1 per 1,000 pages per content type; answer $5 per 1,000. Read costDollars in the response for the exact charge.",
			"Content options sit under contents in /search but at the top level in /contents. maxCharacters belongs inside text or highlights, not directly under contents.",
			"Rate limits are per Exa team, shared by all its keys: about 10 searches or answers a second, 100 contents calls a second.",
			"Use includeDomains rather than a site: operator in the query.",
		},
	}
}

func firecrawlGuide() *Guide {
	return &Guide{
		Summary: "Scrape any page (JavaScript rendered) into markdown, HTML or LLM extracted JSON, crawl or map whole sites, and search with scraped results. " +
			"Pick it for hard pages and site wide jobs; for a quick read of a few pages TinyFish Fetch, Tavily Extract or Exa Contents are cheaper.",
		Endpoints: []GuideEndpoint{
			{Name: "Scrape", Method: "POST", Path: "/v2/scrape", Description: "One URL to clean content, synchronously.",
				Params: []GuideParam{
					sgP("url", "body", "string", true, "Page to scrape."),
					sgP("formats", "body", "array", false, "markdown (default), html, rawHtml, links, screenshot, summary, or {\"type\": \"json\", \"schema\": {...}} for structured output."),
					sgP("onlyMainContent", "body", "boolean", false, "Drop navigation and footers (default true)."),
					sgP("waitFor", "body", "integer", false, "Milliseconds to wait for the page before scraping."),
					sgP("maxAge", "body", "integer", false, "Accept Firecrawl's cached copy up to this age in milliseconds.")},
				Body:     map[string]any{"url": "https://example.com", "formats": []string{"markdown"}},
				Response: "data.markdown (or data.html, data.json, data.links), data.metadata with title, sourceURL, statusCode."},
			{Name: "Search", Method: "POST", Path: "/v2/search", Description: "Web search, optionally scraping each result.",
				Params: []GuideParam{
					sgP("query", "body", "string", true, "Up to 500 characters."),
					sgP("limit", "body", "integer", false, "1 to 100 (default 10), per source."),
					sgP("sources", "body", "string[]", false, "web (default), news, images."),
					sgP("tbs", "body", "string", false, "Time filter, for example qdr:d."),
					sgP("scrapeOptions", "body", "object", false, "{\"formats\": [\"markdown\"]} to get page content; without it only url, title and description come back.")},
				Body:     map[string]any{"query": "firecrawl pricing", "limit": 5},
				Response: "data.web[] with url, title, description (and markdown when scraped); data.news[] and data.images[] for those sources; creditsUsed."},
			{Name: "Map", Method: "POST", Path: "/v2/map", Description: "List a site's URLs quickly (sitemap plus discovery), no content.",
				Params:   []GuideParam{sgP("url", "body", "string", true, "Site URL."), sgP("search", "body", "string", false, "Rank URLs by relevance to this text."), sgP("limit", "body", "integer", false, "Default 5000, max 100000.")},
				Body:     map[string]any{"url": "https://docs.example.com", "search": "pricing"},
				Response: "links[] with url, title, description."},
			{Name: "Start a crawl", Method: "POST", Path: "/v2/crawl", Description: "Start an asynchronous crawl; poll its status for the pages.",
				Params: []GuideParam{
					sgP("url", "body", "string", true, "Start URL."),
					sgP("limit", "body", "integer", false, "Maximum pages. Set it: the default is 10,000 and every page costs a credit."),
					sgP("includePaths", "body", "string[]", false, "Regex patterns for paths to keep."),
					sgP("excludePaths", "body", "string[]", false, "Regex patterns for paths to skip."),
					sgP("maxDiscoveryDepth", "body", "integer", false, "Link depth from the start page."),
					sgP("scrapeOptions", "body", "object", false, "Per page options, for example {\"formats\": [\"markdown\"]}.")},
				Body:     map[string]any{"url": "https://docs.example.com", "limit": 50, "scrapeOptions": map[string]any{"formats": []string{"markdown"}}},
				Response: "id (the crawl job id) and url."},
			{Name: "Crawl status", Method: "GET", Path: "/v2/crawl/{id}", Description: "Progress and pages of a crawl.",
				Params:   []GuideParam{sgP("id", "path", "string", true, "Job id from Start a crawl.")},
				Response: "status (scraping, completed, failed), total, completed, creditsUsed, data[] with markdown and metadata, next (URL of the next batch while the crawl runs or when data is over 10 MB)."},
			{Name: "Start an extract", Method: "POST", Path: "/v2/extract", Description: "LLM extraction of structured data from one or more pages (asynchronous).",
				Params: []GuideParam{
					sgP("urls", "body", "string[]", true, "URLs; a trailing /* covers the whole domain."),
					sgP("prompt", "body", "string", false, "What to extract."),
					sgP("schema", "body", "object", false, "JSON Schema of the result.")},
				Body:     map[string]any{"urls": []string{"https://example.com/pricing"}, "prompt": "List each plan with its monthly price."},
				Response: "id (the extract job id)."},
			{Name: "Extract status", Method: "GET", Path: "/v2/extract/{id}", Description: "Result of an extract job.",
				Params:   []GuideParam{sgP("id", "path", "string", true, "Job id from Start an extract.")},
				Response: "status (processing, completed, failed, cancelled), data (the extracted object)."},
			{Name: "Credit usage", Method: "GET", Path: "/v2/team/credit-usage", Description: "Remaining credits of the key Acnologia picked.",
				Response: "data.remainingCredits, data.planCredits, data.billingPeriodEnd."},
		},
		Notes: []string{
			sgNoKey,
			"Crawl and extract are asynchronous: the POST returns an id, then poll GET /v2/crawl/{id} or /v2/extract/{id} every few seconds until status is completed or failed.",
			"The next field in a crawl status is an absolute https://api.firecrawl.dev/v2/... URL. Do not call it directly: take its path and query and call it through the same Acnologia service.",
			"A job belongs to the Firecrawl account whose key started it, and Acnologia may pick a different key for each request. If a status call returns 404 on a service with keys from several accounts, the poll reached another account: retry it, or run jobs through a service with one key.",
			"Credits: 1 per scraped page (scrape, crawl, batch); search 2 credits per 10 results plus the scrape cost of any scraped result; JSON format and some options cost more. Responses report creditsUsed where available.",
			"Acnologia does not cache responses for this provider.",
		},
	}
}

func jinaGuide() *Guide {
	return &Guide{
		Summary: "Jina's search endpoint (s.jina.ai): runs a web search and returns the top results already converted to LLM ready text. " +
			"Pick it to get readable page content for a query in one GET; it is billed in tokens, so it costs more than a links only search.",
		Endpoints: []GuideEndpoint{
			{Name: "Search", Method: "GET", Path: "/", Description: "Search the web and read the top results.",
				Params: []GuideParam{
					sgP("q", "query", "string", true, "Search query, URL encoded."),
					sgP("Accept", "header", "string", false, "application/json for JSON; the default is plain text."),
					sgP("X-Site", "header", "string", false, "Limit results to one site, for example https://docs.python.org."),
					sgP("X-Respond-With", "header", "string", false, "no-content returns titles, URLs and descriptions only, which uses far fewer tokens.")},
				Response: "With Accept: application/json: data[] with title, url, description, content; meta.usage.tokens (what the call cost)."},
		},
		Notes: []string{
			sgNoKey,
			"This service's base URL is s.jina.ai (search). The Jina Reader for a single URL (r.jina.ai) is a different host and is not reachable through this service.",
			sgCacheGET("The Accept header is part of the cache key, so JSON and text answers are cached separately."),
			"Billing is per token of returned content, from 10,000 tokens a request; use X-Respond-With: no-content when you only need links.",
			"About 5 results come back per search, each with the page's full text, so responses can be large.",
		},
	}
}

func searchapiGuide() *Guide {
	return &Guide{
		Summary: "One GET endpoint for Google, Google News, Images, Maps, Shopping, Scholar, Bing, YouTube, Amazon and more, picked with the engine parameter. " +
			"Pick it for engines other than Google web, or as a cheaper SerpAPI style alternative.",
		Endpoints: []GuideEndpoint{
			{Name: "Search", Method: "GET", Path: "/api/v1/search", Description: "Run a search on the chosen engine.",
				Params: []GuideParam{
					sgP("engine", "query", "string", true, "google, google_news, google_images, google_maps, google_shopping, google_scholar, bing, youtube, amazon_search and others; each engine's docs page names it."),
					sgP("q", "query", "string", true, "Search query."),
					sgP("location", "query", "string", false, "Location name, for example \"Austin,Texas,United States\"."),
					sgP("gl", "query", "string", false, "Country (default us)."),
					sgP("hl", "query", "string", false, "Language (default en)."),
					sgP("page", "query", "integer", false, "Results page (default 1). Google returns 10 results a page; num is no longer honored."),
					sgP("time_period", "query", "string", false, "Google: last_hour, last_day, last_week, last_month, last_year.")},
				Response: "organic_results[] with position, title, link, snippet, date; knowledge_graph, answer_box, related_questions when present; pagination.next; search_metadata.id."},
			{Name: "Account", Method: "GET", Path: "/api/v1/me", Description: "Usage and limits of the key Acnologia picked.",
				Response: "account.current_month_usage, account.monthly_allowance, account.remaining_credits, api_usage.searches_this_hour, api_usage.hourly_rate_limit."},
		},
		Notes: []string{
			sgNoKey,
			"Every parameter is a query parameter; there is no request body.",
			sgCacheGET("Identical searches within the hour are served from Acnologia's cache and cost nothing."),
			"Each engine has its own result arrays (for example news_results, images, local_results, shopping_results); read the engine's docs page for its fields.",
			"Plans cap any one hour at about 20% of the monthly searches; a 429 then means wait or use another key, which Acnologia does.",
		},
	}
}

func linkupGuide() *Guide {
	return &Guide{
		Summary: "Search API for AI apps that returns ranked results, a sourced answer, or JSON matching your schema, with a deep mode that iterates. " +
			"Pick it when you want a cited answer or structured output from the web in one call.",
		Endpoints: []GuideEndpoint{
			{Name: "Search", Method: "POST", Path: "/v1/search", Description: "Search, then return results, an answer with sources, or structured data.",
				Params: []GuideParam{
					sgP("q", "body", "string", true, "Natural language question."),
					sgP("depth", "body", "string", true, "fast (recommended for agents), flash (lowest latency), standard, or deep (several iterations, about 10 times the price)."),
					sgP("outputType", "body", "string", true, "searchResults, sourcedAnswer or structured."),
					sgP("structuredOutputSchema", "body", "string", false, "JSON Schema as a string, root type object. Required for structured."),
					sgP("includeDomains", "body", "string[]", false, "Only these domains (up to 100)."),
					sgP("excludeDomains", "body", "string[]", false, "Skip these domains."),
					sgP("fromDate", "body", "string", false, "YYYY-MM-DD; toDate is the upper bound."),
					sgP("maxResults", "body", "integer", false, "Upper bound on results."),
					sgP("includeImages", "body", "boolean", false, "Add image results.")},
				Body:     map[string]any{"q": "What is Linkup's pricing?", "depth": "standard", "outputType": "sourcedAnswer"},
				Response: "searchResults: results[] with type, name, url, content. sourcedAnswer: answer, sources[] with name, url, snippet. structured: your schema's object."},
			{Name: "Fetch", Method: "POST", Path: "/v1/fetch", Description: "One URL to markdown, optionally with structured extraction.",
				Params: []GuideParam{
					sgP("url", "body", "string", true, "Page to fetch."),
					sgP("renderJs", "body", "boolean", false, "Render JavaScript first (default false)."),
					sgP("schema", "body", "object", false, "JSON Schema of data to extract."),
					sgP("instructions", "body", "string", false, "Extraction guidance; needs schema.")},
				Body:     map[string]any{"url": "https://example.com"},
				Response: "markdown; data when schema is set; images[] when extractImages is set."},
			{Name: "Credit balance", Method: "GET", Path: "/v1/credits/balance", Description: "Credits left on the key Acnologia picked.",
				Response: "balance (number)."},
		},
		Notes: []string{
			sgNoKey,
			"depth and outputType are both required on every search; leaving either out is a 400.",
			"Price per search: standard $0.005, deep $0.05; sourcedAnswer or structured adds about $0.001.",
			"Search and fetch are POSTs, so Acnologia does not cache them; GET /v1/credits/balance is cached for 1 hour.",
			"Linkup answers 429 both for rate limits and for insufficient credits; Acnologia rotates keys on either.",
		},
	}
}

func youcomGuide() *Guide {
	return &Guide{
		Summary: "You.com's web index with LLM ready snippets, optional full page markdown, and freshness and domain filters. " +
			"Pick it for snippet rich results meant to ground an LLM, or when you want page content without a separate scrape call.",
		Endpoints: []GuideEndpoint{
			{Name: "Search (GET)", Method: "GET", Path: "/v1/search", Description: "Search with query parameters. Cacheable through Acnologia.",
				Params: []GuideParam{
					sgP("query", "query", "string", true, "Search query; operators such as site: are supported."),
					sgP("count", "query", "integer", false, "Results, 1 to 100 (default 10)."),
					sgP("offset", "query", "integer", false, "Page offset in pages of count, 0 to 9."),
					sgP("freshness", "query", "string", false, "day, week, month, year, or YYYY-MM-DDtoYYYY-MM-DD."),
					sgP("country", "query", "string", false, "Country code, for example US."),
					sgP("safesearch", "query", "string", false, "off, moderate (default) or strict.")},
				Response: "results.web[] with url, title, description, snippets[], page_age; metadata.search_uuid."},
			{Name: "Search (POST)", Method: "POST", Path: "/v1/search", Description: "Same search with a JSON body; the only form that can extract page content.",
				Params: []GuideParam{
					sgP("query", "body", "string", true, "Search query."),
					sgP("count", "body", "integer", false, "1 to 100 (default 10)."),
					sgP("include_domains", "body", "string[]", false, "Allowlist; cannot be combined with exclude_domains."),
					sgP("exclude_domains", "body", "string[]", false, "Domains to drop."),
					sgP("extraction", "body", "object", false, "{\"extraction_mode\": \"full_page\"} adds contents.markdown; {\"extraction_mode\": \"highlights\"} adds contents.highlights. Extraction may cost more.")},
				Body:     map[string]any{"query": "coffee roasting temperature", "count": 5},
				Response: "results.web[] with url, title, description, snippets[], and contents when extraction is set; results.knowledge[] when relevant."},
		},
		Notes: []string{
			sgNoKey,
			sgCacheGET("The POST form is never cached."),
			"News articles come back inside results.web, not in a separate array.",
			"The GET form gets no new features; use POST when you need extraction or domain lists.",
		},
	}
}

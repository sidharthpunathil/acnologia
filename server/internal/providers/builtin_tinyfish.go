package providers

// TinyFish serves each product from its own host, all with the same key
// (X-API-Key). A spec has one base URL, so Search and Fetch are two
// providers; add the same key to both services. Facts come from the OpenAPI
// specs at https://agent.tinyfish.ai/v1/openapi/search and .../fetch, the
// docs at https://docs.tinyfish.ai and https://www.tinyfish.ai/pricing,
// checked 2026-11-02.
func tinyfishBuiltins() []Builtin {
	t := func(name string, status int, body string, expect Outcome) Fixture {
		return Fixture{Name: name, Status: status, Body: body, Expect: expect}
	}
	// Both APIs share one error envelope: {"error":{"code","message"},"request_id"}.
	rules := []Rule{
		{Name: "Free allowance used and no wallet funds", Match: Match{BodyContains: []string{"INSUFFICIENT_CREDITS", "FREE_ALLOWANCE_EXHAUSTED", "DAILY_LIMIT_EXCEEDED"}}, Outcome: QuotaExhausted},
		// Legacy credit accounts answer 403 FORBIDDEN when out of credits.
		{Name: "Legacy account out of credits", Match: Match{Status: []int{403}, BodyContains: []string{"insufficient credits", "no active subscription"}}, Outcome: QuotaExhausted},
		{Name: "Upstream refused this request (not the key's fault)", Match: Match{Status: []int{403}, JSONPath: "error.code", JSONEquals: "FORBIDDEN"}, Outcome: OK},
		{Name: "Invalid or missing key", Match: Match{BodyContains: []string{"INVALID_API_KEY", "MISSING_API_KEY"}}, Outcome: Invalid},
		{Name: "Per minute limit", Match: Match{Status: []int{429}, BodyContains: []string{"RATE_LIMIT_EXCEEDED"}}, Outcome: RateLimited, RetryAfter: &RetryAfter{Source: "header", Value: "Retry-After"}},
		{Name: "Busy (not the key's fault)", Match: Match{BodyContains: []string{"SERVICE_BUSY"}}, Outcome: Error},
	}
	tests := func(ok string) []Fixture {
		return []Fixture{
			t("success", 200, ok, OK),
			t("invalid key", 401, `{"error":{"code":"INVALID_API_KEY","message":"Invalid or expired API key"},"request_id":"r1"}`, Invalid),
			t("missing key", 401, `{"error":{"code":"MISSING_API_KEY","message":"Missing API key"},"request_id":"r2"}`, Invalid),
			t("per minute limit", 429, `{"error":{"code":"RATE_LIMIT_EXCEEDED","message":"Rate limit exceeded"},"request_id":"r3"}`, RateLimited),
			t("daily allowance and wallet empty", 402, `{"error":{"code":"INSUFFICIENT_CREDITS","message":"Insufficient credits"},"request_id":"r4"}`, QuotaExhausted),
			t("free allowance used", 402, `{"error":{"code":"FREE_ALLOWANCE_EXHAUSTED","message":"Free allowance exhausted"},"request_id":"r5"}`, QuotaExhausted),
			t("legacy account out of credits", 403, `{"error":{"code":"FORBIDDEN","message":"Insufficient credits or no active subscription"}}`, QuotaExhausted),
			t("upstream refused the request", 403, `{"error":{"code":"FORBIDDEN","message":"Search request forbidden by the upstream service"}}`, OK),
			t("bad input is the caller's fault", 400, `{"error":{"code":"INVALID_INPUT","message":"query is required"},"request_id":"r6"}`, OK),
			t("service busy", 503, `{"error":{"code":"SERVICE_BUSY","message":"busy"},"request_id":"r7"}`, Error),
		}
	}
	// GET /usage lists the key's usage records: free, and 401 for a bad key.
	health := &Health{Method: "GET", Path: "/usage", Query: map[string]string{"limit": "1"},
		Invalid: &Match{BodyContains: []string{"INVALID_API_KEY", "MISSING_API_KEY"}}}
	const pricing, checked = "https://www.tinyfish.ai/pricing", "2026-11-02"
	return []Builtin{
		{
			Slug: "tinyfish-search", Name: "TinyFish Search", Category: "Search", Color: "#d74b28",
			Description: "Fast, structured web search from TinyFish. Free within per minute and per hour limits; one key works across all TinyFish products.",
			Website:     "https://www.tinyfish.ai", DocsURL: "https://docs.tinyfish.ai/search-api/reference",
			Spec: Spec{
				BaseURL:  "https://api.search.tinyfish.ai",
				Auth:     Auth{Type: "header", Name: "X-API-Key"},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 5, QueueTimeoutSec: 10, CacheTTLSec: 3600},
				Quota:    QuotaInfo{Unit: "requests", FreeTier: "Free: 30 requests a minute, 500 an hour, up to 12,000 a day"},
				Offers: []Offer{{
					Name: "TinySearch", Match: &OfferMatch{Method: "GET", Path: "/$"}, Free: true,
					Price:  &Price{Amount: 0, Currency: "USD", Unit: "request"},
					Limits: []Limit{{Count: 30, Per: "minute", Unit: "requests"}, {Count: 500, Per: "hour", Unit: "requests"}},
					Notes: "Limits are per person or organization, so they hold per key when each key is its own account. " +
						"The docs add a free allowance of 12,000 requests a day (resets 00:00 UTC); past it a request needs wallet funds or gets 402.",
					SourceURL: pricing, CheckedAt: checked,
				}},
				Rules:            rules,
				UseStandardRules: true,
				Health:           health,
				Examples: []Example{
					{Name: "Search", Method: "GET", Path: "/", Query: map[string]string{"query": "coffee roasters"}},
					{Name: "Usage", Method: "GET", Path: "/usage", Query: map[string]string{"limit": "1"}, Safe: true},
				},
				Tests: tests(`{"query":"coffee roasters","results":[],"total_results":0,"page":0}`),
				Guide: tinyfishSearchGuide(),
			},
		},
		{
			Slug: "tinyfish-fetch", Name: "TinyFish Fetch", Category: "Search", Color: "#d74b28",
			Description: "Turns up to 10 URLs per request into clean markdown, HTML or JSON. Free within per minute and per day limits.",
			Website:     "https://www.tinyfish.ai", DocsURL: "https://docs.tinyfish.ai/fetch-api/reference",
			Spec: Spec{
				BaseURL:  "https://api.fetch.tinyfish.ai",
				Auth:     Auth{Type: "header", Name: "X-API-Key"},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 5, QueueTimeoutSec: 10},
				Quota:    QuotaInfo{Unit: "urls", FreeTier: "Free: 150 URLs a minute, 1,000 a day"},
				Offers: []Offer{{
					Name: "TinyFetch", Match: &OfferMatch{Method: "POST", Path: "/$"}, Free: true,
					Price:  &Price{Amount: 0, Currency: "USD", Unit: "url"},
					Limits: []Limit{{Count: 150, Per: "minute", Unit: "urls"}, {Count: 1000, Per: "day", Unit: "urls"}},
					Notes: "Counted per successfully fetched URL, up to 10 URLs a request; Acnologia's pacer counts requests, so send one URL per request to stay exact. " +
						"The daily allowance resets 00:00 UTC; past it a request needs wallet funds or gets 402.",
					SourceURL: pricing, CheckedAt: checked,
				}},
				Rules:            rules,
				UseStandardRules: true,
				Health:           health,
				Examples: []Example{
					{Name: "Fetch a page", Method: "POST", Path: "/", Body: map[string]any{"urls": []string{"https://example.com"}, "format": "markdown"}},
					{Name: "Usage", Method: "GET", Path: "/usage", Query: map[string]string{"limit": "1"}, Safe: true},
				},
				Tests: tests(`{"results":[{"url":"https://example.com","text":"Example Domain"}],"errors":[]}`),
				Guide: tinyfishFetchGuide(),
			},
		},
	}
}

// tinyfishUsage is the usage listing both TinyFish hosts serve.
func tinyfishUsage(what string) GuideEndpoint {
	return GuideEndpoint{Name: "Usage", Method: "GET", Path: "/usage", Description: "Recent " + what + " records of the key Acnologia picked. Free.",
		Params: []GuideParam{
			sgP("limit", "query", "integer", false, "1 to 1000 (default 100)."),
			sgP("page", "query", "integer", false, "Starting at 1."),
			sgP("status", "query", "string", false, "completed or failed."),
			sgP("start_after", "query", "string", false, "RFC 3339 time; end_before is the upper bound.")},
		Response: "items[] (one per request or URL, with status and created_at), total, page, total_pages, has_more."}
}

func tinyfishSearchGuide() *Guide {
	return &Guide{
		Summary: "Fast structured web, news and research paper search from TinyFish, free within per minute, per hour and per day limits. " +
			"Pick it first for everyday link finding when cost matters; use Exa or Tavily when you need page content or answers in the same call.",
		Endpoints: []GuideEndpoint{
			{Name: "Search", Method: "GET", Path: "/", Description: "Search the web. Every parameter is a query parameter.",
				Params: []GuideParam{
					sgP("query", "query", "string", true, "Search query, 1 to 2000 characters."),
					sgP("purpose", "query", "string", false, "The goal behind the search, used to rank results."),
					sgP("domain_type", "query", "string", false, "web (default), news or research_paper."),
					sgP("location", "query", "string", false, "Country code (default US)."),
					sgP("language", "query", "string", false, "Language code (default en)."),
					sgP("include_domains", "query", "string", false, "Comma separated domains to keep; exclude_domains drops them."),
					sgP("recency_minutes", "query", "integer", false, "Only results from the last N minutes."),
					sgP("after_date", "query", "string", false, "YYYY-MM-DD; before_date is the upper bound."),
					sgP("page", "query", "integer", false, "Zero based page, 0 to 10.")},
				Response: "results[] with position, title, url, snippet, site_name, date (plus publisher for news; authors, year, venue, pdf_url for papers); total_results, page."},
			tinyfishUsage("search"),
		},
		Notes: []string{
			sgNoKey,
			"The search is GET / with query parameters; there is no body. page is zero based.",
			sgCacheGET(""),
			"Free per account: 30 requests a minute, 500 an hour and 12,000 a day (reset 00:00 UTC). Past the daily allowance a request needs wallet funds or gets 402; Acnologia then moves to another key.",
			"One TinyFish key also works for TinyFish Fetch, which turns result URLs into page content.",
		},
	}
}

func tinyfishFetchGuide() *Guide {
	return &Guide{
		Summary: "Turns up to 10 URLs per request into clean markdown, HTML or JSON, free within per minute and per day limits. " +
			"Pick it to read pages you already have URLs for; use Firecrawl for whole site crawls or interactive pages.",
		Endpoints: []GuideEndpoint{
			{Name: "Fetch", Method: "POST", Path: "/", Description: "Fetch and extract the content of one or more URLs in parallel.",
				Params: []GuideParam{
					sgP("urls", "body", "string[]", true, "1 to 10 URLs."),
					sgP("format", "body", "string", false, "markdown (default), html or json."),
					sgP("purpose", "body", "string", false, "What the content is for; tailors extraction."),
					sgP("links", "body", "boolean", false, "true adds outbound links[]."),
					sgP("image_links", "body", "boolean", false, "true adds image_links[]."),
					sgP("include_selectors", "body", "string[]", false, "CSS selectors to keep; exclude_selectors removes elements first."),
					sgP("ttl", "body", "integer", false, "Accept a cached copy up to this many seconds old; 0 prefers a live fetch."),
					sgP("per_url_timeout_ms", "body", "integer", false, "Per URL timeout, up to 110000.")},
				Body:     map[string]any{"urls": []string{"https://example.com"}, "format": "markdown"},
				Response: "results[] with url, final_url, title, description, text (string, or object for json), published_date; errors[] with url and error code (for example bot_blocked, timeout, page_not_found)."},
			tinyfishUsage("fetch"),
		},
		Notes: []string{
			sgNoKey,
			"A 200 can still hold failures: always check errors[] as well as results[].",
			"Billed and limited per successfully fetched URL: 150 a minute and 1,000 a day free (reset 00:00 UTC). Acnologia's pacer counts requests, so send one URL per request when you need its limits to be exact.",
			"Acnologia does not cache fetch responses (they are POSTs).",
			"if_none_match and if_modified_since work with a single URL only; with several URLs they are a 400.",
		},
	}
}

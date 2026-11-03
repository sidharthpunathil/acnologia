package providers

import _ "embed"

//go:embed seedlogos/serpapi.png
var serpLogo []byte

//go:embed seedlogos/elevenlabs.png
var elevenLogo []byte

// Builtin is a provider shipped with Acnologia. It is seeded into the database on
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
				// Cached and failed searches are free, so quota is read from the
				// account endpoint rather than counted per request.
				Quota: QuotaInfo{Unit: "searches"},
				Offers: []Offer{
					{Name: "Search", Match: &OfferMatch{Method: "GET", Path: "/search.json"},
						Price:     &Price{Amount: 0.025, Currency: "USD", Unit: "search"},
						Notes:     "Starter: $25 a month for 1,000 searches, 200 an hour. Free plan: 250 searches a month, 50 an hour. Only successful searches count.",
						SourceURL: "https://serpapi.com/pricing", CheckedAt: "2026-11-02"},
					{Name: "Locations", Match: &OfferMatch{Method: "GET", Path: "/locations.json"}, Free: true,
						Notes: "The Locations API is free to use.", SourceURL: "https://serpapi.com/locations-api", CheckedAt: "2026-11-02"},
				},
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
						ResetAt:   "plan_renewal_date", ResetFormat: "iso8601",
						// this_hour_searches is the running hour; last_hour_searches is the previous one.
						Window: &QuotaWindow{Limit: "account_rate_limit_per_hour", Used: "this_hour_searches", Period: "hour"},
					},
				},
				Examples: []Example{
					{Name: "Google search", Method: "GET", Path: "/search.json", Query: map[string]string{"engine": "google", "q": "coffee"}, Safe: false},
					// The Locations API is free, so live checks can test a real request.
					{Name: "Locations", Method: "GET", Path: "/locations.json", Query: map[string]string{"q": "Austin", "limit": "3"}, Safe: true},
				},
				Guide: serpapiGuide(),
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
				Offers: []Offer{
					{Name: "Text to speech", Match: &OfferMatch{Method: "POST", Path: "/v1/text-to-speech"},
						Price: &Price{Amount: 0.00008, Currency: "USD", Unit: "character"},
						Notes: "API price per 1,000 characters: Multilingual v2 and v3 $0.08, Flash and Turbo $0.04. Free plan: 10,000 credits a month. " +
							"Concurrent requests per plan (Multilingual v2 / Flash): Free 2/4, Starter 3/6, Creator 5/10, Pro 10/20.",
						SourceURL: "https://elevenlabs.io/pricing/api", CheckedAt: "2026-11-02"},
				},
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
				Guide:    elevenlabsGuide(),
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
				Guide:            genericGuide(),
				Tests: []Fixture{
					{Name: "429", Status: 429, Expect: RateLimited},
					{Name: "402", Status: 402, Expect: QuotaExhausted},
					{Name: "403", Status: 403, Expect: Invalid},
					{Name: "400 is the caller's fault", Status: 400, Expect: OK},
				},
			},
		},
	}, append(append(append(append(append(searchBuiltins(), tinyfishBuiltins()...), llmBuiltins()...), labBuiltins()...), hostBuiltins()...), peerBuiltin())...)
}

func serpapiGuide() *Guide {
	return &Guide{
		Summary: "Google, Bing, YouTube, Google Maps, News, Shopping, Scholar and 50+ other engines as structured JSON, chosen with the engine parameter. " +
			"Pick it for the widest engine coverage and rich parsed blocks; it costs more per search than Serper or SearchApi, and identical searches within an hour are free.",
		Endpoints: []GuideEndpoint{
			{Name: "Search", Method: "GET", Path: "/search.json", Description: "Run a search on one engine.",
				Params: []GuideParam{
					sgP("engine", "query", "string", true, "google (default), google_news, google_images, google_maps, google_shopping, google_scholar, bing, youtube, duckduckgo and more; each engine's docs page names it."),
					sgP("q", "query", "string", true, "Search query (youtube uses search_query instead)."),
					sgP("location", "query", "string", false, "Canonical location name from the Locations API, for example \"Austin, Texas, United States\"."),
					sgP("gl", "query", "string", false, "Country code, for example us."),
					sgP("hl", "query", "string", false, "Language code, for example en."),
					sgP("start", "query", "integer", false, "Google result offset for paging: 0, 10, 20."),
					sgP("no_cache", "query", "boolean", false, "true forces a fresh search, which is billed; by default SerpAPI serves a cached copy for up to 1 hour for free."),
					sgP("async", "query", "boolean", false, "true returns at once with search_metadata.id; fetch the result later from the Search Archive.")},
				Response: "search_metadata.status (Success) and id; organic_results[] with position, title, link, snippet; also answer_box, knowledge_graph, related_questions, local_results and serpapi_pagination.next when present. An error field means the search failed."},
			{Name: "Search Archive", Method: "GET", Path: "/searches/{search_id}.json", Description: "A past or async search's result by its id.",
				Params:   []GuideParam{sgP("search_id", "path", "string", true, "search_metadata.id of an earlier search.")},
				Response: "The same JSON as the original search; search_metadata.status is Processing until an async search finishes."},
			{Name: "Account", Method: "GET", Path: "/account.json", Description: "Plan and remaining searches of the key Acnologia picked. Free.",
				Response: "searches_per_month, total_searches_left, this_hour_searches, account_rate_limit_per_hour, plan_renewal_date."},
			{Name: "Locations", Method: "GET", Path: "/locations.json", Description: "Look up canonical location names for the location parameter. Free.",
				Params:   []GuideParam{sgP("q", "query", "string", false, "Part of a place name."), sgP("limit", "query", "integer", false, "Results to return.")},
				Response: "Array of objects with canonical_name, country_code, target_type, reach."},
		},
		Notes: []string{
			sgNoKey,
			"Every parameter is a query parameter on a GET; there is no request body.",
			"Only successful, uncached searches use quota. SerpAPI's own cache makes an identical search within 1 hour free; no_cache=true bypasses it and is billed. Acnologia itself does not cache this provider.",
			"Plans cap searches per hour as well as per month (free plan: 50 an hour, 250 a month). An hourly cap error makes Acnologia rest that key until the next hour.",
			"Account, Locations and Search Archive calls are free.",
			"Async searches and the Search Archive belong to the SerpAPI account that ran them; with keys from several accounts, an archive lookup can reach another account and fail. Prefer synchronous searches through a pool.",
		},
	}
}

func elevenlabsGuide() *Guide {
	voice := sgP("voice_id", "path", "string", true, "Voice id from List voices.")
	format := sgP("output_format", "query", "string", false, "codec_samplerate_bitrate, default mp3_44100_128; others include mp3_22050_32, pcm_16000, pcm_24000, ulaw_8000, opus_48000_128. Some formats need a higher plan.")
	tts := []GuideParam{voice, format,
		sgP("text", "body", "string", true, "Text to speak."),
		sgP("model_id", "body", "string", false, "Default eleven_multilingual_v2; the Flash and Turbo models cost half per character. GET /v1/models lists ids."),
		sgP("voice_settings", "body", "object", false, "stability, similarity_boost, style, speed, use_speaker_boost; overrides the voice's stored settings for this call."),
		sgP("language_code", "body", "string", false, "ISO 639-1 code to force a language (not supported by multilingual v2).")}
	ttsBody := map[string]any{"text": "Hello from Acnologia.", "model_id": "eleven_multilingual_v2"}
	return &Guide{
		Summary: "Text to speech, speech to text, voices and sound effects. Pick it for natural voices in many languages; text to speech is billed per character of input text.",
		Endpoints: []GuideEndpoint{
			{Name: "Text to speech", Method: "POST", Path: "/v1/text-to-speech/{voice_id}", Description: "Synthesize the whole clip and return it as audio.",
				Params: tts, Body: ttsBody,
				Response: "Binary audio (audio/mpeg for mp3 formats), not JSON. Save the bytes; do not parse them."},
			{Name: "Text to speech, streamed", Method: "POST", Path: "/v1/text-to-speech/{voice_id}/stream", Description: "Same input, audio streamed in chunks as it is generated, for low latency playback.",
				Params: tts, Body: ttsBody,
				Response: "Chunked binary audio stream in the chosen output_format."},
			{Name: "Text to speech with timestamps", Method: "POST", Path: "/v1/text-to-speech/{voice_id}/with-timestamps", Description: "Audio plus character level timing, for captions or lip sync.",
				Params: tts, Body: ttsBody,
				Response: "JSON: audio_base64 (base64 audio), alignment with characters[], character_start_times_seconds[], character_end_times_seconds[]."},
			{Name: "List voices", Method: "GET", Path: "/v2/voices", Description: "Voices available to the key's account, searchable and paged.",
				Params: []GuideParam{
					sgP("search", "query", "string", false, "Filter by name, description or labels."),
					sgP("page_size", "query", "integer", false, "Up to 100 (default 10)."),
					sgP("next_page_token", "query", "string", false, "From the previous page.")},
				Response: "voices[] with voice_id, name, category, labels, preview_url; has_more, next_page_token."},
			{Name: "List models", Method: "GET", Path: "/v1/models", Description: "Models and what each can do.",
				Response: "Array of models with model_id, name, can_do_text_to_speech, languages[]."},
			{Name: "Speech to text", Method: "POST", Path: "/v1/speech-to-text", Description: "Transcribe an audio or video file. Send multipart/form-data, not JSON.",
				Params: []GuideParam{
					sgP("model_id", "body", "string", true, "Form field, for example scribe_v2."),
					sgP("file", "body", "file", false, "Form file field with the audio or video. Send file or source_url."),
					sgP("source_url", "body", "string", false, "Form field: a hosted audio or video URL instead of a file."),
					sgP("language_code", "body", "string", false, "ISO 639-1 or 639-3; detected when omitted."),
					sgP("diarize", "body", "boolean", false, "Label speakers.")},
				Response: "text (the transcript), language_code, words[] with text, start, end, speaker_id."},
			{Name: "Sound effects", Method: "POST", Path: "/v1/sound-generation", Description: "Generate a sound effect from a description.",
				Params: []GuideParam{format,
					sgP("text", "body", "string", true, "Description of the sound."),
					sgP("duration_seconds", "body", "number", false, "Length in seconds; the model picks one when omitted."),
					sgP("prompt_influence", "body", "number", false, "0 to 1 (default 0.3); higher follows the text more literally.")},
				Body:     map[string]any{"text": "Rain on a tin roof", "duration_seconds": 5},
				Response: "Binary audio (audio/mpeg by default)."},
			{Name: "Subscription", Method: "GET", Path: "/v1/user/subscription", Description: "Character quota of the key Acnologia picked.",
				Response: "tier, character_count (used), character_limit, next_character_count_reset_unix."},
		},
		Notes: []string{
			sgNoKey,
			"Voice ids are account specific apart from the shared default voices: list voices through this service, then use a voice_id from that answer. A voice another account added may not exist on the key Acnologia picks.",
			"Text to speech and sound effects return raw audio bytes. Read the body as binary and check Content-Type; an error comes back as JSON with detail.status and detail.message.",
			"Quota is in characters (credits) per billing cycle: each text to speech call uses about one credit per input character on multilingual models, half on Flash and Turbo.",
			"Concurrency is low per key (2 on the free plan). Acnologia queues extra requests for up to 30 seconds and spreads them across keys; expect waits rather than errors under load.",
			"Speech to text takes multipart/form-data; Acnologia forwards the body and Content-Type unchanged.",
			"Acnologia does not cache this provider's responses.",
		},
	}
}

func genericGuide() *Guide {
	return &Guide{
		Summary: "A pass through service for any HTTP API the admin set up. Acnologia forwards your request to the service's base URL and puts a pooled key where the service config says (a header, a bearer token or a query parameter).",
		Notes: []string{
			sgNoKey,
			"Call /proxy/<service>/<path> and Acnologia forwards to <base URL>/<path> with the same method, query string, headers and body. Paths are relative to the base URL the admin set, so do not repeat its path prefix.",
			"Any endpoint of that API works; this guide lists none because they depend on the API behind the service. Use that API's own documentation for paths and parameters.",
			"Do not send the API's own auth header or key parameter. Acnologia sets it, replacing whatever you send there.",
			"Standard status codes drive rotation: 429 is a rate limit and 402 is out of quota (the next key is tried), 401 and 403 mark the key invalid, other 4xx answers are your request's fault and are returned as is.",
			"Responses are not cached unless the admin set a cache TTL on the service; then successful GETs are cached and carry X-Acnologia-Cache: hit.",
		},
	}
}

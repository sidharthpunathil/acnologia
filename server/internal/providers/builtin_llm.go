package providers

// gemini is a model's offer: free on free tier keys, paid per token otherwise.
func gemini(name, model, paid string) Offer {
	return Offer{Name: name, Match: &OfferMatch{Method: "POST", Path: "/v1beta/models/" + model + ":"}, Free: true,
		Notes:     "Free of charge on free tier keys, within per model limits shown in AI Studio. Paid tier: " + paid + ".",
		SourceURL: "https://ai.google.dev/gemini-api/docs/pricing", CheckedAt: "2026-11-02"}
}

func geminiGuide() *Guide {
	contents := []map[string]any{{"role": "user", "parts": []map[string]any{{"text": "Say hello in five words"}}}}
	model := llmParam("model", "path", "string", true, "Model id without the models/ prefix, for example gemini-3.6-flash or gemini-3.5-flash-lite.")
	return &Guide{
		Summary: "Google's Gemini models through the Gemini API: text and multimodal generation, embeddings and token counting. " +
			"Pick it for long multimodal context (images, audio, video, PDFs) and a free tier on most models.",
		Endpoints: []GuideEndpoint{
			{Name: "Generate content", Method: "POST", Path: "/v1beta/models/{model}:generateContent",
				Description: "Generates a reply. The model is in the path, not the body.",
				Params: []GuideParam{
					model,
					llmParam("contents", "body", "array", true, "Turns {role: user or model, parts: [{text} or {inline_data: {mime_type, data}}]}."),
					llmParam("systemInstruction", "body", "object", false, "{\"parts\": [{\"text\": \"...\"}]}."),
					llmParam("generationConfig", "body", "object", false, "maxOutputTokens, temperature, responseMimeType (application/json), responseSchema, thinkingConfig."),
					llmParam("tools", "body", "array", false, "functionDeclarations, or {\"google_search\": {}} for grounding with Google Search."),
				},
				Body:     map[string]any{"contents": contents, "generationConfig": map[string]any{"maxOutputTokens": 256}},
				Response: "candidates[0].content.parts[].text, candidates[0].finishReason, usageMetadata (promptTokenCount, candidatesTokenCount, totalTokenCount), promptFeedback.blockReason when the prompt was blocked."},
			{Name: "Stream generate content", Method: "POST", Path: "/v1beta/models/{model}:streamGenerateContent",
				Description: "Same body as Generate content, streamed. Add alt=sse to get Server-Sent Events; without it the reply is one JSON array delivered in pieces.",
				Params: []GuideParam{
					model,
					llmParam("alt", "query", "string", false, "sse for Server-Sent Events."),
					llmParam("contents", "body", "array", true, "As in Generate content."),
				},
				Body:     map[string]any{"contents": contents},
				Response: "Each data: event is a GenerateContentResponse chunk; read candidates[0].content.parts[].text. The last chunk carries usageMetadata."},
			{Name: "Embed content", Method: "POST", Path: "/v1beta/models/{model}:embedContent",
				Description: "One embedding. For many texts use :batchEmbedContents with a requests array.",
				Params: []GuideParam{
					llmParam("model", "path", "string", true, "gemini-embedding-001 (text) or gemini-embedding-2 (multimodal)."),
					llmParam("content", "body", "object", true, "{\"parts\": [{\"text\": \"...\"}]}."),
					llmParam("taskType", "body", "string", false, "RETRIEVAL_DOCUMENT, RETRIEVAL_QUERY, SEMANTIC_SIMILARITY, CLASSIFICATION or CLUSTERING."),
					llmParam("outputDimensionality", "body", "integer", false, "Truncate the vector, for example 768."),
				},
				Body:     map[string]any{"content": map[string]any{"parts": []map[string]any{{"text": "first text"}}}, "taskType": "RETRIEVAL_DOCUMENT"},
				Response: "embedding.values (array of floats)."},
			{Name: "Count tokens", Method: "POST", Path: "/v1beta/models/{model}:countTokens",
				Description: "Counts the tokens of a request without running it. Free.",
				Params:      []GuideParam{model, llmParam("contents", "body", "array", true, "As in Generate content.")},
				Body:        map[string]any{"contents": contents},
				Response:    "totalTokens."},
			{Name: "List models", Method: "GET", Path: "/v1beta/models",
				Description: "Models and the methods each supports.",
				Params: []GuideParam{
					llmParam("pageSize", "query", "integer", false, "Page size."),
					llmParam("pageToken", "query", "string", false, "nextPageToken from the previous page."),
				},
				Response: "models[].name (models/<id>), models[].supportedGenerationMethods, models[].inputTokenLimit, models[].outputTokenLimit, nextPageToken."},
			{Name: "OpenAI compatible chat", Method: "POST", Path: "/v1beta/openai/chat/completions",
				Description: "OpenAI Chat Completions format for clients that only speak it. The model goes in the body here.",
				Params: []GuideParam{
					llmParam("model", "body", "string", true, "For example gemini-3.6-flash."),
					llmParam("messages", "body", "array", true, "OpenAI style messages."),
				},
				Body:     map[string]any{"model": "gemini-3.6-flash", "messages": llmMessages("Say hello in five words")},
				Response: "choices[0].message.content, usage."},
		},
		Notes: []string{
			llmKeyNote,
			"The native API puts the model in the path, uses role model (not assistant) and camelCase fields. Model ids in the list carry a models/ prefix; drop it in paths.",
			"gemini-2.5-flash is deprecated: from 2026-12-01 Google routes it to gemini-3.5-flash-lite. Use gemini-3.6-flash or gemini-3.5-flash-lite for new work.",
			llmStreamNote + " Use :streamGenerateContent with alt=sse; generateContent never streams.",
			"Errors look like {\"error\": {\"code\", \"message\", \"status\"}}. 429 RESOURCE_EXHAUSTED is a per minute or per day quota (free tier daily caps reset at midnight Pacific); 503 UNAVAILABLE means the model is overloaded, so retry later.",
			"A reply with no candidates, or finishReason SAFETY, means the content was blocked; check promptFeedback and safetyRatings.",
		},
	}
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
				Guide:   geminiGuide(),
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

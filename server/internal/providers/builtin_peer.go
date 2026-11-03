package providers

// PeerSlug is the provider of a service another Acnologia server shares with
// this one. Its one key is the share token, and its base URL is the owner's
// /proxy/<service>. Such services can never be shared onward.
const PeerSlug = "acnologia-peer"

// PeerManifestPath is the peer provider's health check, at the owner's
// origin rather than under its /proxy/<service> base URL.
const PeerManifestPath = "/api/peer/v1/manifest"

// peerBuiltin describes another Acnologia proxy. The owner already rotated
// its own keys before answering, so anything it relays from the upstream
// provider is not the share token's fault. Only the owner's own refusals
// count against the token.
func peerBuiltin() Builtin {
	rules := []Rule{
		{Name: "Share revoked or paused by its owner", Match: Match{Status: []int{403}, BodyContains: []string{"share_revoked", "share_paused", "share_expired"}}, Outcome: Invalid},
		{Name: "Share token rejected", Match: Match{Status: []int{401}}, Outcome: Invalid},
		{Name: "Share limit reached", Match: Match{Status: []int{429}, JSONPath: "error.code", JSONEquals: "app_limit_reached"},
			Outcome: QuotaExhausted, RetryAfter: &RetryAfter{Source: "body_json", Value: "retry_after_sec"}},
		{Name: "Owner has no free key right now", Match: Match{Status: []int{503}, BodyContains: []string{"no_key_available", "all_keys_busy", "service_unavailable"}},
			Outcome: RateLimited, RetryAfter: &RetryAfter{Source: "body_json", Value: "retry_after_sec"}},
		// Everything else is the upstream provider's answer, relayed as is.
		{Name: "Relayed from the upstream provider (not the token's fault)", Match: Match{BodyRegex: `(?s).*`}, Outcome: OK},
	}
	return Builtin{
		Slug: PeerSlug, Name: "Acnologia server", Category: "Acnologia", Color: "#5b5bd6",
		Description: "A service another Acnologia server shares with this one. Requests go through that server's proxy; its keys never leave it.",
		Website:     "https://github.com/sidharthpunathil/acnologia",
		Spec: Spec{
			BaseURL:  "https://acnologia.example.com/proxy/service",
			Auth:     Auth{Type: "bearer", Name: "Authorization"},
			Defaults: Defaults{Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 1, BreakerThreshold: -1},
			Quota:    QuotaInfo{Unit: "requests"},
			Rules:    rules,
			Guide:    peerGuide(),
			Health: &Health{Method: "GET", Path: PeerManifestPath, FromOrigin: true,
				Invalid: &Match{BodyContains: []string{"share_revoked", "share_paused", "share_expired"}}},
			Tests: []Fixture{
				{Name: "success", Status: 200, Body: `{"ok":true}`, Expect: OK},
				{Name: "revoked", Status: 403, Body: `{"error":{"code":"share_revoked","message":"this share was revoked"}}`, Expect: Invalid},
				{Name: "paused", Status: 403, Body: `{"error":{"code":"share_paused","message":"this share is paused"}}`, Expect: Invalid},
				{Name: "bad token", Status: 401, Body: `{"error":{"code":"unauthenticated","message":"invalid app token"}}`, Expect: Invalid},
				{Name: "share limit", Status: 429, Body: `{"error":{"code":"app_limit_reached","message":"limit"},"retry_after_sec":60}`, Expect: QuotaExhausted},
				{Name: "owner has no key", Status: 503, Body: `{"error":{"code":"no_key_available","message":"none"},"retry_after_sec":30}`, Expect: RateLimited},
				{Name: "owner keys busy", Status: 503, Body: `{"error":{"code":"all_keys_busy","message":"busy"},"retry_after_sec":1}`, Expect: RateLimited},
				{Name: "upstream 429 relayed", Status: 429, Body: `{"error":"Your searches for the past hour have exceeded the limit"}`, Expect: OK},
				{Name: "upstream 500 relayed", Status: 500, Body: `oops`, Expect: OK},
				{Name: "service not shared", Status: 403, Body: `{"error":{"code":"service_not_allowed","message":"no"}}`, Expect: OK},
			},
		},
	}
}

// peerGuide lists no endpoints: they are those of the service the other
// server shares, whatever provider that is.
func peerGuide() *Guide {
	return &Guide{
		Summary: "A service another Acnologia server shares with this one. Your call goes to that server's proxy and is answered with a key from its pool; its keys never reach this server. " +
			"Use it exactly like the shared service on the owner's side: same paths, parameters and bodies.",
		Notes: []string{
			sgNoKey + " Acnologia sends the share token to the owner server for you.",
			"Paths are the remote service's. A call to /proxy/<this service>/<path> reaches <owner>/proxy/<shared service>/<path>, so use the endpoints of the provider behind the shared service (the service's peer_provider, for example serpapi or tavily) and read that provider's guide.",
			"The owner server rotates its own keys before answering, so a provider error relayed from upstream (429, 5xx and the like) means its pool could not serve the call; it is not this service's fault and is returned as is.",
			"403 with share_revoked, share_paused or share_expired means the owner ended or paused the share; nothing on this side can fix it.",
			"429 with error.code app_limit_reached means the share's request or budget limit is used up; retry_after_sec says when it resets.",
			"503 with no_key_available or all_keys_busy means the owner has no free key right now; retry after retry_after_sec.",
			"Each request is tried once (no retries through another share key), and services shared by another server cannot be leased or shared onward: proxy calls only.",
			"Caching follows this service's settings here (off by default) and the owner's cache on its side.",
		},
	}
}

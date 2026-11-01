package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/cogito-keys/cogito/internal/providers"
	"github.com/cogito-keys/cogito/internal/store"
)

func prStatus(st *string) store.RunPatch { return store.RunPatch{Status: st} }

const specGuide = `You are the provider engineer inside Cogito, a self-hosted API key pool. Cogito stores many API keys per provider, hands out a working key on demand, and rotates to the next key when one hits a rate limit or runs out of quota. A provider in Cogito is a declarative JSON spec that a generic engine interprets. You research an API and write that spec. Nothing you write is executed; the engine only reads it.

# What the spec does
- base_url and auth: where requests go and where the key is placed (query param, header, or bearer token). The proxy puts the key there for every request.
- defaults: strategy (round_robin, fill_first, least_used, most_quota, random), cooldown_sec (base cooldown after a rate limit, doubled on repeats), max_attempts (keys tried per proxied request), max_concurrency (parallel requests allowed per key; 0 = unlimited; set it when the provider documents a concurrency cap), queue_timeout_sec (how long a request waits for a free key when all are busy; 0 to 30 is sensible when max_concurrency is set).
- quota: unit (searches, characters, credits, requests, tokens) and counts_per_request (true only when each successful request spends exactly one unit).
- rules: ordered list, first match wins, evaluated on error responses. Each rule: name, match, outcome, optional retry_after.
  - match fields are ANDed; values inside a list are ORed; text matching is case insensitive substring.
    status: [codes] (empty = any status >= 400; rules never see 2xx/3xx unless status lists such a code),
    body_contains: ["..."], body_regex: "...", json_path: "error.code" with json_equals: "RATE_LIMITED", header_present: "X-Header".
  - outcome: ok (the key is fine, for example a validation error), rate_limited (temporary, key cools down), quota_exhausted (key is out until its quota resets), invalid (key revoked, wrong or suspended), error (provider side failure, key not penalized).
  - retry_after: {source: header, value: "Retry-After"} | {source: fixed, value: "60"} | {source: next_hour} | {source: body_json, value: "error.retry_after_seconds"} | {source: none}.
- use_standard_rules: true appends 429 rate_limited (Retry-After header), 402 quota_exhausted, 401/403 invalid, 5xx error after your rules. Keep it true unless the provider uses those codes differently.
- health: an endpoint that tells whether a key works and how much quota is left WITHOUT spending quota (account, usage, subscription or user endpoints). method, path, query, invalid (match for a rejected key; status may be omitted to match a 200 body), ok_anyway (match meaning the key works but cannot read quota), quota: {limit: [paths summed], remaining: [paths summed], used: [paths] (remaining = limit - used when remaining is empty), reset_at: path, reset_format: unix | unix_ms | iso8601, window: {limit: path, used: path, period: minute | hour | day} for short caps}. Paths are dot paths, numeric segments index arrays. Omit health entirely if no free endpoint exists.
- examples: realistic requests for the integration docs and the live check. Mark safe: true only on requests that are free or nearly free and read only.
- tests: fixture responses with the expected outcome. They prove the rules work and guard future edits. Write at least one success case, one test per rule using the provider's real documented error bodies, and tests for 429, 401 and 5xx. A request validation error (400/422) should usually be ok.

# Provider metadata
slug (lowercase, hyphens), name (official product name), category (one or two words, for example Search, Voice, LLM, Images, Data, Email, Maps, Finance), description (one or two plain sentences: what it does and how its quota works), website (homepage), docs_url (API reference), color (#rrggbb, the brand's primary color), logo_url (optional: a direct link to the brand logo or icon image if you saw one; Cogito also discovers icons on the website itself).

# How to work
1. Find the official API docs: authentication, base URL, rate limits, quotas, concurrency limits, error format and error codes, and any account or usage endpoint. Prefer official docs and official SDK source over blogs.
2. Read the actual error format. Rules must match what the API really returns; quote real error strings in body_contains and tests.
3. Draft the provider, call validate_spec, fix every problem.
4. If a live_check tool is available, use it once the auth and health check are drafted.
5. Call submit_provider with a short summary that names your sources.

# Hard rules
- Base every rule on documentation or observed responses. Never invent error strings; if the docs do not say, rely on the standard rules and say so in the summary.
- Never put an API key, token or secret in the spec, examples or tests.
- The health check must not spend quota. If unsure, omit it.
- Do not mark an example safe if it costs money or changes data.
- Keep the spec compact: no duplicate rules, no rules the standard rules already cover unless the body text distinguishes outcomes.
- Your text between tool calls should be one short progress sentence. The operator watches a live log.`

func examplesBlock() string {
	var b strings.Builder
	b.WriteString("# Worked examples: Cogito's built-in providers\n")
	for _, bi := range providers.Builtins() {
		if bi.Slug == "generic" {
			continue
		}
		p := map[string]any{"slug": bi.Slug, "name": bi.Name, "category": bi.Category, "description": bi.Description,
			"website": bi.Website, "docs_url": bi.DocsURL, "color": bi.Color, "spec": bi.Spec}
		j, _ := json.MarshalIndent(p, "", "  ")
		fmt.Fprintf(&b, "\n## %s\n```json\n%s\n```\n", bi.Name, j)
	}
	return b.String()
}

func (a *Agent) rulesBlock(ctx context.Context) string {
	r := strings.TrimSpace(a.GetRules(ctx).Rules)
	if r == "" {
		return ""
	}
	return "# Operator rules\nThe operator of this Cogito instance set these rules. Follow them unless they conflict with the hard rules above.\n\n" + r
}

func (a *Agent) systemPrompt(ctx context.Context) ([]anthropic.BetaTextBlockParam, error) {
	blocks := []anthropic.BetaTextBlockParam{{
		Text:         specGuide + "\n\n" + examplesBlock(),
		CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
	}}
	if r := a.rulesBlock(ctx); r != "" {
		blocks = append(blocks, anthropic.BetaTextBlockParam{Text: r})
	}
	return blocks, nil
}

func (a *Agent) taskPrompt(ctx context.Context, s *runState) (string, error) {
	in := s.run.Input
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\n", time.Now().UTC().Format("2006-01-02"))
	if s.run.Kind == "update_provider" {
		p, err := a.Store.GetProvider(ctx, s.run.ProviderSlug)
		if err != nil {
			return "", err
		}
		cur, _ := json.MarshalIndent(map[string]any{"slug": p.Slug, "name": p.Name, "category": p.Category, "description": p.Description,
			"website": p.Website, "docs_url": p.DocsURL, "color": p.Color, "spec": json.RawMessage(p.Spec)}, "", "  ")
		fmt.Fprintf(&b, "Task: improve the existing provider %q (keep slug %q).\n\nCurrent version %d:\n```json\n%s\n```\n\n", p.Name, p.Slug, p.Version, cur)
	} else {
		b.WriteString("Task: add a new provider to Cogito and fill in everything: metadata, brand color, logo URL if you find one, auth, defaults, rules, health check, examples and tests.\n\n")
		if ps, err := a.Store.ListProviders(ctx); err == nil {
			names := []string{}
			for _, p := range ps {
				names = append(names, p.Slug)
			}
			fmt.Fprintf(&b, "Existing provider slugs (do not reuse): %s\n\n", strings.Join(names, ", "))
		}
	}
	if in.Context != "" {
		fmt.Fprintf(&b, "Operator's notes:\n<notes>\n%s\n</notes>\n\n", in.Context)
	}
	if len(in.URLs) > 0 {
		b.WriteString("Documentation URLs to start from:\n")
		for _, u := range in.URLs {
			b.WriteString("- " + u + "\n")
		}
		b.WriteString("\n")
	}
	if in.GitHub != "" {
		fmt.Fprintf(&b, "GitHub repository to read: %s\n\n", in.GitHub)
	}
	if s.testKey != "" {
		b.WriteString("The operator supplied a test key. Use live_check to confirm auth placement and the health check before submitting.\n")
	} else {
		b.WriteString("No test key was supplied, so verify everything from the documentation.\n")
	}
	return b.String(), nil
}

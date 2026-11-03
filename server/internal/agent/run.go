package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/redact"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// draft is what the model submits: provider metadata plus spec, plus an
// optional logo URL it found while researching.
type draft struct {
	catalog.Input
	LogoURL string `json:"logo_url,omitempty"`
}

// runState is the per-run memory the tools share.
type runState struct {
	run       *store.AgentRun
	testKey   string
	submits   int
	accepted  *draft
	report    providers.TestReport
	live      *catalog.LiveReport
	summary   string
	usage     store.AgentUsage
	fixedSlug string
	// liveChecks counts live_check tool calls, which spend the operator's key.
	liveChecks int
	// pinNoted is set once the "nothing to pin to" note has been logged.
	pinNoted bool
	// unpinned is set when a draft was accepted with test keys but no URL
	// vouched for its base_url host. Such a run always waits for review.
	unpinned bool
}

const (
	betaFallback = "server-side-fallback-2026-07-01"
	betaUpdates  = "thinking-display-updates-2026-08-18"
)

func (a *Agent) execute(id int64) {
	a.sem <- struct{}{}
	defer func() { <-a.sem }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	a.cancels.Store(id, cancel)
	defer a.cancels.Delete(id)

	run, err := a.Store.GetRun(ctx, id)
	if err != nil || run.Status != "queued" {
		return
	}
	st := "running"
	_ = a.Store.UpdateRun(ctx, id, store.RunPatch{Status: &st})
	s := &runState{run: run}
	if run.TestKeyEnc != nil {
		s.testKey, _ = a.Box.Decrypt(run.TestKeyEnc)
	}
	// The test keys ride in the context from here on, so every model
	// request, step, run error and log line of this run is scrubbed of them.
	ctx = redact.WithSecrets(ctx, a.runSecrets(run, s.testKey)...)
	if run.Kind == "update_provider" {
		s.fixedSlug = run.ProviderSlug
	}
	defer func() {
		// The live check key goes when the agent stops. The full list stays
		// only while the run waits for review, for the service a publish
		// creates then.
		u := s.usage
		p := store.RunPatch{Usage: &u, ClearTestKey: true, ClearTestKeys: true}
		if cur, err := a.Store.GetRun(context.Background(), id); err == nil && cur.Status == "needs_review" {
			p.ClearTestKeys = false
		}
		_ = a.Store.UpdateRun(context.Background(), id, p)
	}()

	if err := a.loop(ctx, s); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return // cancelled by the user; Cancel already updated the run
		}
		a.fail(context.WithoutCancel(ctx), id, err.Error())
		return
	}
	a.finish(ctx, s)
}

func (a *Agent) loop(ctx context.Context, s *runState) error {
	id := s.run.ID
	system, err := a.systemPrompt(ctx)
	if err != nil {
		return err
	}
	task, err := a.taskPrompt(ctx, s)
	if err != nil {
		return err
	}
	// The run keeps the model it was queued with, even if the setting
	// changes while it works. Runs from before models were recorded use
	// the builder's current model.
	model := s.run.Model
	if model == "" {
		model = a.ModelFor(ctx, FeatureBuilder).Model
	}
	a.logf(ctx, id, "info", "Agent started", fmt.Sprintf("Model %s. Researching the API.", model))

	messages := []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(task))}
	tools := a.tools(s)

	for turn := 1; turn <= MaxTurns; turn++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.usage.Turns = turn
		params := anthropic.BetaMessageNewParams{
			Model:     anthropic.Model(model),
			MaxTokens: 16000,
			System:    system,
			Messages:  messages,
			Tools:     tools,
			Thinking: anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
				Display: anthropic.BetaThinkingConfigAdaptiveDisplay("updates"),
			}},
			OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortHigh},
			Fallbacks:    anthropic.BetaFallbacksParamOfDefault(),
			Betas:        []anthropic.AnthropicBeta{betaFallback, betaUpdates},
		}
		resp, err := a.client.Beta.Messages.New(ctx, params)
		if err != nil {
			return fmt.Errorf("AI request failed: %w", err)
		}
		cost := a.Record(ctx, store.AIFeatureBuilder, Caller{ID: s.run.CreatedByID, Email: s.run.CreatedBy}, runRef(id), resp)
		a.addUsage(s, resp.Usage, cost)
		u := s.usage
		_ = a.Store.UpdateRun(ctx, id, store.RunPatch{Usage: &u}) // live cost in the UI
		messages = append(messages, resp.ToParam())

		results := []anthropic.BetaContentBlockParamUnion{}
		done := false
		for _, block := range resp.Content {
			switch b := block.AsAny().(type) {
			case anthropic.BetaThinkingBlock:
				if t := strings.TrimSpace(b.Thinking); t != "" {
					a.logf(ctx, id, "thinking", firstLine(t), t)
				}
			case anthropic.BetaTextBlock:
				if t := strings.TrimSpace(b.Text); t != "" {
					a.logf(ctx, id, "thinking", firstLine(t), t)
				}
			case anthropic.BetaServerToolUseBlock:
				in, _ := json.Marshal(b.Input)
				var q struct {
					Query string `json:"query"`
					URL   string `json:"url"`
				}
				_ = json.Unmarshal(in, &q)
				switch {
				case b.Name == "web_fetch" && q.URL != "":
					a.logf(ctx, id, "fetch", "Reading "+q.URL, q.URL)
				case b.Name == "web_search" && q.Query != "":
					a.logf(ctx, id, "search", "Searching: "+q.Query, q.Query)
				}
				// Other server tool calls are the web tools' internal result filtering.
			case anthropic.BetaWebSearchToolResultBlock:
				a.logf(ctx, id, "result", "Search results", summarizeSearch(b))
			case anthropic.BetaWebFetchToolResultBlock:
				a.logf(ctx, id, "result", "Page read", summarizeFetch(b))
			case anthropic.BetaToolUseBlock:
				out, isErr, finished := a.runTool(ctx, s, b.Name, []byte(b.JSON.Input.Raw()))
				results = append(results, anthropic.NewBetaToolResultBlock(b.ID, out, isErr))
				if finished {
					done = true
				}
			}
		}
		if done {
			return nil
		}
		switch resp.StopReason {
		case anthropic.BetaStopReasonToolUse:
			messages = append(messages, anthropic.NewBetaUserMessage(results...))
		case anthropic.BetaStopReasonPauseTurn:
			// Server tools paused a long turn; send the conversation back to continue.
			if len(results) > 0 {
				messages = append(messages, anthropic.NewBetaUserMessage(results...))
			}
		case anthropic.BetaStopReasonRefusal:
			return fmt.Errorf("Claude declined this request (%s)", resp.StopDetails.Category)
		case anthropic.BetaStopReasonMaxTokens:
			// Every tool_use needs its tool_result in the next message, or the API rejects it.
			blocks := append(results, anthropic.NewBetaTextBlock(
				"Your last message hit the output limit. Continue, and keep tool inputs compact."))
			messages = append(messages, anthropic.NewBetaUserMessage(blocks...))
		default:
			if len(results) > 0 {
				messages = append(messages, anthropic.NewBetaUserMessage(results...))
				continue
			}
			messages = append(messages, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(
				"You have not submitted yet. Finish the research and call submit_provider, or explain in one sentence why this API cannot be supported.")))
		}
	}
	if s.accepted != nil {
		return nil
	}
	return fmt.Errorf("stopped after %d turns without an accepted provider", MaxTurns)
}

// addUsage adds one call to the run's running totals. The cost is the
// ledger's, priced in pricing.go, so a run and the Usage page agree.
func (a *Agent) addUsage(s *runState, u anthropic.BetaUsage, cost float64) {
	s.usage.InputTokens += u.InputTokens + u.CacheCreationInputTokens
	s.usage.CacheRead += u.CacheReadInputTokens
	s.usage.OutputTokens += u.OutputTokens
	s.usage.WebSearches += u.ServerToolUse.WebSearchRequests
	s.usage.CostUSD += cost
}

// finish publishes (or parks for review) the accepted draft.
func (a *Agent) finish(ctx context.Context, s *runState) {
	id := s.run.ID
	d := s.accepted
	if d == nil {
		a.fail(ctx, id, "the agent finished without an accepted provider")
		return
	}
	logo, mime := a.findLogo(ctx, id, d)
	summary := s.summary
	_ = a.Store.UpdateRun(ctx, id, store.RunPatch{Draft: d, Report: s.report, Live: s.live, Summary: &summary, ProviderSlug: &d.Slug})
	if !s.run.Input.AutoPublish || s.unpinned {
		st := "needs_review"
		_ = a.Store.UpdateRun(ctx, id, store.RunPatch{Status: &st, Finished: true})
		a.storePendingLogo(id, pendingLogo{logo, mime})
		msg := "All checks passed. Review the draft and publish it."
		if s.unpinned {
			// Publishing creates a service at base_url with the test keys,
			// so a host nobody vouched for needs a person to confirm it.
			host := "its base_url"
			if spec, err := providers.DecodeSpec(d.Spec); err == nil {
				if h := URLHost(spec.BaseURL); h != "" {
					host = h
				}
			}
			msg = "No docs URL said where this API lives, so the test keys were not sent. Confirm that " + host +
				" is the provider's own API host before publishing: the keys go there."
		}
		a.logf(ctx, id, "done", "Ready for review", msg)
		return
	}
	if err := a.publish(ctx, s.run, d, "ai:"+s.run.CreatedBy, logo, mime, s.run.Input.CreateService); err != nil {
		a.fail(ctx, id, "publish: "+err.Error())
	}
}

type pendingLogo struct {
	data []byte
	mime string
}

// findLogo downloads the logo the model pointed at, or discovers one on the website.
func (a *Agent) findLogo(ctx context.Context, id int64, d *draft) ([]byte, string) {
	lctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	if d.LogoURL != "" {
		if data, mime, err := catalog.DownloadImage(lctx, nil, d.LogoURL); err == nil {
			a.logf(ctx, id, "result", "Logo downloaded", d.LogoURL)
			return data, mime
		}
	}
	site := d.Website
	if site == "" {
		site = d.DocsURL
	}
	if site == "" {
		return nil, ""
	}
	data, mime, from, err := catalog.FetchLogo(lctx, site)
	if err != nil {
		a.logf(ctx, id, "info", "No logo found", err.Error()+". A lettermark will be used; upload one any time.")
		return nil, ""
	}
	a.logf(ctx, id, "result", "Logo found", from)
	return data, mime
}

func (a *Agent) publish(ctx context.Context, run *store.AgentRun, d *draft, by string, logo []byte, mime string, createService bool) error {
	id := run.ID
	if run.Kind == "create_provider" {
		// A create run must never overwrite a provider, including one added
		// after the draft was submitted.
		slug := strings.TrimSpace(strings.ToLower(d.Slug))
		if p, err := a.Store.GetProvider(ctx, slug); err == nil && p.Slug != "" {
			return fmt.Errorf("slug %q is already taken by %s; this run only creates new providers", slug, p.Name)
		}
	}
	if logo == nil {
		if pl, ok := a.pendingLogos.LoadAndDelete(id); ok {
			logo, mime = pl.(pendingLogo).data, pl.(pendingLogo).mime
		} else {
			logo, mime = a.findLogo(ctx, id, d)
		}
	}
	note := "built by AI"
	if run.Kind == "update_provider" {
		note = "improved by AI"
	}
	p, rep, err := a.Catalog.Publish(ctx, d.Input, "ai", catalog.Publisher{Name: by}, note+" (run "+fmt.Sprint(id)+")")
	if err != nil {
		return err
	}
	if logo != nil {
		_ = a.Store.SetProviderLogo(ctx, p.Slug, logo, mime)
	}
	a.logf(ctx, id, "done", fmt.Sprintf("Published %s v%d", p.Name, p.Version),
		fmt.Sprintf("%d/%d fixture tests passed. Live now, no restart needed.", rep.Passed, rep.Passed+rep.Failed))
	patch := store.RunPatch{Finished: true, ClearTestKeys: true}
	st := "published"
	patch.Status = &st
	if createService {
		sv, err := catalog.ServiceDefaults(p)
		if err == nil {
			sv.Slug = catalog.UniqueServiceSlug(ctx, a.Store, sv.Slug)
			if created, err := a.Store.CreateService(ctx, sv); err == nil {
				patch.ServiceID = &created.ID
				a.logf(ctx, id, "done", "Added "+created.Name+" to the catalog", "Service /"+created.Slug+" is ready for keys.")
				a.Store.AddAudit(ctx, nil, "service.create", created.Slug, "by AI run "+fmt.Sprint(id))
				a.addTestKeys(ctx, run, created)
			} else {
				a.logf(ctx, id, "error", "Could not create the service", err.Error())
			}
		}
	}
	a.Store.AddAudit(ctx, nil, "provider.publish", p.Slug, fmt.Sprintf("v%d by AI run %d for %s", p.Version, id, run.CreatedBy))
	return a.Store.UpdateRun(ctx, id, patch)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n"); i > 0 {
		s = s[:i]
	}
	if len(s) > 140 {
		s = store.TruncateUTF8(s, 140) + "..."
	}
	return s
}

func summarizeSearch(b anthropic.BetaWebSearchToolResultBlock) string {
	raw := b.JSON.Content.Raw()
	var results []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	if json.Unmarshal([]byte(raw), &results) != nil {
		return "search failed: " + truncate(raw, 300)
	}
	lines := []string{}
	for _, r := range results {
		lines = append(lines, r.Title+"\n"+r.URL)
	}
	return strings.Join(lines, "\n\n")
}

func summarizeFetch(b anthropic.BetaWebFetchToolResultBlock) string {
	var c struct {
		URL       string `json:"url"`
		ErrorCode string `json:"error_code"`
	}
	_ = json.Unmarshal([]byte(b.JSON.Content.Raw()), &c)
	if c.ErrorCode != "" {
		return "fetch failed: " + c.ErrorCode
	}
	return c.URL
}

func truncate(s string, n int) string {
	if len(s) > n {
		return store.TruncateUTF8(s, n) + "..."
	}
	return s
}

// addTestKeys stores the run's test keys in the service its publish created
// and logs how many, never the secrets. The caller wipes them from the run.
func (a *Agent) addTestKeys(ctx context.Context, run *store.AgentRun, sv *store.Service) {
	if len(run.TestKeysEnc) == 0 {
		return
	}
	plain, err := a.Box.Decrypt(run.TestKeysEnc)
	var keys []string
	if err == nil {
		err = json.Unmarshal([]byte(plain), &keys)
	}
	if err != nil {
		a.logf(ctx, run.ID, "error", "Could not add the test keys", "they could not be decrypted")
		return
	}
	nks := make([]store.NewKey, 0, len(keys))
	for _, k := range keys {
		enc, err := a.Box.Encrypt(k)
		if err != nil {
			a.logf(ctx, run.ID, "error", "Could not add the test keys", err.Error())
			return
		}
		hint := crypto.Hint(k)
		nks = append(nks, store.NewKey{ServiceID: sv.ID, SecretEnc: enc, SecretFP: a.Box.Fingerprint(k), Hint: hint,
			Label: fmt.Sprintf("%s key %s", sv.Name, hint[3:]), Tier: "primary", Weight: 1, Priority: 100, CreatedBy: run.CreatedByID})
	}
	created, dups, err := a.Store.CreateKeys(ctx, nks)
	if err != nil {
		a.logf(ctx, run.ID, "error", "Could not add the test keys", err.Error())
		return
	}
	detail := fmt.Sprintf("%d added to /%s.", len(created), sv.Slug)
	if len(dups) > 0 {
		detail += fmt.Sprintf(" %d were already there.", len(dups))
	}
	a.logf(ctx, run.ID, "done", fmt.Sprintf("Added %d %s to %s", len(created), pluralKeys(len(created)), sv.Name), detail)
	a.Store.AddAudit(ctx, nil, "key.create", sv.Slug, fmt.Sprintf("%d added, %d skipped, by AI run %d", len(created), len(dups), run.ID))
}

func pluralKeys(n int) string {
	if n == 1 {
		return "key"
	}
	return "keys"
}

// runSecrets is the live check key and every other test key of a run.
func (a *Agent) runSecrets(run *store.AgentRun, testKey string) []string {
	out := []string{testKey}
	if len(run.TestKeysEnc) > 0 {
		if plain, err := a.Box.Decrypt(run.TestKeysEnc); err == nil {
			var keys []string
			if json.Unmarshal([]byte(plain), &keys) == nil {
				out = append(out, keys...)
			}
		}
	}
	return out
}

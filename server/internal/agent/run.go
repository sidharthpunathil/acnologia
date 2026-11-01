package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/cogito-keys/cogito/internal/catalog"
	"github.com/cogito-keys/cogito/internal/providers"
	"github.com/cogito-keys/cogito/internal/store"
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
	if run.Kind == "update_provider" {
		s.fixedSlug = run.ProviderSlug
	}
	defer func() {
		u := s.usage
		_ = a.Store.UpdateRun(context.Background(), id, store.RunPatch{Usage: &u, ClearTestKey: true})
	}()

	if err := a.loop(ctx, s); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return // cancelled by the user; Cancel already updated the run
		}
		a.fail(context.Background(), id, err.Error())
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
	a.logf(ctx, id, "info", "Agent started", fmt.Sprintf("Model %s. Researching the API.", a.model))

	messages := []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(task))}
	tools := a.tools(s)

	for turn := 1; turn <= MaxTurns; turn++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.usage.Turns = turn
		params := anthropic.BetaMessageNewParams{
			Model:     anthropic.Model(a.model),
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
		a.addUsage(s, resp.Usage)
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

func (a *Agent) addUsage(s *runState, u anthropic.BetaUsage) {
	s.usage.InputTokens += u.InputTokens + u.CacheCreationInputTokens
	s.usage.CacheRead += u.CacheReadInputTokens
	s.usage.OutputTokens += u.OutputTokens
	s.usage.WebSearches += u.ServerToolUse.WebSearchRequests
	// Claude Opus 5.5 list prices: $4 input, $20 output, $0.20 cache reads per MTok; web search $10 per 1,000.
	s.usage.CostUSD = float64(s.usage.InputTokens)*4/1e6 + float64(s.usage.OutputTokens)*20/1e6 +
		float64(s.usage.CacheRead)*0.2/1e6 + float64(s.usage.WebSearches)*0.01
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
	if !s.run.Input.AutoPublish {
		st := "needs_review"
		_ = a.Store.UpdateRun(ctx, id, store.RunPatch{Status: &st, Finished: true})
		a.storePendingLogo(id, pendingLogo{logo, mime})
		a.logf(ctx, id, "done", "Ready for review", "All checks passed. Review the draft and publish it.")
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
	p, rep, err := a.Catalog.Publish(ctx, d.Input, "ai", by, note+" (run "+fmt.Sprint(id)+")")
	if err != nil {
		return err
	}
	if logo != nil {
		_ = a.Store.SetProviderLogo(ctx, p.Slug, logo, mime)
	}
	a.logf(ctx, id, "done", fmt.Sprintf("Published %s v%d", p.Name, p.Version),
		fmt.Sprintf("%d/%d fixture tests passed. Live now, no restart needed.", rep.Passed, rep.Passed+rep.Failed))
	patch := store.RunPatch{Finished: true}
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

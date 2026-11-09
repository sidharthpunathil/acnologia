// Package agent is Acnologia's AI harness. It researches an API with Claude,
// writes a provider spec, tests it with the real engine, fetches a logo, and
// publishes it without restarting anything. It also powers "Fill with AI".
//
// Guardrails live in code, not only in the prompt: specs are data and cannot
// execute; every submit is re-validated and fixture tested by the harness;
// live checks use a safe HTTP client; test keys are encrypted and wiped;
// runs are capped in turns and searches.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/redact"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const (
	MaxTurns       = 60
	MaxSubmits     = 3
	MaxWebSearches = 15
	MaxWebFetches  = 30
	maxConcurrent  = 2
	// MaxLiveChecks caps live_check calls per run; each one spends the operator's key.
	MaxLiveChecks = 5
	// MaxTestKeys caps the keys one run carries.
	MaxTestKeys = 5000
)

type Config struct {
	APIKey string
	// Model is ACNOLOGIA_AI_MODEL, empty when it is not set. The model saved
	// on the Settings page wins over it (models.go).
	Model       string
	GitHubToken string
	// BaseURL overrides the Anthropic API endpoint; tests point it at a fake.
	BaseURL string
}

type Agent struct {
	Store   *store.Store
	Catalog *catalog.Catalog
	Box     *crypto.Box

	client anthropic.Client
	// envModel is ACNOLOGIA_AI_MODEL; models holds the saved model settings.
	envModel string
	models   modelState
	// prices holds the saved price overrides (prices.go).
	prices  priceState
	enabled bool
	github  string
	sem     chan struct{}
	cancels sync.Map // run id -> context.CancelFunc
	// pendingLogos holds logos for runs waiting for review (not persisted).
	pendingLogos sync.Map
}

func New(cfg Config, st *store.Store, cat *catalog.Catalog, box *crypto.Box) *Agent {
	a := &Agent{Store: st, Catalog: cat, Box: box, envModel: strings.TrimSpace(cfg.Model), github: cfg.GitHubToken, sem: make(chan struct{}, maxConcurrent)}
	if strings.TrimSpace(cfg.APIKey) != "" {
		// Every request to Anthropic, from the builder, the Assistant, Fill
		// with AI and run naming, passes through redactRequest, so a new
		// caller or tool cannot forget to scrub what it sends.
		opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey), option.WithMaxRetries(3),
			option.WithMiddleware(a.redactRequest)}
		if cfg.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(cfg.BaseURL))
		}
		a.client = anthropic.NewClient(opts...)
		a.enabled = true
	}
	return a
}

type Status struct {
	Enabled bool `json:"enabled"`
	// Model is the effective default model; Models is the effective model of
	// each feature (assistant, builder, fill, summary).
	Model  string            `json:"model"`
	Models map[string]string `json:"models"`
	Reason string            `json:"reason"`
}

func (a *Agent) Status() Status {
	ctx := context.Background()
	st := Status{Enabled: a.enabled, Model: a.ModelFor(ctx, "").Model, Models: map[string]string{}}
	for _, f := range ModelFeatures {
		st.Models[f] = a.ModelFor(ctx, f).Model
	}
	if !a.enabled {
		st.Reason = "Set ANTHROPIC_API_KEY in .env (or the environment) and restart Acnologia."
	}
	return st
}

var ErrDisabled = errors.New("AI is not configured. Set ANTHROPIC_API_KEY and restart Acnologia.")

// Boot marks runs interrupted by a restart as failed.
func (a *Agent) Boot(ctx context.Context) error { return a.Store.FailStaleRuns(ctx) }

// ---- rules ----

type Rules struct {
	Rules     string     `json:"rules"`
	UpdatedAt *time.Time `json:"updated_at"`
	UpdatedBy string     `json:"updated_by"`
}

func (a *Agent) GetRules(ctx context.Context) Rules {
	r := Rules{Rules: a.Store.GetMeta(ctx, "ai_rules"), UpdatedBy: a.Store.GetMeta(ctx, "ai_rules_by")}
	if t, err := time.Parse(time.RFC3339, a.Store.GetMeta(ctx, "ai_rules_at")); err == nil {
		r.UpdatedAt = &t
	}
	return r
}

func (a *Agent) SetRules(ctx context.Context, rules, by string) (Rules, error) {
	if len(rules) > 20000 {
		return Rules{}, errors.New("rules must be under 20,000 characters")
	}
	for k, v := range map[string]string{"ai_rules": rules, "ai_rules_by": by, "ai_rules_at": time.Now().UTC().Format(time.RFC3339)} {
		if err := a.Store.SetMeta(ctx, k, v); err != nil {
			return Rules{}, err
		}
	}
	return a.GetRules(ctx), nil
}

// ---- runs ----

type RunRequest struct {
	Kind         string   `json:"kind"`
	ProviderSlug string   `json:"provider_slug"`
	Context      string   `json:"context"`
	URLs         []string `json:"urls"`
	GitHub       string   `json:"github"`
	TestKey      string   `json:"test_key"`
	// TestKeys are more keys for the run. The first of test_key and
	// test_keys runs the live check; all are added to the service a publish
	// creates. At most MaxTestKeys.
	TestKeys      []string `json:"test_keys"`
	AutoPublish   *bool    `json:"auto_publish"`
	CreateService *bool    `json:"create_service"`
}

func (a *Agent) Submit(ctx context.Context, u *store.User, req RunRequest) (*store.AgentRun, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	// Notes are free text a key can be pasted into. They are stored, shown
	// and sent to Claude, so keys (the run's own and anything key shaped)
	// are replaced by their hint first.
	req.Context = strings.TrimSpace(a.Store.Scrub(redact.WithSecrets(ctx, testKeys(req)...), req.Context))
	urls := []string{}
	for _, x := range req.URLs {
		if x = strings.TrimSpace(x); x != "" {
			urls = append(urls, x)
		}
	}
	switch req.Kind {
	case "create_provider":
		if req.Context == "" && len(urls) == 0 && req.GitHub == "" {
			return nil, errors.New("give the AI something to start from: a name, a docs URL or a GitHub repo")
		}
	case "update_provider":
		if _, err := a.Store.GetProvider(ctx, req.ProviderSlug); err != nil {
			return nil, fmt.Errorf("no provider named %q", req.ProviderSlug)
		}
		if req.Context == "" {
			req.Context = "Review this provider against its current documentation and fix anything wrong or missing."
		}
	default:
		return nil, errors.New("kind must be create_provider or update_provider")
	}
	// Updates change a provider that may already be in use, so they wait for
	// review unless the operator asks for auto publish explicitly.
	autoPublish := req.Kind == "create_provider"
	if req.AutoPublish != nil {
		autoPublish = *req.AutoPublish
	}
	keys := testKeys(req)
	if len(keys) > MaxTestKeys {
		return nil, fmt.Errorf("send at most %d test keys", MaxTestKeys)
	}
	in := store.AgentInput{Context: req.Context, URLs: urls, GitHub: strings.TrimSpace(req.GitHub),
		AutoPublish: autoPublish, CreateService: req.CreateService == nil || *req.CreateService,
		HasTestKey: len(keys) > 0, TestKeyCount: len(keys)}
	if req.Kind == "update_provider" {
		in.CreateService = false
	}
	run := &store.AgentRun{Kind: req.Kind, Status: "queued", ProviderSlug: req.ProviderSlug, Input: in,
		Title: a.fallbackTitle(ctx, req), CreatedByID: u.ID, CreatedBy: u.Email, Model: a.ModelFor(ctx, FeatureBuilder).Model}
	if len(keys) > 0 {
		enc, err := a.Box.Encrypt(keys[0])
		if err != nil {
			return nil, err
		}
		run.TestKeyEnc = enc
		all, _ := json.Marshal(keys)
		if run.TestKeysEnc, err = a.Box.Encrypt(string(all)); err != nil {
			return nil, err
		}
	}
	created, err := a.Store.CreateRun(ctx, run)
	if err != nil {
		return nil, err
	}
	a.Store.AddStep(ctx, created.ID, "info", "Queued", "Waiting for a free agent slot.")
	go a.nameRun(created.ID, CallerOf(u), req, created.Title)
	go a.execute(created.ID)
	return created, nil
}

// testKeys is test_key then test_keys, trimmed, without blanks or repeats.
func testKeys(req RunRequest) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, k := range append([]string{req.TestKey}, req.TestKeys...) {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func (a *Agent) Cancel(ctx context.Context, id int64) error {
	r, err := a.Store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if r.Status != "queued" && r.Status != "running" && r.Status != "testing" {
		return errors.New("run is not in progress")
	}
	if c, ok := a.cancels.Load(id); ok {
		c.(context.CancelFunc)()
	}
	a.pendingLogos.Delete(id)
	st := "cancelled"
	return a.Store.UpdateRun(ctx, id, store.RunPatch{Status: &st, Finished: true, ClearTestKey: true, ClearTestKeys: true})
}

// PublishDraft publishes a run that is waiting for review.
func (a *Agent) PublishDraft(ctx context.Context, id int64, by string) (*store.AgentRun, error) {
	r, err := a.Store.GetRun(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.Status != "needs_review" {
		return nil, errors.New("only runs waiting for review can be published")
	}
	var d draft
	if err := json.Unmarshal(r.Draft, &d); err != nil {
		return nil, errors.New("run has no usable draft")
	}
	// Claim the run so two concurrent publish calls cannot both publish it.
	ok, err := a.Store.ClaimRun(ctx, id, "needs_review", "publishing")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("only runs waiting for review can be published")
	}
	createService := r.Input.CreateService && r.Kind == "create_provider"
	if err := a.publish(ctx, r, &d, by, nil, "", createService); err != nil {
		_, _ = a.Store.ClaimRun(context.Background(), id, "publishing", "needs_review")
		return nil, err
	}
	return a.Store.GetRun(ctx, id)
}

func (a *Agent) logf(ctx context.Context, id int64, typ, title, detail string) {
	a.Store.AddStep(ctx, id, typ, title, detail)
}

// maxPendingLogos bounds the logos kept in memory for runs awaiting review.
const maxPendingLogos = 50

func (a *Agent) storePendingLogo(id int64, pl pendingLogo) {
	n := 0
	a.pendingLogos.Range(func(k, _ any) bool {
		n++
		if n >= maxPendingLogos {
			a.pendingLogos.Delete(k)
		}
		return true
	})
	a.pendingLogos.Store(id, pl)
}

func (a *Agent) fail(ctx context.Context, id int64, msg string) {
	msg = a.Store.Scrub(ctx, msg)
	a.pendingLogos.Delete(id)
	st := "failed"
	_ = a.Store.UpdateRun(ctx, id, store.RunPatch{Status: &st, Error: &msg, Finished: true, ClearTestKey: true, ClearTestKeys: true})
	a.logf(ctx, id, "error", "Run failed", msg)
	slog.Warn("agent run failed", "run", id, "err", msg)
}

// redactRequest is the model boundary. It rewrites the body of every
// Messages API request: known secrets (pool keys, plus the extras in the
// request context, such as a run's test keys) and key shaped strings become
// hint placeholders before anything leaves for Anthropic.
func (a *Agent) redactRequest(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return next(req)
	}
	b, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	ctx := redact.WithSecrets(req.Context(), a.github)
	b = a.Store.Redactor().MessagesRequest(ctx, b)
	req.Body = io.NopCloser(bytes.NewReader(b))
	req.ContentLength = int64(len(b))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
	return next(req)
}

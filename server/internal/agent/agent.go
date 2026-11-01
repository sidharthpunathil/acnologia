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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/cogito-keys/cogito/internal/catalog"
	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/store"
)

const (
	MaxTurns       = 40
	MaxSubmits     = 3
	MaxWebSearches = 15
	MaxWebFetches  = 30
	maxConcurrent  = 2
	// MaxLiveChecks caps live_check calls per run; each one spends the operator's key.
	MaxLiveChecks = 5
)

type Config struct {
	APIKey      string
	Model       string
	GitHubToken string
}

type Agent struct {
	Store   *store.Store
	Catalog *catalog.Catalog
	Box     *crypto.Box

	client  anthropic.Client
	model   string
	enabled bool
	github  string
	sem     chan struct{}
	cancels sync.Map // run id -> context.CancelFunc
	// pendingLogos holds logos for runs waiting for review (not persisted).
	pendingLogos sync.Map
}

func New(cfg Config, st *store.Store, cat *catalog.Catalog, box *crypto.Box) *Agent {
	a := &Agent{Store: st, Catalog: cat, Box: box, model: cfg.Model, github: cfg.GitHubToken, sem: make(chan struct{}, maxConcurrent)}
	if a.model == "" {
		a.model = "claude-opus-5-5"
	}
	if strings.TrimSpace(cfg.APIKey) != "" {
		a.client = anthropic.NewClient(option.WithAPIKey(cfg.APIKey), option.WithMaxRetries(3))
		a.enabled = true
	}
	return a
}

type Status struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model"`
	Reason  string `json:"reason"`
}

func (a *Agent) Status() Status {
	if !a.enabled {
		return Status{Model: a.model, Reason: "Set ANTHROPIC_API_KEY in .env (or the environment) and restart Acnologia."}
	}
	return Status{Enabled: true, Model: a.model}
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
	Kind          string   `json:"kind"`
	ProviderSlug  string   `json:"provider_slug"`
	Context       string   `json:"context"`
	URLs          []string `json:"urls"`
	GitHub        string   `json:"github"`
	TestKey       string   `json:"test_key"`
	AutoPublish   *bool    `json:"auto_publish"`
	CreateService *bool    `json:"create_service"`
}

func (a *Agent) Submit(ctx context.Context, u *store.User, req RunRequest) (*store.AgentRun, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	req.Context = strings.TrimSpace(req.Context)
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
	in := store.AgentInput{Context: req.Context, URLs: urls, GitHub: strings.TrimSpace(req.GitHub),
		AutoPublish: autoPublish, CreateService: req.CreateService == nil || *req.CreateService,
		HasTestKey: strings.TrimSpace(req.TestKey) != ""}
	if req.Kind == "update_provider" {
		in.CreateService = false
	}
	run := &store.AgentRun{Kind: req.Kind, Status: "queued", ProviderSlug: req.ProviderSlug, Input: in,
		Title: runTitle(req), CreatedByID: u.ID, CreatedBy: u.Email}
	if in.HasTestKey {
		enc, err := a.Box.Encrypt(strings.TrimSpace(req.TestKey))
		if err != nil {
			return nil, err
		}
		run.TestKeyEnc = enc
	}
	created, err := a.Store.CreateRun(ctx, run)
	if err != nil {
		return nil, err
	}
	a.Store.AddStep(ctx, created.ID, "info", "Queued", "Waiting for a free agent slot.")
	go a.execute(created.ID)
	return created, nil
}

func runTitle(req RunRequest) string {
	if req.Kind == "update_provider" {
		return "Improve " + req.ProviderSlug
	}
	t := req.Context
	if t == "" && len(req.URLs) > 0 {
		t = req.URLs[0]
	}
	if t == "" {
		t = req.GitHub
	}
	t = strings.Join(strings.Fields(t), " ")
	if len(t) > 80 {
		t = store.TruncateUTF8(t, 80) + "..."
	}
	return "New provider: " + t
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
	return a.Store.UpdateRun(ctx, id, store.RunPatch{Status: &st, Finished: true, ClearTestKey: true})
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
	a.pendingLogos.Delete(id)
	st := "failed"
	_ = a.Store.UpdateRun(ctx, id, store.RunPatch{Status: &st, Error: &msg, Finished: true, ClearTestKey: true})
	a.logf(ctx, id, "error", "Run failed", msg)
	slog.Warn("agent run failed", "run", id, "err", msg)
}

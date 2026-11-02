// Package catalog owns providers: seeding built-ins, validating, testing,
// versioning and hot-loading specs into the engine registry. Every change,
// manual or AI made, goes through Publish.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type Catalog struct {
	Store *store.Store
}

// Input is a provider as edited by a person or an agent.
type Input struct {
	Slug        string          `json:"slug"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Website     string          `json:"website"`
	DocsURL     string          `json:"docs_url"`
	Color       string          `json:"color"`
	Status      string          `json:"status"`
	Spec        json.RawMessage `json:"spec"`
}

// ValidationError carries every problem plus the test report.
type ValidationError struct {
	Errors []string
	Report providers.TestReport
}

func (e *ValidationError) Error() string {
	if len(e.Errors) > 0 {
		return strings.Join(e.Errors, "; ")
	}
	return fmt.Sprintf("%d fixture test(s) failed", e.Report.Failed)
}

var (
	slugRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// Check validates an input without saving it. It returns the parsed spec, the
// problems found, and the fixture test report.
func Check(in *Input) (*providers.Spec, []string, providers.TestReport) {
	var errs []string
	in.Slug = strings.TrimSpace(strings.ToLower(in.Slug))
	in.Name = strings.TrimSpace(in.Name)
	if !slugRe.MatchString(in.Slug) {
		errs = append(errs, "slug must be lowercase letters, digits and hyphens (max 48)")
	}
	if in.Name == "" {
		errs = append(errs, "name is required")
	}
	if in.Color != "" && !colorRe.MatchString(in.Color) {
		errs = append(errs, "color must look like #1a73e8")
	}
	in.Website, in.DocsURL = strings.TrimSpace(in.Website), strings.TrimSpace(in.DocsURL)
	if in.Website != "" && !httpURL(in.Website) {
		errs = append(errs, "website must be an http(s) URL")
	}
	if in.DocsURL != "" && !httpURL(in.DocsURL) {
		errs = append(errs, "docs_url must be an http(s) URL")
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Status != "active" && in.Status != "disabled" {
		errs = append(errs, "status must be active or disabled")
	}
	if len(in.Spec) == 0 || string(in.Spec) == "null" {
		return nil, append(errs, "spec is required"), providers.TestReport{Results: []providers.TestResult{}}
	}
	spec, err := providers.DecodeSpec(in.Spec)
	if err != nil {
		return nil, append(errs, "spec is not valid JSON for a provider: "+err.Error()), providers.TestReport{Results: []providers.TestResult{}}
	}
	errs = append(errs, spec.Validate()...)
	if len(spec.Tests) == 0 {
		errs = append(errs, "add at least one fixture test so changes can be verified")
	}
	rep := spec.RunTests()
	// Store the normalized form.
	in.Spec, _ = json.Marshal(spec)
	return spec, errs, rep
}

// httpURL reports whether s is an absolute http(s) URL with a host. Links
// such as javascript: would run in the dashboard when clicked.
func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// Publish validates, tests, saves a new version and swaps the live spec.
// Requests already in flight keep the spec they started with.
func (c *Catalog) Publish(ctx context.Context, in Input, source, by, note string) (*store.Provider, providers.TestReport, error) {
	spec, errs, rep := Check(&in)
	if len(errs) > 0 || rep.Failed > 0 {
		return nil, rep, &ValidationError{Errors: errs, Report: rep}
	}
	existing, err := c.Store.GetProvider(ctx, in.Slug)
	builtin := false
	if err == nil {
		builtin = existing.Builtin
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, rep, err
	}
	p := &store.Provider{
		Slug: in.Slug, Name: in.Name, Category: in.Category, Description: in.Description, Website: in.Website,
		DocsURL: in.DocsURL, Color: in.Color, Status: in.Status, Builtin: builtin, Source: source, Spec: in.Spec,
	}
	saved, err := c.Store.SaveProvider(ctx, p, by, note)
	if err != nil {
		return nil, rep, err
	}
	providers.Set(saved.Slug, spec)
	slog.Info("provider published", "slug", saved.Slug, "version", saved.Version, "source", source)
	return saved, rep, nil
}

// Rollback republishes an older version as a new version.
func (c *Catalog) Rollback(ctx context.Context, slug string, version int, by string) (*store.Provider, error) {
	old, err := c.Store.ProviderVersionSnapshot(ctx, slug, version)
	if err != nil {
		return nil, err
	}
	cur, err := c.Store.GetProvider(ctx, slug)
	if err != nil {
		return nil, err
	}
	in := Input{Slug: slug, Name: old.Name, Category: old.Category, Description: old.Description, Website: old.Website,
		DocsURL: old.DocsURL, Color: old.Color, Status: old.Status, Spec: old.Spec}
	p, _, err := c.Publish(ctx, in, cur.Source, by, fmt.Sprintf("rollback to v%d", version))
	return p, err
}

// Delete removes a provider that no service uses.
func (c *Catalog) Delete(ctx context.Context, slug string) error {
	p, err := c.Store.GetProvider(ctx, slug)
	if err != nil {
		return err
	}
	if p.Builtin {
		return errors.New("built-in providers can be disabled but not deleted")
	}
	if p.ServicesCount > 0 {
		return fmt.Errorf("%d service(s) still use this provider", p.ServicesCount)
	}
	if err := c.Store.DeleteProvider(ctx, slug); err != nil {
		return err
	}
	providers.Remove(slug)
	return nil
}

// Boot seeds built-in providers on first run and loads every spec.
func (c *Catalog) Boot(ctx context.Context) error {
	for _, b := range providers.Builtins() {
		if _, err := c.Store.GetProvider(ctx, b.Slug); err == nil {
			// Already seeded: only fill in a logo that is still missing, so
			// existing installs get logos added in newer releases.
			if _, _, lerr := c.Store.ProviderLogo(ctx, b.Slug); errors.Is(lerr, store.ErrNotFound) {
				if data, mime := providers.SeedLogo(b.Slug); data != nil {
					_ = c.Store.SetProviderLogo(ctx, b.Slug, data, mime)
				}
			}
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		spec, _ := json.Marshal(b.Spec)
		p := &store.Provider{Slug: b.Slug, Name: b.Name, Category: b.Category, Description: b.Description, Website: b.Website,
			DocsURL: b.DocsURL, Color: b.Color, Status: "active", Builtin: true, Source: "builtin", Spec: spec}
		if _, err := c.Store.SaveProvider(ctx, p, "system", "built-in"); err != nil {
			return err
		}
		logo, mime := b.Logo, b.LogoMime
		if logo == nil {
			logo, mime = providers.SeedLogo(b.Slug)
		}
		if logo != nil {
			if err := c.Store.SetProviderLogo(ctx, b.Slug, logo, mime); err != nil {
				return err
			}
		}
	}
	return c.LoadAll(ctx)
}

func (c *Catalog) LoadAll(ctx context.Context) error {
	ps, err := c.Store.ListProviders(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		spec, err := providers.DecodeSpec(p.Spec)
		if err != nil {
			slog.Error("provider spec unreadable, using standard rules", "slug", p.Slug, "err", err)
			continue
		}
		providers.Set(p.Slug, spec)
	}
	return nil
}

// ServiceDefaults builds a new service from a provider's spec.
func ServiceDefaults(p *store.Provider) (*store.Service, error) {
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil {
		return nil, err
	}
	return &store.Service{
		Slug: p.Slug, Name: p.Name, Provider: p.Slug, Category: p.Category, Description: p.Description,
		BaseURL: spec.BaseURL, AuthType: spec.Auth.Type, AuthName: spec.Auth.Name, Strategy: spec.Defaults.Strategy,
		CooldownSec: max(spec.Defaults.CooldownSec, 1), MaxAttempts: max(spec.Defaults.MaxAttempts, 1),
		MaxConcurrency: spec.Defaults.MaxConcurrency, QueueTimeoutSec: spec.Defaults.QueueTimeoutSec,
	}, nil
}

// UniqueServiceSlug returns base, or base-2, base-3... if taken.
func UniqueServiceSlug(ctx context.Context, st *store.Store, base string) string {
	slug := base
	for i := 2; i < 100; i++ {
		if _, err := st.GetServiceBySlug(ctx, slug); errors.Is(err, store.ErrNotFound) {
			return slug
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	return fmt.Sprintf("%s-%d", base, time.Now().Unix())
}

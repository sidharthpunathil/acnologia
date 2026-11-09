package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

func schema(props map[string]any, required ...string) anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{Properties: props, Required: required}
}

var providerProp = map[string]any{
	"type":        "object",
	"description": "The provider: slug, name, category, description, website, docs_url, color (#rrggbb brand color), logo_url (optional direct image URL of the brand logo), and spec (the provider spec object described in the system prompt, including offers from the pricing page with source_url and checked_at, and a guide with summary, endpoints and notes).",
	"properties": map[string]any{
		"slug": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
		"category": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"},
		"website": map[string]any{"type": "string"}, "docs_url": map[string]any{"type": "string"},
		"color": map[string]any{"type": "string"}, "logo_url": map[string]any{"type": "string"},
		"spec": map[string]any{"type": "object"},
	},
	"required": []string{"slug", "name", "category", "description", "website", "docs_url", "color", "spec"},
}

func (a *Agent) tools(s *runState) []anthropic.BetaToolUnionParam {
	custom := []anthropic.BetaToolParam{
		{
			Name:        "github_read",
			Description: anthropic.String("Read a public GitHub repository: list a directory or read a file (OpenAPI specs, SDK error handling, READMEs). Use it when the user gave a repo or an official SDK exists."),
			InputSchema: schema(map[string]any{
				"repo": map[string]any{"type": "string", "description": "owner/name or a github.com URL"},
				"path": map[string]any{"type": "string", "description": "file or directory path; empty for the root"},
				"ref":  map[string]any{"type": "string", "description": "branch, tag or commit; empty for the default branch"},
			}, "repo"),
		},
		{
			Name:        "validate_spec",
			Description: anthropic.String("Check a draft provider for schema problems (offers included) and run its fixture tests through Acnologia's real engine. Call it before submitting."),
			InputSchema: schema(map[string]any{"provider": providerProp}, "provider"),
		},
		{
			Name: "submit_provider",
			Description: anthropic.String("Submit the finished provider. The harness validates it, runs every fixture test" +
				liveSuffix(s) + ", then publishes it. If anything fails you get the errors back and can fix and resubmit (max 3 submits)."),
			InputSchema: schema(map[string]any{
				"provider": providerProp,
				"summary":  map[string]any{"type": "string", "description": "Two to five sentences for the operator: what the API is, how keys, limits and quota work, what is free, and the source URLs you relied on (docs and pricing page)."},
			}, "provider", "summary"),
		},
	}
	custom = append(custom, stageTool)
	if s.testKey != "" {
		custom = append(custom, anthropic.BetaToolParam{
			Name:        "live_check",
			Description: anthropic.String("Try the draft against the real API with the operator's test key: runs the health check and the first example marked safe. Spends at most one cheap request. Use it to confirm auth placement, the health endpoint and quota paths."),
			InputSchema: schema(map[string]any{"provider": providerProp}, "provider"),
		})
	}
	out := []anthropic.BetaToolUnionParam{
		{OfWebSearchTool20260209: &anthropic.BetaWebSearchTool20260209Param{MaxUses: anthropic.Int(MaxWebSearches)}},
		{OfWebFetchTool20260209: &anthropic.BetaWebFetchTool20260209Param{MaxUses: anthropic.Int(MaxWebFetches), MaxContentTokens: anthropic.Int(40000)}},
	}
	for i := range custom {
		out = append(out, anthropic.BetaToolUnionParam{OfTool: &custom[i]})
	}
	return out
}

func liveSuffix(s *runState) string {
	if s.testKey != "" {
		return " and a live check with the operator's test key"
	}
	return ""
}

// runTool executes a client tool. It returns the result text, whether it is
// an error, and whether the run is finished.
func (a *Agent) runTool(ctx context.Context, s *runState, name string, raw []byte) (string, bool, bool) {
	id := s.run.ID
	var in struct {
		Repo     string          `json:"repo"`
		Path     string          `json:"path"`
		Ref      string          `json:"ref"`
		Provider json.RawMessage `json:"provider"`
		Summary  string          `json:"summary"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "INVALID_JSON: " + err.Error(), true, false
	}
	switch name {
	case "stage_parts":
		out, isErr := a.stage(ctx, s, raw)
		return out, isErr, false
	case "github_read":
		a.logf(ctx, id, "fetch", "GitHub: "+strings.TrimSpace(in.Repo+" "+in.Path), in.Ref)
		out, err := a.githubRead(ctx, in.Repo, in.Path, in.Ref)
		if err != nil {
			return err.Error(), true, false
		}
		return out, false, false

	case "validate_spec", "live_check", "submit_provider":
		d, err := a.decodeDraft(ctx, s, in.Provider)
		if err != nil {
			return err.Error(), true, false
		}
		checked, errs, rep := catalog.Check(&d.Input)
		var gaps []string
		if checked != nil {
			gaps = GuideGaps(checked)
		}
		switch name {
		case "validate_spec":
			text := checkText(errs, rep) + guideText(gaps)
			a.logf(ctx, id, "tool", fmt.Sprintf("Validated draft: %d problem(s), %d/%d tests pass", len(errs)+len(gaps), rep.Passed, rep.Passed+rep.Failed), text)
			return text, false, false
		case "live_check":
			if s.liveChecks >= MaxLiveChecks {
				return fmt.Sprintf("live_check is limited to %d calls per run. Submit the provider instead.", MaxLiveChecks), true, false
			}
			// The real key only rides on a spec that passes the schema: its
			// paths start with / and its health method is GET or POST, so
			// no path can move the request off base_url's host.
			if len(errs) > 0 {
				return "Fix these schema problems before a live check:\n- " + strings.Join(errs, "\n- "), true, false
			}
			spec, err := providers.DecodeSpec(d.Spec)
			if err != nil {
				return err.Error(), true, false
			}
			if msg := a.pinKeyHost(ctx, s, spec.BaseURL); msg != "" {
				return msg, true, false
			}
			s.liveChecks++
			lr := catalog.LiveCheck(ctx, spec, s.testKey)
			s.live = &lr
			b, _ := json.MarshalIndent(lr, "", "  ")
			a.logf(ctx, id, "tool", "Live check: "+okWord(lr.OK), string(b))
			return string(b), false, false
		}
		// submit_provider
		s.submits++
		st := "testing"
		_ = a.Store.UpdateRun(ctx, id, prStatus(&st))
		a.logf(ctx, id, "tool", fmt.Sprintf("Submitted %s (attempt %d of %d)", d.Name, s.submits, MaxSubmits), "")
		problems := append([]string{}, errs...)
		if rep.Failed > 0 {
			problems = append(problems, fmt.Sprintf("%d fixture test(s) fail", rep.Failed))
		}
		// The guide is required, but a provider that works should not fail
		// on it alone: the last submit is accepted with the gap noted.
		lastTry := s.submits >= MaxSubmits
		if len(gaps) > 0 && (len(problems) > 0 || !lastTry) {
			problems = append(problems, gaps...)
		}
		if len(problems) == 0 && s.testKey != "" {
			spec, _ := providers.DecodeSpec(d.Spec)
			if len(a.pinnedDomains(ctx, s)) == 0 {
				// Nothing vouches for base_url's host: accept without a live
				// check and let the operator confirm the host on review.
				s.unpinned = true
			} else if msg := a.pinKeyHost(ctx, s, spec.BaseURL); msg != "" {
				problems = append(problems, msg)
			} else {
				lr := catalog.LiveCheck(ctx, spec, s.testKey)
				s.live = &lr
				if !lr.Health.OK {
					problems = append(problems, "live health check failed: "+lr.Health.Detail)
				}
				if lr.Example.Ran && !lr.Example.OK {
					problems = append(problems, fmt.Sprintf("live example returned %d (%s): %s", lr.Example.Status, lr.Example.Outcome, truncate(lr.Example.Detail, 400)))
				}
			}
		}
		s.report = rep
		back := "running"
		_ = a.Store.UpdateRun(ctx, id, prStatus(&back))
		if len(problems) > 0 {
			text := "Not accepted:\n- " + strings.Join(problems, "\n- ") + "\n\n" + checkText(nil, rep)
			a.logf(ctx, id, "error", "Submission rejected", text)
			if s.submits >= MaxSubmits {
				return text + "\nNo submits left.", true, true
			}
			return text + "\nFix these and submit again.", true, false
		}
		s.accepted, s.summary = d, in.Summary
		if len(gaps) > 0 {
			a.logf(ctx, id, "info", "Accepted without a full usage guide", strings.Join(gaps, "\n")+"\nWrite one later from the provider's Guide tab.")
		}
		a.nameFromDraft(ctx, s, d)
		a.logf(ctx, id, "result", fmt.Sprintf("Accepted: %d/%d tests pass", rep.Passed, rep.Passed+rep.Failed), in.Summary)
		return "Accepted. Acnologia will fetch the logo and publish it now. Reply with nothing more.", false, true
	}
	return "unknown tool " + name, true, false
}

func (a *Agent) decodeDraft(ctx context.Context, s *runState, raw json.RawMessage) (*draft, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("provider is required")
	}
	var d draft
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("provider is not valid JSON: %v", err)
	}
	d.Spec = withStaged(d.Spec, s.staged)
	// Normalize exactly as catalog.Check does, so " serpapi" cannot slip past
	// the existing-slug check and then overwrite serpapi on publish.
	d.Slug = strings.TrimSpace(strings.ToLower(d.Slug))
	if s.fixedSlug != "" {
		d.Slug = s.fixedSlug // updates keep the slug: services reference it
	} else if d.Slug != "" {
		if p, err := a.Store.GetProvider(ctx, d.Slug); err == nil && p.Slug != "" {
			return nil, fmt.Errorf("slug %q is taken by an existing provider (%s); pick another slug", d.Slug, p.Name)
		}
	}
	return &d, nil
}

func checkText(errs []string, rep providers.TestReport) string {
	var b strings.Builder
	if len(errs) == 0 {
		b.WriteString("Schema: ok.\n")
	} else {
		b.WriteString("Schema problems:\n")
		for _, e := range errs {
			b.WriteString("- " + e + "\n")
		}
	}
	fmt.Fprintf(&b, "Fixture tests: %d passed, %d failed.\n", rep.Passed, rep.Failed)
	for _, r := range rep.Results {
		mark := "PASS"
		if !r.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "%s %s: expected %s, got %s (%s)\n", mark, r.Name, r.Expect, r.Got, r.Detail)
	}
	return b.String()
}

// guideText reports the guide's gaps under the schema check.
func guideText(gaps []string) string {
	if len(gaps) == 0 {
		return "Guide: ok.\n"
	}
	return "Guide problems (the guide is required):\n- " + strings.Join(gaps, "\n- ") + "\n"
}

func okWord(ok bool) string {
	if ok {
		return "passed"
	}
	return "failed"
}

var ghNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func ghName(s string) bool { return s != "." && s != ".." && ghNameRe.MatchString(s) }

// githubRead lists a directory or reads a file through the GitHub API.
func (a *Agent) githubRead(ctx context.Context, repo, path, ref string) (string, error) {
	repo = strings.TrimSpace(repo)
	repo = strings.TrimPrefix(strings.TrimPrefix(repo, "https://"), "http://")
	repo = strings.TrimPrefix(repo, "github.com/")
	repo = strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git")
	parts := strings.Split(repo, "/")
	if len(parts) < 2 || !ghName(parts[0]) || !ghName(parts[1]) {
		return "", fmt.Errorf("repo must look like owner/name")
	}
	segs := []string{}
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if seg == "" {
			continue
		}
		if seg == "." || seg == ".." || strings.ContainsAny(seg, "?#\\") {
			return "", fmt.Errorf("path must be a plain file or directory path inside the repo")
		}
		segs = append(segs, url.PathEscape(seg))
	}
	u := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]), strings.Join(segs, "/"))
	if ref != "" {
		u += "?ref=" + url.QueryEscape(ref)
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if a.github != "" {
		req.Header.Set("Authorization", "Bearer "+a.github)
	}
	resp, err := catalog.SafeClient(20 * time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub returned %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var list []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"`
		Size int    `json:"size"`
	}
	if json.Unmarshal(body, &list) == nil {
		var b strings.Builder
		for _, e := range list {
			fmt.Fprintf(&b, "%s\t%s\t%d\n", e.Type, e.Path, e.Size)
		}
		return b.String(), nil
	}
	var file struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Size     int    `json:"size"`
	}
	if err := json.Unmarshal(body, &file); err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return "", err
	}
	const limit = 80000
	if len(data) > limit {
		return string(data[:limit]) + fmt.Sprintf("\n[truncated: file is %d bytes]", len(data)), nil
	}
	return string(data), nil
}

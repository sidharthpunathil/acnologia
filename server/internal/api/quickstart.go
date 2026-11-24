package api

// The first run path for agents and scripts: GET /api/admin/checklist says
// which of the dashboard's Getting started steps are done (the same rules
// as web/src/pages/GettingStarted.tsx), and the control tool quickstart
// does the first two in one call. GET /api/openapi.json (openapi.go) is
// mounted here too.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// mountAgentAPI adds the routes made for agents: the OpenAPI spec (open,
// like the docs) and the setup checklist (any signed in user or token).
func (s *Server) mountAgentAPI(r chi.Router) {
	r.Get("/api/openapi.json", s.openAPIJSON)
	r.With(jsonOnly, s.requireUser).Get("/api/admin/checklist", s.adminChecklist)
}

// checklistStep is one Getting started step.
type checklistStep struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
	// Next is how to do the step: a control MCP tool, an acn command and
	// the dashboard page.
	Next checklistNext `json:"next"`
}

type checklistNext struct {
	Tool    string `json:"tool,omitempty"`
	Command string `json:"command,omitempty"`
	URL     string `json:"url,omitempty"`
}

// checklistUsageDays is how far back the first call step looks, as the
// dashboard card does.
const checklistUsageDays = 90

// adminChecklist is GET /api/admin/checklist.
func (s *Server) adminChecklist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(r)
	base := s.baseURL(r)

	svcs, err := s.Store.ListServices(ctx)
	if err == nil {
		err = s.Store.FillStats(ctx, svcs, isSuper(r), nil)
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	apps, err := s.Store.ListApps(ctx)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	tokens, err := s.Store.ListTokens(ctx, u.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}

	// A service with a key.
	var target *store.Service
	requests := int64(0)
	for _, sv := range svcs {
		if sv.KeyCounts.Total > 0 && target == nil {
			target = sv
		}
		requests += int64(sv.Requests24h)
	}
	// An app the person made: the control MCP's own app does not count.
	ownApps, appUsed := 0, false
	for _, a := range apps {
		if a.Name == controlAppName {
			continue
		}
		ownApps++
		if a.LastUsedAt != nil {
			appUsed = true
		}
	}
	// An agent connected: this user's admin token (acn login, acn mcp) was
	// used, or one of their apps was.
	cliUsed := false
	for _, t := range tokens {
		if t.LastUsedAt != nil {
			cliUsed = true
		}
	}
	// A first call: requests in the last 90 days, plus the last day's.
	if rep, err := s.Store.UsageReport(ctx, time.Now(), checklistUsageDays, isSuper(r)); err == nil {
		requests += rep.Totals["range"].Requests
	}

	callCmd := fmt.Sprintf(`curl "%s/proxy/<service>/<path>" -H "Authorization: Bearer acn_..."`, base)
	callURL := base + "/"
	if target != nil {
		callCmd = fmt.Sprintf(`curl "%s/proxy/%s/<path>" -H "Authorization: Bearer acn_..."`, base, target.Slug)
		callURL = fmt.Sprintf("%s/services/%d", base, target.ID)
	}
	steps := []checklistStep{
		{ID: "service", Title: "Add your first service and key", Done: target != nil, Next: checklistNext{
			Tool: ctlPublicTool("quickstart"), Command: "acn tool quickstart provider=<slug> keys='[\"<key>\"]'", URL: base + "/"}},
		{ID: "app_token", Title: "Create an app token", Done: ownApps > 0, Next: checklistNext{
			Tool: ctlPublicTool("create_app"), Command: "acn apps create <name>", URL: base + "/apps"}},
		{ID: "agent", Title: "Connect your agent", Done: cliUsed || appUsed, Next: checklistNext{
			Command: "acn login && acn mcp install", URL: base + "/docs/cli#set-up-your-agents-in-one-step"}},
		{ID: "first_call", Title: "Make a first call", Done: requests > 0, Next: checklistNext{
			Tool: ctlPublicTool("call_service"), Command: callCmd, URL: callURL}},
	}
	done := 0
	for _, st := range steps {
		if st.Done {
			done++
		}
	}
	writeJSON(w, 200, map[string]any{"steps": steps, "done": done, "total": len(steps), "complete": done == len(steps)})
}

// ---- quickstart ----

// quickstartKeys turns the keys input (strings or {secret, label}) into the
// add keys body.
func quickstartKeys(in map[string]any) []any {
	out := []any{}
	list, _ := in["keys"].([]any)
	for _, x := range list {
		switch k := x.(type) {
		case string:
			if k = strings.TrimSpace(k); k != "" {
				out = append(out, map[string]any{"secret": k})
			}
		case map[string]any:
			out = append(out, k)
		}
	}
	return out
}

// quickstartPlan works out what quickstart would do: the service slug,
// whether it exists already, and the app name.
func quickstartPlan(c *ctlCtx, in map[string]any) (slug string, existing *store.Service, appName string, err error) {
	provider := argStr(in, "provider")
	if provider == "" {
		return "", nil, "", errors.New("provider is required")
	}
	if _, err := c.s.Store.GetProvider(c.r.Context(), provider); err != nil {
		return "", nil, "", fmt.Errorf("no provider %q in the catalog; %s lists them", provider, ctlPublicTool("list_providers"))
	}
	slug = orDefault(argStr(in, "slug"), provider)
	if sv, e := c.s.Store.GetServiceBySlug(c.r.Context(), slug); e == nil {
		if sv.Provider != provider {
			return "", nil, "", fmt.Errorf("service %s already exists for provider %s; pass another slug", slug, sv.Provider)
		}
		existing = sv
	}
	appName = orDefault(argStr(in, "app_name"), slug+" app")
	return slug, existing, appName, nil
}

func describeQuickstart(c *ctlCtx, in map[string]any) (string, error) {
	slug, existing, appName, err := quickstartPlan(c, in)
	if err != nil {
		return "", err
	}
	what := "create service " + slug + " from provider " + argStr(in, "provider")
	if existing != nil {
		what = "use the existing service " + slug
	}
	n := len(quickstartKeys(in))
	keys := fmt.Sprintf("add %d keys", n)
	if argStr(in, "text") != "" {
		keys = fmt.Sprintf("add %d keys plus the keys in text", n)
	}
	return fmt.Sprintf("%s, %s, and create the app %q (mode %s) for it with a new token", what, keys, appName, orDefault(argStr(in, "mode"), "proxy")), nil
}

// ctlQuickstart is quickstart: service, keys, app and token through the
// routes, so roles, validation and audit apply to each step. A failed step
// reports what was already done.
func ctlQuickstart(c *ctlCtx, in map[string]any) map[string]any {
	slug, existing, appName, err := quickstartPlan(c, in)
	if err != nil {
		return ctlFailCode("invalid", err.Error())
	}
	done := map[string]any{}
	fail := func(step string, code int, out map[string]any) map[string]any {
		e := routeErr(code, out)
		e["step"], e["done"] = step, done
		res := ctlFailCode(fmt.Sprint(e["code"]), fmt.Sprintf("quickstart stopped at %s: %v", step, e["message"]))
		res["structuredContent"] = e
		return res
	}

	var svcID int64
	if existing != nil {
		svcID = existing.ID
		done["service"] = map[string]any{"id": existing.ID, "slug": existing.Slug, "created": false}
	} else {
		code, out, _ := c.apiJSON(http.MethodPost, "/api/admin/services", map[string]any{"provider": argStr(in, "provider"), "slug": slug})
		if code >= 400 {
			return fail("service", code, out)
		}
		svcID, _ = argNum(out, "id")
		done["service"] = map[string]any{"id": svcID, "slug": argStr(out, "slug"), "name": argStr(out, "name"), "created": true}
	}

	keys, text := quickstartKeys(in), argStr(in, "text")
	if len(keys) > 0 || text != "" {
		body := map[string]any{"keys": keys}
		if text != "" {
			body["text"] = text
		}
		code, out, _ := c.apiJSON(http.MethodPost, fmt.Sprintf("/api/admin/services/%d/keys", svcID), body)
		if code >= 400 {
			return fail("keys", code, out)
		}
		added, _ := out["keys"].([]any)
		skipped, _ := out["skipped"].([]any)
		done["keys"] = map[string]any{"added": len(added), "skipped": len(skipped)}
	} else {
		done["keys"] = map[string]any{"added": 0, "skipped": 0, "note": "no keys given; add them with " + ctlPublicTool("add_keys")}
	}

	code, out, _ := c.apiJSON(http.MethodPost, "/api/admin/apps", map[string]any{
		"name": appName, "mode": orDefault(argStr(in, "mode"), "proxy"), "allowed_services": []string{slug},
		"description": "Made by quickstart for " + slug,
	})
	if code >= 400 {
		return fail("app", code, out)
	}
	app, _ := out["app"].(map[string]any)
	tok := argStr(out, "token")
	done["app"] = pick(app, "id", "name", "mode", "allowed_services")

	base := c.s.baseURL(c.r)
	mcpURL := base + "/mcp"
	done["token"] = tok
	done["proxy_url"] = base + "/proxy/" + slug + "/"
	done["mcp"] = map[string]any{
		"url":         mcpURL,
		"claude_code": fmt.Sprintf(`claude mcp add --transport http acnologia %s --header "Authorization: Bearer %s"`, mcpURL, tok),
		"config": map[string]any{"mcpServers": map[string]any{"acnologia": map[string]any{
			"type": "http", "url": mcpURL, "headers": map[string]any{"Authorization": "Bearer " + tok},
		}}},
	}
	done["note"] = "The token is shown only this once. New keys are health checked in the background; " + ctlPublicTool("checklist") + " shows what is left."
	return ctlJSON(done)
}

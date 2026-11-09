package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// providerTools let the Assistant change a provider by chat: its name,
// description and the rest of its metadata, any part of its spec, its logo,
// or a whole AI pass over it. Every change publishes a new provider version
// through the dashboard API, so validation, fixture tests, history and
// rollback apply as in the editor.
func providerTools() []*chatTool {
	slugProp := map[string]any{"type": "string", "description": "provider slug from list_providers"}
	return []*chatTool{
		{
			Name: "get_provider_spec", Role: store.RoleMember, Required: []string{"slug"},
			Props: map[string]any{
				"slug":  slugProp,
				"parts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "top level spec fields to return, such as auth, defaults, offers, hosts, guide, rules, health, examples, tests; empty for all"},
			},
			Desc: "Read only. A provider's metadata and its spec as JSON (or only the parts asked for), to see exactly what update_provider would change.",
			Run:  getProviderSpecTool,
		},
		{
			Name: "update_provider", Role: store.RoleAdmin, Required: []string{"slug"},
			Props: map[string]any{
				"slug":        slugProp,
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string", "description": "one or two plain sentences: what it does and how its quota works"},
				"category":    map[string]any{"type": "string", "description": "one or two words, such as Search, LLM, Data, Scraping"},
				"website":     map[string]any{"type": "string"},
				"docs_url":    map[string]any{"type": "string"},
				"color":       map[string]any{"type": "string", "description": "#rrggbb brand color"},
				"status":      map[string]any{"type": "string", "enum": []string{"active", "disabled"}},
				"spec_patch": map[string]any{"type": "object", "description": "a JSON merge patch (RFC 7386) on the spec: objects merge, any other value replaces, null deletes. " +
					"Lists such as offers, hosts, examples or guide.endpoints are replaced whole, so read them with get_provider_spec first and send the full new list."},
				"note": map[string]any{"type": "string", "description": "a short note for the version history, such as \"renamed at the user's request\""},
			},
			Desc: "Change an existing provider: name, description, category, website, docs URL, color, status, and any part of its spec through spec_patch. " +
				"Publishes a new version after validation and the fixture tests; the old version stays in its history for rollback. " +
				"Use it for edits the user states. For research the user wants done (fill in missing APIs, rewrite the rules from the docs), use improve_provider.",
			ConfirmIf: providerChangeNeedsConfirm,
			Describe:  describeProviderChange,
			Run:       updateProviderTool,
		},
		{
			Name: "improve_provider", Role: store.RoleAdmin, Required: []string{"slug", "instruction"},
			Props: map[string]any{
				"slug":        slugProp,
				"instruction": map[string]any{"type": "string", "description": "what to change or research, in the user's words, such as \"add every Omkar API, grouped by category\""},
				"urls":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "docs, pricing or catalog URLs the user gave"},
			},
			Desc: "Start the AI provider builder on an existing provider: it reads the provider's docs, makes the change asked for, re-tests and publishes a new version in a few minutes. " +
				"Use it for work that needs research: adding the rest of a provider's APIs or products, fixing rules or quota from the docs, filling offers. Then call wait_for_build with the run id.",
			Run: improveProviderTool,
		},
		{
			Name: "fetch_provider_logo", Role: store.RoleAdmin, Required: []string{"slug"},
			Props: map[string]any{
				"slug": slugProp,
				"url":  map[string]any{"type": "string", "description": "a direct image URL, or a website to find the icon on; empty uses the provider's website"},
			},
			Desc: "Fetch and set a provider's logo from an image URL or a website.",
			Run:  fetchProviderLogoTool,
		},
	}
}

// keyTargetFields are the spec fields that decide where keys are sent.
var keyTargetFields = []string{"base_url", "auth", "health", "hosts"}

// providerChangeNeedsConfirm asks the user before a change that moves where
// keys go or stops a provider's traffic. Text the model read (a provider's
// docs, an event) must never be able to make those on its own.
func providerChangeNeedsConfirm(in map[string]any) bool {
	if strings.EqualFold(argStr(in, "status"), "disabled") {
		return true
	}
	patch, _ := in["spec_patch"].(map[string]any)
	for _, f := range keyTargetFields {
		if _, ok := patch[f]; ok {
			return true
		}
	}
	return false
}

func describeProviderChange(cs *chatSession, in map[string]any) (string, error) {
	slug := argStr(in, "slug")
	p, err := cs.s.Store.GetProvider(cs.ctx, slug)
	if err != nil {
		return "", fmt.Errorf("no provider %q", slug)
	}
	parts := []string{}
	if strings.EqualFold(argStr(in, "status"), "disabled") {
		parts = append(parts, "disable it, so its services stop serving requests")
	}
	patch, _ := in["spec_patch"].(map[string]any)
	for _, f := range keyTargetFields {
		if _, ok := patch[f]; ok {
			parts = append(parts, "change "+f)
		}
	}
	return fmt.Sprintf("Change provider %s: %s. This moves where its keys are sent or stops its traffic; hidden keys pause until a superadmin confirms the service.",
		p.Name, strings.Join(parts, ", ")), nil
}

func getProviderSpecTool(cs *chatSession, in map[string]any) toolOut {
	slug := argStr(in, "slug")
	p, err := cs.s.Store.GetProvider(cs.ctx, slug)
	if err != nil {
		return toolFail("no provider " + slug + "; call list_providers")
	}
	var spec map[string]any
	if err := json.Unmarshal(p.Spec, &spec); err != nil {
		return toolFail("the provider's spec is unreadable")
	}
	if want, ok := in["parts"].([]any); ok && len(want) > 0 {
		keep := map[string]any{}
		for _, w := range want {
			if k, ok := w.(string); ok {
				if v, ok := spec[k]; ok {
					keep[k] = v
				}
			}
		}
		spec = keep
	}
	out := map[string]any{"slug": p.Slug, "name": p.Name, "category": p.Category, "description": p.Description, "website": p.Website,
		"docs_url": p.DocsURL, "color": p.Color, "status": p.Status, "version": p.Version, "source": p.Source, "spec": spec}
	model := asJSON(out)
	if len(model) > chatMaxModelText*4 {
		keys := make([]string, 0, len(spec))
		for k := range spec {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return toolFail(fmt.Sprintf("the spec is %d characters; ask for some parts: %s", len(model), strings.Join(keys, ", ")))
	}
	return toolOut{OK: true, Model: model, Summary: "Read the " + p.Name + " spec", Link: "/providers/" + p.Slug,
		Entities: []chatEntity{{Type: "provider", Slug: p.Slug, Name: p.Name}}}
}

// mergePatch applies an RFC 7386 JSON merge patch.
func mergePatch(target any, patch any) any {
	pm, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	tm, ok := target.(map[string]any)
	if !ok {
		tm = map[string]any{}
	}
	for k, v := range pm {
		if v == nil {
			delete(tm, k)
			continue
		}
		tm[k] = mergePatch(tm[k], v)
	}
	return tm
}

func updateProviderTool(cs *chatSession, in map[string]any) toolOut {
	slug := argStr(in, "slug")
	p, err := cs.s.Store.GetProvider(cs.ctx, slug)
	if err != nil {
		return toolFail("no provider " + slug + "; call list_providers")
	}
	body := providerBody{Input: catalog.Input{Slug: p.Slug, Name: p.Name, Category: p.Category, Description: p.Description,
		Website: p.Website, DocsURL: p.DocsURL, Color: p.Color, Status: p.Status, Spec: p.Spec}}
	changed := []string{}
	set := func(field string, dst *string) {
		if _, ok := in[field]; !ok {
			return
		}
		if v := argStr(in, field); v != *dst {
			*dst = v
			changed = append(changed, field)
		}
	}
	set("name", &body.Name)
	set("description", &body.Description)
	set("category", &body.Category)
	set("website", &body.Website)
	set("docs_url", &body.DocsURL)
	set("color", &body.Color)
	set("status", &body.Status)
	if patch, ok := in["spec_patch"].(map[string]any); ok && len(patch) > 0 {
		var cur any
		if err := json.Unmarshal(p.Spec, &cur); err != nil {
			return toolFail("the provider's spec is unreadable")
		}
		next, err := json.Marshal(mergePatch(cur, patch))
		if err != nil {
			return toolFail("the spec patch is not valid JSON")
		}
		body.Spec = next
		keys := make([]string, 0, len(patch))
		for k := range patch {
			keys = append(keys, "spec."+k)
		}
		sort.Strings(keys)
		changed = append(changed, keys...)
	}
	if len(changed) == 0 {
		return toolFail("nothing to change: pass the fields to set")
	}
	body.Note = argStr(in, "note")
	if body.Note == "" {
		body.Note = "Assistant: changed " + strings.Join(changed, ", ")
	}
	code, out, msg := cs.api("PUT", "/api/admin/providers/"+url.PathEscape(p.Slug), body)
	if code >= 400 {
		if errs, ok := out["errors"].([]any); ok && len(errs) > 0 {
			list := []string{}
			for _, e := range errs {
				list = append(list, fmt.Sprint(e))
			}
			msg = "not published, the provider has problems: " + strings.Join(list, "; ")
		}
		return toolFail(msg)
	}
	saved, _ := out["provider"].(map[string]any)
	version, _ := argNum(saved, "version")
	rep, _ := out["report"].(map[string]any)
	passed, _ := argNum(rep, "passed")
	failed, _ := argNum(rep, "failed")
	name := argStr(saved, "name")
	if name == "" {
		name = body.Name
	}
	ent := chatEntity{Type: "provider", Slug: p.Slug, Name: name}
	return toolOut{OK: true, Summary: fmt.Sprintf("Updated %s: %s (v%d)", name, strings.Join(changed, ", "), version),
		Link: "/providers/" + p.Slug, Entities: []chatEntity{ent},
		Card: &chatCard{Type: "provider", Provider: &chatCardProv{Slug: p.Slug, Name: name, Version: int(version), Passed: int(passed), Failed: int(failed)}},
		Model: asJSON(map[string]any{"slug": p.Slug, "version": version, "changed": changed, "tests_passed": passed, "tests_failed": failed})}
}

func improveProviderTool(cs *chatSession, in map[string]any) toolOut {
	slug := argStr(in, "slug")
	p, err := cs.s.Store.GetProvider(cs.ctx, slug)
	if err != nil {
		return toolFail("no provider " + slug + "; call list_providers")
	}
	body := map[string]any{"kind": "update_provider", "provider_slug": p.Slug, "context": argStr(in, "instruction")}
	if u, ok := in["urls"].([]any); ok {
		body["urls"] = u
	}
	code, out, msg := cs.api("POST", "/api/admin/ai/runs", body)
	if code >= 400 {
		return toolFail(msg)
	}
	id, _ := argNum(out, "id")
	title := argStr(out, "title")
	if title == "" {
		title = "Improve " + p.Name
	}
	link := fmt.Sprintf("/providers/ai/%d", id)
	cs.addItem(chatItem{Kind: "build", OK: true, Link: link,
		Build: &chatBuild{RunID: id, Title: title, Status: "queued", Step: "Queued", StartedAt: time.Now().UTC()}})
	return toolOut{OK: true, Summary: fmt.Sprintf("Started AI pass %d on %s", id, p.Name), Link: link,
		Entities: []chatEntity{{Type: "provider", Slug: p.Slug, Name: p.Name}},
		Model: asJSON(map[string]any{"run_id": id, "status": out["status"], "title": title,
			"next": "call wait_for_build with this run_id now; an update run may wait for review before it publishes"})}
}

func fetchProviderLogoTool(cs *chatSession, in map[string]any) toolOut {
	slug := argStr(in, "slug")
	code, out, msg := cs.api("POST", "/api/admin/providers/"+url.PathEscape(slug)+"/logo/fetch", map[string]any{"url": argStr(in, "url")})
	if code >= 400 {
		return toolFail("could not set the logo: " + msg)
	}
	name := argStr(out, "name")
	return toolOut{OK: true, Summary: "Set the " + name + " logo", Link: "/providers/" + slug,
		Entities: []chatEntity{{Type: "provider", Slug: slug, Name: name}}, Model: asJSON(map[string]any{"slug": slug, "logo": "updated"})}
}

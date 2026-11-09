package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// capabilityTools let the Assistant build and test capabilities: one job
// that several providers can do, behind one name apps call.
func capabilityTools() []*chatTool {
	routeItem := map[string]any{"type": "object", "description": "how one service does it: {service, method, path, query: {name: template}, body (JSON template), " +
		"weight, disabled, output: {items: dot path of the result list (empty for one object), fields: {output field: dot path in one item}}}. " +
		"Templates use {{param}}; a field whose param has no value is left out; a body string that is exactly one placeholder keeps the value's type."}
	paramItem := map[string]any{"type": "object", "description": "{name, type: string | integer | number | boolean, required, description, default}"}
	return []*chatTool{
		{
			Name: "list_capabilities", Role: store.RoleMember, Props: map[string]any{},
			Desc: "Read only. Capabilities: one job (such as search places) that several providers can do, behind one name apps call at /proxy/<slug>, with their params, strategy and routes.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/capabilities", nil, "Listed capabilities", func(m map[string]any) any { return m["capabilities"] })
			},
		},
		{
			Name: "save_capability", Role: store.RoleAdmin, Required: []string{"slug"},
			Props: map[string]any{
				"slug":        map[string]any{"type": "string", "description": "lowercase letters, digits and hyphens; apps call /proxy/<slug>"},
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string", "description": "what it does, for apps and AI agents"},
				"strategy": map[string]any{"type": "string", "enum": []string{"priority", "round_robin", "random", "weighted", "cheapest", "most_keys"},
					"description": "which route goes first; the rest follow as fallbacks. priority keeps the route order; cheapest uses offer prices; most_keys prefers the provider with the most free keys"},
				"params": map[string]any{"type": "array", "items": paramItem},
				"routes": map[string]any{"type": "array", "items": routeItem},
			},
			Desc: "Create a capability, or change one with the same slug (send only the fields that change; params and routes replace the whole list). " +
				"Build each route from the provider's guide (provider_guide), map every param, and map the output to the same fields on every route. Test it with try_capability.",
			Run: saveCapabilityTool,
		},
		{
			Name: "try_capability", Role: store.RoleAdmin, Required: []string{"slug"},
			Props: map[string]any{
				"slug":    map[string]any{"type": "string"},
				"params":  map[string]any{"type": "object", "description": "the capability's params"},
				"service": map[string]any{"type": "string", "description": "test only this route's service"},
				"raw":     map[string]any{"type": "boolean", "description": "return the provider's body unmapped, to write an output mapping"},
			},
			Desc: "Call a capability for real, as the Assistant app, and see which provider answered, which were tried and the answer. Spends a request of provider quota. Test each route with service set before you finish.",
			Run:  tryCapabilityTool,
		},
		{
			Name: "delete_capability", Role: store.RoleAdmin, Confirm: true, Required: []string{"slug"},
			Props: map[string]any{"slug": map[string]any{"type": "string"}},
			Desc:  "Delete a capability. Apps calling it get 404 afterwards.",
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				c, err := cs.s.Store.GetCapabilityBySlug(cs.ctx, argStr(in, "slug"))
				if err != nil {
					return "", fmt.Errorf("no capability %q", argStr(in, "slug"))
				}
				return fmt.Sprintf("Delete capability %s (/proxy/%s). Apps calling it get 404 afterwards.", c.Name, c.Slug), nil
			},
			Run: func(cs *chatSession, in map[string]any) toolOut {
				c, err := cs.s.Store.GetCapabilityBySlug(cs.ctx, argStr(in, "slug"))
				if err != nil {
					return toolFail("no capability " + argStr(in, "slug"))
				}
				return cs.simple("DELETE", fmt.Sprintf("/api/admin/capabilities/%d", c.ID), nil, "Deleted capability "+c.Slug, nil)
			},
		},
	}
}

func saveCapabilityTool(cs *chatSession, in map[string]any) toolOut {
	slug := argStr(in, "slug")
	body := pick(in, "name", "description", "strategy", "params", "routes")
	ent := []chatEntity{}
	if c, err := cs.s.Store.GetCapabilityBySlug(cs.ctx, slug); err == nil {
		code, out, msg := cs.api("PATCH", fmt.Sprintf("/api/admin/capabilities/%d", c.ID), body)
		if code >= 400 {
			return toolFail("not saved: " + msg)
		}
		return toolOut{OK: true, Summary: "Updated capability " + slug, Entities: ent, Model: asJSON(capSummary(out))}
	}
	body["slug"] = slug
	code, out, msg := cs.api("POST", "/api/admin/capabilities", body)
	if code >= 400 {
		return toolFail("not saved: " + msg)
	}
	return toolOut{OK: true, Summary: "Created capability " + slug, Entities: ent, Model: asJSON(capSummary(out))}
}

func capSummary(m map[string]any) map[string]any {
	out := pick(m, "slug", "name", "strategy", "params")
	routes := []string{}
	if rs, ok := m["routes"].([]any); ok {
		for _, r := range rs {
			if rm, ok := r.(map[string]any); ok {
				routes = append(routes, argStr(rm, "service"))
			}
		}
	}
	out["routes"] = routes
	out["url"] = "/proxy/" + argStr(m, "slug")
	return out
}

// assistantAppMeta holds the id of the app try_capability runs as.
const assistantAppMeta = "assistant_app_id"

// assistantApp is the app the Assistant's test calls run as, made on first
// use through the apps route so it is audited and shows on the Apps page.
func (cs *chatSession) assistantApp() (*store.App, error) {
	s, ctx := cs.s, cs.ctx
	s.ctlAppMu.Lock()
	defer s.ctlAppMu.Unlock()
	if id, err := strconv.ParseInt(s.Store.GetMeta(ctx, assistantAppMeta), 10, 64); err == nil && id > 0 {
		if a, err := s.Store.GetApp(ctx, id); err == nil {
			if ts, err := s.Store.ListAppTokens(ctx, a.ID); err == nil {
				for _, t := range ts {
					if t.RevokedAt == nil {
						a.TokenID = t.ID
						break
					}
				}
			}
			return a, nil
		}
	}
	code, out, msg := cs.api(http.MethodPost, "/api/admin/apps", map[string]any{
		"name": "Assistant", "mode": "proxy",
		"description": "Test calls the dashboard Assistant makes with try_capability. Limit or turn it off here.",
	})
	if code >= 400 {
		return nil, fmt.Errorf("could not make the Assistant app: %s", msg)
	}
	app, _ := out["app"].(map[string]any)
	id, _ := argNum(app, "id")
	if err := s.Store.SetMeta(ctx, assistantAppMeta, strconv.FormatInt(id, 10)); err != nil {
		return nil, err
	}
	a, err := s.Store.GetApp(ctx, id)
	if err != nil {
		return nil, err
	}
	if t, ok := out["app_token"].(map[string]any); ok {
		a.TokenID, _ = argNum(t, "id")
	}
	return a, nil
}

func tryCapabilityTool(cs *chatSession, in map[string]any) toolOut {
	slug := argStr(in, "slug")
	if _, err := cs.s.Store.GetCapabilityBySlug(cs.ctx, slug); err != nil {
		return toolFail("no capability " + slug + "; call list_capabilities")
	}
	a, err := cs.assistantApp()
	if err != nil {
		return toolFail(err.Error())
	}
	if !a.Active {
		return toolFail("the Assistant app is turned off; turn it on in Apps to test capabilities")
	}
	params, _ := in["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	if sv := argStr(in, "service"); sv != "" {
		params["_service"] = sv
	}
	if b, _ := in["raw"].(bool); b {
		params["_raw"] = "1"
	}
	body, _ := json.Marshal(params)
	ctx := store.WithAppToken(context.WithValue(cs.ctx, appKey, a), a.TokenID)
	const limit = 24 << 10
	rec, err := cs.s.proxyCapture(ctx, cs.r, slug, http.MethodPost, "", nil, body, nil, limit)
	if err != nil {
		return toolFail(err.Error())
	}
	b := rec.Body.Bytes()
	out := map[string]any{"status": rec.Code, "service": rec.Header().Get("X-Acnologia-Service"), "tried": rec.Header().Get("X-Acnologia-Tried")}
	var v any
	if len(b) <= limit && json.Unmarshal(b, &v) == nil {
		out["body"] = v
	} else {
		out["body"] = string(truncate(b, limit))
		if len(b) > limit {
			out["note"] = "body trimmed"
		}
	}
	ok := rec.Code >= 200 && rec.Code < 300
	summary := fmt.Sprintf("Tried %s: %d from %s", slug, rec.Code, orDefault(out["service"].(string), "no provider"))
	return toolOut{OK: ok, Summary: summary, Model: cs.s.Store.Scrub(cs.ctx, asJSON(out))}
}

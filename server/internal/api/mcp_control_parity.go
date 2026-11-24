package api

// Control MCP tools for the dashboard features that used to be reachable
// only through api_request: capabilities, provider rotation, key sources
// and families, the shared keys report, AI rules, models and prices, share
// analytics, app token names, logos, upstream MCP tool access, the
// Assistant's chats, and the first run checklist and quickstart. Where the
// Assistant has a tool for the same job, its description and input schema
// are reused (assistantSchema), so the two stay in step.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// assistantDescs fill in input descriptions the Assistant's schemas leave
// out, since every control tool input says what it is.
var assistantDescs = map[string]string{
	"slug":    "Capability slug: lowercase letters, digits and hyphens; apps call /proxy/<slug>",
	"name":    "Display name",
	"service": "Service id, slug or name",
	"params":  "The capability's params",
	"routes":  "How each service does it, in order",
}

// assistantSchema returns the description, input properties and required
// inputs of the Assistant tool name from list. The properties are a copy,
// with a description on each.
func assistantSchema(list []*chatTool, name string) (string, map[string]any, []string) {
	for _, t := range list {
		if t.Name != name {
			continue
		}
		props := map[string]any{}
		for k, v := range t.Props {
			m, _ := v.(map[string]any)
			cp := make(map[string]any, len(m)+1)
			for mk, mv := range m {
				cp[mk] = mv
			}
			if d, _ := cp["description"].(string); d == "" {
				cp["description"] = orDefault(assistantDescs[k], k)
			}
			props[k] = cp
		}
		desc := strings.TrimPrefix(t.Desc, "Read only. ")
		return desc, props, append([]string(nil), t.Required...)
	}
	panic("no assistant tool " + name)
}

func ctlParityTools() []ctlTool {
	m, a, sa := store.RoleMember, store.RoleAdmin, store.RoleSuperadmin
	get, post, put, patch, del := http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete
	capTools, callT, provT := capabilityTools(), callTools(), providerTools()

	capIn := sProp("Capability slug or id")
	saveDesc, saveProps, _ := assistantSchema(capTools, "save_capability")
	createProps := props(saveProps)
	updateProps := props(saveProps, map[string]any{"capability": capIn})
	delete(updateProps, "slug")
	_, tryProps, _ := assistantSchema(capTools, "try_capability")
	rotDesc, rotProps, rotReq := assistantSchema(callT, "service_rotation")
	setRotDesc, setRotProps, setRotReq := assistantSchema(callT, "set_rotation")
	sharedDesc, _, _ := assistantSchema(capTools, "shared_keys")
	linkDesc, linkProps, linkReq := assistantSchema(capTools, "link_service_keys")
	linkAllDesc, _, _ := assistantSchema(capTools, "link_all_shared_keys")
	famDesc, famProps, famReq := assistantSchema(provT, "link_service_family")
	fillDesc, _, _ := assistantSchema(provT, "fill_service_logos")

	logoIn := map[string]any{
		"data_base64": sProp("The image, base64 or a data: URL: png, jpeg, webp, gif, svg or ico, at most 512 KB"),
	}
	priceFields := map[string]any{
		"input":          nProp("USD per million input tokens"),
		"output":         nProp("USD per million output tokens"),
		"cache_write":    nProp("USD per million cache write tokens"),
		"cache_read":     nProp("USD per million cache read tokens"),
		"context_window": iProp("Context window in tokens; 0 keeps the built in one"),
		"source_url":     sProp("Where the price comes from"),
		"checked_at":     sProp("Day the price was checked, YYYY-MM-DD; today when empty"),
	}
	modelFields := map[string]any{
		"default":   sProp("Model every AI feature uses unless it has its own; empty for the built in default"),
		"assistant": sProp("Model for the Assistant chat; empty follows default"),
		"builder":   sProp("Model for the provider builder; empty follows default"),
		"fill":      sProp("Model for AI form fill; empty follows default"),
		"summary":   sProp("Model for summaries; empty follows default"),
		"refresh":   sProp("Model for the offers refresh; empty follows default"),
	}
	threadIn := iProp("Chat thread id from assistant_threads")
	waitIn := iProp(fmt.Sprintf("Seconds to wait for the Assistant to finish its answer, 0 to %d, default %d; when it is still working, read it later with assistant_thread", chatWaitMax, chatWaitDefault))

	return []ctlTool{
		// ---- overview ----
		{Name: "checklist", Title: "Setup checklist", Group: "overview", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/checklist",
			Desc: "What is left to set up on this server: the four first run steps the dashboard's Getting started card shows (a service with a key, an app token, an agent connected, a first call), " +
				"each with done and the next step as a tool, an acn command and a dashboard URL. On a fresh server, quickstart does the first two in one call."},

		// ---- services ----
		{Name: "quickstart", Title: "Quickstart a provider", Group: "services", Kind: ctlDestructive, Role: a, OpenWorld: true, Run: ctlQuickstart,
			Desc: "Set up a provider end to end in one call: create a service from a catalog provider (or use the one with that slug), add the keys, create an app for it with a token, " +
				"and return the app's MCP URL with a ready config snippet. The token is in the result, shown once: hand it to the user. list_providers shows the catalog slugs.",
			Props: map[string]any{
				"provider": sProp("Catalog provider slug from list_providers, such as serpapi"),
				"slug":     sProp("Slug for the service; default the provider slug"),
				"keys":     aProp(map[string]any{}, "API keys: strings, or objects {secret, label}"),
				"text":     sProp("Or pasted keys in any common format (lines, CSV, .env, JSON)"),
				"app_name": sProp("Name of the app to create; default <service> app"),
				"mode":     eProp("App mode; proxy keeps keys inside Acnologia", "proxy", "lease", "both"),
			},
			Required: []string{"provider"},
			Describe: describeQuickstart},
		{Name: "service_rotation", Title: "Service rotation", Group: "services", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/services/{service}/rotation",
			Desc: rotDesc, Props: rotProps, Required: rotReq},
		{Name: "set_rotation", Title: "Set provider rotation", Group: "services", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/services/rotation",
			Desc: setRotDesc, Props: setRotProps, Required: setRotReq,
			BodyDoc: map[string]any{
				"services": aProp(map[string]any{}, "Service ids or slugs, 1 to 500"),
				"rotation": eProp("Rotation mode", "off", "fallback", "round_robin"),
			},
			Prepare: func(c *ctlCtx, in map[string]any, _ string) (any, error) {
				names := argStrings(in, "services")
				if len(names) == 0 {
					return nil, errors.New("services is required: the slugs that should share requests")
				}
				ids := []any{}
				for _, n := range names {
					sv, err := c.s.findService(c.r.Context(), n)
					if err != nil {
						return nil, err
					}
					ids = append(ids, sv.ID)
				}
				return map[string]any{"services": ids, "rotation": argStr(in, "rotation")}, nil
			}},
		{Name: "set_service_mcp_tool_access", Title: "Set an upstream tool's access", Group: "services", Kind: ctlWrite, Role: a, Method: patch, Path: "/api/admin/services/{service}/mcp/tools",
			Desc: "Say whether one synced upstream MCP tool of a service counts as read only. Read only apps and tokens can use only read tools. get_service_mcp lists the tools.",
			Props: map[string]any{
				"service": svcIn,
				"name":    sProp("Upstream tool name from get_service_mcp"),
				"access":  eProp("\"\" follows the upstream server's readOnlyHint, read always counts as read only, write never does", "", "read", "write"),
			},
			Required: []string{"service", "name"}, Body: []string{"name", "access"},
			Prepare: func(_ *ctlCtx, in map[string]any, _ string) (any, error) {
				// access "" is a real value, so it is always sent.
				return map[string]any{"name": argStr(in, "name"), "access": argStr(in, "access")}, nil
			}},
		{Name: "fetch_service_logo", Title: "Fetch a service logo", Group: "services", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/services/{service}/logo/fetch", OpenWorld: true,
			Desc:  "Download a service's own logo from an image URL or a website's icon. With no url, the logo of the provider host the service calls.",
			Props: map[string]any{"service": svcIn, "url": sProp("Image or website URL")}, Required: []string{"service"}, Body: []string{"url"}},
		{Name: "set_service_logo", Title: "Upload a service logo", Group: "services", Kind: ctlWrite, Role: a, Method: put, Path: "/api/admin/services/{service}/logo",
			Desc:  "Set a service's own logo from image data.",
			Props: props(logoIn, map[string]any{"service": svcIn}), Required: []string{"service", "data_base64"}, Body: []string{"data_base64"}},
		{Name: "delete_service_logo", Title: "Remove a service logo", Group: "services", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/services/{service}/logo",
			Desc: "Remove a service's own logo; it shows its provider's logo again.", Props: map[string]any{"service": svcIn}, Required: []string{"service"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				sv, err := c.s.findService(c.r.Context(), argStr(in, "service"))
				if err != nil {
					return "", err
				}
				return "remove the logo of service " + sv.Slug, nil
			}},
		{Name: "fill_service_logos", Title: "Fill service logos", Group: "services", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/services/logos/fill", OpenWorld: true,
			Desc: fillDesc},

		// ---- keys ----
		{Name: "shared_keys", Title: "Shared keys report", Group: "keys", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/keys/shared", Desc: sharedDesc},
		{Name: "link_service_keys", Title: "Use another service's keys", Group: "keys", Kind: ctlWrite, Role: a, Method: put, Path: "/api/admin/services/{service}/key-source",
			Desc: linkDesc, Props: linkProps, Required: linkReq, Body: []string{"source"},
			Prepare: func(c *ctlCtx, in map[string]any, _ string) (any, error) {
				if v := argStr(in, "source"); v != "" {
					return map[string]any{"source": v}, nil
				}
				return map[string]any{"source": nil}, nil
			}},
		{Name: "link_all_shared_keys", Title: "Link all suggested key sources", Group: "keys", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/keys/shared/link", Desc: linkAllDesc},
		{Name: "link_service_family", Title: "Link a service family", Group: "keys", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/services/families/link",
			Desc: famDesc, Props: famProps, Required: famReq,
			BodyDoc: map[string]any{"key": sProp("The family key: the site and auth placement its services share (the tool works it out from service)")},
			Prepare: func(c *ctlCtx, in map[string]any, _ string) (any, error) {
				sv, err := c.s.findService(c.r.Context(), argStr(in, "service"))
				if err != nil {
					return nil, err
				}
				key := catalog.FamilyKey(sv)
				if key == "" {
					return nil, errors.New(sv.Slug + " is not in a family")
				}
				return map[string]any{"key": key}, nil
			}},

		// ---- apps ----
		{Name: "rename_app_token", Title: "Rename an app token", Group: "apps", Kind: ctlWrite, Role: a, Method: patch, Path: "/api/admin/apps/{app_id}/tokens/{token_id}",
			Desc:     "Rename one token of an app. Its value and access do not change.",
			Props:    map[string]any{"app_id": iProp("App id"), "token_id": iProp("Token id from list_app_tokens"), "name": sProp("New name, such as ci or staging")},
			Required: []string{"app_id", "token_id", "name"}, Body: []string{"name"}},

		// ---- providers ----
		{Name: "set_provider_logo", Title: "Upload a provider logo", Group: "providers", Kind: ctlWrite, Role: a, Method: put, Path: "/api/admin/providers/{slug}/logo",
			Desc:  "Set a provider's logo from image data. fetch_provider_logo downloads one instead.",
			Props: props(logoIn, map[string]any{"slug": sProp("Provider slug")}), Required: []string{"slug", "data_base64"}, Body: []string{"data_base64"}},
		{Name: "delete_provider_logo", Title: "Remove a provider logo", Group: "providers", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/providers/{slug}/logo",
			Desc: "Remove a provider's logo.", Props: map[string]any{"slug": sProp("Provider slug")}, Required: []string{"slug"},
			Describe: func(_ *ctlCtx, in map[string]any) (string, error) {
				return "remove the logo of provider " + argStr(in, "slug"), nil
			}},

		// ---- routing: capabilities ----
		{Name: "list_capabilities", Title: "List capabilities", Group: "routing", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/capabilities",
			Desc: "Capabilities: one job (such as search places) that several providers can do, behind one name apps call at /proxy/<slug>, with their params, strategy and routes."},
		{Name: "get_capability", Title: "Get a capability", Group: "routing", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/capabilities/{capability}",
			Desc: "One capability with its params, strategy and routes.", Props: map[string]any{"capability": capIn}, Required: []string{"capability"}},
		{Name: "create_capability", Title: "Create a capability", Group: "routing", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/capabilities",
			Desc:  "Create a capability. " + strings.TrimPrefix(saveDesc, "Create a capability, or change one with the same slug (send only the fields that change; params and routes replace the whole list). "),
			Props: createProps, Required: []string{"slug", "name"}, Body: []string{"slug", "name", "description", "strategy", "params", "routes"}},
		{Name: "update_capability", Title: "Update a capability", Group: "routing", Kind: ctlWrite, Role: a, Method: patch, Path: "/api/admin/capabilities/{capability}",
			Desc:  "Change a capability. Only the fields you send change; params and routes replace the whole list. The slug never changes. Test it with try_capability.",
			Props: updateProps, Required: []string{"capability"}, Body: []string{"name", "description", "strategy", "params", "routes"}},
		{Name: "delete_capability", Title: "Delete a capability", Group: "routing", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/capabilities/{capability}",
			Desc: "Delete a capability. Apps calling it get 404 afterwards.", Props: map[string]any{"capability": capIn}, Required: []string{"capability"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				cp, err := c.capability(argStr(in, "capability"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("delete capability %s (/proxy/%s); apps calling it get 404 afterwards", cp.Name, cp.Slug), nil
			}},
		{Name: "try_capability", Title: "Try a capability", Group: "routing", Kind: ctlWrite, Role: a, OpenWorld: true, Run: ctlTryCapability,
			Desc: "Call a capability for real, as the app \"Control MCP\", and see which provider answered, which were tried and the answer. Spends a request of provider quota. " +
				"Pass service to test one route, and raw to see the provider's body unmapped while writing an output mapping.",
			Props: props(tryProps, map[string]any{"slug": capIn}), Required: []string{"slug"}},

		// ---- ai ----
		{Name: "ai_rules", Title: "AI rules", Group: "ai", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/ai/rules",
			Desc: "The house rules every AI feature follows (the Assistant, the provider builder, form fill), as text."},
		{Name: "update_ai_rules", Title: "Set the AI rules", Group: "ai", Kind: ctlWrite, Role: sa, Method: put, Path: "/api/admin/ai/rules",
			Desc:  "Replace the house rules every AI feature follows. Send the whole text; ai_rules reads the current one.",
			Props: map[string]any{"rules": sProp("The rules, as plain text")}, Required: []string{"rules"}, Body: []string{"rules"}},
		{Name: "update_ai_models", Title: "Choose the AI models", Group: "ai", Kind: ctlWrite, Role: sa, Method: put, Path: "/api/admin/ai/models",
			Desc:  "Choose the Claude model each AI feature uses. Only the fields you send change; ai_models lists the choices.",
			Props: modelFields, Body: keysOf(modelFields),
			Prepare: func(c *ctlCtx, in map[string]any, path string) (any, error) {
				return c.merge(path, "settings", in, keysOf(modelFields))
			}},
		{Name: "set_ai_price", Title: "Set a model price", Group: "ai", Kind: ctlWrite, Role: sa, Method: put, Path: "/api/admin/ai/prices/{model}",
			Desc:  "Override what one Claude model costs, for AI spend and budgets.",
			Props: props(priceFields, map[string]any{"model": sProp("Model id, such as claude-sonnet-4-5")}), Required: []string{"model", "input", "output"},
			Body: keysOf(priceFields)},
		{Name: "reset_ai_price", Title: "Reset a model price", Group: "ai", Kind: ctlDestructive, Role: sa, Method: del, Path: "/api/admin/ai/prices/{model}",
			Desc: "Drop a model's price override and go back to the built in price.", Props: map[string]any{"model": sProp("Model id")}, Required: []string{"model"},
			Describe: func(_ *ctlCtx, in map[string]any) (string, error) {
				return "reset the price of " + argStr(in, "model") + " to the built in one", nil
			}},
		{Name: "clear_ai_runs", Title: "Clear finished AI runs", Group: "ai", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/ai/runs",
			Desc: "Delete every finished AI run and its log. Running builds stay.",
			Describe: func(*ctlCtx, map[string]any) (string, error) {
				return "delete every finished AI run and its log", nil
			}},

		// ---- ai: the Assistant ----
		{Name: "assistant_threads", Title: "Assistant chats", Group: "ai", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/chat/threads",
			Desc: "Your chats with the dashboard Assistant, newest first, and whether AI is on."},
		{Name: "assistant_thread", Title: "Read an Assistant chat", Group: "ai", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/chat/threads/{thread_id}",
			Desc:  "One chat with the Assistant: its items (your messages, its answers, tool calls, confirm cards and questions) and whether it is still answering (turn.active).",
			Props: map[string]any{"thread_id": threadIn}, Required: []string{"thread_id"}},
		{Name: "assistant_new_thread", Title: "Start an Assistant chat", Group: "ai", Kind: ctlWrite, Role: m, Method: post, Path: "/api/admin/chat/threads",
			Desc: "Start an empty chat with the Assistant. assistant_send starts one itself when you leave thread_id out."},
		{Name: "assistant_send", Title: "Ask the Assistant", Group: "ai", Kind: ctlWrite, Role: m, OpenWorld: true, Run: ctlAssistantSend,
			Method: post, Path: "/api/admin/chat/threads/{thread_id}/messages",
			Desc: "Send a message to the dashboard Assistant (Claude with the dashboard's tools, as you) and wait for its answer. It may ask before a risky step: " +
				"an item with kind confirm (answer with assistant_confirm) or kind ask (answer with assistant_answer). Uses AI and counts against AI spend.",
			Props:    map[string]any{"thread_id": iProp("Chat to continue; empty starts a new chat"), "text": sProp("Your message"), "wait_sec": waitIn},
			Required: []string{"text"}, Body: []string{"text"}},
		{Name: "assistant_confirm", Title: "Answer an Assistant confirm", Group: "ai", Kind: ctlWrite, Role: m, OpenWorld: true,
			Method: post, Path: "/api/admin/chat/threads/{thread_id}/confirm", Run: ctlAssistantReply("confirm", []string{"pending_id", "approve"}),
			Desc:     "Approve or decline the action the Assistant asked to confirm (an item with kind confirm and its pending id), then wait for it to go on. Ask the user before approving.",
			Props:    map[string]any{"thread_id": threadIn, "pending_id": sProp("The pending id of the confirm item"), "approve": bProp("true runs the action, false declines it"), "wait_sec": waitIn},
			Required: []string{"thread_id", "pending_id", "approve"}, Body: []string{"pending_id", "approve"}},
		{Name: "assistant_answer", Title: "Answer an Assistant question", Group: "ai", Kind: ctlWrite, Role: m, OpenWorld: true,
			Method: post, Path: "/api/admin/chat/threads/{thread_id}/answer", Run: ctlAssistantReply("answer", []string{"ask_id", "choice", "names", "auto"}),
			Desc: "Answer a question the Assistant asked (an item with kind ask), then wait for it to go on.",
			Props: map[string]any{
				"thread_id": threadIn,
				"ask_id":    sProp("The id of the ask item"),
				"choice":    sProp("The option picked, for a choice question"),
				"names":     map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Labels by key hint, for a names question"},
				"auto":      bProp("Let the Assistant decide itself"),
				"wait_sec":  waitIn,
			},
			Required: []string{"thread_id", "ask_id"}, Body: []string{"ask_id", "choice", "names", "auto"}},
		{Name: "assistant_steer", Title: "Steer the Assistant", Group: "ai", Kind: ctlWrite, Role: m, Method: post, Path: "/api/admin/chat/threads/{thread_id}/steer",
			Desc:  "Add a message to a chat while the Assistant is still answering; it reads it before its next step.",
			Props: map[string]any{"thread_id": threadIn, "text": sProp("Your message")}, Required: []string{"thread_id", "text"}, Body: []string{"text"}},
		{Name: "assistant_stop", Title: "Stop the Assistant", Group: "ai", Kind: ctlWrite, Role: m, Method: post, Path: "/api/admin/chat/threads/{thread_id}/stop",
			Desc: "Stop the Assistant's answer in a chat.", Props: map[string]any{"thread_id": threadIn}, Required: []string{"thread_id"}},
		{Name: "assistant_compact", Title: "Compact an Assistant chat", Group: "ai", Kind: ctlWrite, Role: m, Method: post, Path: "/api/admin/chat/threads/{thread_id}/compact", OpenWorld: true,
			Desc: "Summarize a long chat's older messages so it can go on. Uses AI.", Props: map[string]any{"thread_id": threadIn}, Required: []string{"thread_id"}},
		{Name: "delete_assistant_thread", Title: "Delete an Assistant chat", Group: "ai", Kind: ctlDestructive, Role: m, Method: del, Path: "/api/admin/chat/threads/{thread_id}",
			Desc: "Delete a chat with the Assistant.", Props: map[string]any{"thread_id": threadIn}, Required: []string{"thread_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				code, out, msg := c.apiJSON(get, "/api/admin/chat/threads/"+argStr(in, "thread_id"), nil)
				if code >= 400 {
					return "", errors.New(msg)
				}
				t, _ := out["thread"].(map[string]any)
				return fmt.Sprintf("delete the Assistant chat %s (%s)", argStr(in, "thread_id"), argStr(t, "title")), nil
			}},

		// ---- sharing ----
		{Name: "share_analytics", Title: "Share analytics", Group: "sharing", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/shares/analytics",
			Desc:  "Requests, errors and spend of every share per day, with totals, for the last days.",
			Props: map[string]any{"days": iProp("Days to cover, 1 to 90, default 30")}, Query: []string{"days"}},

		// ---- refresh ----
		{Name: "get_refresh_run", Title: "Get a refresh run", Group: "refresh", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/providers/refresh/runs/{run_id}",
			Desc: "One offers refresh run with what it checked and changed per provider.", Props: map[string]any{"run_id": iProp("Run id from list_refresh_runs")}, Required: []string{"run_id"}},
	}
}

// capability finds a capability by id or slug.
func (c *ctlCtx) capability(v string) (*store.Capability, error) {
	ctx := c.r.Context()
	if id, err := strconv.ParseInt(v, 10, 64); err == nil {
		if cp, err := c.s.Store.GetCapability(ctx, id); err == nil {
			return cp, nil
		}
	}
	cp, err := c.s.Store.GetCapabilityBySlug(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("no capability %q", v)
	}
	return cp, nil
}

// ctlTryCapability is try_capability: one call to the capability through
// the proxy as the Control MCP app, like the Assistant's tool does as its
// own app.
func ctlTryCapability(c *ctlCtx, in map[string]any) map[string]any {
	cp, err := c.capability(argStr(in, "slug"))
	if err != nil {
		return ctlFailCode("not_found", err.Error()+"; "+ctlPublicTool("list_capabilities")+" lists them")
	}
	a, err := c.controlApp()
	if err != nil {
		return ctlFail(err.Error())
	}
	if !a.Active {
		return ctlFail("the " + controlAppName + " app is turned off; turn it on in Apps to try capabilities")
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
	ctx := store.WithAppToken(context.WithValue(c.r.Context(), appKey, a), a.TokenID)
	limit := ctlResultCap - 4<<10
	rec, err := c.s.proxyCapture(ctx, c.r, cp.Slug, http.MethodPost, "", nil, body, nil, limit)
	if err != nil {
		return ctlFail(err.Error())
	}
	b := rec.Body.Bytes()
	out := map[string]any{"status": rec.Code, "capability": cp.Slug, "service": rec.Header().Get("X-Acnologia-Service"), "tried": rec.Header().Get("X-Acnologia-Tried")}
	var v any
	if len(b) <= limit && json.Unmarshal(b, &v) == nil {
		out["body"] = v
	} else {
		out["body"] = string(truncate(b, limit))
		if len(b) > limit {
			out["note"] = fmt.Sprintf("body trimmed to %d KB", limit>>10)
		}
	}
	res := ctlText(c.s.Store.Scrub(c.r.Context(), asJSON(out)), false)
	if rec.Code >= 400 {
		res["isError"] = true
	}
	return res
}

// ---- the Assistant ----

const (
	chatWaitDefault = 60
	chatWaitMax     = 240
)

// chatPoll is how often a waiting assistant tool reads the chat again.
var chatPoll = 300 * time.Millisecond

// chatItems reads a chat: its items and whether a turn is running.
func (c *ctlCtx) chatItems(id string) ([]any, map[string]any, error) {
	code, out, msg := c.apiJSON(http.MethodGet, "/api/admin/chat/threads/"+id, nil)
	if code >= 400 {
		return nil, nil, errors.New(msg)
	}
	items, _ := out["items"].([]any)
	turn, _ := out["turn"].(map[string]any)
	return items, turn, nil
}

// chatWait waits up to wait_sec for the chat's turn to end and returns the
// items added after the first since items.
func (c *ctlCtx) chatWait(id string, since int, in map[string]any) map[string]any {
	wait := chatWaitDefault
	if _, ok := in["wait_sec"]; ok {
		wait = min(max(argInt(in, "wait_sec"), 0), chatWaitMax)
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	for {
		items, turn, err := c.chatItems(id)
		if err != nil {
			return ctlFail(err.Error())
		}
		active, _ := turn["active"].(bool)
		if !active || !time.Now().Before(deadline) {
			fresh := []any{}
			if since < len(items) {
				fresh = items[since:]
			}
			n, _ := strconv.ParseInt(id, 10, 64)
			out := map[string]any{"thread_id": n, "done": !active, "turn": turn, "items": fresh}
			if active {
				out["note"] = ctlPublicText("the Assistant is still answering; read the rest with assistant_thread")
			}
			for _, x := range fresh {
				it, _ := x.(map[string]any)
				switch it["kind"] {
				case "confirm":
					out["next"] = ctlPublicText("the Assistant waits for a confirm: ask the user, then call assistant_confirm with the item's pending id")
				case "ask":
					out["next"] = ctlPublicText("the Assistant asked a question: answer it with assistant_answer")
				}
			}
			return ctlJSON(out)
		}
		select {
		case <-c.r.Context().Done():
			return ctlFail("the request ended while the Assistant was answering")
		case <-time.After(chatPoll):
		}
	}
}

// ctlAssistantSend is assistant_send: start a chat when none is named, send
// the message and wait for the answer.
func ctlAssistantSend(c *ctlCtx, in map[string]any) map[string]any {
	id := argStr(in, "thread_id")
	since := 0
	if id == "" {
		code, out, _ := c.apiJSON(http.MethodPost, "/api/admin/chat/threads", map[string]any{})
		if code >= 400 {
			b, _ := json.Marshal(out)
			return ctlRouteError(code, b)
		}
		t, _ := out["thread"].(map[string]any)
		id = argStr(t, "id")
	} else {
		items, _, err := c.chatItems(id)
		if err != nil {
			return ctlFailCode("not_found", err.Error())
		}
		since = len(items)
	}
	code, b, _ := c.api(http.MethodPost, "/api/admin/chat/threads/"+id+"/messages", map[string]any{"text": argStr(in, "text")})
	if code >= 400 {
		return ctlRouteError(code, b)
	}
	return c.chatWait(id, since, in)
}

// ctlAssistantReply answers a confirm card or a question in a chat, then
// waits for the Assistant to go on.
func ctlAssistantReply(route string, fields []string) func(c *ctlCtx, in map[string]any) map[string]any {
	return func(c *ctlCtx, in map[string]any) map[string]any {
		id := argStr(in, "thread_id")
		items, _, err := c.chatItems(id)
		if err != nil {
			return ctlFailCode("not_found", err.Error())
		}
		code, b, _ := c.api(http.MethodPost, "/api/admin/chat/threads/"+id+"/"+route, pick(in, fields...))
		if code >= 400 {
			return ctlRouteError(code, b)
		}
		return c.chatWait(id, len(items), in)
	}
}

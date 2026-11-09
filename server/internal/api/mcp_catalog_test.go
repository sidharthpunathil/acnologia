package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// catalogProvider registers a provider like serpapi whose tools come from
// its guide: a LinkedIn jobs search, a profile lookup, and n more filler
// endpoints.
func catalogProvider(t *testing.T, slug string, n int) {
	spec := *providers.Get("serpapi")
	spec.Examples = []providers.Example{{Name: "Google search", Path: "/search.json", Query: map[string]string{"q": "coffee"}}}
	eps := []providers.GuideEndpoint{
		{Group: "LinkedIn Jobs", Name: "Search jobs", Method: "GET", Path: "/linkedin/jobs", Description: "Find job postings on LinkedIn.",
			Params: []providers.GuideParam{{Name: "keywords", In: "query", Required: true, Description: "Job title"}, {Name: "page", In: "query", Type: "integer"}}},
		{Group: "LinkedIn Profiles", Name: "Profile", Method: "GET", Path: "/linkedin/in/{user}", Description: "A person's public profile."},
		// The same call as the example: no second tool.
		{Name: "Search", Method: "GET", Path: "/search.json"},
		{Name: "Create video", Method: "POST", Path: "/video", Params: []providers.GuideParam{{Name: "prompt", In: "body", Required: true}}},
	}
	for i := 0; i < n; i++ {
		eps = append(eps, providers.GuideEndpoint{Name: fmt.Sprintf("Filler %d", i), Method: "GET", Path: fmt.Sprintf("/filler/%d", i)})
	}
	spec.Guide = &providers.Guide{Summary: "test", Endpoints: eps}
	providers.Set(slug, &spec)
	t.Cleanup(func() { providers.Remove(slug) })
}

func catalogService(t *testing.T, e *env, slug string) *store.Service {
	base, err := e.st.GetService(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	sv := *base
	sv.ID, sv.Slug, sv.Name, sv.Provider = 0, slug, slug, "catalog-test"
	out, err := e.st.CreateService(context.Background(), &sv)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (m *mcpClient) tools() map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, x := range m.call("tools/list", nil)["result"].(map[string]any)["tools"].([]any) {
		t := x.(map[string]any)
		out[t["name"].(string)] = t
	}
	return out
}

func TestMCPGuideEndpointsBecomeTools(t *testing.T) {
	e := newEnv(t)
	catalogProvider(t, "catalog-test", 0)
	svc := catalogService(t, e, "lnk")
	e.do(e.super, "POST", "/api/admin/services/"+itoa(int(svc.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "lnkkey01"}}}, 201, nil)
	_, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}

	tools := m.tools()
	jobs := tools["lnk_linkedin_jobs_search_jobs"]
	if jobs == nil || tools["lnk_google_search"] == nil || tools["lnk_linkedin_profiles_profile"] == nil || tools["lnk_create_video"] == nil {
		t.Fatalf("guide tools missing: %v", keysOfMap(tools))
	}
	if tools["lnk_search"] != nil {
		t.Fatal("a guide endpoint that repeats an example made a second tool")
	}
	schema := jobs["inputSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if props["page"].(map[string]any)["type"] != "integer" || !strings.Contains(fmt.Sprint(schema["required"]), "keywords") {
		t.Fatalf("schema: %v", schema)
	}
	if !strings.HasPrefix(jobs["description"].(string), "Find job postings on LinkedIn.") {
		t.Fatalf("description: %v", jobs["description"])
	}
	video := tools["lnk_create_video"]["inputSchema"].(map[string]any)
	if !strings.Contains(fmt.Sprint(video["required"]), "body") {
		t.Fatalf("a required body param must make body required: %v", video)
	}
	out, isErr := m.tool("lnk_linkedin_jobs_search_jobs", map[string]any{"keywords": "go developer", "page": 2})
	if isErr || out["status"].(float64) != 200 || e.up.hits["lnkkey01"] == 0 {
		t.Fatalf("call: %v %v", out, e.up.hits)
	}
}

func TestMCPSearchModeFindsAndCallsTools(t *testing.T) {
	e := newEnv(t)
	catalogProvider(t, "catalog-test", 0)
	svc := catalogService(t, e, "lnk")
	e.do(e.super, "POST", "/api/admin/services/"+itoa(int(svc.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "lnkkey02"}}}, 201, nil)
	appID, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}

	var view map[string]any
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{"core": true, "service_tools": true, "resources": true, "tool_mode": "search"}, 200, &view)
	if l := view["listing"].(map[string]any); l["mode"] != "search" {
		t.Fatalf("listing: %v", l)
	}
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{"tool_mode": "nope"}, 400, nil)

	tools := m.tools()
	if tools["search_tools"] == nil || tools["call_tool"] == nil || tools["proxy_request"] == nil || tools["lnk_linkedin_jobs_search_jobs"] != nil {
		t.Fatalf("search mode list: %v", keysOfMap(tools))
	}
	// A typo and a plural still find the LinkedIn jobs tool first.
	out, isErr := m.tool("search_tools", map[string]any{"query": "linkdin jobs"})
	if isErr {
		t.Fatal(out)
	}
	found := out["tools"].([]any)
	if len(found) == 0 || found[0].(map[string]any)["name"] != "lnk_linkedin_jobs_search_jobs" || found[0].(map[string]any)["inputSchema"] == nil {
		t.Fatalf("search: %v", out)
	}
	out, _ = m.tool("search_tools", map[string]any{"query": "profile"})
	if out["tools"].([]any)[0].(map[string]any)["name"] != "lnk_linkedin_profiles_profile" {
		t.Fatalf("profile search: %v", out)
	}
	// An empty query is the directory.
	out, _ = m.tool("search_tools", map[string]any{})
	if out["services"] == nil || out["catalog_size"].(float64) < 4 {
		t.Fatalf("directory: %v", out)
	}
	out, _ = m.tool("search_tools", map[string]any{"query": "zzqx"})
	if out["matches"].(float64) != 0 || out["hint"] == nil {
		t.Fatalf("no match: %v", out)
	}
	// call_tool runs a found tool; the hidden tool is also callable by name.
	out, isErr = m.tool("call_tool", map[string]any{"name": "lnk_linkedin_jobs_search_jobs", "arguments": map[string]any{"keywords": "rust"}})
	if isErr || out["status"].(float64) != 200 {
		t.Fatalf("call_tool: %v", out)
	}
	if out, isErr := m.tool("lnk_google_search", map[string]any{"q": "tea"}); isErr || out["status"].(float64) != 200 {
		t.Fatalf("direct call: %v", out)
	}
	if _, isErr := m.tool("call_tool", map[string]any{"name": "call_tool"}); !isErr {
		t.Fatal("call_tool must not call itself")
	}
	if _, isErr := m.tool("call_tool", map[string]any{"name": "nope"}); !isErr {
		t.Fatal("unknown tool")
	}
}

func TestMCPAutoModeSwitchesToSearch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	catalogProvider(t, "catalog-big", mcpAutoListMax)
	if _, err := e.st.CreateService(ctx, &store.Service{Slug: "big", Name: "Big", Provider: "catalog-big", BaseURL: e.upURL}); err != nil {
		t.Fatal(err)
	}
	a := &store.App{Mode: "both"}
	st := store.DefaultMCPSettings()
	listed := e.api.mcpListedTools(ctx, a, st)
	if findTool(listed, "search_tools") == nil || findTool(listed, "big_filler_0") != nil {
		t.Fatalf("auto with many tools should search: %d listed", len(listed))
	}
	st.ToolMode = toolModeAll
	if findTool(e.api.mcpListedTools(ctx, a, st), "big_filler_0") == nil {
		t.Fatal("mode all lists every tool")
	}
}

// GET /mcp is the notification stream: adding a service sends
// tools/list_changed at once.
func TestMCPStreamNotifiesListChanged(t *testing.T) {
	e := newEnv(t)
	catalogProvider(t, "catalog-test", 0)
	_, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}
	init := m.call("initialize", map[string]any{})["result"].(map[string]any)
	if init["capabilities"].(map[string]any)["tools"].(map[string]any)["listChanged"] != true {
		t.Fatal("tools.listChanged should be true")
	}

	req, _ := http.NewRequest("GET", e.srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 405 {
		t.Fatalf("GET without Accept: %v %v", resp, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %v %v", resp, err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	<-lines // the opening comment
	time.Sleep(100 * time.Millisecond)
	catalogService(t, e, "lnk-new")
	// A store write is no admin request: wake the streams as the admin
	// routes do.
	e.api.catalogChanged()
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("stream closed")
			}
			if strings.Contains(l, "notifications/tools/list_changed") {
				return
			}
		case <-ctx.Done():
			t.Fatal("no list_changed notification")
		}
	}
}

func keysOfMap(m map[string]map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

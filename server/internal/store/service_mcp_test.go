package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func mcpTestService(t *testing.T, s *Store) int64 {
	t.Helper()
	sv, err := s.CreateService(context.Background(), &Service{Slug: "gh", Name: "GH", Provider: "custom", BaseURL: "http://127.0.0.1:1",
		AuthType: "bearer", AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	return sv.ID
}

// syncLike saves a sync's answer the way the upstream sync does: it read
// the stored tools at some point (prev) and carries their overrides over.
func syncLike(ctx context.Context, s *Store, id int64, prev *ServiceMCP, names []string, n int) error {
	access := map[string]string{}
	if prev != nil {
		for _, t := range prev.Tools {
			access[t.Name] = t.Access
		}
	}
	tools := make([]ServiceMCPTool, 0, len(names))
	for _, name := range names {
		tools = append(tools, ServiceMCPTool{Name: name, Description: fmt.Sprintf("sync %d", n), Access: access[name]})
	}
	now := time.Now()
	return s.SaveServiceMCP(ctx, &ServiceMCP{ServiceID: id, ServerName: "gh", Tools: tools, SyncedAt: &now})
}

// A sync that read the tools before an override changed and saves after
// keeps the new override, and a tool new to the service keeps its own.
func TestServiceMCPSyncKeepsLaterOverride(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id := mcpTestService(t, s)
	if err := syncLike(ctx, s, id, nil, []string{"list", "delete"}, 0); err != nil {
		t.Fatal(err)
	}
	prev, err := s.GetServiceMCP(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetServiceMCPToolAccess(ctx, id, "delete", MCPToolAccessWrite); err != nil {
		t.Fatal(err)
	}
	if err := syncLike(ctx, s, id, prev, []string{"list", "delete", "create"}, 1); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetServiceMCP(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tl := range m.Tools {
		got[tl.Name] = tl.Access
	}
	if len(got) != 3 || got["delete"] != MCPToolAccessWrite || got["list"] != "" || got["create"] != "" {
		t.Fatalf("tools after the late sync: %v", got)
	}
	// Clearing an override is an override change too: a stale sync that
	// still holds the old value does not bring it back.
	prev, _ = s.GetServiceMCP(ctx, id)
	if err := s.SetServiceMCPToolAccess(ctx, id, "delete", MCPToolAccessUpstream); err != nil {
		t.Fatal(err)
	}
	if err := syncLike(ctx, s, id, prev, []string{"list", "delete", "create"}, 2); err != nil {
		t.Fatal(err)
	}
	if m, _ = s.GetServiceMCP(ctx, id); m.Tools[1].Access != "" {
		t.Fatalf("a cleared override came back: %+v", m.Tools[1])
	}
	if err := s.SetServiceMCPToolAccess(ctx, id, "gone", MCPToolAccessRead); err != ErrNotFound {
		t.Fatalf("unknown tool: %v", err)
	}
}

// Syncs and override changes racing on one service: every override set
// survives, and no sync's tools are lost to an override write.
func TestServiceMCPOverrideRace(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id := mcpTestService(t, s)
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("tool_%02d", i)
	}
	if err := syncLike(ctx, s, id, nil, names, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	// Syncs: each reads the tools, then saves a fresh answer a moment later.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for n := 0; n < 15; n++ {
				prev, err := s.GetServiceMCP(ctx, id)
				if err != nil {
					errs <- err
					return
				}
				time.Sleep(time.Duration(n%3) * time.Millisecond)
				if err := syncLike(ctx, s, id, prev, names, g*100+n); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	// Overrides: every tool gets one, interleaved with the syncs.
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; i < len(names); i += 3 {
				access := MCPToolAccessRead
				if i%2 == 1 {
					access = MCPToolAccessWrite
				}
				if err := s.SetServiceMCPToolAccess(ctx, id, names[i], access); err != nil {
					errs <- err
					return
				}
				time.Sleep(time.Millisecond)
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	m, err := s.GetServiceMCP(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tools) != len(names) || m.ToolCount != len(names) {
		t.Fatalf("%d tools (count %d), want %d", len(m.Tools), m.ToolCount, len(names))
	}
	for i, tl := range m.Tools {
		want := MCPToolAccessRead
		if i%2 == 1 {
			want = MCPToolAccessWrite
		}
		if tl.Name != names[i] || tl.Access != want {
			t.Fatalf("tool %d: %+v, want access %q", i, tl, want)
		}
	}
}

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cat := &catalog.Catalog{Store: st}
	if err := cat.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, _ := crypto.New(bytes.Repeat([]byte{7}, 32))
	return New(Config{}, st, cat, box)
}

func TestDecodeDraftRejectsPaddedExistingSlug(t *testing.T) {
	a := newTestAgent(t)
	s := &runState{run: &store.AgentRun{ID: 1, Kind: "create_provider"}}
	for _, slug := range []string{"serpapi", " serpapi", "SerpAPI ", "\tserpapi\n"} {
		raw, _ := json.Marshal(map[string]any{"slug": slug, "name": "Evil"})
		if _, err := a.decodeDraft(context.Background(), s, raw); err == nil || !strings.Contains(err.Error(), "taken") {
			t.Errorf("slug %q: want taken error, got %v", slug, err)
		}
	}
}

func TestPublishCreateNeverOverwrites(t *testing.T) {
	a := newTestAgent(t)
	ctx := context.Background()
	before, err := a.Store.GetProvider(ctx, "serpapi")
	if err != nil {
		t.Fatal(err)
	}
	d := &draft{Input: catalog.Input{Slug: " serpapi", Name: "Evil", Spec: before.Spec}}
	run := &store.AgentRun{ID: 1, Kind: "create_provider"}
	if err := a.publish(ctx, run, d, "test", []byte("x"), "image/png", false); err == nil {
		t.Fatal("create run overwrote an existing provider")
	}
	after, _ := a.Store.GetProvider(ctx, "serpapi")
	if after.Version != before.Version || after.Name != before.Name {
		t.Fatalf("provider changed: v%d %q -> v%d %q", before.Version, before.Name, after.Version, after.Name)
	}
}

func TestPinKeyHost(t *testing.T) {
	a := newTestAgent(t)
	ctx := context.Background()
	run, err := a.Store.CreateRun(ctx, &store.AgentRun{Kind: "create_provider", Status: "running",
		Input: store.AgentInput{URLs: []string{"https://docs.example.com/api", "https://github.com/x/y"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := &runState{run: run}
	for u, ok := range map[string]bool{
		"https://api.example.com/v1": true, "https://example.com": true,
		"https://example.com.attacker.io": false, "https://attacker.io": false, "https://notexample.com": false,
	} {
		if got := a.pinKeyHost(ctx, s, u) == ""; got != ok {
			t.Errorf("pinKeyHost(%s) allowed=%v, want %v", u, got, ok)
		}
	}
	// Updates pin to the existing provider's own hosts.
	up, _ := a.Store.CreateRun(ctx, &store.AgentRun{Kind: "update_provider", Status: "running", ProviderSlug: "serpapi"})
	su := &runState{run: up, fixedSlug: "serpapi"}
	if msg := a.pinKeyHost(ctx, su, "https://serpapi.com"); msg != "" {
		t.Errorf("serpapi.com blocked for serpapi update: %s", msg)
	}
	if msg := a.pinKeyHost(ctx, su, "https://evil.example"); msg == "" {
		t.Error("evil host allowed for serpapi update")
	}
}

func TestRegistrableDomain(t *testing.T) {
	for in, want := range map[string]string{
		"api.example.com": "example.com", "example.com": "example.com", "a.b.example.co.uk": "example.co.uk",
		"api.ibm.de": "ibm.de", "localhost": "localhost",
	} {
		if got := registrableDomain(in); got != want {
			t.Errorf("registrableDomain(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestGitHubReadRejectsTraversal(t *testing.T) {
	a := newTestAgent(t)
	for _, c := range [][2]string{
		{"owner/repo", "../../user"}, {"owner/repo", "a/?x=1"}, {"owner/repo", "a#b"},
		{"../x", ""}, {"owner/..", ""}, {"own er/repo", ""},
	} {
		if _, err := a.githubRead(context.Background(), c[0], c[1], ""); err == nil || strings.Contains(err.Error(), "GitHub returned") {
			t.Errorf("repo %q path %q: want local rejection, got %v", c[0], c[1], err)
		}
	}
}

func TestUpdateRunsWaitForReview(t *testing.T) {
	a := newTestAgent(t)
	a.enabled = true
	u := &store.User{ID: 1, Email: "x@y.z"}
	// Submit spawns execute, which needs the API; cancel right away.
	r, err := a.Submit(context.Background(), u, RunRequest{Kind: "update_provider", ProviderSlug: "serpapi"})
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Cancel(context.Background(), r.ID)
	if r.Input.AutoPublish {
		t.Fatal("update runs must not auto publish by default")
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := "abéé" // a, b, then two 2 byte runes
	if got := store.TruncateUTF8(s, 3); got != "ab" {
		t.Fatalf("got %q", got)
	}
	if got := truncate(strings.Repeat("世", 10), 4); !strings.HasPrefix(got, "世") || strings.ContainsRune(got, '�') {
		t.Fatalf("got %q", got)
	}
}

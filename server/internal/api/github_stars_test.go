package api

import (
	"context"
	"errors"
	"testing"
)

func TestGitHubStars(t *testing.T) {
	e := newGateEnv(t)
	calls := 0
	n := 42
	e.api.ghStars.fetch = func(_ context.Context, repo string) (*int, error) {
		calls++
		if repo != defaultGitHubRepo {
			t.Fatalf("repo %q", repo)
		}
		return &n, nil
	}
	// Open behind the access gate, with no cookie.
	var out struct {
		Repo  string `json:"repo"`
		URL   string `json:"url"`
		Stars *int   `json:"stars"`
	}
	e.do(client(), "GET", "/api/public/github", nil, 200, &out)
	if out.Stars == nil || *out.Stars != 42 || out.URL != "https://github.com/"+defaultGitHubRepo {
		t.Fatalf("stars: %+v", out)
	}
	// Cached: a second call does not ask GitHub again.
	e.do(client(), "GET", "/api/public/github", nil, 200, &out)
	if calls != 1 {
		t.Fatalf("%d fetches", calls)
	}

	// No count (private repo) is null, and an error keeps the last count.
	var c githubStarsCache
	c.fetch = func(context.Context, string) (*int, error) { return nil, nil }
	if c.get(context.Background(), "a/b") != nil {
		t.Fatal("private repo should be null")
	}
	c = githubStarsCache{repo: "a/b", stars: &n}
	c.fetch = func(context.Context, string) (*int, error) { return nil, errors.New("down") }
	if got := c.get(context.Background(), "a/b"); got == nil || *got != 42 {
		t.Fatalf("blip lost the count: %v", got)
	}
	t.Setenv("ACNOLOGIA_GITHUB_REPO", "fork-owner/acnologia")
	if githubRepo() != "fork-owner/acnologia" {
		t.Fatal("env repo ignored")
	}
	t.Setenv("ACNOLOGIA_GITHUB_REPO", "not a repo")
	if githubRepo() != defaultGitHubRepo {
		t.Fatal("bad env repo accepted")
	}
}

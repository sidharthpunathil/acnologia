package api

// GET /api/public/github: the project's GitHub star count for the landing
// page's GitHub link. The page's CSP keeps the browser on this origin, so
// the server asks GitHub instead, through the public only SafeClient, and
// caches the answer: an hour when it has a count, ten minutes when it does
// not. stars is null when GitHub gives no count (a private repo answers
// 404 to an anonymous call), and the page then shows no number at all.
// Open in the access gate, like the landing page itself; it carries no data
// beyond what github.com shows anyone.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/config"
)

// defaultGitHubRepo is the project's repository; ACNOLOGIA_GITHUB_REPO
// (owner/name) points a fork's landing page at its own.
const defaultGitHubRepo = "sidharthpunathil/acnologia"

var githubRepoRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100}$`)

const (
	githubStarsTTL     = time.Hour
	githubStarsMissTTL = 10 * time.Minute
)

type githubStarsCache struct {
	mu    sync.Mutex
	repo  string
	stars *int
	at    time.Time
	// fetch asks GitHub; tests replace it.
	fetch func(ctx context.Context, repo string) (*int, error)
}

func githubRepo() string {
	if r := config.Getenv("ACNOLOGIA_GITHUB_REPO"); githubRepoRe.MatchString(r) {
		return r
	}
	return defaultGitHubRepo
}

// fetchGitHubStars reads stargazers_count from the GitHub REST API. A
// non 200 answer (404 for a private repo, 403 when rate limited) is no
// count, not an error worth logging.
func fetchGitHubStars(ctx context.Context, repo string) (*int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repo, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "acnologia")
	resp, err := catalog.SafeClient(8 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	var out struct {
		Stars *int `json:"stargazers_count"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out.Stars, nil
}

// get returns the cached count, refreshing it when stale. Only one caller
// refreshes at a time; the rest wait for its answer.
func (c *githubStarsCache) get(ctx context.Context, repo string) *int {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := githubStarsTTL
	if c.stars == nil {
		ttl = githubStarsMissTTL
	}
	if c.repo == repo && !c.at.IsZero() && time.Since(c.at) < ttl {
		return c.stars
	}
	fetch := c.fetch
	if fetch == nil {
		fetch = fetchGitHubStars
	}
	n, err := fetch(ctx, repo)
	if err != nil {
		// Keep the last good count through a blip.
		if c.repo == repo && c.stars != nil {
			n = c.stars
		}
	}
	c.repo, c.stars, c.at = repo, n, time.Now()
	return n
}

func (s *Server) githubStars(w http.ResponseWriter, r *http.Request) {
	repo := githubRepo()
	stars := s.ghStars.get(r.Context(), repo)
	w.Header().Set("Cache-Control", "public, max-age=600")
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "url": "https://github.com/" + repo, "stars": stars})
}
